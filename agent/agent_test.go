package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"ternura/tool"
)

func TestRunWithTraceUsesEinoReactToolLoop(t *testing.T) {
	fakeTool := &fakeAgentTool{
		name:   tool.AgentTool("fake_tool"),
		result: "tool ok",
	}
	model := &scriptedChatModel{}
	modelHook := &runtimeContextHook{}
	model.generate = func(call int, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
		switch call {
		case 1:
			if len(model.boundTools) != 1 || model.boundTools[0].Name != string(fakeTool.name) {
				t.Fatalf("model tools = %+v, want fake_tool", model.boundTools)
			}
			if !containsSystemContent(input, "first model call") {
				t.Fatalf("first model input missing runtime context: %+v", input)
			}
			return schema.AssistantMessage("", []schema.ToolCall{{
				ID: "call-1",
				Function: schema.FunctionCall{
					Name:      string(fakeTool.name),
					Arguments: `{"value":"hello"}`,
				},
			}}), nil
		case 2:
			if !containsToolMessage(input, "call-1", "tool ok") {
				t.Fatalf("second model input does not contain tool result: %+v", input)
			}
			if !containsSystemContent(input, "second model call") {
				t.Fatalf("second model input missing refreshed runtime context: %+v", input)
			}
			return schema.AssistantMessage("done", nil), nil
		default:
			t.Fatalf("unexpected generate call %d", call)
			return nil, nil
		}
	}

	agent := NewAgent(testModelConfig(), "system", []tool.Tool{fakeTool}, WithHooks(modelHook))
	agent.chatModel = model

	result, err := agent.RunWithTrace(context.Background(), "use the fake tool")
	if err != nil {
		t.Fatalf("run with trace: %v", err)
	}
	if result.Content != "done" {
		t.Fatalf("content = %q, want done", result.Content)
	}
	if model.generateCalls != 2 {
		t.Fatalf("generate calls = %d, want 2", model.generateCalls)
	}
	if modelHook.calls != 2 {
		t.Fatalf("before model hook calls = %d, want 2", modelHook.calls)
	}
	if len(fakeTool.calls) != 1 || fakeTool.calls[0] != `{"value":"hello"}` {
		t.Fatalf("tool calls = %+v", fakeTool.calls)
	}
	if len(result.Trace) != 1 || result.Trace[0].Title != "Tool use: fake_tool" {
		t.Fatalf("trace = %+v", result.Trace)
	}
	if !containsToolMessage(agent.messages, "call-1", "tool ok") {
		t.Fatalf("conversation history does not contain Eino tool message: %+v", agent.messages)
	}
}

func TestRunWithTraceRetriesWhenFinalizeRequiresTool(t *testing.T) {
	fakeTool := &fakeAgentTool{
		name:   tool.AgentTool("fake_tool"),
		result: "grounded output",
	}
	model := &scriptedChatModel{}
	model.generate = func(call int, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
		switch call {
		case 1:
			return schema.AssistantMessage("I ran fake_tool and got a result.", nil), nil
		case 2:
			if !containsSystemContent(input, "must call the fake_tool tool") {
				t.Fatalf("retry input missing required tool policy: %+v", input)
			}
			return schema.AssistantMessage("", []schema.ToolCall{{
				ID: "call-retry",
				Function: schema.FunctionCall{
					Name:      string(fakeTool.name),
					Arguments: `{"value":"retry"}`,
				},
			}}), nil
		case 3:
			if !containsToolMessage(input, "call-retry", "grounded output") {
				t.Fatalf("final input missing tool result: %+v", input)
			}
			return schema.AssistantMessage("Grounded answer.", nil), nil
		default:
			t.Fatalf("unexpected generate call %d", call)
			return nil, nil
		}
	}

	agent := NewAgent(testModelConfig(), "system", []tool.Tool{fakeTool}, WithHooks(finalizeRetryHook{toolName: fakeTool.name}))
	agent.chatModel = model

	result, err := agent.RunWithTrace(context.Background(), "use the fake tool")
	if err != nil {
		t.Fatalf("run with trace: %v", err)
	}
	if result.Content != "Grounded answer." {
		t.Fatalf("content = %q, want grounded answer", result.Content)
	}
	if model.generateCalls != 3 {
		t.Fatalf("generate calls = %d, want 3", model.generateCalls)
	}
	if len(fakeTool.calls) != 1 {
		t.Fatalf("tool calls = %+v, want one retry call", fakeTool.calls)
	}
}

