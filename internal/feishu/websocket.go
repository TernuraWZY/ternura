package feishu

import (
	"context"
	"log"

	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

func (s *Service) StartWebSocket(ctx context.Context) {
	if !s.WebSocketEnabled() {
		return
	}
	if s.cfg.AppID == "" || s.cfg.AppSecret == "" {
		log.Printf("feishu websocket disabled: FEISHU_APP_ID and FEISHU_APP_SECRET are required")
		return
	}

	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			inbound, ok, err := s.inboundFromSDKEvent(event)
			if err != nil {
				log.Printf("feishu websocket event rejected: %v", err)
				return nil
			}
			if !ok || inbound == nil {
				return nil
			}
			if !s.markProcessed(inbound.MessageID) {
				return nil
			}
			go s.process(context.Background(), *inbound)
			return nil
		})

	client := larkws.NewClient(
		s.cfg.AppID,
		s.cfg.AppSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithDomain(s.cfg.BaseURL),
		larkws.WithLogLevel(larkcore.LogLevelWarn),
		larkws.WithOnReady(func() {
			log.Printf("feishu websocket connected")
		}),
		larkws.WithOnError(func(err error) {
			log.Printf("feishu websocket error: %v", err)
		}),
		larkws.WithOnReconnecting(func() {
			log.Printf("feishu websocket reconnecting")
		}),
		larkws.WithOnReconnected(func() {
			log.Printf("feishu websocket reconnected")
		}),
		larkws.WithOnDisconnected(func() {
			log.Printf("feishu websocket disconnected")
		}),
	)

	log.Printf("starting feishu websocket long connection")
	if err := client.Start(ctx); err != nil && ctx.Err() == nil {
		log.Printf("feishu websocket stopped: %v", err)
	}
}

func (s *Service) inboundFromSDKEvent(event *larkim.P2MessageReceiveV1) (*InboundMessage, bool, error) {
	if event == nil || event.Event == nil || event.Event.Message == nil {
		return nil, false, nil
	}
	sender := event.Event.Sender
	msg := event.Event.Message
	if sender != nil && stringValue(sender.SenderType) == "bot" {
		return nil, false, nil
	}

	senderOpenID := "unknown"
	if sender != nil && sender.SenderId != nil && stringValue(sender.SenderId.OpenId) != "" {
		senderOpenID = stringValue(sender.SenderId.OpenId)
	}
	if !s.isAllowed(senderOpenID) {
		return nil, false, nil
	}

	converted := eventMessage{
		MessageID:   stringValue(msg.MessageId),
		RootID:      stringValue(msg.RootId),
		ParentID:    stringValue(msg.ParentId),
		ThreadID:    stringValue(msg.ThreadId),
		ChatID:      stringValue(msg.ChatId),
		ChatType:    stringValue(msg.ChatType),
		MessageType: stringValue(msg.MessageType),
		Content:     stringValue(msg.Content),
		Mentions:    convertSDKMentions(msg.Mentions),
	}
	if converted.MessageID == "" {
		return nil, false, nil
	}
	if isGroupChatType(converted.ChatType) && !s.groupMessageAllowed(converted) {
		return nil, false, nil
	}
	content := extractMessageContent(converted, s.cfg.BotOpenID)
	if content == "" {
		return nil, false, nil
	}

	receiveIDType := "open_id"
	receiveID := senderOpenID
	if isGroupChatType(converted.ChatType) {
		receiveIDType = "chat_id"
		receiveID = converted.ChatID
	}
	return &InboundMessage{
		SenderOpenID:  senderOpenID,
		ChatID:        converted.ChatID,
		ChatType:      converted.ChatType,
		MessageID:     converted.MessageID,
		MessageType:   converted.MessageType,
		Content:       content,
		SessionKey:    s.sessionKey(senderOpenID, converted),
		ReceiveIDType: receiveIDType,
		ReceiveID:     receiveID,
		RootID:        converted.RootID,
		ParentID:      converted.ParentID,
		ThreadID:      converted.ThreadID,
	}, true, nil
}

func convertSDKMentions(mentions []*larkim.MentionEvent) []messageMention {
	out := make([]messageMention, 0, len(mentions))
	for _, mention := range mentions {
		if mention == nil {
			continue
		}
		item := messageMention{
			Key:           stringValue(mention.Key),
			Name:          stringValue(mention.Name),
			MentionedType: stringValue(mention.MentionedType),
		}
		if mention.Id != nil {
			item.ID = feishuUserID{
				OpenID:  stringValue(mention.Id.OpenId),
				UserID:  stringValue(mention.Id.UserId),
				UnionID: stringValue(mention.Id.UnionId),
			}
		}
		out = append(out, item)
	}
	return out
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
