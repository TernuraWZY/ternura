package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"ternura/tool"
)

type einoAgentRun struct {
	agent   *Agent
	runCtx  *RunContext
	result  *AgentRunResult
	runner  *adk.Runner
	emit    func(AgentStreamEvent) error
	traceID int

	mu                sync.Mutex
	emitMu            sync.Mutex
	ignoredToolPolicy ToolPolicy
	requiredPolicies  []ToolPolicy
	observedMessages  map[string]struct{}
	toolCalls         map[string]schema.ToolCall
	toolResults       map[string]ToolResult
	preparedModelCall bool
	modelCallErr      error
}

func (a *Agent) newEinoAgentRun(ctx context.Context, runCtx *RunContext, result *AgentRunResult, emit func(AgentStreamEvent) error) (*einoAgentRun, error) {
	if err := a.ensureChatModel(); err != nil {
		return nil, err
	}

	runtime := &einoAgentRun{
		agent:            a,
		runCtx:           runCtx,
		result:           result,
		emit:             emit,
		observedMessages: make(map[string]struct{}),
		toolCalls:        make(map[string]schema.ToolCall),
		toolResults:      make(map[string]ToolResult),
	}
	if err := runtime.beforeModelCall(ctx); err != nil {
		return nil, err
	}
	runtime.preparedModelCall = true

	tools := a.toolsForRun(runCtx)
	baseTools, toolSearchMiddleware, err := a.prepareToolSearch(ctx, runCtx, tools)
	if err != nil {
		return nil, fmt.Errorf("configure dynamic tool search: %w", err)
	}
	handlers := make([]adk.ChatModelAgentMiddleware, 0, 2)
	if toolSearchMiddleware != nil {
		handlers = append(handlers, toolSearchMiddleware)
	}
	handlers = append(handlers, &adkRuntimeMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		run:                          runtime,
	})
	adkAgent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "ternura",
		Description: "Ternura general-purpose tool-using agent",
		Model:       a.chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               baseTools,
				ExecuteSequentially: false,
				ToolCallMiddlewares: []compose.ToolMiddleware{
					runtime.toolCallMiddleware(),
				},
				UnknownToolsHandler: func(ctx context.Context, name, input string) (string, error) {
					return fmt.Sprintf("tool not found: %s", name), nil
				},
			},
		},
		MaxIterations: a.reactMaxSteps(),
		GenModelInput: func(_ context.Context, _ string, input *adk.AgentInput) ([]*schema.Message, error) {
			return input.Messages, nil
		},
		Handlers:            handlers,
		ModelRetryConfig:    defaultModelRetryConfig(),
		ModelFailoverConfig: defaultModelFailoverConfig(a.fallbackModels),
	})
	if err != nil {
		return nil, err
	}

	runtime.runner = adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           adkAgent,
		EnableStreaming: runCtx != nil && runCtx.Mode == RunModeStreaming,
		CheckPointStore: a.checkpointStore,
	})
	return runtime, nil
}

func (r *einoAgentRun) Generate(ctx context.Context) (*schema.Message, error) {
	log.Printf("calling Eino ADK agent with model %s...", r.agent.model)
	iter, err := r.startADKRun(ctx, cloneMessages(r.agent.messages))
	if err != nil {
		return nil, err
	}
	message, err := r.consumeADKEvents(ctx, iter)
	if !isPromptTooLongError(err) {
		return message, err
	}

	compacted := r.reactiveCompactHistory(ctx, r.contextBuilder(), cloneMessages(r.agent.messages))
	r.agent.messages = compacted
	iter, startErr := r.startADKRun(ctx, compacted)
	if startErr != nil {
		return nil, startErr
	}
	return r.consumeADKEvents(ctx, iter)
}

