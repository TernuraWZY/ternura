package feishu

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestServeHTTPURLVerification(t *testing.T) {
	service := NewService(Config{
		Enabled:           true,
		VerificationToken: "verify-token",
	}, nil)
	body := `{"type":"url_verification","token":"verify-token","challenge":"abc123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/feishu/events", strings.NewReader(body))
	rec := httptest.NewRecorder()

	service.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload["challenge"] != "abc123" {
		t.Fatalf("challenge = %q", payload["challenge"])
	}
}

func TestDecodeIncomingTextMessage(t *testing.T) {
	service := NewService(Config{
		Enabled:           true,
		VerificationToken: "verify-token",
		GroupPolicy:       "mention",
		BotOpenID:         "ou_bot",
		TopicIsolation:    true,
	}, nil)
	body := `{
		"schema":"2.0",
		"header":{"event_id":"evt-1","event_type":"im.message.receive_v1","token":"verify-token"},
		"event":{
			"sender":{"sender_type":"user","sender_id":{"open_id":"ou_user"}},
			"message":{
				"message_id":"om_1",
				"chat_id":"oc_group",
				"chat_type":"group",
				"message_type":"text",
				"content":"{\"text\":\"@_user_1 你好\"}",
				"mentions":[{"key":"@_user_1","id":{"open_id":"ou_bot"},"name":"Ternura"}]
			}
		}
	}`

	challenge, inbound, err := service.decodeIncoming([]byte(body))
	if err != nil {
		t.Fatalf("decode incoming: %v", err)
	}
	if challenge != "" {
		t.Fatalf("unexpected challenge %q", challenge)
	}
	if inbound == nil {
		t.Fatalf("expected inbound message")
	}
	if inbound.Content != "你好" {
		t.Fatalf("content = %q", inbound.Content)
	}
	if inbound.SessionKey != "feishu:oc_group:om_1" {
		t.Fatalf("session key = %q", inbound.SessionKey)
	}
	if inbound.ReceiveIDType != "chat_id" || inbound.ReceiveID != "oc_group" {
		t.Fatalf("receive target = %s/%s", inbound.ReceiveIDType, inbound.ReceiveID)
	}
}

func TestDecodeIncomingIgnoresUnmentionedGroupMessage(t *testing.T) {
	service := NewService(Config{
		Enabled:     true,
		GroupPolicy: "mention",
		BotOpenID:   "ou_bot",
	}, nil)
	body := `{
		"schema":"2.0",
		"header":{"event_id":"evt-1","event_type":"im.message.receive_v1"},
		"event":{
			"sender":{"sender_type":"user","sender_id":{"open_id":"ou_user"}},
			"message":{
				"message_id":"om_1",
				"chat_id":"oc_group",
				"chat_type":"group",
				"message_type":"text",
				"content":"{\"text\":\"普通群聊消息\"}"
			}
		}
	}`

	_, inbound, err := service.decodeIncoming([]byte(body))
	if err != nil {
		t.Fatalf("decode incoming: %v", err)
	}
	if inbound != nil {
		t.Fatalf("expected message to be ignored, got %+v", inbound)
	}
}

func TestInboundFromSDKEventTopicGroup(t *testing.T) {
	service := NewService(Config{
		Enabled:        true,
		GroupPolicy:    "mention",
		BotOpenID:      "ou_bot",
		TopicIsolation: true,
	}, nil)
	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderType: ptr("user"),
				SenderId: &larkim.UserId{
					OpenId: ptr("ou_user"),
				},
			},
			Message: &larkim.EventMessage{
				MessageId:   ptr("om_ws"),
				ChatId:      ptr("oc_topic"),
				ChatType:    ptr("topic_group"),
				MessageType: ptr("text"),
				Content:     ptr(`{"text":"@_user_1 hello"}`),
				Mentions: []*larkim.MentionEvent{{
					Key:  ptr("@_user_1"),
					Name: ptr("Ternura"),
					Id: &larkim.UserId{
						OpenId: ptr("ou_bot"),
					},
				}},
			},
		},
	}

	inbound, ok, err := service.inboundFromSDKEvent(event)
	if err != nil {
		t.Fatalf("convert sdk event: %v", err)
	}
	if !ok || inbound == nil {
		t.Fatalf("expected inbound message")
	}
	if inbound.Content != "hello" {
		t.Fatalf("content = %q", inbound.Content)
	}
	if inbound.ReceiveIDType != "chat_id" || inbound.ReceiveID != "oc_topic" {
		t.Fatalf("receive target = %s/%s", inbound.ReceiveIDType, inbound.ReceiveID)
	}
	if inbound.SessionKey != "feishu:oc_topic:om_ws" {
		t.Fatalf("session key = %q", inbound.SessionKey)
	}
}

func TestFormatMessageContentUsesCardForMarkdown(t *testing.T) {
	msgType, content, err := formatMessageContent("## 标题\n\n- item")
	if err != nil {
		t.Fatalf("format content: %v", err)
	}
	if msgType != "interactive" {
		t.Fatalf("msg type = %q", msgType)
	}
	if !strings.Contains(content, `"markdown"`) || !strings.Contains(content, "## 标题") {
		t.Fatalf("interactive content = %s", content)
	}
}

func TestSessionIDForKeyIsDeterministic(t *testing.T) {
	first := SessionIDForKey("feishu:oc_group:om_1")
	second := SessionIDForKey("feishu:oc_group:om_1")
	if first != second {
		t.Fatalf("session ids differ: %q vs %q", first, second)
	}
	if !strings.HasPrefix(first, "feishu-") {
		t.Fatalf("session id = %q", first)
	}
}

func ptr(value string) *string {
	return &value
}
