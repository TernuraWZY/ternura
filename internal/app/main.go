package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/joho/godotenv"

	"ternura/agent"
	"ternura/config"
	"ternura/internal/cron"
	"ternura/internal/feishu"
	"ternura/tool"
)

func Run() {
	_ = godotenv.Load()

	query := flag.String("q", "hello", "prompt text")
	serve := flag.Bool("serve", false, "run web console")
	addr := flag.String("addr", ":8080", "web server address")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	modelConf := config.NewModelConfig()

	if *serve {
		server := newAgentServer(modelConf)
		go newCronRunner(server).Run(ctx)
		if server.feishu.WebSocketEnabled() {
			go server.feishu.StartWebSocket(ctx)
		}
		log.Printf("serving Ternura console on http://localhost%s", *addr)
		if err := http.ListenAndServe(*addr, server.routes()); err != nil {
			log.Fatalf("server error: %v", err)
		}
		return
	}

	cliAgent := agent.NewAgent(modelConf, agent.TernuraAgentSystemPrompt, newAgentTools(nil, nil, nil, tool.NewCronTool(nil, nil, nil)))
	result, err := cliAgent.RunWithTrace(ctx, *query)
	if err != nil {
		log.Printf("agent run error: %v", err)
		return
	}

	for _, item := range result.Trace {
		log.Printf("agent trace [%s] %s:\n%s", item.Type, item.Title, item.Content)
	}
	log.Printf("agent result: %s", result.Content)
}

type agentServer struct {
	modelConf config.ModelConfig
	mu        sync.Mutex
	agent     *agent.Agent
	store     *sessionStore
	memory    *memoryStore
	cron      *cron.Service
	cronTool  *tool.CronTool
	cronWake  chan struct{}
	feishu    *feishu.Service
}