func (r *einoAgentRun) Stream(ctx context.Context) (*schema.Message, error) {
	log.Printf("streaming Eino ADK agent with model %s...", r.agent.model)
	iter, err := r.startADKRun(ctx, cloneMessages(r.agent.messages))
	if err != nil {
		return nil, err
	}
	message, err := r.consumeADKEvents(ctx, iter)
	if !isPromptTooLongError(err) {
		return message, err
	}

	compacted := r.reactiveCompactHistory(ctx, r.contextBuilder(), cloneMessages(r.agent.messages))
	r.agent.messages = compacted
	iter, startErr := r.startADKRun(ctx, compacted)
	if startErr != nil {
		return nil, startErr
	}
	return r.consumeADKEvents(ctx, iter)
}

func (r *einoAgentRun) RetryIgnoredToolPolicy(ctx context.Context) bool {
	r.mu.Lock()
	policy := r.ignoredToolPolicy
	r.ignoredToolPolicy = ToolPolicy{}
	r.mu.Unlock()

	return r.agent.retryIgnoredToolPolicy(ctx, r.runCtx, policy)
}

func (r *einoAgentRun) messageModifier(ctx context.Context, input []*schema.Message) []*schema.Message {
	if !r.consumePreparedModelCall() {
		if err := r.beforeModelCall(ctx); err != nil {
			r.setModelCallError(err)
			return input
		}
	}
	messages, err := r.buildModelMessages(ctx, input)
	if err != nil {
		r.setModelCallError(err)
		return input
	}
	return messages
}

func (r *einoAgentRun) buildModelMessages(ctx context.Context, input []*schema.Message) ([]*schema.Message, error) {
	builder := r.contextBuilder()
	messages, err := builder.BuildPreCompact(ctx, r.runCtx, input)
	if err != nil {
		return nil, err
	}
	messages = r.compactHistoryIfNeeded(ctx, builder, messages)
	messages = builder.FinalizeBudget(messages)
	r.recordModelInput(messages)
	return messages, nil
}

func (r *einoAgentRun) contextBuilder() *ContextBuilder {
	if r == nil || r.agent == nil || r.agent.contextBuilder == nil {
		systemPrompt := ""
		if r != nil && r.agent != nil {
			systemPrompt = r.agent.systemPrompt
		}
		return NewContextBuilder(systemPrompt)
	}
	return r.agent.contextBuilder
}

func isPromptTooLongError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "prompt_too_long") ||
		strings.Contains(lower, "too many tokens") ||
		strings.Contains(lower, "context length") ||
		strings.Contains(lower, "maximum context")
}

