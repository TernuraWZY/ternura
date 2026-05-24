package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"ternura/config"
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

func TestModelCallFiltersDisabledTools(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{
		tool.NewReadTool(),
		tool.NewBashTool(),
	})
	runCtx := NewRunContext("hello", RunModeSync)
	runCtx.DisableTool(tool.AgentToolBash, "shell disabled")

	req, err := agent.newModelCall(runCtx)

	if err != nil {
		t.Fatalf("new model call: %v", err)
	}
	if len(req.Tools) != 1 {
		t.Fatalf("tools = %d, want 1 enabled tool", len(req.Tools))
	}
}

func TestModelCallDefaultsToolChoiceToAuto(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{tool.NewBashTool()})
	runCtx := NewRunContext("hello", RunModeSync)

	req, err := agent.newModelCall(runCtx)
	if err != nil {
		t.Fatalf("new model call: %v", err)
	}
	opts := einomodel.GetCommonOptions(&einomodel.Options{}, req.Options...)

	if opts.ToolChoice != nil {
		t.Fatalf("expected no tool choice by default, got %+v", opts.ToolChoice)
	}
}

func TestModelCallAppliesSpecificToolChoice(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{tool.NewBashTool()})
	runCtx := NewRunContext("run command", RunModeSync)
	runCtx.SetToolChoice(ToolChoice{Mode: ToolChoiceSpecific, Name: tool.AgentToolBash})

	req, err := agent.newModelCall(runCtx)
	if err != nil {
		t.Fatalf("new model call: %v", err)
	}
	opts := einomodel.GetCommonOptions(&einomodel.Options{}, req.Options...)

	if opts.ToolChoice == nil || *opts.ToolChoice != schema.ToolChoiceForced {
		t.Fatalf("expected forced tool choice, got %+v", opts.ToolChoice)
	}
	if len(opts.Tools) != 1 {
		t.Fatalf("tools = %d, want only selected tool", len(opts.Tools))
	}
	if got := opts.Tools[0].Name; got != string(tool.AgentToolBash) {
		t.Fatalf("tool choice name = %q, want %q", got, string(tool.AgentToolBash))
	}
}

func TestModelCallAppliesRequiredToolChoice(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{tool.NewReadTool(), tool.NewBashTool()})
	runCtx := NewRunContext("run command", RunModeSync)
	runCtx.SetToolChoice(ToolChoice{Mode: ToolChoiceRequired})

	req, err := agent.newModelCall(runCtx)
	if err != nil {
		t.Fatalf("new model call: %v", err)
	}
	opts := einomodel.GetCommonOptions(&einomodel.Options{}, req.Options...)

	if opts.ToolChoice == nil || *opts.ToolChoice != schema.ToolChoiceForced {
		t.Fatalf("expected forced tool choice, got %+v", opts.ToolChoice)
	}
	if len(opts.Tools) != 2 {
		t.Fatalf("tools = %d, want all available tools", len(opts.Tools))
	}
}

func TestModelCallDropsToolChoiceWhenTargetUnavailable(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{tool.NewBashTool()})
	runCtx := NewRunContext("run command", RunModeSync)
	runCtx.SetToolChoice(ToolChoice{Mode: ToolChoiceSpecific, Name: tool.AgentToolRead})

	req, err := agent.newModelCall(runCtx)
	if err != nil {
		t.Fatalf("new model call: %v", err)
	}
	opts := einomodel.GetCommonOptions(&einomodel.Options{}, req.Options...)

	if opts.ToolChoice != nil {
		t.Fatalf("expected tool choice to be dropped when target unavailable, got %+v", opts.ToolChoice)
	}
}

func TestModelCallDropsToolChoiceWhenTargetDisabled(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{tool.NewBashTool()})
	runCtx := NewRunContext("run command", RunModeSync)
	runCtx.DisableTool(tool.AgentToolBash, "shell disabled")
	runCtx.SetToolChoice(ToolChoice{Mode: ToolChoiceSpecific, Name: tool.AgentToolBash})

	req, err := agent.newModelCall(runCtx)
	if err != nil {
		t.Fatalf("new model call: %v", err)
	}
	opts := einomodel.GetCommonOptions(&einomodel.Options{}, req.Options...)

	if opts.ToolChoice != nil {
		t.Fatalf("expected tool choice to be dropped when target disabled, got %+v", opts.ToolChoice)
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

	req, err := agent.newModelCall(runCtx)

	if err != nil {
		t.Fatalf("new model call: %v", err)
	}
	if len(req.Messages) != len(agent.messages) {
		t.Fatalf("messages = %d, want %d; runtime context should be merged into first system message", len(req.Messages), len(agent.messages))
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

func testModelConfig() config.ModelConfig {
	return config.ModelConfig{
		BaseURL: "http://example.test/v1",
		ApiKey:  "test-key",
		Model:   "test-model",
	}
}
