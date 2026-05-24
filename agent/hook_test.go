package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/compose"
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

func TestToolsForRunFiltersDisabledTools(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{
		tool.NewReadTool(),
		tool.NewBashTool(),
	})
	runCtx := NewRunContext("hello", RunModeSync)
	runCtx.DisableTool(tool.AgentToolBash, "shell disabled")

	tools := agent.toolsForRun(runCtx)

	if len(tools) != 1 {
		t.Fatalf("tools = %d, want 1 enabled tool", len(tools))
	}
	info, err := tools[0].Info(context.Background())
	if err != nil {
		t.Fatalf("tool info: %v", err)
	}
	if info.Name != string(tool.AgentToolRead) {
		t.Fatalf("tool = %q, want read", info.Name)
	}
}

func TestToolsForRunDefaultsToolPolicyToAuto(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{tool.NewBashTool()})
	runCtx := NewRunContext("hello", RunModeSync)

	_, available := agent.enabledToolsForRun(runCtx)

	if policy := effectiveToolPolicy(runCtx, available); !policy.Empty() {
		t.Fatalf("expected no tool policy by default, got %+v", policy)
	}
}

func TestToolsForRunAppliesRequiredSpecificToolPolicy(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{tool.NewBashTool()})
	runCtx := NewRunContext("run command", RunModeSync)
	runCtx.SetToolPolicy(RequireTool(tool.AgentToolBash))

	tools := agent.toolsForRun(runCtx)
	_, available := agent.enabledToolsForRun(runCtx)
	policy := effectiveToolPolicy(runCtx, available)

	if !policy.Required || len(policy.AllowedTools) != 1 || policy.AllowedTools[0] != tool.AgentToolBash {
		t.Fatalf("expected required bash tool policy, got %+v", policy)
	}
	if len(tools) != 1 {
		t.Fatalf("tools = %d, want only selected tool", len(tools))
	}
	info, err := tools[0].Info(context.Background())
	if err != nil {
		t.Fatalf("tool info: %v", err)
	}
	if got := info.Name; got != string(tool.AgentToolBash) {
		t.Fatalf("tool name = %q, want %q", got, string(tool.AgentToolBash))
	}
}

func TestToolsForRunAppliesRequiredAnyToolPolicy(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{tool.NewReadTool(), tool.NewBashTool()})
	runCtx := NewRunContext("run command", RunModeSync)
	runCtx.SetToolPolicy(RequireAnyTool())

	tools := agent.toolsForRun(runCtx)
	_, available := agent.enabledToolsForRun(runCtx)
	policy := effectiveToolPolicy(runCtx, available)

	if !policy.Required || len(policy.AllowedTools) != 0 {
		t.Fatalf("expected required any-tool policy, got %+v", policy)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %d, want all available tools", len(tools))
	}
}

func TestToolsForRunDropsToolPolicyWhenTargetUnavailable(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{tool.NewBashTool()})
	runCtx := NewRunContext("run command", RunModeSync)
	runCtx.SetToolPolicy(RequireTool(tool.AgentToolRead))

	_, available := agent.enabledToolsForRun(runCtx)

	if policy := effectiveToolPolicy(runCtx, available); !policy.Empty() {
		t.Fatalf("expected tool policy to be dropped when target unavailable, got %+v", policy)
	}
}

func TestToolsForRunDropsToolPolicyWhenTargetDisabled(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{tool.NewBashTool()})
	runCtx := NewRunContext("run command", RunModeSync)
	runCtx.DisableTool(tool.AgentToolBash, "shell disabled")
	runCtx.SetToolPolicy(RequireTool(tool.AgentToolBash))

	_, available := agent.enabledToolsForRun(runCtx)

	if policy := effectiveToolPolicy(runCtx, available); !policy.Empty() {
		t.Fatalf("expected tool policy to be dropped when target disabled, got %+v", policy)
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

	messages := agent.messagesWithRuntimeContext(runCtx)

	if len(messages) != len(agent.messages) {
		t.Fatalf("messages = %d, want %d; runtime context should be merged into first system message", len(messages), len(agent.messages))
	}
}

func TestBeforeToolCallHookCanBlockExecution(t *testing.T) {
	agent := NewAgent(testModelConfig(), "system", []tool.Tool{
		tool.NewBashTool(),
	}, WithHooks(blockingToolHook{}))
	runCtx := NewRunContext("hello", RunModeSync)
	runtime := &einoAgentRun{
		agent:  agent,
		runCtx: runCtx,
		result: &AgentRunResult{Trace: make([]AgentTraceItem, 0)},
	}
	middleware := runtime.toolCallMiddleware()

	wrapped := middleware.Invokable(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) {
		t.Fatalf("next should not execute when BeforeToolCall blocks")
		return nil, nil
	})
	output, err := wrapped(context.Background(), &compose.ToolInput{
		Name:      string(tool.AgentToolBash),
		Arguments: `{"command":"echo should-not-run"}`,
		CallID:    "call-1",
	})

	if err != nil {
		t.Fatalf("middleware returned error: %v", err)
	}
	if output == nil || !strings.Contains(output.Result, "blocked by policy") {
		t.Fatalf("output = %+v, want blocked result", output)
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

func (blockingToolHook) BeforeToolCall(context.Context, *RunContext, *schema.ToolCall) error {
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