func (r *einoAgentRun) recordModelInput(messages []*schema.Message) {
	if r == nil || r.result == nil {
		return
	}
	call := 0
	if r.runCtx != nil {
		call = r.runCtx.ModelCallCount
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	snapshot := NewModelInputSnapshot(call, messages)
	if len(r.result.ModelInput) > 0 && sameModelInputSnapshot(r.result.ModelInput[len(r.result.ModelInput)-1], snapshot) {
		return
	}
	r.result.ModelInput = append(r.result.ModelInput, snapshot)
}

func sameModelInputSnapshot(left, right ModelInputSnapshot) bool {
	if left.Call != right.Call || left.TotalRunes != right.TotalRunes || len(left.Messages) != len(right.Messages) {
		return false
	}
	for idx := range left.Messages {
		if !sameModelInputMessage(left.Messages[idx], right.Messages[idx]) {
			return false
		}
	}
	return true
}

func sameModelInputMessage(left, right ModelInputMessage) bool {
	if left.Role != right.Role ||
		left.Content != right.Content ||
		left.ToolName != right.ToolName ||
		left.ToolCallID != right.ToolCallID ||
		left.Truncated != right.Truncated ||
		len(left.ToolCalls) != len(right.ToolCalls) {
		return false
	}
	for idx := range left.ToolCalls {
		if left.ToolCalls[idx] != right.ToolCalls[idx] {
			return false
		}
	}
	return true
}

func (r *einoAgentRun) beforeModelCall(ctx context.Context) error {
	if r.runCtx != nil {
		if err := r.runCtx.reserveModelCall(); err != nil {
			return err
		}
		r.runCtx.SetContextBlockWithPriority(
			"run-budget",
			"Run Budget",
			r.runCtx.BudgetContextText(),
			RuntimeContextPriorityHigh,
			1200,
		)
	}
	if err := r.agent.hooks.BeforeModelCall(ctx, r.runCtx); err != nil {
		return err
	}

	_, available := r.agent.enabledToolsForRun(r.runCtx)
	policy := effectiveToolPolicy(r.runCtx, available)
	if policy.Empty() && r.runCtx != nil && !r.runCtx.RequestedToolPolicy().Empty() {
		r.runCtx.ClearToolPolicy()
	}
	r.applyToolPolicyContext(policy)
	r.rememberRequiredToolPolicy(policy)
	return nil
}

func (a *Agent) reactMaxSteps() int {
	if a == nil || a.runLimits.MaxReactSteps <= 0 {
		return defaultMaxReactSteps
	}
	return a.runLimits.MaxReactSteps
}

func (r *einoAgentRun) applyToolPolicyContext(policy ToolPolicy) {
	if r.runCtx == nil {
		return
	}
	r.runCtx.SetContextBlockWithPriority(
		"tool-policy",
		"Tool Policy",
		toolPolicyGuidance(policy),
		RuntimeContextPriorityCritical,
		2000,
	)
}

func toolPolicyGuidance(policy ToolPolicy) string {
	if policy.Empty() {
		return ""
	}
	if policy.Required && len(policy.AllowedTools) == 1 {
		return fmt.Sprintf("The next assistant response must call the %s tool before giving a final answer.", policy.AllowedTools[0])
	}
	if policy.Required && len(policy.AllowedTools) > 1 {
		return fmt.Sprintf("The next assistant response must call one of these tools before giving a final answer: %s.", joinToolNames(policy.AllowedTools))
	}
	if policy.Required {
		return "The next assistant response must call one of the available tools before giving a final answer."
	}
	return fmt.Sprintf("If a tool is needed, use only these tools: %s.", joinToolNames(policy.AllowedTools))
}

func joinToolNames(names []tool.AgentTool) string {
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, string(name))
	}
	return strings.Join(parts, ", ")
}

func (r *einoAgentRun) consumePreparedModelCall() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.preparedModelCall {
		return false
	}
	r.preparedModelCall = false
	return true
}

func (r *einoAgentRun) setModelCallError(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.modelCallErr == nil {
		r.modelCallErr = err
	}
}

func (r *einoAgentRun) modelCallError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.modelCallErr
}

func (r *einoAgentRun) recordEinoMessage(ctx context.Context, message *schema.Message) error {
	if message == nil {
		return nil
	}
	if !r.markObservedMessage(message) {
		return nil
	}
	if message.Role == schema.Assistant {
		return r.recordAssistantMessage(ctx, message)
	}
	if message.Role == schema.Tool {
		if err := r.recordToolMessage(message); err != nil {
			return err
		}
	}

	r.mu.Lock()
	r.agent.messages = append(r.agent.messages, message)
	r.mu.Unlock()
	return nil
}

func (r *einoAgentRun) markObservedMessage(message *schema.Message) bool {
	key := observedMessageKey(message)

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.observedMessages[key]; ok {
		return false
	}
	r.observedMessages[key] = struct{}{}
	return true
}

func observedMessageKey(message *schema.Message) string {
	type messageKey struct {
		Role       schema.RoleType   `json:"role"`
		Content    string            `json:"content,omitempty"`
		ToolCallID string            `json:"tool_call_id,omitempty"`
		ToolName   string            `json:"tool_name,omitempty"`
		ToolCalls  []schema.ToolCall `json:"tool_calls,omitempty"`
	}
	payload, err := json.Marshal(messageKey{
		Role:       message.Role,
		Content:    message.Content,
		ToolCallID: message.ToolCallID,
		ToolName:   message.ToolName,
		ToolCalls:  message.ToolCalls,
	})
	if err != nil {
		return fmt.Sprintf("%s:%s:%s:%s", message.Role, message.ToolCallID, message.ToolName, message.Content)
	}
	return string(payload)
}

