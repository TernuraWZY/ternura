package app

import (
	"context"
	"fmt"
	"strings"

	"ternura/agent"
)

type approvalCommand struct {
	Approved     bool
	CheckpointID string
	Reason       string
}

func parseApprovalCommand(content string) (approvalCommand, bool) {
	fields := strings.Fields(strings.TrimSpace(content))
	if len(fields) == 0 {
		return approvalCommand{}, false
	}
	command := strings.ToLower(strings.Trim(fields[0], "/。！？!?.,，"))
	approved := false
	switch command {
	case "approve", "approved", "批准", "同意", "继续执行":
		approved = true
	case "reject", "rejected", "拒绝", "不同意", "取消执行":
	default:
		return approvalCommand{}, false
	}

	checkpointID := ""
	reasonStart := 1
	if len(fields) > 1 && strings.HasPrefix(strings.ToLower(fields[1]), "run-") {
		checkpointID = fields[1]
		reasonStart = 2
	}
	reason := ""
	if len(fields) > reasonStart {
		reason = strings.Join(fields[reasonStart:], " ")
	}
	return approvalCommand{
		Approved:     approved,
		CheckpointID: checkpointID,
		Reason:       reason,
	}, true
}

func (s *agentSession) resumeApproval(ctx context.Context, displayMessage string, command approvalCommand, onStart func(runLifecycle)) agentSessionRunOutcome {
	pending, ok := s.server.store.PendingApprovalForSession(s.sessionID, command.CheckpointID)
	if !ok || pending.PendingApproval == nil {
		result := agent.AgentRunResult{Content: "当前会话没有等待确认的工具调用。"}
		return s.run(ctx, agentSessionRunRequest{
			Kind:           agentSessionRunUser,
			DisplayMessage: displayMessage,
			DirectResult:   &result,
			OmitMessages:   true,
		})
	}

	run, err := s.startRun(agentSessionRunRequest{
		Kind:           agentSessionRunUser,
		DisplayMessage: displayMessage,
		RuntimePrompt:  pending.UserMessage,
	})
	if err != nil {
		return agentSessionRunOutcome{Run: run, Err: err}
	}
	if onStart != nil {
		onStart(run)
	}
	result, runErr := s.agent().ResumeWithTrace(
		ctx,
		pending.UserMessage,
		pending.CheckpointID,
		pending.PendingApproval.InterruptID,
		agent.ToolApprovalDecision{Approved: command.Approved, Reason: command.Reason},
	)
	if runErr != nil {
		result = failedAgentRunResult(result, runErr)
	}
	resolvedStatus := runStatusRejected
	if command.Approved {
		resolvedStatus = runStatusApproved
	}
	if err := s.server.store.ResolvePendingApproval(s.sessionID, pending.CheckpointID, resolvedStatus); err != nil {
		if runErr == nil {
			runErr = fmt.Errorf("resolve approval: %w", err)
		}
	}
	s.finishRun(run, agentSessionRunRequest{
		Kind:           agentSessionRunUser,
		DisplayMessage: displayMessage,
		RuntimePrompt:  pending.UserMessage,
	}, result, runErr)
	return agentSessionRunOutcome{Run: run, Result: result, Err: runErr}
}
