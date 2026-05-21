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

func TestChatCompletionParamsDefaultsToolChoiceToAuto(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{tool.NewBashTool()})
	runCtx := NewRunContext("hello", RunModeSync)

	params := agent.newChatCompletionParams(runCtx)

	if params.ToolChoice.OfFunctionToolChoice != nil {
		t.Fatalf("expected no specific tool choice by default, got %+v", params.ToolChoice.OfFunctionToolChoice)
	}
	if params.ToolChoice.OfAuto.Valid() {
		t.Fatalf("expected OfAuto to be unset, got %q", params.ToolChoice.OfAuto.Value)
	}
}

func TestChatCompletionParamsAppliesSpecificToolChoice(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{tool.NewBashTool()})
	runCtx := NewRunContext("run command", RunModeSync)
	runCtx.SetToolChoice(ToolChoice{Mode: ToolChoiceSpecific, Name: tool.AgentToolBash})

	params := agent.newChatCompletionParams(runCtx)

	if params.ToolChoice.OfFunctionToolChoice == nil {
		t.Fatalf("expected specific tool choice to be set")
	}
	if got := params.ToolChoice.OfFunctionToolChoice.Function.Name; got != string(tool.AgentToolBash) {
		t.Fatalf("tool choice name = %q, want %q", got, string(tool.AgentToolBash))
	}
}

func TestChatCompletionParamsAppliesRequiredToolChoice(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{tool.NewBashTool()})
	runCtx := NewRunContext("run command", RunModeSync)
	runCtx.SetToolChoice(ToolChoice{Mode: ToolChoiceRequired})

	params := agent.newChatCompletionParams(runCtx)

	if !params.ToolChoice.OfAuto.Valid() {
		t.Fatalf("expected OfAuto to be set to \"required\"")
	}
	if got := params.ToolChoice.OfAuto.Value; got != "required" {
		t.Fatalf("tool choice = %q, want \"required\"", got)
	}
}

func TestChatCompletionParamsDropsToolChoiceWhenTargetUnavailable(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{tool.NewBashTool()})
	runCtx := NewRunContext("run command", RunModeSync)
	runCtx.SetToolChoice(ToolChoice{Mode: ToolChoiceSpecific, Name: tool.AgentToolRead})

	params := agent.newChatCompletionParams(runCtx)

	if params.ToolChoice.OfFunctionToolChoice != nil {
		t.Fatalf("expected tool choice to be dropped when target unavailable, got %+v", params.ToolChoice.OfFunctionToolChoice)
	}
}

func TestChatCompletionParamsDropsToolChoiceWhenTargetDisabled(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{tool.NewBashTool()})
	runCtx := NewRunContext("run command", RunModeSync)
	runCtx.DisableTool(tool.AgentToolBash, "shell disabled")
	runCtx.SetToolChoice(ToolChoice{Mode: ToolChoiceSpecific, Name: tool.AgentToolBash})

	params := agent.newChatCompletionParams(runCtx)

	if params.ToolChoice.OfFunctionToolChoice != nil {
		t.Fatalf("expected tool choice to be dropped when target disabled, got %+v", params.ToolChoice.OfFunctionToolChoice)
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
	toolResults := runCtx.ToolResults()
	if len(toolResults) != 1 {
		t.Fatalf("recorded tool results = %d, want 1", len(toolResults))
	}
	if toolResults[0].Error == "" || !strings.Contains(toolResults[0].Content, "blocked by policy") {
		t.Fatalf("recorded blocked tool result = %+v", toolResults[0])
	}
}

func TestFinalizeRunHookCanRewriteResult(t *testing.T) {
	runCtx := NewRunContext("hello", RunModeSync)
	result := AgentRunResult{Content: "raw"}

	err := NewHookManager(finalizeHook{}).FinalizeRun(context.Background(), runCtx, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if result.Content != "finalized" {
		t.Fatalf("result content = %q, want finalized", result.Content)
	}
}

type blockingToolHook struct{}

func (blockingToolHook) HookName() string {
	return "blocker"
}

func (blockingToolHook) BeforeToolCall(context.Context, *RunContext, *ToolCall) error {
	return errors.New("blocked by policy")
}

type finalizeHook struct{}

func (finalizeHook) HookName() string {
	return "finalizer"
}

func (finalizeHook) FinalizeRun(_ context.Context, _ *RunContext, result *AgentRunResult) error {
	result.Content = "finalized"
	return nil
}

func testModelConfig() shared.ModelConfig {
	return shared.ModelConfig{
		BaseURL: "http://example.test/v1",
		ApiKey:  "test-key",
		Model:   "test-model",
	}
}