func (r *einoAgentRun) recordAssistantMessage(ctx context.Context, message *schema.Message) error {
	policy := r.nextRequiredToolPolicy()
	if r.runCtx != nil && message.ResponseMeta != nil {
		r.runCtx.recordModelUsage(message.ResponseMeta.Usage)
	}

	r.mu.Lock()
	for _, call := range message.ToolCalls {
		if call.ID != "" {
			r.toolCalls[call.ID] = call
		}
	}
	r.agent.messages = append(r.agent.messages, message)
	if r.emit == nil {
		appendThinkTrace(r.result, message.Content)
	}
	if len(message.ToolCalls) == 0 && policy.Required {
		r.ignoredToolPolicy = policy
	}
	r.mu.Unlock()

	if err := r.agent.hooks.AfterModelResponse(ctx, r.runCtx, message); err != nil {
		return err
	}
	if len(message.ToolCalls) > 0 {
		r.runCtx.ClearToolPolicy()
	}
	return nil
}

func (r *einoAgentRun) recordToolMessage(message *schema.Message) error {
	toolResult := r.toolResultForMessage(message)
	return r.recordToolTrace(toolResult)
}

func (r *einoAgentRun) toolResultForMessage(message *schema.Message) ToolResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	if result, ok := r.toolResults[message.ToolCallID]; ok {
		return result
	}

	call := schema.ToolCall{
		ID: message.ToolCallID,
		Function: schema.FunctionCall{
			Name: message.ToolName,
		},
	}
	if savedCall, ok := r.toolCalls[message.ToolCallID]; ok {
		call = savedCall
	}
	return ToolResult{
		Call:    call,
		Content: message.Content,
	}
}

func (r *einoAgentRun) newContentRouter() *streamingContentRouter {
	return newStreamingContentRouter(
		func() string { return r.newTraceID() },
		func(event AgentStreamEvent) error {
			r.mu.Lock()
			if event.Type == "trace_start" {
				r.result.Trace = append(r.result.Trace, AgentTraceItem{
					Type:  event.TraceType,
					Title: event.Title,
				})
			}
			if event.Type == "trace_delta" && len(r.result.Trace) > 0 {
				r.result.Trace[len(r.result.Trace)-1].Content += event.Delta
			}
			if event.Type == "content_delta" {
				r.result.Content += event.Delta
			}
			r.mu.Unlock()
			return r.emitEvent(event)
		},
	)
}

func (r *einoAgentRun) emitEvent(event AgentStreamEvent) error {
	if r == nil || r.emit == nil {
		return nil
	}
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	return r.emit(event)
}

func (r *einoAgentRun) emitTrace(id string, item AgentTraceItem) error {
	if r == nil || r.emit == nil {
		return nil
	}
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	return emitTraceItem(r.emit, id, item)
}

func (r *einoAgentRun) appendRawContent(content string) {
	if content == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.result.RawContent += content
}

