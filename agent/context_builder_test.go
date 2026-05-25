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
