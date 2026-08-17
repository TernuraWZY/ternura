package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

const (
	runMetadataCheckpointID = "eino_checkpoint_id"
	runMetadataResume       = "eino_resume"
)

type adkResumeRequest struct {
	InterruptID string
	Decision    ToolApprovalDecision
}

type adkRuntimeMiddleware struct {
	*adk.BaseChatModelAgentMiddleware
	run *einoAgentRun
}

func (m *adkRuntimeMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if m == nil || m.run == nil || state == nil {
		return ctx, state, nil
	}
	if !m.run.consumePreparedModelCall() {
		if err := m.run.beforeModelCall(ctx); err != nil {
			return ctx, state, err
		}
	}
	messages, err := m.run.buildModelMessages(ctx, state.Messages)
	if err != nil {
		return ctx, state, err
	}
	state.Messages = messages
	return ctx, state, nil
}

func (m *adkRuntimeMiddleware) AfterModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	if m == nil || m.run == nil || state == nil || len(state.Messages) == 0 {
		return ctx, state, nil
	}
	message := state.Messages[len(state.Messages)-1]
	if message == nil || message.Role != schema.Assistant {
		return ctx, state, nil
	}
	if err := m.run.recordEinoMessage(ctx, message); err != nil {
		return ctx, state, err
	}
	return ctx, state, nil
}

func defaultModelRetryConfig() *adk.ModelRetryConfig {
	maxRetries := 2
	if value := strings.TrimSpace(os.Getenv("TERNURA_MODEL_MAX_RETRIES")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed >= 0 {
			maxRetries = parsed
		}
	}
	if maxRetries == 0 {
		return nil
	}
	return &adk.ModelRetryConfig{
		MaxRetries: maxRetries,
		IsRetryAble: func(ctx context.Context, err error) bool {
			if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrRunBudgetExceeded) {
				return false
			}
			lower := strings.ToLower(err.Error())
			for _, marker := range []string{
				"timeout", "temporarily unavailable", "rate limit", "too many requests",
				"connection reset", "connection refused", "unexpected eof", "status code: 500",
				"status code: 502", "status code: 503", "status code: 504",
			} {
				if strings.Contains(lower, marker) {
					return true
				}
			}
			return false
		},
		BackoffFunc: func(_ context.Context, attempt int) time.Duration {
			if attempt < 1 {
				attempt = 1
			}
			delay := time.Duration(attempt*attempt) * 250 * time.Millisecond
			if delay > 3*time.Second {
				return 3 * time.Second
			}
			return delay
		},
	}
}

func (r *einoAgentRun) startADKRun(ctx context.Context, messages []*schema.Message, runOptions ...adk.AgentRunOption) (*adk.AsyncIterator[*adk.AgentEvent], error) {
	if r == nil || r.runner == nil {
		return nil, errors.New("Eino ADK runner is not initialized")
	}
	checkpointID := checkpointIDFromRunContext(r.runCtx)
	if resume := resumeRequestFromRunContext(r.runCtx); resume != nil {
		if checkpointID == "" {
			return nil, errors.New("checkpoint id is required to resume an agent run")
		}
		if strings.TrimSpace(resume.InterruptID) == "" {
			return nil, errors.New("interrupt id is required to resume an agent run")
		}
		return r.runner.ResumeWithParams(ctx, checkpointID, &adk.ResumeParams{
			Targets: map[string]any{
				resume.InterruptID: &resume.Decision,
			},
		})
	}

	options := append([]adk.AgentRunOption(nil), runOptions...)
	if checkpointID != "" {
		options = append(options, adk.WithCheckPointID(checkpointID))
	}
	return r.runner.Run(ctx, cloneMessages(messages), options...), nil
}

func (r *einoAgentRun) consumeADKEvents(ctx context.Context, iter *adk.AsyncIterator[*adk.AgentEvent]) (*schema.Message, error) {
	if iter == nil {
		return nil, nil
	}
	var final *schema.Message
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			return nil, event.Err
		}
		if event.Action != nil && event.Action.Interrupted != nil {
			if err := r.captureADKInterrupt(event.Action.Interrupted); err != nil {
				return nil, err
			}
			return nil, nil
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}

		variant := event.Output.MessageOutput
		var message *schema.Message
		var err error
		if variant.IsStreaming {
			message, err = r.recordEinoMessageStream(ctx, variant.MessageStream)
		} else {
			message, err = variant.GetMessage()
			if err == nil {
				err = r.recordEinoMessage(ctx, message)
			}
		}
		if err != nil {
			return nil, err
		}
		if message != nil && message.Role == schema.Assistant && len(message.ToolCalls) == 0 {
			final = message
		}
	}
	if err := r.modelCallError(); err != nil {
		return nil, err
	}
	return final, nil
}

func (r *einoAgentRun) captureADKInterrupt(interrupt *adk.InterruptInfo) error {
	if r == nil || r.result == nil || interrupt == nil || len(interrupt.InterruptContexts) == 0 {
		return errors.New("agent run was interrupted without resumable context")
	}
	root := interrupt.InterruptContexts[0]
	for _, candidate := range interrupt.InterruptContexts {
		if candidate != nil && candidate.IsRootCause {
			root = candidate
			break
		}
	}
	if root == nil {
		return errors.New("agent run was interrupted without a root cause")
	}

	request, ok := root.Info.(*ToolApprovalRequest)
	if !ok || request == nil {
		request = &ToolApprovalRequest{
			Reason: fmt.Sprint(root.Info),
		}
	}
	copyRequest := *request
	copyRequest.CheckpointID = checkpointIDFromRunContext(r.runCtx)
	copyRequest.InterruptID = root.ID
	r.result.CheckpointID = copyRequest.CheckpointID
	r.result.PendingApproval = &copyRequest
	r.result.Content = renderApprovalRequest(copyRequest)
	r.result.Trace = append(r.result.Trace, AgentTraceItem{
		Type:    "approval",
		Title:   "Tool approval required",
		Content: r.result.Content,
	})
	if r.emit != nil {
		if err := r.emitEvent(AgentStreamEvent{
			Type:     "approval_required",
			Content:  r.result.Content,
			Approval: &copyRequest,
		}); err != nil {
			return err
		}
	}
	return nil
}

func checkpointIDFromRunContext(runCtx *RunContext) string {
	if runCtx == nil || runCtx.Metadata == nil {
		return ""
	}
	value, _ := runCtx.Metadata[runMetadataCheckpointID].(string)
	return strings.TrimSpace(value)
}

func resumeRequestFromRunContext(runCtx *RunContext) *adkResumeRequest {
	if runCtx == nil || runCtx.Metadata == nil {
		return nil
	}
	request, _ := runCtx.Metadata[runMetadataResume].(*adkResumeRequest)
	return request
}

func renderApprovalRequest(request ToolApprovalRequest) string {
	return strings.Join([]string{
		"这个操作需要你确认后才能继续执行。",
		"",
		fmt.Sprintf("- 工具：`%s`", request.Tool),
		fmt.Sprintf("- 原因：%s", strings.TrimSpace(request.Reason)),
		fmt.Sprintf("- 参数：`%s`", compactApprovalArguments(request.Arguments)),
		"",
		fmt.Sprintf("回复 `approve %s` 继续，或回复 `reject %s 原因` 拒绝。", request.CheckpointID, request.CheckpointID),
	}, "\n")
}

func compactApprovalArguments(arguments string) string {
	arguments = strings.Join(strings.Fields(arguments), " ")
	const maxRunes = 300
	chars := []rune(arguments)
	if len(chars) <= maxRunes {
		return arguments
	}
	return string(chars[:maxRunes]) + "..."
}
