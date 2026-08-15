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

	lock := s.taskSessionLock(sessionID)
	lock.Lock()
	defer lock.Unlock()

	return s.runTrackedFeishuTurn(ctx, func(runCtx context.Context, onStart func(runLifecycle)) (feishu.Reply, error) {
		if messageRequestsNewSession(msg.Content) {
			return s.resetFeishuSession(runCtx, sessionID, msg, onStart)
		}

		if _, err := s.store.EnsureSession(sessionID, feishu.SessionTitle(msg)); err != nil {
			return feishu.Reply{}, err
		}

		session := s.newAgentSession(sessionID, nil)
		if command, ok := parseApprovalCommand(msg.Content); ok {
			return formatFeishuOutcomeForSession(sessionID, session.resumeApproval(runCtx, msg.Content, command, onStart))
		}

		if result, handled, err := s.tryScheduleShortcutForSession(runCtx, msg.Content, sessionID, delivery); handled {
			outcome := session.run(runCtx, agentSessionRunRequest{
				Kind:           agentSessionRunUser,
				DisplayMessage: msg.Content,
				DirectResult:   &result,
				DirectErr:      err,
				OnStart:        onStart,
			})
			return formatFeishuOutcomeForSession(sessionID, outcome)
		}

		cronTool := tool.NewCronTool(
			s.cronAddForSessionWithDelivery(sessionID, delivery),
			s.cronList,
			s.cronRemove,
		)
		session.cronTool = cronTool
		outcome := session.run(runCtx, agentSessionRunRequest{
			Kind:           agentSessionRunUser,
			DisplayMessage: msg.Content,
			OnStart:        onStart,
		})
		return formatFeishuOutcomeForSession(sessionID, outcome)
	})
}

func (s *agentServer) runTrackedFeishuTurn(ctx context.Context, run func(context.Context, func(runLifecycle)) (feishu.Reply, error)) (feishu.Reply, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	trackedRunID := ""
	onStart := func(started runLifecycle) {
		trackedRunID = started.ID
		s.trackTask(started.ID, cancel)
		feishu.ReportProgress(runCtx, feishu.ProgressUpdate{
			RunID:  started.ID,
			Stage:  feishu.ProgressStageStarted,
			Detail: "已经收到，正在开始处理",
		})
	}
	reply, err := run(runCtx, onStart)
	if trackedRunID != "" {
		s.untrackTask(trackedRunID)
	}
	return reply, err
}

func (s *agentServer) resetFeishuSession(ctx context.Context, sessionID string, msg feishu.InboundMessage, onStart func(runLifecycle)) (feishu.Reply, error) {
	if _, err := s.store.ResetSession(sessionID, feishu.SessionTitle(msg)); err != nil {
		return feishu.Reply{}, err
	}
	if s.memory != nil {
		if err := s.memory.ResetSession(sessionID); err != nil {
			return feishu.Reply{}, err
		}
	}
	s.mu.Lock()
	s.resetAgentFromSnapshot(s.store.Snapshot())
	s.mu.Unlock()

	session := s.newAgentSession(sessionID, nil)
	result := agent.AgentRunResult{
		Content: "新会话已开始。这个飞书会话的历史消息、短期记忆、工具记忆和待办都已清空。",
	}
	outcome := session.run(ctx, agentSessionRunRequest{
		Kind:           agentSessionRunUser,
		DisplayMessage: msg.Content,
		DirectResult:   &result,
		OmitMessages:   true,
		OnStart:        onStart,
	})
	return formatFeishuOutcomeForSession(sessionID, outcome)
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
	return formatFeishuOutcomeForSession("", outcome)
}

func formatFeishuOutcomeForSession(sessionID string, outcome agentSessionRunOutcome) (feishu.Reply, error) {
	reply := formatFeishuAgentReplyForSession(outcome.Result, sessionID)
	if outcome.Err != nil {
		if strings.TrimSpace(reply.Content) != "" || reply.Card != nil {
			log.Printf("feishu agent turn failed for run %s: %v", outcome.Run.ID, outcome.Err)
			return reply, nil
		}
		return feishu.Reply{}, outcome.Err
	}
	return reply, nil
}

