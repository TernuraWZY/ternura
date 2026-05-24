package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"

	einomodel "github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	einoreact "github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

const einoReactMaxStep = 100

type einoAgentRun struct {
	agent   *Agent
	runCtx  *RunContext
	result  *AgentRunResult
	react   *einoreact.Agent
	emit    func(AgentStreamEvent) error
	traceID int

	mu                sync.Mutex
	ignoredToolChoice ToolChoice
	forcedToolChoices []ToolChoice
	observedMessages  map[string]struct{}
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
	}

	tools := make([]einotool.BaseTool, 0, len(a.tools))
	for _, t := range a.tools {
		tools = append(tools, t)
	}

	reactAgent, err := einoreact.NewAgent(ctx, &einoreact.AgentConfig{
		ToolCallingModel: &hookedChatModel{
			base: a.chatModel,
			run:  runtime,
		},
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
		MaxStep:               einoReactMaxStep,
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
	messageFutureOption, messageFuture := einoreact.WithMessageFuture()
	message, err := r.react.Generate(ctx, cloneMessages(r.agent.messages), messageFutureOption)
	if err != nil {
		return nil, err
	}
	if err := r.collectGeneratedMessages(ctx, messageFuture); err != nil {
		return nil, err
	}
	return message, nil
}

func (r *einoAgentRun) Stream(ctx context.Context) (*schema.Message, error) {
	log.Printf("streaming Eino ReAct agent with model %s...", r.agent.model)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	messageFutureOption, messageFuture := einoreact.WithMessageFuture()
	stream, err := r.react.Stream(streamCtx, cloneMessages(r.agent.messages), messageFutureOption)
	if err != nil {
		log.Printf("failed to stream Eino ReAct agent: %v", err)
		return nil, err
	}

	messageObservationDone := make(chan error, 1)
	go func() {
		messageObservationDone <- r.collectStreamedMessages(streamCtx, messageFuture)
	}()

	message, err := schema.ConcatMessageStream(stream)
	if err != nil {
		cancel()
		if observationErr := <-messageObservationDone; observationErr != nil {
			log.Printf("failed to collect Eino ReAct messages: %v", observationErr)
		}
		log.Printf("failed to stream Eino ReAct agent: %v", err)
		return nil, err
	}
	if err := <-messageObservationDone; err != nil {
		return nil, err
	}
	return message, nil
}

func (r *einoAgentRun) RetryIgnoredToolChoice(ctx context.Context) bool {
	r.mu.Lock()
	forced := r.ignoredToolChoice
	r.ignoredToolChoice = ToolChoice{}
	r.mu.Unlock()

	return r.agent.retryIgnoredToolChoice(ctx, r.runCtx, forced)
}

func (r *einoAgentRun) prepareModelCall(ctx context.Context, input []*schema.Message, opts []einomodel.Option) ([]*schema.Message, []einomodel.Option, error) {
	r.runCtx.ModelCallCount++
	if err := r.agent.hooks.BeforeModelCall(ctx, r.runCtx); err != nil {
		return nil, nil, err
	}

	forcedToolChoice := r.runCtx.RequestedToolChoice()
	modelCall, err := r.agent.newModelCallWithMessages(ctx, r.runCtx, input)
	if err != nil {
		return nil, nil, err
	}
	r.rememberForcedToolChoice(forcedToolChoice)

	modelOptions := make([]einomodel.Option, 0, len(opts)+len(modelCall.Options)+1)
	modelOptions = append(modelOptions, opts...)
	modelOptions = append(modelOptions, einomodel.WithTools(modelCall.Tools))
	modelOptions = append(modelOptions, modelCall.Options...)
	return modelCall.Messages, modelOptions, nil
}

func (r *einoAgentRun) collectGeneratedMessages(ctx context.Context, future einoreact.MessageFuture) error {
	iter := future.GetMessages()
	for {
		message, hasNext, err := iter.Next()
		if err != nil {
			return err
		}
		if !hasNext {
			return nil
		}
		if err := r.recordEinoMessage(ctx, message); err != nil {
			return err
		}
	}
}

func (r *einoAgentRun) collectStreamedMessages(ctx context.Context, future einoreact.MessageFuture) error {
	iter := future.GetMessageStreams()
	var firstErr error
	for {
		messageStream, hasNext, err := iter.Next()
		if err != nil {
			return err
		}
		if !hasNext {
			return firstErr
		}
		if err := r.recordEinoMessageStream(ctx, messageStream); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
}

func (r *einoAgentRun) recordEinoMessageStream(ctx context.Context, stream *schema.StreamReader[*schema.Message]) error {
	if stream == nil {
		return nil
	}
	defer stream.Close()

	var contentRouter *streamingContentRouter
	if r.emit != nil {
		contentRouter = r.newContentRouter()
	}

	chunks := make([]*schema.Message, 0)
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if chunk == nil {
			continue
		}
		chunks = append(chunks, chunk)
		if chunk.Role != schema.Tool && chunk.Content != "" {
			r.appendRawContent(chunk.Content)
			if contentRouter != nil {
				if err := contentRouter.Write(chunk.Content); err != nil {
					return err
				}
			}
		}
	}
	if contentRouter != nil {
		if err := contentRouter.Flush(); err != nil {
			return err
		}
	}
	if len(chunks) == 0 {
		return nil
	}

	message, err := schema.ConcatMessages(chunks)
	if err != nil {
		return err
	}
	return r.recordEinoMessage(ctx, message)
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
	forced := r.nextForcedToolChoice()

	r.mu.Lock()
	r.agent.messages = append(r.agent.messages, message)
	if r.emit == nil {
		appendThinkTrace(r.result, message.Content)
	}
	if len(message.ToolCalls) == 0 && forced.Mode != "" {
		r.ignoredToolChoice = forced
	}
	r.mu.Unlock()

	if err := r.agent.hooks.AfterModelResponse(ctx, r.runCtx, message); err != nil {
		return err
	}
	if len(message.ToolCalls) > 0 {
		r.runCtx.ClearToolChoice()
	}
	return nil
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
				toolResult := r.agent.executeToolWithRunner(ctx, r.runCtx, call, func(ctx context.Context) (string, error) {
					output, err := next(ctx, input)
					if output == nil {
						return "", err
					}
					return output.Result, err
				})
				if err := r.recordToolTrace(toolResult); err != nil {
					return nil, err
				}
				return &compose.ToolOutput{Result: toolResult.Content}, nil
			}
		},
	}
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

