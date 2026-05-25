package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestContextBuilderBuildClonesMessagesWithoutRuntimeContext(t *testing.T) {
	input := []*schema.Message{
		schema.SystemMessage("system"),
		schema.UserMessage("hello"),
	}
	builder := NewContextBuilder("system")

	messages, err := builder.Build(context.Background(), nil, input)

	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if len(messages) != len(input) {
		t.Fatalf("messages = %d, want %d", len(messages), len(input))
	}
	if &messages[0] == &input[0] {
		t.Fatalf("message slice was not cloned")
	}
	messages[0] = schema.SystemMessage("changed")
	if input[0].Content != "system" {
		t.Fatalf("input was mutated: %+v", input[0])
	}
}

func TestContextBuilderBuildMergesRuntimeContextIntoSystemMessage(t *testing.T) {
	input := []*schema.Message{
		schema.SystemMessage("system"),
		schema.UserMessage("hello"),
	}
	runCtx := NewRunContext("hello", RunModeSync)
	runCtx.SetContextBlock("memory", "Memory", "User prefers concise replies.")
	builder := NewContextBuilder("system")

	messages, err := builder.Build(context.Background(), runCtx, input)

	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if len(messages) != len(input) {
		t.Fatalf("messages = %d, want %d", len(messages), len(input))
	}
	if messages[0].Role != schema.System {
		t.Fatalf("first message role = %s, want system", messages[0].Role)
	}
	if !strings.Contains(messages[0].Content, "## Memory") {
		t.Fatalf("system message missing runtime context:\n%s", messages[0].Content)
	}
	if strings.Count(messages[0].Content, "system") != 1 {
		t.Fatalf("system prompt should appear once:\n%s", messages[0].Content)
	}
}

func TestContextBuilderBuildDropsHistoricalToolExchangeBeforeLatestUser(t *testing.T) {
	oldToolCall := schema.AssistantMessage("", []schema.ToolCall{{
		ID: "old-call",
		Function: schema.FunctionCall{
			Name:      "read",
			Arguments: `{"path":"old.txt"}`,
		},
	}})
	currentToolCall := schema.AssistantMessage("", []schema.ToolCall{{
		ID: "current-call",
		Function: schema.FunctionCall{
			Name:      "read",
			Arguments: `{"path":"current.txt"}`,
		},
	}})
	input := []*schema.Message{
		schema.SystemMessage("system"),
		schema.UserMessage("old question"),
		oldToolCall,
		schema.ToolMessage("old huge output", "old-call", schema.WithToolName("read")),
		schema.AssistantMessage("old final answer", nil),
		schema.UserMessage("current question"),
		currentToolCall,
		schema.ToolMessage("current output", "current-call", schema.WithToolName("read")),
	}
	builder := NewContextBuilder("system")

	messages, err := builder.Build(context.Background(), nil, input)

	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if hasAssistantToolCall(messages, "old-call") {
		t.Fatalf("historical assistant tool call should be dropped: %+v", messages)
	}
	if containsToolMessage(messages, "old-call", "old huge output") {
		t.Fatalf("historical tool result should be dropped: %+v", messages)
	}
	if !containsAssistantContent(messages, "old final answer") {
		t.Fatalf("historical final assistant answer should be kept: %+v", messages)
	}
	if !hasAssistantToolCall(messages, "current-call") {
		t.Fatalf("current run assistant tool call should be kept: %+v", messages)
	}
	if !containsToolMessage(messages, "current-call", "current output") {
		t.Fatalf("current run tool result should be kept: %+v", messages)
	}
}

func hasAssistantToolCall(messages []*schema.Message, callID string) bool {
	for _, message := range messages {
		if message == nil || message.Role != schema.Assistant {
			continue
		}
		for _, call := range message.ToolCalls {
			if call.ID == callID {
				return true
			}
		}
	}
	return false
}

func containsAssistantContent(messages []*schema.Message, content string) bool {
	for _, message := range messages {
		if message != nil && message.Role == schema.Assistant && message.Content == content {
			return true
		}
	}
	return false
}