type scheduleCreateRequest struct {
	Title        string `json:"title,omitempty"`
	Prompt       string `json:"prompt"`
	RunAt        string `json:"run_at,omitempty"`
	DelaySeconds int    `json:"delay_seconds,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
}

type scheduleCancelRequest struct {
	ID string `json:"id"`
}

type schedulesResponse struct {
	CurrentSessionID string            `json:"current_session_id"`
	Tasks            []cron.LegacyTask `json:"tasks"`
}

type chatRequest struct {
	Message string `json:"message"`
}

type memoryDeleteRequest struct {
	ID string `json:"id"`
}

type chatResponse struct {
	Content    string                 `json:"content,omitempty"`
	Trace      []agent.AgentTraceItem `json:"trace,omitempty"`
	RawContent string                 `json:"raw_content,omitempty"`
	RunID      string                 `json:"run_id,omitempty"`
	Status     string                 `json:"status,omitempty"`
	StartedAt  string                 `json:"started_at,omitempty"`
	FinishedAt string                 `json:"finished_at,omitempty"`
	DurationMS int64                  `json:"duration_ms,omitempty"`
	Error      string                 `json:"error,omitempty"`
}

func newAgentServer(modelConf config.ModelConfig) *agentServer {
	s := &agentServer{
		modelConf: modelConf,
		store:     newSessionStore(defaultSessionPath),
		cronWake:  make(chan struct{}, 1),
	}
	s.memory = newMemoryStore(s.store.root)
	s.cron = cron.NewService(s.store.root)
	s.cronTool = tool.NewCronTool(s.cronAdd, s.cronList, s.cronRemove)
	feishuConfig := feishu.NewConfigFromEnv()
	s.feishu = feishu.NewService(feishuConfig, s.handleFeishuMessage)
	if err := s.store.Load(); err != nil {
		log.Printf("load persisted session: %v", err)
	}
	if err := s.cron.Load(); err != nil {
		log.Printf("load cron jobs: %v", err)
	}
	s.resetAgentFromHistory()
	return s
}

func (s *agentServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/history", s.handleHistory)
	mux.HandleFunc("/api/memory", s.handleMemory)
	mux.HandleFunc("/api/memory/status", s.handleMemoryStatus)
	mux.HandleFunc("/api/schedules", s.handleSchedules)
	mux.Handle("/api/feishu/events", s.feishu)
	mux.HandleFunc("/api/session", s.handleSessionDetail)
	mux.Handle("/", noCache(http.FileServer(http.Dir("web"))))
	return mux
}

func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *agentServer) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, chatResponse{Error: "invalid request"})
		return
	}
	if req.Message == "" {
		writeJSON(w, http.StatusBadRequest, chatResponse{Error: "message is required"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	run := newRunLifecycle()
	logRunStart(run)
	if err := s.store.StartRun(run, req.Message); err != nil {
		log.Printf("persist run start %s: %v", run.ID, err)
	}

	if result, handled, err := s.tryScheduleShortcut(r.Context(), req.Message); handled {
		finished := time.Now()
		status := runStatusSucceeded
		httpStatus := http.StatusOK
		if err != nil {
			status = runStatusFailed
			httpStatus = http.StatusBadGateway
			result = agent.AgentRunResult{Content: err.Error()}
		}
		logRunFinish(run, status, finished)
		if persistErr := s.store.FinishRun(run, req.Message, result, status, finished, err); persistErr != nil {
			log.Printf("persist schedule shortcut run %s: %v", run.ID, persistErr)
		}
		resp := chatResponse{Content: result.Content, Trace: result.Trace, RawContent: result.RawContent}
		if err != nil {
			resp.Error = err.Error()
		}
		applyRunFields(&resp, run, status, finished)
		writeJSON(w, httpStatus, resp)
		return
	}

	result, err := s.agent.RunWithTrace(r.Context(), req.Message)
	if err != nil {
		finished := time.Now()
		logRunFinish(run, runStatusFailed, finished)
		if persistErr := s.store.FinishRun(run, req.Message, result, runStatusFailed, finished, err); persistErr != nil {
			log.Printf("persist failed run %s: %v", run.ID, persistErr)
		}
		resp := chatResponse{Error: err.Error()}
		applyRunFields(&resp, run, runStatusFailed, finished)
		writeJSON(w, http.StatusBadGateway, resp)
		return
	}
	finished := time.Now()
	logRunFinish(run, runStatusSucceeded, finished)
	if err := s.store.FinishRun(run, req.Message, result, runStatusSucceeded, finished, nil); err != nil {
		log.Printf("persist completed run %s: %v", run.ID, err)
	}
	resp := chatResponse{
		Content:    result.Content,
		Trace:      result.Trace,
		RawContent: result.RawContent,
	}
	applyRunFields(&resp, run, runStatusSucceeded, finished)
	writeJSON(w, http.StatusOK, resp)
}

func (s *agentServer) handleChatStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.Message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	s.mu.Lock()
	defer s.mu.Unlock()

	run := newRunLifecycle()
	logRunStart(run)
	if err := s.store.StartRun(run, req.Message); err != nil {
		log.Printf("persist run start %s: %v", run.ID, err)
	}

	streamer := newSmoothStreamWriter(r.Context(), w, flusher)
	defer func() {
		_ = streamer.Close()
	}()
	emit := func(event agent.AgentStreamEvent) error {
		event.RunID = run.ID
		return streamer.Emit(event)
	}

	if err := emit(run.startEvent()); err != nil {
		return
	}
	if err := emit(agent.AgentStreamEvent{
		Type:      eventTypeStart,
		Status:    runStatusRunning,
		StartedAt: run.StartedAt.Format(time.RFC3339Nano),
	}); err != nil {
		return
	}

	if result, handled, err := s.tryScheduleShortcut(r.Context(), req.Message); handled {
		finished := time.Now()
		status := runStatusSucceeded
		eventType := eventTypeRunDone
		if err != nil {
			status = runStatusFailed
			eventType = eventTypeRunFailed
			result = agent.AgentRunResult{Content: err.Error(), RawContent: err.Error()}
		} else if emitErr := emitScheduleShortcutResult(emit, result); emitErr != nil {
			return
		}
		logRunFinish(run, status, finished)
		if persistErr := s.store.FinishRun(run, req.Message, result, status, finished, err); persistErr != nil {
			log.Printf("persist schedule shortcut run %s: %v", run.ID, persistErr)
		}
		_ = emit(run.finishEvent(eventType, status, finished, err))
		if err != nil {
			_ = emit(agent.AgentStreamEvent{Type: eventTypeError, Error: err.Error()})
		}
		return
	}

	result, err := s.agent.RunStreaming(r.Context(), req.Message, emit)
	if err != nil {
		status := runStatusFailed
		eventType := eventTypeRunFailed
		if r.Context().Err() != nil {
			status = runStatusCancelled
			eventType = eventTypeRunCancelled
		}
		finished := time.Now()
		logRunFinish(run, status, finished)
		if persistErr := s.store.FinishRun(run, req.Message, result, status, finished, err); persistErr != nil {
			log.Printf("persist %s run %s: %v", status, run.ID, persistErr)
		}
		_ = emit(run.finishEvent(eventType, status, finished, err))
		if status == runStatusFailed {
			_ = emit(agent.AgentStreamEvent{
				Type:  eventTypeError,
				Error: err.Error(),
			})
		}
		return
	}

	finished := time.Now()
	logRunFinish(run, runStatusSucceeded, finished)
	if err := s.store.FinishRun(run, req.Message, result, runStatusSucceeded, finished, nil); err != nil {
		log.Printf("persist completed run %s: %v", run.ID, err)
	}
	_ = emit(run.finishEvent(eventTypeRunDone, runStatusSucceeded, finished, nil))
}

const (
	defaultStreamChunkRunes = 3
	defaultStreamIntervalMS = 35
	streamChunkRunesEnvKey  = "TERNURA_STREAM_CHUNK_RUNES"
	streamIntervalMSEnvKey  = "TERNURA_STREAM_INTERVAL_MS"
	traceTypeThink          = "think"
	eventTypeStart          = "start"
	eventTypeError          = "error"
	eventTypeRunStart       = "run_start"
	eventTypeRunDone        = "run_done"
	eventTypeRunFailed      = "run_failed"
	eventTypeRunCancelled   = "run_cancelled"
	eventTypeContentDelta   = "content_delta"
	eventTypeTraceStart     = "trace_start"
	eventTypeTraceDelta     = "trace_delta"
	eventTypeTraceDone      = "trace_done"
	runStatusRunning        = "running"
	runStatusSucceeded      = "succeeded"
	runStatusFailed         = "failed"
	runStatusCancelled      = "cancelled"
)

var runSequence uint64

type runLifecycle struct {
	ID        string
	StartedAt time.Time
}

func newRunLifecycle() runLifecycle {
	startedAt := time.Now()
	sequence := atomic.AddUint64(&runSequence, 1)
	return runLifecycle{
		ID:        fmt.Sprintf("run-%s-%04d", startedAt.UTC().Format("20060102T150405"), sequence),
		StartedAt: startedAt,
	}
}

func (r runLifecycle) startEvent() agent.AgentStreamEvent {
	return agent.AgentStreamEvent{
		Type:      eventTypeRunStart,
		RunID:     r.ID,
		Status:    runStatusRunning,
		StartedAt: r.StartedAt.Format(time.RFC3339Nano),
	}
}

func (r runLifecycle) finishEvent(eventType string, status string, finishedAt time.Time, runErr error) agent.AgentStreamEvent {
	event := agent.AgentStreamEvent{
		Type:       eventType,
		RunID:      r.ID,
		Status:     status,
		StartedAt:  r.StartedAt.Format(time.RFC3339Nano),
		FinishedAt: finishedAt.Format(time.RFC3339Nano),
		DurationMS: durationMillis(r.StartedAt, finishedAt),
	}
	if runErr != nil {
		event.Error = runErr.Error()
	}
	return event
}

func applyRunFields(resp *chatResponse, run runLifecycle, status string, finishedAt time.Time) {
	resp.RunID = run.ID
	resp.Status = status
	resp.StartedAt = run.StartedAt.Format(time.RFC3339Nano)
	resp.FinishedAt = finishedAt.Format(time.RFC3339Nano)
	resp.DurationMS = durationMillis(run.StartedAt, finishedAt)
}

func durationMillis(startedAt time.Time, finishedAt time.Time) int64 {
	duration := finishedAt.Sub(startedAt).Milliseconds()
	if duration < 0 {
		return 0
	}
	return duration
}

func logRunStart(run runLifecycle) {
	log.Printf("run %s started at %s", run.ID, run.StartedAt.Format(time.RFC3339Nano))
}

func logRunFinish(run runLifecycle, status string, finishedAt time.Time) {
	log.Printf("run %s finished status=%s duration_ms=%d", run.ID, status, durationMillis(run.StartedAt, finishedAt))
}

type smoothStreamWriter struct {
	ctx            context.Context
	w              http.ResponseWriter
	flusher        http.Flusher
	chunkRunes     int
	interval       time.Duration
	events         chan agent.AgentStreamEvent
	done           chan struct{}
	closeOnce      sync.Once
	errMu          sync.Mutex
	err            error
	lastSmoothSent time.Time
	traceTypes     map[string]string
}

func newSmoothStreamWriter(ctx context.Context, w http.ResponseWriter, flusher http.Flusher) *smoothStreamWriter {
	chunkRunes := envInt(streamChunkRunesEnvKey, defaultStreamChunkRunes)
	if chunkRunes < 1 {
		chunkRunes = defaultStreamChunkRunes
	}

	intervalMS := envInt(streamIntervalMSEnvKey, defaultStreamIntervalMS)
	if intervalMS < 0 {
		intervalMS = defaultStreamIntervalMS
	}

	streamer := &smoothStreamWriter{
		ctx:        ctx,
		w:          w,
		flusher:    flusher,
		chunkRunes: chunkRunes,
		interval:   time.Duration(intervalMS) * time.Millisecond,
		events:     make(chan agent.AgentStreamEvent, 128),
		done:       make(chan struct{}),
		traceTypes: make(map[string]string),
	}
	go streamer.run()
	return streamer
}

func (s *smoothStreamWriter) Emit(event agent.AgentStreamEvent) error {
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case <-s.done:
		return s.getErr()
	case s.events <- event:
		return nil
	}
}

func (s *smoothStreamWriter) Close() error {
	s.closeOnce.Do(func() {
		close(s.events)
	})
	<-s.done
	return s.getErr()
}

func (s *smoothStreamWriter) run() {
	defer close(s.done)
	for event := range s.events {
		if err := s.writeEvent(event); err != nil {
			s.setErr(err)
			return
		}
	}
}

func (s *smoothStreamWriter) writeEvent(event agent.AgentStreamEvent) error {
	switch event.Type {
	case eventTypeContentDelta:
		return s.emitDelta(event, true)
	case eventTypeTraceStart:
		s.traceTypes[event.ID] = event.TraceType
		return writeSSE(s.w, s.flusher, event)
	case eventTypeTraceDelta:
		return s.emitDelta(event, s.traceTypes[event.ID] == traceTypeThink)
	case eventTypeTraceDone:
		delete(s.traceTypes, event.ID)
		return writeSSE(s.w, s.flusher, event)
	default:
		return writeSSE(s.w, s.flusher, event)
	}
}

func (s *smoothStreamWriter) setErr(err error) {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	s.err = err
}

func (s *smoothStreamWriter) getErr() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.err
}

func (s *smoothStreamWriter) emitDelta(event agent.AgentStreamEvent, smooth bool) error {
	if event.Delta == "" {
		return nil
	}
	if !smooth || s.interval == 0 {
		return writeSSE(s.w, s.flusher, event)
	}

	chunks := chunkStringByRunes(event.Delta, s.chunkRunes)
	for _, chunk := range chunks {
		if err := s.waitForCadence(); err != nil {
			return err
		}
		next := event
		next.Delta = chunk
		if err := writeSSE(s.w, s.flusher, next); err != nil {
			return err
		}
		s.lastSmoothSent = time.Now()
	}
	return nil
}

func (s *smoothStreamWriter) waitForCadence() error {
	if s.interval == 0 || s.lastSmoothSent.IsZero() {
		return nil
	}

	delay := s.interval - time.Since(s.lastSmoothSent)
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case <-timer.C:
		return nil
	}
}

func chunkStringByRunes(value string, chunkRunes int) []string {
	if value == "" {
		return nil
	}
	if chunkRunes < 1 {
		chunkRunes = 1
	}

	runes := []rune(value)
	chunks := make([]string, 0, (len(runes)+chunkRunes-1)/chunkRunes)
	for start := 0; start < len(runes); start += chunkRunes {
		end := start + chunkRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
	}
	return chunks
}

func envInt(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func (s *agentServer) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	snapshot := s.store.Snapshot()
	writeHistoryJSON(w, http.StatusOK, historyFromSnapshot(snapshot))
}

func (s *agentServer) handleSessionDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	snapshot := s.store.Snapshot()
	detail, err := sessionDetailFromSnapshot(snapshot, r.URL.Query().Get("session_id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeSessionDetailJSON(w, http.StatusOK, detail)
}

func (s *agentServer) handleMemoryStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		sessionID = s.store.CurrentSessionID()
	}
	status, err := s.memory.Status(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeMemoryStatusJSON(w, http.StatusOK, status)
}

func (s *agentServer) handleMemory(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		sessionID := r.URL.Query().Get("session_id")
		if sessionID == "" {
			sessionID = s.store.CurrentSessionID()
		}
		detail, err := s.memory.Detail(sessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeMemoryDetailJSON(w, http.StatusOK, detail)
	case http.MethodDelete:
		var req memoryDeleteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := s.memory.Forget(r.Context(), req.ID); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		status, err := s.memory.Status(s.store.CurrentSessionID())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeMemoryStatusJSON(w, http.StatusOK, status)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *agentServer) handleSchedules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeSchedulesJSON(w, http.StatusOK, s.schedulesResponse())
	case http.MethodPost:
		var req scheduleCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		sessionID := req.SessionID
		if sessionID == "" {
			sessionID = s.store.CurrentSessionID()
		}
		if !s.sessionExists(sessionID) {
			http.Error(w, fmt.Sprintf("session %q not found", sessionID), http.StatusNotFound)
			return
		}
		if _, err := s.cron.Add(r.Context(), cron.AddParams{
			Name:           req.Title,
			Message:        req.Prompt,
			SessionID:      sessionID,
			At:             req.RunAt,
			DelaySeconds:   req.DelaySeconds,
			DeleteAfterRun: true,
			Deliver:        true,
		}); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.wakeCronRunner()
		writeSchedulesJSON(w, http.StatusCreated, s.schedulesResponse())
	case http.MethodDelete:
		var req scheduleCancelRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if _, err := s.cron.Cancel(r.Context(), req.ID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.wakeCronRunner()
		writeSchedulesJSON(w, http.StatusOK, s.schedulesResponse())
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *agentServer) schedulesResponse() schedulesResponse {
	return schedulesResponse{
		CurrentSessionID: s.store.CurrentSessionID(),
		Tasks:            s.cron.LegacySnapshot(),
	}
}

func (s *agentServer) sessionExists(sessionID string) bool {
	snapshot := s.store.Snapshot()
	return findSession(snapshot.Sessions, sessionID) != nil
}

func (s *agentServer) handleSelectSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req selectSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.SessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	snapshot, err := s.store.SelectSession(req.SessionID)
	if err == nil {
		s.resetAgentFromSnapshot(snapshot)
	}
	s.mu.Unlock()

	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeHistoryJSON(w, http.StatusOK, historyFromSnapshot(snapshot))
}

func (s *agentServer) handleReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	s.mu.Lock()
	snapshot, err := s.store.NewSession()
	if err != nil {
		log.Printf("create new session: %v", err)
	}
	s.resetAgent()
	s.mu.Unlock()

	if err != nil {
		writeJSON(w, http.StatusInternalServerError, chatResponse{Error: "failed to create session"})
		return
	}
	writeHistoryJSON(w, http.StatusOK, historyFromSnapshot(snapshot))
}

func (s *agentServer) resetAgent() {
	s.agent = agent.NewAgent(
		s.modelConf,
		agent.TernuraAgentSystemPrompt,
		newAgentTools(s.updateTodos, s.rememberMemory, s.forgetMemory, s.cronTool),
		agent.WithHooks(
			newCurrentTimeHook(),
			newMemoryHook(s.memory, s.store.CurrentSessionID),
			newScheduleGuidanceHook(),
			newStateGuardHook(s.cron),
		),
	)
}

func (s *agentServer) resetAgentFromHistory() {
	s.resetAgentFromSnapshot(s.store.Snapshot())
}

func (s *agentServer) resetAgentFromSnapshot(snapshot sessionSnapshot) {
	s.resetAgent()
	session, ok := currentSessionFromSnapshot(snapshot)
	if !ok || len(session.Messages) == 0 {
		return
	}
	restoreAgentConversation(s.agent, session.Messages)
	log.Printf("restored %d persisted conversation messages from %s", len(session.Messages), session.SessionID)
}

func restoreAgentConversation(agentInstance *agent.Agent, persisted []persistedMessage) {
	messages := make([]agent.ConversationMessage, 0, len(persisted))
	for _, message := range persisted {
		messages = append(messages, agent.ConversationMessage{
			Role:    message.Role,
			Content: message.Content,
		})
	}
	agentInstance.RestoreConversation(messages)
}

func writeJSON(w http.ResponseWriter, status int, value chatResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write json response: %v", err)
	}
}

func writeHistoryJSON(w http.ResponseWriter, status int, value historyResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write history response: %v", err)
	}
}

func writeSessionDetailJSON(w http.ResponseWriter, status int, value sessionDetailResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write session detail response: %v", err)
	}
}

func writeMemoryStatusJSON(w http.ResponseWriter, status int, value memoryStatusResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write memory status response: %v", err)
	}
}

func writeMemoryDetailJSON(w http.ResponseWriter, status int, value memoryDetailResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write memory detail response: %v", err)
	}
}

func writeSchedulesJSON(w http.ResponseWriter, status int, value schedulesResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write schedules response: %v", err)
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, event agent.AgentStreamEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}
