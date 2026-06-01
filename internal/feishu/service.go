package feishu

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultBaseURL            = "https://open.feishu.cn"
	defaultGroupPolicy        = "mention"
	defaultMaxEventSize       = 1 << 20
	defaultProcessingReaction = "OneSecond"
	defaultProcessingDelay    = time.Second
	defaultAgentTurnTimeout   = 2 * time.Minute
)

type Config struct {
	Enabled                bool
	AppID                  string
	AppSecret              string
	VerificationToken      string
	BaseURL                string
	BotOpenID              string
	GroupPolicy            string
	EventMode              string
	ReplyToMessage         bool
	TopicIsolation         bool
	ProcessingReaction     bool
	ProcessingReactionType string
	ProcessingDelay        time.Duration
	AgentTurnTimeout       time.Duration
	AllowOpenIDs           []string
	HTTPClient             *http.Client
}

type InboundMessage struct {
	SenderOpenID  string
	ChatID        string
	ChatType      string
	MessageID     string
	MessageType   string
	Content       string
	SessionKey    string
	ReceiveIDType string
	ReceiveID     string
	RootID        string
	ParentID      string
	ThreadID      string
}

type OutboundMessage struct {
	ReceiveIDType string
	ReceiveID     string
	MessageID     string
	ThreadID      string
	Content       string
	Card          any
	Reply         bool
}

type Reply struct {
	Content string
	Card    any
}

func (r Reply) Empty() bool {
	return r.Card == nil && strings.TrimSpace(r.Content) == ""
}

type HandlerFunc func(context.Context, InboundMessage) (Reply, error)

type Service struct {
	cfg    Config
	handle HandlerFunc
	client *http.Client

	mu        sync.Mutex
	token     tenantToken
	processed []string
	seen      map[string]struct{}
}

type tenantToken struct {
	value     string
	expiresAt time.Time
}

func NewConfigFromEnv() Config {
	return Config{
		Enabled:                envBool("FEISHU_ENABLED", false),
		AppID:                  strings.TrimSpace(os.Getenv("FEISHU_APP_ID")),
		AppSecret:              strings.TrimSpace(os.Getenv("FEISHU_APP_SECRET")),
		VerificationToken:      strings.TrimSpace(os.Getenv("FEISHU_VERIFICATION_TOKEN")),
		BaseURL:                envDefault("FEISHU_BASE_URL", defaultBaseURL),
		BotOpenID:              strings.TrimSpace(os.Getenv("FEISHU_BOT_OPEN_ID")),
		GroupPolicy:            envDefault("FEISHU_GROUP_POLICY", defaultGroupPolicy),
		EventMode:              envDefault("FEISHU_EVENT_MODE", "websocket"),
		ReplyToMessage:         envBool("FEISHU_REPLY_TO_MESSAGE", true),
		TopicIsolation:         envBool("FEISHU_TOPIC_ISOLATION", true),
		ProcessingReaction:     envBool("FEISHU_PROCESSING_REACTION", true),
		ProcessingReactionType: envDefault("FEISHU_PROCESSING_REACTION_TYPE", defaultProcessingReaction),
		ProcessingDelay:        envDuration("FEISHU_PROCESSING_DELAY", defaultProcessingDelay),
		AgentTurnTimeout:       envDuration("TERNURA_AGENT_TURN_TIMEOUT", defaultAgentTurnTimeout),
		AllowOpenIDs:           splitCSV(os.Getenv("FEISHU_ALLOW_OPEN_IDS")),
	}
}

func NewService(cfg Config, handle HandlerFunc) *Service {
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(firstNonEmpty(cfg.BaseURL, defaultBaseURL)), "/")
	cfg.GroupPolicy = strings.ToLower(strings.TrimSpace(firstNonEmpty(cfg.GroupPolicy, defaultGroupPolicy)))
	cfg.EventMode = strings.ToLower(strings.TrimSpace(firstNonEmpty(cfg.EventMode, "websocket")))
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.AgentTurnTimeout <= 0 {
		cfg.AgentTurnTimeout = defaultAgentTurnTimeout
	}
	return &Service{
		cfg:    cfg,
		handle: handle,
		client: cfg.HTTPClient,
		seen:   make(map[string]struct{}),
	}
}

func (s *Service) Enabled() bool {
	return s != nil && s.cfg.Enabled
}

