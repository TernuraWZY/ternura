package feishu

import (
	"encoding/json"
	"regexp"
	"strings"
)

type urlVerification struct {
	Type      string `json:"type"`
	Token     string `json:"token"`
	Challenge string `json:"challenge"`
}

type eventEnvelope struct {
	Schema    string       `json:"schema,omitempty"`
	Challenge string       `json:"challenge,omitempty"`
	Header    eventHeader  `json:"header"`
	Event     messageEvent `json:"event"`
}

type eventHeader struct {
	EventID   string `json:"event_id"`
	EventType string `json:"event_type"`
	Token     string `json:"token"`
}

type messageEvent struct {
	Sender  eventSender  `json:"sender"`
	Message eventMessage `json:"message"`
}

type eventSender struct {
	SenderType string       `json:"sender_type"`
	SenderID   feishuUserID `json:"sender_id"`
}

type feishuUserID struct {
	OpenID  string `json:"open_id"`
	UserID  string `json:"user_id"`
	UnionID string `json:"union_id"`
}

type eventMessage struct {
	MessageID   string           `json:"message_id"`
	RootID      string           `json:"root_id"`
	ParentID    string           `json:"parent_id"`
	ThreadID    string           `json:"thread_id"`
	ChatID      string           `json:"chat_id"`
	ChatType    string           `json:"chat_type"`
	MessageType string           `json:"message_type"`
	Content     string           `json:"content"`
	Mentions    []messageMention `json:"mentions"`
}

type messageMention struct {
	Key           string       `json:"key"`
	ID            feishuUserID `json:"id"`
	Name          string       `json:"name"`
	MentionedType string       `json:"mentioned_type"`
}

func extractMessageContent(msg eventMessage, botOpenID string) string {
	var content map[string]any
	if msg.Content != "" {
		_ = json.Unmarshal([]byte(msg.Content), &content)
	}
	switch msg.MessageType {
	case "text":
		text, _ := content["text"].(string)
		return strings.TrimSpace(resolveMentions(text, msg.Mentions, botOpenID))
	case "post":
		return strings.TrimSpace(extractPostText(content))
	case "image", "audio", "file", "media":
		return "[" + msg.MessageType + "]"
	case "interactive", "share_chat", "share_user", "share_calendar_event", "merge_forward", "system":
		return strings.TrimSpace(extractStructuredText(content, msg.MessageType))
	default:
		if msg.MessageType != "" {
			return "[" + msg.MessageType + "]"
		}
		return ""
	}
}

func resolveMentions(text string, mentions []messageMention, botOpenID string) string {
	if text == "" || len(mentions) == 0 {
		return text
	}
	for _, mention := range mentions {
		if mention.Key == "" {
			continue
		}
		replacement := strings.TrimSpace(mention.Name)
		if replacement != "" {
			replacement = "@" + replacement
		}
		if isBotMention(mention, botOpenID) {
			replacement = ""
		}
		text = strings.ReplaceAll(text, mention.Key, replacement)
	}
	return strings.Join(strings.Fields(text), " ")
}

func botMentioned(msg eventMessage, botOpenID string) bool {
	if strings.Contains(msg.Content, "@_all") {
		return true
	}
	for _, mention := range msg.Mentions {
		if isBotMention(mention, botOpenID) {
			return true
		}
	}
	return false
}

func isBotMention(mention messageMention, botOpenID string) bool {
	if botOpenID != "" && mention.ID.OpenID == botOpenID {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(mention.MentionedType)) {
	case "app", "bot", "robot":
		return true
	default:
		return botOpenID == "" && len(mention.ID.OpenID) > 0
	}
}

func extractPostText(content map[string]any) string {
	if len(content) == 0 {
		return ""
	}
	root := content
	if post, ok := content["post"].(map[string]any); ok {
		root = post
	}
	for _, key := range []string{"zh_cn", "en_us", "ja_jp"} {
		if block, ok := root[key].(map[string]any); ok {
			if text := extractPostBlock(block); text != "" {
				return text
			}
		}
	}
	if _, ok := root["content"].([]any); ok {
		return extractPostBlock(root)
	}
	return extractStructuredText(root, "post")
}

func extractPostBlock(block map[string]any) string {
	parts := make([]string, 0)
	if title, _ := block["title"].(string); title != "" {
		parts = append(parts, title)
	}
	rows, _ := block["content"].([]any)
	for _, row := range rows {
		items, _ := row.([]any)
		for _, item := range items {
			el, _ := item.(map[string]any)
			tag, _ := el["tag"].(string)
			switch tag {
			case "text", "a", "code_block":
				if text, _ := el["text"].(string); text != "" {
					parts = append(parts, text)
				}
			case "at":
				if name, _ := el["user_name"].(string); name != "" {
					parts = append(parts, "@"+name)
				}
			case "img":
				parts = append(parts, "[image]")
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func extractStructuredText(content map[string]any, fallbackType string) string {
	parts := make([]string, 0)
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				parts = append(parts, typed)
			}
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			for _, key := range []string{"title", "text", "content", "href", "url"} {
				if value, ok := typed[key]; ok {
					walk(value)
				}
			}
			for _, key := range []string{"elements", "fields", "columns", "card", "header"} {
				if value, ok := typed[key]; ok {
					walk(value)
				}
			}
		}
	}
	walk(content)
	if len(parts) == 0 && fallbackType != "" {
		return "[" + fallbackType + "]"
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

var (
	complexMarkdownRE = regexp.MustCompile("(?m)(```|^#{1,6}\\s+|^\\s*[-*+]\\s+|^\\s*\\d+\\.\\s+|\\|.+\\|\\s*$|\\*\\*.+\\*\\*)")
	markdownLinkRE    = regexp.MustCompile(`\[[^\]]+\]\(https?://[^\)]+\)`)
)

func formatMessageContent(content string) (string, string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		payload, err := json.Marshal(map[string]string{"text": ""})
		return "text", string(payload), err
	}
	if shouldUseText(content) {
		payload, err := json.Marshal(map[string]string{"text": content})
		return "text", string(payload), err
	}
	card := map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"elements": []map[string]string{{
			"tag":     "markdown",
			"content": content,
		}},
	}
	payload, err := json.Marshal(card)
	return "interactive", string(payload), err
}

func shouldUseText(content string) bool {
	if len([]rune(content)) > 200 {
		return false
	}
	if complexMarkdownRE.MatchString(content) || markdownLinkRE.MatchString(content) {
		return false
	}
	return true
}
