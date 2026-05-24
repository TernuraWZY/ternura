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

func (s *agentServer) handleFeishuMessage(ctx context.Context, msg feishu.InboundMessage) (string, error) {
	sessionID := feishu.SessionIDForKey(msg.SessionKey)
	delivery := feishuDeliveryTarget(msg)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.store.EnsureSession(sessionID, feishu.SessionTitle(msg)); err != nil {
		return "", err
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

func (s *agentServer) finishFeishuRun(sessionID string, run runLifecycle, message string, result agent.AgentRunResult, runErr error) (string, error) {
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
		return result.Content, runErr
	}
	return result.Content, nil
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
		err := s.feishu.Send(ctx, feishu.OutboundMessage{
			ReceiveIDType: job.Payload.Delivery.ReceiveIDType,
			ReceiveID:     job.Payload.Delivery.ReceiveID,
			MessageID:     job.Payload.Delivery.MessageID,
			ThreadID:      job.Payload.Delivery.ThreadID,
			Content:       result.Content,
			Reply:         job.Payload.Delivery.ThreadID != "",
		})
		if err != nil {
			log.Printf("deliver cron job %s to feishu: %v", job.ID, err)
		}
	}
}
