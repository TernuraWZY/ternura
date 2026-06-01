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

	"github.com/cloudwego/eino/compose"
	einoreact "github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"ternura/tool"
)

type einoAgentRun struct {
	agent   *Agent
	runCtx  *RunContext
	result  *AgentRunResult
	react   *einoreact.Agent
	emit    func(AgentStreamEvent) error
	traceID int

	mu                sync.Mutex
	ignoredToolPolicy ToolPolicy
	requiredPolicies  []ToolPolicy
	observedMessages  map[string]struct{}
	toolCalls         map[string]schema.ToolCall
	toolResults       map[string]ToolResult
	preparedModelCall bool
	modelCallErr      error
	callbackErr       error
	callbackWG        sync.WaitGroup
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

	reactAgent, err := einoreact.NewAgent(ctx, &einoreact.AgentConfig{
		ToolCallingModel: a.chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools:               tools,
			ExecuteSequentially: true,
			ToolCallMiddlewares: []compose.ToolMiddleware{
				runtime.toolCallMiddleware(),
			},
			UnknownToolsHandler: func(ctx context.Context, name, input string) (string, error) {
				return fmt.Sprintf("tool not found: %s", name), nil
			},
		},
		MaxStep:               a.reactMaxSteps(),
		MessageModifier:       runtime.messageModifier,
		StreamToolCallChecker: runtime.streamContainsToolCall,
	})
	if err != nil {
		return nil, err
	}

	runtime.react = reactAgent
	return runtime, nil
}

func (r *einoAgentRun) Generate(ctx context.Context) (*schema.Message, error) {
	log.Printf("calling Eino ReAct agent with model %s...", r.agent.model)
	messages, err := r.buildModelMessages(ctx, r.agent.messages)
	if err != nil {
		return nil, err
	}
	message, err := r.react.Generate(ctx, messages, r.traceCallbackOption())
	if err != nil {
		return nil, err
	}
	if err := r.modelCallError(); err != nil {
		return nil, err
	}
	if err := r.waitTraceCallbacks(); err != nil {
		return nil, err
	}
	return message, nil
}

func (r *einoAgentRun) Stream(ctx context.Context) (*schema.Message, error) {
	log.Printf("streaming Eino ReAct agent with model %s...", r.agent.model)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	messages, err := r.buildModelMessages(ctx, r.agent.messages)
	if err != nil {
		return nil, err
	}
	stream, err := r.react.Stream(streamCtx, messages, r.traceCallbackOption())
	if err != nil {
		log.Printf("failed to stream Eino ReAct agent: %v", err)
		return nil, err
	}

	message, err := schema.ConcatMessageStream(stream)
	if err != nil {
		cancel()
		if callbackErr := r.waitTraceCallbacks(); callbackErr != nil {
			log.Printf("failed to collect Eino callback trace: %v", callbackErr)
		}
		log.Printf("failed to stream Eino ReAct agent: %v", err)
		return nil, err
	}
	if err := r.waitTraceCallbacks(); err != nil {
		return nil, err
	}
	if err := r.modelCallError(); err != nil {
		return nil, err
	}
	return message, nil
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
	builder := r.agent.contextBuilder
	if builder == nil {
		builder = NewContextBuilder(r.agent.systemPrompt)
	}
	return builder.Build(ctx, r.runCtx, input)
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
			return r.emit(event)
		},
	)
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
							Call:    call,
							Content: budgetExceededToolContent(err),
							Err:     err,
						}
						toolResult = limitToolResult(toolResult)
						r.rememberToolResult(toolResult)
						r.runCtx.recordToolResult(toolResult)
						return &compose.ToolOutput{Result: toolResult.Content}, nil
					}
				}
				if err := r.agent.hooks.BeforeToolCall(ctx, r.runCtx, &call); err != nil {
					toolResult := ToolResult{
						Call:    call,
						Content: err.Error(),
						Err:     err,
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
					Call:    call,
					Content: content,
					Err:     err,
				}

				if err := r.agent.hooks.AfterToolCall(ctx, r.runCtx, &toolResult); err != nil {
					toolResult.Err = err
					toolResult.Content = err.Error()
				}
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
		if err := emitTraceItem(r.emit, r.newTraceID(), traceItem); err != nil {
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