func (s *Service) WebSocketEnabled() bool {
	if !s.Enabled() {
		return false
	}
	switch s.cfg.EventMode {
	case "ws", "websocket", "long", "long_connection", "long-connection":
		return true
	default:
		return false
	}
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !s.Enabled() {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, defaultMaxEventSize))
	if err != nil {
		http.Error(w, "read request", http.StatusBadRequest)
		return
	}

	challenge, inbound, err := s.decodeIncoming(body)
	if err != nil {
		log.Printf("feishu event rejected: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if challenge != "" {
		writeJSON(w, http.StatusOK, map[string]string{"challenge": challenge})
		return
	}
	if inbound == nil {
		writeJSON(w, http.StatusOK, map[string]any{"code": 0})
		return
	}

	if !s.markProcessed(inbound.MessageID) {
		writeJSON(w, http.StatusOK, map[string]any{"code": 0})
		return
	}

	go s.process(context.Background(), *inbound)
	writeJSON(w, http.StatusOK, map[string]any{"code": 0})
}

func (s *Service) process(ctx context.Context, inbound InboundMessage) {
	if s.handle == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.AgentTurnTimeout)
	defer cancel()

	replyMessage, err := s.handleWithProcessingReaction(ctx, inbound)
	if err != nil {
		log.Printf("feishu agent turn failed for %s: %v", inbound.MessageID, err)
		replyMessage = Reply{Content: "处理失败：" + err.Error()}
	}
	if replyMessage.Empty() {
		return
	}

	if err := s.Send(ctx, OutboundMessage{
		ReceiveIDType: inbound.ReceiveIDType,
		ReceiveID:     inbound.ReceiveID,
		MessageID:     inbound.MessageID,
		ThreadID:      inbound.ThreadID,
		Content:       replyMessage.Content,
		Card:          replyMessage.Card,
		Reply:         s.shouldReplyToInbound(inbound),
	}); err != nil {
		log.Printf("feishu send reply failed for %s: %v", inbound.MessageID, err)
	}
}

type handlerResult struct {
	reply Reply
	err   error
}

func (s *Service) handleWithProcessingReaction(ctx context.Context, inbound InboundMessage) (Reply, error) {
	if !s.processingReactionEnabled() {
		return s.handle(ctx, inbound)
	}

	resultCh := make(chan handlerResult, 1)
	go func() {
		reply, err := s.handle(ctx, inbound)
		resultCh <- handlerResult{reply: reply, err: err}
	}()

	delay := s.cfg.ProcessingDelay
	if delay <= 0 {
		s.addProcessingReaction(ctx, inbound)
		return waitForHandlerResult(ctx, resultCh)
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case result := <-resultCh:
		return result.reply, result.err
	case <-timer.C:
		s.addProcessingReaction(ctx, inbound)
		return waitForHandlerResult(ctx, resultCh)
	case <-ctx.Done():
		return Reply{}, ctx.Err()
	}
}

func waitForHandlerResult(ctx context.Context, resultCh <-chan handlerResult) (Reply, error) {
	select {
	case result := <-resultCh:
		return result.reply, result.err
	case <-ctx.Done():
		return Reply{}, ctx.Err()
	}
}

func (s *Service) processingReactionEnabled() bool {
	return s.cfg.ProcessingReaction &&
		s.cfg.ProcessingDelay >= 0 &&
		strings.TrimSpace(s.cfg.ProcessingReactionType) != ""
}

func (s *Service) addProcessingReaction(ctx context.Context, inbound InboundMessage) {
	reactionType := strings.TrimSpace(s.cfg.ProcessingReactionType)
	if reactionType == "" || strings.TrimSpace(inbound.MessageID) == "" {
		return
	}
	if err := s.AddReaction(ctx, inbound.MessageID, reactionType); err != nil {
		log.Printf("feishu add processing reaction failed for %s: %v", inbound.MessageID, err)
	}
}

func (s *Service) shouldReplyToInbound(inbound InboundMessage) bool {
	return s.cfg.ReplyToMessage || strings.TrimSpace(inbound.ThreadID) != ""
}