func (r *einoAgentRun) toolCallMiddleware() compose.ToolMiddleware {
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				startedAt := time.Now()
				approvedArguments, approvalOutput, handled, err := r.gateToolCall(ctx, input)
				if err != nil {
					return nil, err
				}
				if handled {
					if approvalOutput != nil {
						call := schema.ToolCall{
							ID: input.CallID,
							Function: schema.FunctionCall{
								Name:      input.Name,
								Arguments: input.Arguments,
							},
						}
						toolResult := limitToolResult(ToolResult{
							Call:       call,
							Content:    approvalOutput.Result,
							StartedAt:  startedAt,
							FinishedAt: time.Now(),
						})
						r.rememberToolResult(toolResult)
						if r.runCtx != nil {
							r.runCtx.recordToolResult(toolResult)
						}
					}
					return approvalOutput, nil
				}
				if approvedArguments != "" && approvedArguments != input.Arguments {
					cloned := *input
					cloned.Arguments = approvedArguments
					input = &cloned
				}
				call := schema.ToolCall{
					ID: input.CallID,
					Function: schema.FunctionCall{
						Name:      input.Name,
						Arguments: input.Arguments,
					},
				}
				if r.runCtx != nil {
					if err := r.runCtx.reserveToolCall(tool.AgentTool(input.Name)); err != nil {
						toolResult := ToolResult{
							Call:       call,
							Content:    budgetExceededToolContent(err),
							Err:        err,
							StartedAt:  startedAt,
							FinishedAt: time.Now(),
						}
						toolResult = limitToolResult(toolResult)
						r.rememberToolResult(toolResult)
						r.runCtx.recordToolResult(toolResult)
						return &compose.ToolOutput{Result: toolResult.Content}, nil
					}
				}
				if err := r.agent.hooks.BeforeToolCall(ctx, r.runCtx, &call); err != nil {
					toolResult := ToolResult{
						Call:       call,
						Content:    err.Error(),
						Err:        err,
						StartedAt:  startedAt,
						FinishedAt: time.Now(),
					}
					toolResult = limitToolResult(toolResult)
					r.rememberToolResult(toolResult)
					if r.runCtx != nil {
						r.runCtx.recordToolResult(toolResult)
					}
					return &compose.ToolOutput{Result: toolResult.Content}, nil
				}

				output, err := next(ctx, input)
				content := ""
				if output != nil {
					content = output.Result
				}
				if err != nil {
					content = err.Error()
				}
				toolResult := ToolResult{
					Call:       call,
					Content:    content,
					Err:        err,
					StartedAt:  startedAt,
					FinishedAt: time.Now(),
				}

				if err := r.agent.hooks.AfterToolCall(ctx, r.runCtx, &toolResult); err != nil {
					toolResult.Err = err
					toolResult.Content = err.Error()
				}
				toolResult.FinishedAt = time.Now()
				toolResult = limitToolResult(toolResult)
				r.rememberToolResult(toolResult)
				if r.runCtx != nil {
					r.runCtx.recordToolResult(toolResult)
				}
				return &compose.ToolOutput{Result: toolResult.Content}, nil
			}
		},
	}
}

func (r *einoAgentRun) rememberToolResult(result ToolResult) {
	if result.Call.ID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.toolResults == nil {
		r.toolResults = make(map[string]ToolResult)
	}
	r.toolResults[result.Call.ID] = result
}

func (r *einoAgentRun) recordToolTrace(toolResult ToolResult) error {
	traceItem := toolTraceFromResult(toolResult)

	r.mu.Lock()
	r.result.Trace = append(r.result.Trace, traceItem)
	r.mu.Unlock()

	if r.emit != nil {
		if err := r.emitTrace(r.newTraceID(), traceItem); err != nil {
			return err
		}
	}
	log.Printf("tool call %s, arguments %s, error: %v", toolResult.Call.Function.Name, toolResult.Call.Function.Arguments, toolResult.Err)
	return nil
}

func (r *einoAgentRun) rememberRequiredToolPolicy(policy ToolPolicy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requiredPolicies = append(r.requiredPolicies, policy)
}

func (r *einoAgentRun) nextRequiredToolPolicy() ToolPolicy {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requiredPolicies) == 0 {
		return ToolPolicy{}
	}
	policy := r.requiredPolicies[0]
	r.requiredPolicies = r.requiredPolicies[1:]
	return policy
}

func (r *einoAgentRun) streamContainsToolCall(ctx context.Context, stream *schema.StreamReader[*schema.Message]) (bool, error) {
	containsToolCall, err := streamContainsToolCall(ctx, stream)
	if containsToolCall {
		r.runCtx.ClearToolPolicy()
	}
	return containsToolCall, err
}

func (r *einoAgentRun) newTraceID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.traceID++
	return fmt.Sprintf("trace-%d", r.traceID)
}

func streamContainsToolCall(_ context.Context, stream *schema.StreamReader[*schema.Message]) (bool, error) {
	defer stream.Close()

	for {
		message, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if message != nil && len(message.ToolCalls) > 0 {
			return true, nil
		}
	}
}