func (s *agentServer) handleFeishuCardAction(_ context.Context, action feishu.CardAction) (feishu.CardActionResponse, error) {
	actionName := cardActionString(action.Value, "action")
	switch actionName {
	case "cancel_run":
		runID := cardActionString(action.Value, "run_id")
		if runID == "" {
			return feishu.CardActionResponse{ToastType: "error", ToastContent: "找不到要取消的任务"}, nil
		}
		if err := s.cancelTask(runID); err != nil {
			return feishu.CardActionResponse{ToastType: "error", ToastContent: "取消失败：" + err.Error()}, nil
		}
		return feishu.CardActionResponse{
			ToastType:    "success",
			ToastContent: "正在取消任务",
			Card:         buildFeishuStatusCard("正在取消", "已收到取消请求，正在停止模型和工具调用。", "orange"),
		}, nil
	case "approve_tool", "reject_tool":
		sessionID := cardActionString(action.Value, "session_id")
		checkpointID := cardActionString(action.Value, "checkpoint_id")
		if sessionID == "" || checkpointID == "" {
			return feishu.CardActionResponse{ToastType: "error", ToastContent: "审批信息不完整"}, nil
		}
		if _, ok := s.store.PendingApprovalForSession(sessionID, checkpointID); !ok {
			return feishu.CardActionResponse{ToastType: "info", ToastContent: "这个审批已经处理或失效"}, nil
		}
		approved := actionName == "approve_tool"
		go s.resumeFeishuCardApproval(action, sessionID, checkpointID, approved)
		title := "已拒绝"
		detail := "已拒绝这个工具调用，Agent 将根据你的决定继续收口。"
		template := "grey"
		toast := "已拒绝工具调用"
		if approved {
			title = "已批准，正在继续"
			detail = "Agent 正在从检查点恢复并执行已批准的工具调用。"
			template = "blue"
			toast = "已批准工具调用"
		}
		return feishu.CardActionResponse{
			ToastType:    "success",
			ToastContent: toast,
			Card:         buildFeishuStatusCard(title, detail, template),
		}, nil
	default:
		return feishu.CardActionResponse{ToastType: "error", ToastContent: "不支持的卡片操作"}, nil
	}
}

func (s *agentServer) resumeFeishuCardApproval(action feishu.CardAction, sessionID string, checkpointID string, approved bool) {
	lock := s.taskSessionLock(sessionID)
	lock.Lock()
	defer lock.Unlock()

	ctx, cancel := context.WithTimeout(s.serverContext(), 4*time.Minute)
	defer cancel()
	command := approvalCommand{Approved: approved, CheckpointID: checkpointID}
	display := "reject " + checkpointID
	if approved {
		display = "approve " + checkpointID
	}
	reply, err := s.runTrackedFeishuTurn(ctx, func(runCtx context.Context, onStart func(runLifecycle)) (feishu.Reply, error) {
		session := s.newAgentSession(sessionID, nil)
		return formatFeishuOutcomeForSession(sessionID, session.resumeApproval(runCtx, display, command, onStart))
	})
	if err != nil {
		reply = feishu.Reply{Content: "继续执行失败：" + err.Error()}
	}
	if s.feishu == nil || !s.feishu.Enabled() || reply.Empty() {
		return
	}
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer sendCancel()
	if err := s.feishu.Send(sendCtx, feishu.OutboundMessage{
		ReceiveIDType: "chat_id",
		ReceiveID:     action.ChatID,
		MessageID:     action.MessageID,
		Content:       reply.Content,
		Card:          reply.Card,
		Reply:         action.MessageID != "",
	}); err != nil {
		log.Printf("send feishu approval result: %v", err)
	}
}

func cardActionString(value map[string]any, key string) string {
	if len(value) == 0 {
		return ""
	}
	text, _ := value[key].(string)
	return strings.TrimSpace(text)
}

func buildFeishuStatusCard(title string, detail string, template string) map[string]any {
	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"title":    map[string]string{"tag": "plain_text", "content": title},
			"template": template,
		},
		"body": map[string]any{
			"elements": []any{map[string]any{
				"tag":     "markdown",
				"content": detail,
			}},
		},
	}
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
