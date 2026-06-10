package app

import (
	"context"
	"log"
	"strings"

	"ternura/agent"
	"ternura/internal/cron"
	"ternura/internal/feishu"
	"ternura/tool"
)

func (s *agentServer) handleFeishuMessage(ctx context.Context, msg feishu.InboundMessage) (feishu.Reply, error) {
	sessionID := feishu.SessionIDForKey(msg.SessionKey)
	delivery := feishuDeliveryTarget(msg)

	s.mu.Lock()
	defer s.mu.Unlock()

	if messageRequestsNewSession(msg.Content) {
		return s.resetFeishuSession(ctx, sessionID, msg)
	}

	if _, err := s.store.EnsureSession(sessionID, feishu.SessionTitle(msg)); err != nil {
		return feishu.Reply{}, err
	}

	session := s.newAgentSession(sessionID, nil)

	if result, handled, err := s.tryScheduleShortcutForSession(ctx, msg.Content, sessionID, delivery); handled {
		outcome := session.run(ctx, agentSessionRunRequest{
			Kind:           agentSessionRunUser,
			DisplayMessage: msg.Content,
			DirectResult:   &result,
			DirectErr:      err,
		})
		return formatFeishuOutcome(outcome)
	}

	cronTool := tool.NewCronTool(
		s.cronAddForSessionWithDelivery(sessionID, delivery),
		s.cronList,
		s.cronRemove,
	)
	session.cronTool = cronTool
	outcome := session.run(ctx, agentSessionRunRequest{
		Kind:           agentSessionRunUser,
		DisplayMessage: msg.Content,
	})
	return formatFeishuOutcome(outcome)
}

func (s *agentServer) resetFeishuSession(_ context.Context, sessionID string, msg feishu.InboundMessage) (feishu.Reply, error) {
	if _, err := s.store.ResetSession(sessionID, feishu.SessionTitle(msg)); err != nil {
		return feishu.Reply{}, err
	}
	if s.memory != nil {
		if err := s.memory.ResetSession(sessionID); err != nil {
			return feishu.Reply{}, err
		}
	}
	s.resetAgentFromSnapshot(s.store.Snapshot())

	session := s.newAgentSession(sessionID, nil)
	result := agent.AgentRunResult{
		Content: "新会话已开始。这个飞书会话的历史消息、短期记忆、工具记忆和待办都已清空。",
	}
	outcome := session.run(context.Background(), agentSessionRunRequest{
		Kind:           agentSessionRunUser,
		DisplayMessage: msg.Content,
		DirectResult:   &result,
		OmitMessages:   true,
	})
	return formatFeishuOutcome(outcome)
}

func messageRequestsNewSession(content string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(content)), " "))
	normalized = strings.Trim(normalized, "。！？!?.,， ")
	switch normalized {
	case "new session", "new chat", "reset session", "reset chat",
		"新会话", "开启新会话", "开始新会话", "新对话", "开启新对话", "重新开始", "清空会话":
		return true
	default:
		return false
	}
}

func formatFeishuOutcome(outcome agentSessionRunOutcome) (feishu.Reply, error) {
	reply := formatFeishuAgentReply(outcome.Result)
	if outcome.Err != nil {
		if strings.TrimSpace(reply.Content) != "" || reply.Card != nil {
			log.Printf("feishu agent turn failed for run %s: %v", outcome.Run.ID, outcome.Err)
			return reply, nil
		}
		return feishu.Reply{}, outcome.Err
	}
	return reply, nil
}

func feishuDeliveryTarget(msg feishu.InboundMessage) *cron.DeliveryTarget {
	if msg.ReceiveID == "" {
		return nil
	}
	return &cron.DeliveryTarget{
		Channel:       "feishu",
		ReceiveIDType: msg.ReceiveIDType,
		ReceiveID:     msg.ReceiveID,
		MessageID:     msg.MessageID,
		ThreadID:      msg.ThreadID,
	}
}

func (s *agentServer) deliverCronResult(ctx context.Context, job cron.Job, result agent.AgentRunResult) {
	if job.Payload.Delivery == nil || result.Content == "" {
		return
	}
	switch job.Payload.Delivery.Channel {
	case "feishu":
		if s.feishu == nil || !s.feishu.Enabled() {
			return
		}
		reply := formatFeishuAgentReply(result)
		err := s.feishu.Send(ctx, feishu.OutboundMessage{
			ReceiveIDType: job.Payload.Delivery.ReceiveIDType,
			ReceiveID:     job.Payload.Delivery.ReceiveID,
			MessageID:     job.Payload.Delivery.MessageID,
			ThreadID:      job.Payload.Delivery.ThreadID,
			Content:       reply.Content,
			Card:          reply.Card,
			Reply:         job.Payload.Delivery.ThreadID != "",
		})
		if err != nil {
			log.Printf("deliver cron job %s to feishu: %v", job.ID, err)
		}
	}
}
