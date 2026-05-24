package agent

import (
	"context"
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
}

func (a *Agent) newEinoAgentRun(ctx context.Context, runCtx *RunContext, result *AgentRunResult, emit func(AgentStreamEvent) error) (*einoAgentRun, error) {
	if err := a.ensureChatModel(); err != nil {
		return nil, err
	}

	runtime := &einoAgentRun{
		agent:  a,
		runCtx: runCtx,
		result: result,
		emit:   emit,
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
		StreamToolCallChecker: streamContainsToolCall,
	})
	if err != nil {
		return nil, err
	}

	runtime.react = reactAgent
	return runtime, nil
}

func (r *einoAgentRun) Generate(ctx context.Context) (*schema.Message, error) {
	log.Printf("calling Eino ReAct agent with model %s...", r.agent.model)
	return r.react.Generate(ctx, cloneMessages(r.agent.messages))
}

func (r *einoAgentRun) Stream(ctx context.Context) (*schema.Message, error) {
	log.Printf("streaming Eino ReAct agent with model %s...", r.agent.model)
	stream, err := r.react.Stream(ctx, cloneMessages(r.agent.messages))
	if err != nil {
		log.Printf("failed to stream Eino ReAct agent: %v", err)
		return nil, err
	}
	message, err := schema.ConcatMessageStream(stream)
	if err != nil {
		log.Printf("failed to stream Eino ReAct agent: %v", err)
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

func (r *einoAgentRun) prepareModelCall(ctx context.Context, input []*schema.Message, opts []einomodel.Option) ([]*schema.Message, []einomodel.Option, ToolChoice, error) {
	r.runCtx.ModelCallCount++
	if err := r.agent.hooks.BeforeModelCall(ctx, r.runCtx); err != nil {
		return nil, nil, ToolChoice{}, err
	}

	forcedToolChoice := r.runCtx.RequestedToolChoice()
	modelCall, err := r.agent.newModelCallWithMessages(ctx, r.runCtx, input)
	if err != nil {
		return nil, nil, ToolChoice{}, err
	}

	modelOptions := make([]einomodel.Option, 0, len(opts)+len(modelCall.Options)+1)
	modelOptions = append(modelOptions, opts...)
	modelOptions = append(modelOptions, einomodel.WithTools(modelCall.Tools))
	modelOptions = append(modelOptions, modelCall.Options...)
	return modelCall.Messages, modelOptions, forcedToolChoice, nil
}

func (r *einoAgentRun) recordModelResponse(ctx context.Context, message *schema.Message, forced ToolChoice) error {
	if message == nil {
		return nil
	}

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
				if err := r.recordToolResult(toolResult); err != nil {
					return nil, err
				}
				return &compose.ToolOutput{Result: toolResult.Content}, nil
			}
		},
	}
}

func (r *einoAgentRun) recordToolResult(toolResult ToolResult) error {
	traceItem := toolTraceFromResult(toolResult)

	r.mu.Lock()
	r.result.Trace = append(r.result.Trace, traceItem)
	r.agent.messages = append(r.agent.messages, schema.ToolMessage(toolResult.Content, toolResult.Call.ID, schema.WithToolName(toolResult.Call.Function.Name)))
	r.mu.Unlock()

	if r.emit != nil {
		if err := emitTraceItem(r.emit, r.newTraceID(), traceItem); err != nil {
			return err
		}
	}
	log.Printf("tool call %s, arguments %s, error: %v", toolResult.Call.Function.Name, toolResult.Call.Function.Arguments, toolResult.Err)
	return nil
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
	modelInput, modelOptions, forcedToolChoice, err := m.run.prepareModelCall(ctx, input, opts)
	if err != nil {
		return nil, err
	}

	message, err := m.base.Generate(ctx, modelInput, modelOptions...)
	if err != nil {
		log.Printf("failed to send a new completion request: %v", err)
		return nil, err
	}
	if err := m.run.recordModelResponse(ctx, message, forcedToolChoice); err != nil {
		return nil, err
	}
	return message, nil
}

func (m *hookedChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	modelInput, modelOptions, forcedToolChoice, err := m.run.prepareModelCall(ctx, input, opts)
	if err != nil {
		return nil, err
	}

	source, err := m.base.Stream(ctx, modelInput, modelOptions...)
	if err != nil {
		log.Printf("failed to stream completion request: %v", err)
		return nil, err
	}

	reader, writer := schema.Pipe[*schema.Message](1)
	go func() {
		defer source.Close()
		defer writer.Close()

		var contentRouter *streamingContentRouter
		if m.run.emit != nil {
			contentRouter = m.run.newContentRouter()
		}

		chunks := make([]*schema.Message, 0)
		for {
			chunk, recvErr := source.Recv()
			if errors.Is(recvErr, io.EOF) {
				break
			}
			if recvErr != nil {
				writer.Send(nil, recvErr)
				return
			}
			if chunk == nil {
				continue
			}
			chunks = append(chunks, chunk)
			if chunk.Content != "" {
				m.run.appendRawContent(chunk.Content)
				if contentRouter != nil {
					if err := contentRouter.Write(chunk.Content); err != nil {
						writer.Send(nil, err)
						return
					}
				}
			}
			if writer.Send(chunk, nil) {
				return
			}
		}
		if contentRouter != nil {
			if err := contentRouter.Flush(); err != nil {
				writer.Send(nil, err)
				return
			}
		}

		if len(chunks) == 0 {
			return
		}
		message, concatErr := schema.ConcatMessages(chunks)
		if concatErr != nil {
			writer.Send(nil, concatErr)
			return
		}
		if err := m.run.recordModelResponse(ctx, message, forcedToolChoice); err != nil {
			writer.Send(nil, err)
			return
		}
	}()

	return reader, nil
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
