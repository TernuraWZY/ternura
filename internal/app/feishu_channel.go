package app

import (
	"context"
	"log"
	"strings"
	"time"

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

	run := newRunLifecycle()
	logRunStart(run)
	if err := s.store.StartRunForSession(sessionID, run, msg.Content); err != nil {
		log.Printf("persist feishu run start %s: %v", run.ID, err)
	}

	if result, handled, err := s.tryScheduleShortcutForSession(ctx, msg.Content, sessionID, delivery); handled {
		return s.finishFeishuRun(sessionID, run, msg.Content, result, err)
	}

	cronTool := tool.NewCronTool(
		s.cronAddForSessionWithDelivery(sessionID, delivery),
		s.cronList,
		s.cronRemove,
	)
	agent := s.newAgentForSessionWithCron(sessionID, cronTool)
	result, err := agent.RunWithTrace(ctx, msg.Content)
	return s.finishFeishuRun(sessionID, run, msg.Content, result, err)
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

	run := newRunLifecycle()
	logRunStart(run)
	if err := s.store.StartRunForSession(sessionID, run, msg.Content); err != nil {
		log.Printf("persist feishu reset run start %s: %v", run.ID, err)
	}
	result := agent.AgentRunResult{
		Content: "新会话已开始。这个飞书会话的历史消息、短期记忆、工具记忆和待办都已清空。",
	}
	return s.finishFeishuResetRun(sessionID, run, msg.Content, result)
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

func (s *agentServer) finishFeishuRun(sessionID string, run runLifecycle, message string, result agent.AgentRunResult, runErr error) (feishu.Reply, error) {
	finished := time.Now()
	status := runStatusSucceeded
	if runErr != nil {
		status = runStatusFailed
	}
	logRunFinish(run, status, finished)
	if err := s.store.FinishRunForSession(sessionID, run, message, result, status, finished, runErr); err != nil {
		log.Printf("persist feishu run %s: %v", run.ID, err)
	}
	if runErr != nil {
		return formatFeishuAgentReply(result), runErr
	}
	return formatFeishuAgentReply(result), nil
}

func (s *agentServer) finishFeishuResetRun(sessionID string, run runLifecycle, message string, result agent.AgentRunResult) (feishu.Reply, error) {
	finished := time.Now()
	logRunFinish(run, runStatusSucceeded, finished)
	if err := s.store.FinishRunForSessionWithoutMessages(sessionID, run, message, result, runStatusSucceeded, finished, nil); err != nil {
		log.Printf("persist feishu reset run %s: %v", run.ID, err)
	}
	return formatFeishuAgentReply(result), nil
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