func (s *Service) Send(ctx context.Context, out OutboundMessage) error {
	if !s.Enabled() {
		return errors.New("feishu is disabled")
	}
	if out.Card == nil && strings.TrimSpace(out.Content) == "" {
		return nil
	}
	msgType, content, err := formatOutboundContent(out.Content, out.Card)
	if err != nil {
		return err
	}
	payload := map[string]string{
		"msg_type": msgType,
		"content":  content,
	}
	if out.Reply && strings.TrimSpace(out.MessageID) != "" {
		return s.postOpenAPI(ctx, "/open-apis/im/v1/messages/"+url.PathEscape(out.MessageID)+"/reply", "", payload, nil)
	}

	receiveIDType := strings.TrimSpace(out.ReceiveIDType)
	if receiveIDType == "" {
		receiveIDType = inferReceiveIDType(out.ReceiveID)
	}
	if receiveIDType == "" || strings.TrimSpace(out.ReceiveID) == "" {
		return errors.New("feishu receive id is required")
	}
	createPayload := map[string]string{
		"receive_id": out.ReceiveID,
		"msg_type":   msgType,
		"content":    content,
	}
	return s.postOpenAPI(ctx, "/open-apis/im/v1/messages", "receive_id_type="+url.QueryEscape(receiveIDType), createPayload, nil)
}

func (s *Service) AddReaction(ctx context.Context, messageID string, reactionType string) error {
	messageID = strings.TrimSpace(messageID)
	reactionType = strings.TrimSpace(reactionType)
	if messageID == "" || reactionType == "" {
		return nil
	}
	payload := map[string]any{
		"reaction_type": map[string]string{
			"emoji_type": reactionType,
		},
	}
	return s.postOpenAPI(ctx, "/open-apis/im/v1/messages/"+url.PathEscape(messageID)+"/reactions", "", payload, nil)
}

func (s *Service) decodeIncoming(body []byte) (string, *InboundMessage, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", nil, err
	}
	if _, ok := raw["encrypt"]; ok {
		return "", nil, errors.New("encrypted Feishu callbacks are not supported yet; disable event encryption or add decryption support")
	}

	var verification urlVerification
	if err := json.Unmarshal(body, &verification); err == nil && verification.Type == "url_verification" {
		if err := s.verifyToken(verification.Token); err != nil {
			return "", nil, err
		}
		return strings.TrimSpace(verification.Challenge), nil, nil
	}

	var event eventEnvelope
	if err := json.Unmarshal(body, &event); err != nil {
		return "", nil, err
	}
	if err := s.verifyToken(event.Header.Token); err != nil {
		return "", nil, err
	}
	if event.Header.EventType == "url_verification" && event.Challenge != "" {
		return event.Challenge, nil, nil
	}
	if event.Header.EventType != "im.message.receive_v1" {
		return "", nil, nil
	}
	if event.Event.Sender.SenderType == "bot" {
		return "", nil, nil
	}

	msg := event.Event.Message
	if msg.MessageID == "" {
		return "", nil, errors.New("missing message_id")
	}
	senderOpenID := event.Event.Sender.SenderID.OpenID
	if senderOpenID == "" {
		senderOpenID = "unknown"
	}
	if !s.isAllowed(senderOpenID) {
		return "", nil, nil
	}
	if isGroupChatType(msg.ChatType) && !s.groupMessageAllowed(msg) {
		return "", nil, nil
	}

	content := extractMessageContent(msg, s.cfg.BotOpenID)
	if strings.TrimSpace(content) == "" {
		return "", nil, nil
	}
	receiveIDType := "open_id"
	receiveID := senderOpenID
	if isGroupChatType(msg.ChatType) {
		receiveIDType = "chat_id"
		receiveID = msg.ChatID
	}

	inbound := InboundMessage{
		SenderOpenID:  senderOpenID,
		ChatID:        msg.ChatID,
		ChatType:      msg.ChatType,
		MessageID:     msg.MessageID,
		MessageType:   msg.MessageType,
		Content:       content,
		SessionKey:    s.sessionKey(senderOpenID, msg),
		ReceiveIDType: receiveIDType,
		ReceiveID:     receiveID,
		RootID:        msg.RootID,
		ParentID:      msg.ParentID,
		ThreadID:      msg.ThreadID,
	}
	return "", &inbound, nil
}

func (s *Service) sessionKey(senderOpenID string, msg eventMessage) string {
	if isGroupChatType(msg.ChatType) {
		if s.cfg.TopicIsolation {
			topic := firstNonEmpty(msg.RootID, msg.ThreadID, msg.MessageID)
			return "feishu:" + msg.ChatID + ":" + topic
		}
		return "feishu:" + msg.ChatID
	}
	return "feishu:" + senderOpenID
}

