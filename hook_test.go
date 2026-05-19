package ternura

import (
	"context"
	"errors"
	"strings"
	"testing"

	"ternura/shared"
	"ternura/tool"
)

func TestRunContextContextBlocksReplaceAndRender(t *testing.T) {
	runCtx := NewRunContext("hello", RunModeSync)
	runCtx.SetContextBlock("memory", "Memory", "first")
	runCtx.SetContextBlock("memory", "Memory", "second")
	runCtx.AddContextBlock("Todos", "- ship hooks")

	rendered := runCtx.RuntimeContextText()
	if strings.Count(rendered, "## Memory") != 1 {
		t.Fatalf("memory block should be replaced once, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "second") || strings.Contains(rendered, "first") {
		t.Fatalf("context block replacement failed:\n%s", rendered)
	}
	if !strings.Contains(rendered, "## Todos") {
		t.Fatalf("added context block missing:\n%s", rendered)
	}
}

func TestChatCompletionParamsFilterDisabledTools(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{
		tool.NewReadTool(),
		tool.NewBashTool(),
	})
	runCtx := NewRunContext("hello", RunModeSync)
	runCtx.DisableTool(tool.AgentToolBash, "shell disabled")

	params := agent.newChatCompletionParams(runCtx)

	if len(params.Tools) != 1 {
		t.Fatalf("tools = %d, want 1 enabled tool", len(params.Tools))
	}
}

func TestRuntimeContextDoesNotAddSecondSystemMessage(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", nil)
	agent.RestoreConversation([]ConversationMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	})
	runCtx := NewRunContext("next", RunModeSync)
	runCtx.SetContextBlock("memory", "Memory", "User prefers concise replies.")

	params := agent.newChatCompletionParams(runCtx)

	if len(params.Messages) != len(agent.messages) {
		t.Fatalf("messages = %d, want %d; runtime context should be merged into first system message", len(params.Messages), len(agent.messages))
	}
}

func TestBeforeToolCallHookCanBlockExecution(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{
		tool.NewBashTool(),
	}, WithHooks(blockingToolHook{}))
	runCtx := NewRunContext("hello", RunModeSync)

	result := agent.executeTool(context.Background(), runCtx, ToolCall{
		ID:        "call-1",
		Name:      string(tool.AgentToolBash),
		Arguments: `{"command":"echo should-not-run"}`,
	})

	if result.Err == nil {
		t.Fatalf("expected hook to block tool call")
	}
	if !strings.Contains(result.Content, "blocked by policy") {
		t.Fatalf("result content = %q", result.Content)
	}
	if runCtx.ToolCallCount != 1 {
		t.Fatalf("tool call count = %d, want 1", runCtx.ToolCallCount)
	}
}

type blockingToolHook struct{}

func (blockingToolHook) HookName() string {
	return "blocker"
}

func (blockingToolHook) BeforeToolCall(context.Context, *RunContext, *ToolCall) error {
	return errors.New("blocked by policy")
}

func testModelConfig() shared.ModelConfig {
	return shared.ModelConfig{
		BaseURL: "http://example.test/v1",
		ApiKey:  "test-key",
		Model:   "test-model",
	}
}
