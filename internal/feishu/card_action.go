package feishu

import (
	"context"
	"encoding/json"
	"strings"
)

type CardAction struct {
	EventID        string
	MessageID      string
	ChatID         string
	OperatorOpenID string
	Value          map[string]any
}

type CardActionResponse struct {
	ToastType    string
	ToastContent string
	Card         any
}

type CardActionHandlerFunc func(context.Context, CardAction) (CardActionResponse, error)

func (s *Service) SetCardActionHandler(handler CardActionHandlerFunc) {
	if s == nil {
		return
	}
	s.handleCardAction = handler
}

func (s *Service) decodeCardAction(body []byte) (*CardAction, bool, error) {
	var envelope struct {
		Header eventHeader `json:"header"`
		Event  struct {
			Operator struct {
				OpenID string `json:"open_id"`
			} `json:"operator"`
			Action struct {
				Value map[string]any `json:"value"`
			} `json:"action"`
			Context struct {
				OpenMessageID string `json:"open_message_id"`
				OpenChatID    string `json:"open_chat_id"`
			} `json:"context"`
		} `json:"event"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, false, nil
	}
	if envelope.Header.EventType != "card.action.trigger" {
		return nil, false, nil
	}
	if err := s.verifyToken(envelope.Header.Token); err != nil {
		return nil, true, err
	}
	return &CardAction{
		EventID:        envelope.Header.EventID,
		MessageID:      envelope.Event.Context.OpenMessageID,
		ChatID:         envelope.Event.Context.OpenChatID,
		OperatorOpenID: envelope.Event.Operator.OpenID,
		Value:          envelope.Event.Action.Value,
	}, true, nil
}

func (s *Service) dispatchCardAction(ctx context.Context, action CardAction) (CardActionResponse, error) {
	if !s.isAllowed(action.OperatorOpenID) {
		return CardActionResponse{ToastType: "error", ToastContent: "你没有权限执行这个操作"}, nil
	}
	if action.EventID != "" && !s.markProcessed("card:"+action.EventID) {
		return CardActionResponse{ToastType: "info", ToastContent: "这个操作已经处理过了"}, nil
	}
	if s.handleCardAction == nil {
		return CardActionResponse{ToastType: "error", ToastContent: "当前没有可用的卡片操作"}, nil
	}
	return s.handleCardAction(ctx, action)
}

func cardActionCallbackPayload(response CardActionResponse) map[string]any {
	payload := make(map[string]any)
	if strings.TrimSpace(response.ToastContent) != "" {
		toastType := strings.TrimSpace(response.ToastType)
		if toastType == "" {
			toastType = "info"
		}
		payload["toast"] = map[string]string{
			"type":    toastType,
			"content": response.ToastContent,
		}
	}
	if response.Card != nil {
		payload["card"] = map[string]any{
			"type": "card_json",
			"data": response.Card,
		}
	}
	return payload
}