func (r *einoAgentRun) rememberForcedToolChoice(choice ToolChoice) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.forcedToolChoices = append(r.forcedToolChoices, choice)
}

func (r *einoAgentRun) nextForcedToolChoice() ToolChoice {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.forcedToolChoices) == 0 {
		return ToolChoice{}
	}
	choice := r.forcedToolChoices[0]
	r.forcedToolChoices = r.forcedToolChoices[1:]
	return choice
}

func (r *einoAgentRun) streamContainsToolCall(ctx context.Context, stream *schema.StreamReader[*schema.Message]) (bool, error) {
	containsToolCall, err := streamContainsToolCall(ctx, stream)
	if containsToolCall {
		r.runCtx.ClearToolChoice()
	}
	return containsToolCall, err
}

func (r *einoAgentRun) newTraceID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.traceID++
	return fmt.Sprintf("trace-%d", r.traceID)
}

type hookedChatModel struct {
	base einomodel.BaseChatModel
	run  *einoAgentRun
}

func (m *hookedChatModel) WithTools(_ []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	return m, nil
}

func (m *hookedChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	modelInput, modelOptions, err := m.run.prepareModelCall(ctx, input, opts)
	if err != nil {
		return nil, err
	}

	message, err := m.base.Generate(ctx, modelInput, modelOptions...)
	if err != nil {
		log.Printf("failed to send a new completion request: %v", err)
		return nil, err
	}
	return message, nil
}

func (m *hookedChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	modelInput, modelOptions, err := m.run.prepareModelCall(ctx, input, opts)
	if err != nil {
		return nil, err
	}

	source, err := m.base.Stream(ctx, modelInput, modelOptions...)
	if err != nil {
		log.Printf("failed to stream completion request: %v", err)
		return nil, err
	}
	return source, nil
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

func cloneMessages(messages []*schema.Message) []*schema.Message {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]*schema.Message, len(messages))
	copy(cloned, messages)
	return cloned
}