func TestRunStreamingUsesEinoReactToolLoop(t *testing.T) {
	fakeTool := &fakeAgentTool{
		name:   tool.AgentTool("fake_stream_tool"),
		result: "stream tool ok",
	}
	model := &scriptedChatModel{}
	model.stream = func(call int, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
		switch call {
		case 1:
			return schema.StreamReaderFromArray([]*schema.Message{
				schema.AssistantMessage("", []schema.ToolCall{{
					ID: "stream-call-1",
					Function: schema.FunctionCall{
						Name:      string(fakeTool.name),
						Arguments: `{"value":"stream"}`,
					},
				}}),
			}), nil
		case 2:
			if !containsToolMessage(input, "stream-call-1", "stream tool ok") {
				t.Fatalf("second stream input does not contain tool result: %+v", input)
			}
			return schema.StreamReaderFromArray([]*schema.Message{
				schema.AssistantMessage("stream ", nil),
				schema.AssistantMessage("done", nil),
			}), nil
		default:
			t.Fatalf("unexpected stream call %d", call)
			return nil, nil
		}
	}

	agent := NewAgent(testModelConfig(), "system", []tool.Tool{fakeTool})
	agent.chatModel = model

	var events []AgentStreamEvent
	result, err := agent.RunStreaming(context.Background(), "use the fake stream tool", func(event AgentStreamEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("run streaming: %v", err)
	}
	if result.Content != "stream done" {
		t.Fatalf("content = %q, want stream done", result.Content)
	}
	if model.streamCalls != 2 {
		t.Fatalf("stream calls = %d, want 2", model.streamCalls)
	}
	if len(fakeTool.calls) != 1 || fakeTool.calls[0] != `{"value":"stream"}` {
		t.Fatalf("tool calls = %+v", fakeTool.calls)
	}
	if combinedEventDeltas(events, "content_delta") != "stream done" || !hasEvent(events, "done", "stream done") {
		t.Fatalf("events = %+v", events)
	}
	if !containsToolMessage(agent.messages, "stream-call-1", "stream tool ok") {
		t.Fatalf("conversation history does not contain Eino stream tool message: %+v", agent.messages)
	}
}

func TestStreamingContentRouterKeepsUTF8Boundaries(t *testing.T) {
	var deltas []string
	router := newStreamingContentRouter(
		func() string { return "trace-1" },
		func(event AgentStreamEvent) error {
			if event.Type == "content_delta" || event.Type == "trace_delta" {
				deltas = append(deltas, event.Delta)
			}
			return nil
		},
	)

	input := "我的回复应该用简洁的方式告诉用户我使用 UTF-8 编码。"
	raw := []byte(input)
	for _, chunk := range []string{string(raw[:17]), string(raw[17:29]), string(raw[29:])} {
		if err := router.Write(chunk); err != nil {
			t.Fatalf("write chunk: %v", err)
		}
	}
	if err := router.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	for _, delta := range deltas {
		if !utf8.ValidString(delta) {
			t.Fatalf("delta is not valid UTF-8: %q", delta)
		}
	}
}

type fakeAgentTool struct {
	name   tool.AgentTool
	result string
	calls  []string
}

func (t *fakeAgentTool) ToolName() tool.AgentTool {
	return t.name
}

func (t *fakeAgentTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: string(t.name),
		Desc: "fake test tool",
	}, nil
}

func (t *fakeAgentTool) InvokableRun(_ context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	t.calls = append(t.calls, argumentsInJSON)
	return t.result, nil
}

type scriptedChatModel struct {
	generate      func(call int, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error)
	stream        func(call int, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error)
	boundTools    []*schema.ToolInfo
	generateCalls int
	streamCalls   int
}

type runtimeContextHook struct {
	calls int
}

func (h *runtimeContextHook) HookName() string {
	return "runtime_context_test"
}

func (h *runtimeContextHook) BeforeModelCall(_ context.Context, run *RunContext) error {
	h.calls++
	content := "first model call"
	if h.calls > 1 {
		content = "second model call"
	}
	run.SetContextBlock("test-runtime", "Test Runtime", content)
	return nil
}

type finalizeRetryHook struct {
	toolName tool.AgentTool
	retried  bool
}

func (h finalizeRetryHook) HookName() string {
	return "finalize_retry_test"
}

func (h finalizeRetryHook) FinalizeRun(_ context.Context, run *RunContext, result *AgentRunResult) error {
	if run.ToolCallCount > 0 {
		return nil
	}
	run.SetToolPolicy(RequireTool(h.toolName))
	result.Trace = append(result.Trace, AgentTraceItem{
		Type:    "guard",
		Title:   "test retry",
		Content: "retry",
	})
	return nil
}

func (m *scriptedChatModel) WithTools(tools []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	m.boundTools = append([]*schema.ToolInfo(nil), tools...)
	return m, nil
}

func (m *scriptedChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	m.generateCalls++
	if m.generate == nil {
		return nil, errors.New("unexpected Generate call")
	}
	return m.generate(m.generateCalls, input, opts...)
}

func (m *scriptedChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	m.streamCalls++
	if m.stream == nil {
		return nil, errors.New("unexpected Stream call")
	}
	return m.stream(m.streamCalls, input, opts...)
}

func containsToolMessage(messages []*schema.Message, callID string, content string) bool {
	for _, message := range messages {
		if message.Role == schema.Tool && message.ToolCallID == callID && message.Content == content {
			return true
		}
	}
	return false
}

func containsSystemContent(messages []*schema.Message, content string) bool {
	for _, message := range messages {
		if message.Role == schema.System && strings.Contains(message.Content, content) {
			return true
		}
	}
	return false
}

func hasEvent(events []AgentStreamEvent, eventType string, content string) bool {
	for _, event := range events {
		if event.Type != eventType {
			continue
		}
		if event.Content == content || event.Delta == content {
			return true
		}
	}
	return false
}

func combinedEventDeltas(events []AgentStreamEvent, eventType string) string {
	var combined string
	for _, event := range events {
		if event.Type == eventType {
			combined += event.Delta
		}
	}
	return combined
}