func (s *Service) groupMessageAllowed(msg eventMessage) bool {
	if s.cfg.GroupPolicy == "open" {
		return true
	}
	return botMentioned(msg, s.cfg.BotOpenID)
}

func (s *Service) isAllowed(openID string) bool {
	if len(s.cfg.AllowOpenIDs) == 0 {
		return true
	}
	for _, allowed := range s.cfg.AllowOpenIDs {
		if allowed == "*" || allowed == openID {
			return true
		}
	}
	return false
}

func (s *Service) verifyToken(token string) error {
	expected := strings.TrimSpace(s.cfg.VerificationToken)
	if expected == "" {
		return nil
	}
	if strings.TrimSpace(token) != expected {
		return errors.New("invalid Feishu verification token")
	}
	return nil
}

func (s *Service) markProcessed(messageID string) bool {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[messageID]; ok {
		return false
	}
	s.seen[messageID] = struct{}{}
	s.processed = append(s.processed, messageID)
	for len(s.processed) > 1000 {
		oldest := s.processed[0]
		s.processed = s.processed[1:]
		delete(s.seen, oldest)
	}
	return true
}

func (s *Service) tenantAccessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	if s.token.value != "" && time.Now().Before(s.token.expiresAt.Add(-2*time.Minute)) {
		token := s.token.value
		s.mu.Unlock()
		return token, nil
	}
	s.mu.Unlock()

	if s.cfg.AppID == "" || s.cfg.AppSecret == "" {
		return "", errors.New("FEISHU_APP_ID and FEISHU_APP_SECRET are required")
	}
	payload := map[string]string{
		"app_id":     s.cfg.AppID,
		"app_secret": s.cfg.AppSecret,
	}
	var resp struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := s.postJSON(ctx, s.cfg.BaseURL+"/open-apis/auth/v3/tenant_access_token/internal", payload, "", &resp); err != nil {
		return "", err
	}
	if resp.Code != 0 || resp.TenantAccessToken == "" {
		return "", fmt.Errorf("fetch Feishu tenant token failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	expires := time.Now().Add(time.Duration(resp.Expire) * time.Second)
	if resp.Expire <= 0 {
		expires = time.Now().Add(90 * time.Minute)
	}

	s.mu.Lock()
	s.token = tenantToken{value: resp.TenantAccessToken, expiresAt: expires}
	s.mu.Unlock()
	return resp.TenantAccessToken, nil
}

func (s *Service) postOpenAPI(ctx context.Context, path string, query string, payload any, target any) error {
	token, err := s.tenantAccessToken(ctx)
	if err != nil {
		return err
	}
	endpoint := s.cfg.BaseURL + path
	if query != "" {
		endpoint += "?" + query
	}
	var resp struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data,omitempty"`
	}
	if err := s.postJSON(ctx, endpoint, payload, token, &resp); err != nil {
		return err
	}
	if resp.Code != 0 {
		return fmt.Errorf("Feishu OpenAPI %s failed: code=%d msg=%s", path, resp.Code, resp.Msg)
	}
	if target != nil && len(resp.Data) > 0 {
		return json.Unmarshal(resp.Data, target)
	}
	return nil
}

func (s *Service) postJSON(ctx context.Context, endpoint string, payload any, bearer string, target any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, defaultMaxEventSize))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Feishu HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if target == nil || len(respBody) == 0 {
		return nil
	}
	return json.Unmarshal(respBody, target)
}

func SessionIDForKey(key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return "feishu-" + hex.EncodeToString(sum[:])[:20]
}

func SessionTitle(msg InboundMessage) string {
	if isGroupChatType(msg.ChatType) && msg.ChatID != "" {
		return "Feishu " + msg.ChatID
	}
	if msg.SenderOpenID != "" {
		return "Feishu " + msg.SenderOpenID
	}
	return "Feishu session"
}

func inferReceiveIDType(receiveID string) string {
	if strings.HasPrefix(receiveID, "oc_") {
		return "chat_id"
	}
	return "open_id"
}

func isGroupChatType(chatType string) bool {
	return chatType == "group" || chatType == "topic_group"
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func envDefault(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on", "enabled":
		return true
	case "0", "false", "no", "off", "disabled":
		return false
	default:
		return fallback
	}
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err == nil {
		return duration
	}
	seconds, err := strconv.Atoi(value)
	if err == nil {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
