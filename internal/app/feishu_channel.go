package app

import (
	"context"
	"log"
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

	if _, err := s.store.EnsureSession(sessionID, feishu.SessionTitle(msg)); err != nil {
		return feishu.Reply{}, err
	}

	run := newRunLifecycle()
	logRunStart(run)
	if err := s.store.StartRunForSession(sessionID, run, msg.Content); err != nil {
		log.Printf("persist feishu run start %s: %v", run.ID, err)
	}

	planDecision, err := s.handlePlanFlow(ctx, sessionID, msg.Content)
	if err != nil {
		return s.finishFeishuRun(sessionID, run, msg.Content, agent.AgentRunResult{}, err)
	}
	if planDecision.Handled {
		if !planDecision.Execute {
			return s.finishFeishuRun(sessionID, run, msg.Content, planDecision.Result, nil)
		}
		if result, handled, err := s.tryScheduleShortcutForSession(ctx, planDecision.ShortcutMessage, sessionID, delivery); handled {
			return s.finishFeishuRunWithRuntimeMessage(sessionID, run, msg.Content, planDecision.RuntimeMessage, result, err)
		}
		cronTool := tool.NewCronTool(
			s.cronAddForSessionWithDelivery(sessionID, delivery),
			s.cronList,
			s.cronRemove,
		)
		agent := s.newAgentForSessionWithCron(sessionID, cronTool)
		result, err := agent.RunWithTrace(ctx, planDecision.RuntimeMessage)
		return s.finishFeishuRunWithRuntimeMessage(sessionID, run, msg.Content, planDecision.RuntimeMessage, result, err)
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

func (s *agentServer) finishFeishuRunWithRuntimeMessage(sessionID string, run runLifecycle, displayMessage string, runtimeMessage string, result agent.AgentRunResult, runErr error) (feishu.Reply, error) {
	finished := time.Now()
	status := runStatusSucceeded
	if runErr != nil {
		status = runStatusFailed
	}
	logRunFinish(run, status, finished)
	if err := s.store.FinishRunForSessionWithRuntimeMessage(sessionID, run, displayMessage, runtimeMessage, result, status, finished, runErr); err != nil {
		log.Printf("persist feishu run %s: %v", run.ID, err)
	}
	if runErr != nil {
		return formatFeishuAgentReply(result), runErr
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
