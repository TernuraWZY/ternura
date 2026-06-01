package agent

import (
	"errors"
	"strings"
	"testing"

	"ternura/tool"
)

func TestRunContextEnforcesNamedToolBudget(t *testing.T) {
	run := NewRunContext("fetch", RunModeSync)
	run.SetRunLimits(RunLimits{
		MaxReactSteps: 10,
		MaxToolCalls:  3,
		MaxToolCallsByName: map[tool.AgentTool]int{
			tool.AgentToolWebFetch: 1,
		},
	})

	if err := run.reserveToolCall(tool.AgentToolWebFetch); err != nil {
		t.Fatalf("first web_fetch should be allowed: %v", err)
	}
	err := run.reserveToolCall(tool.AgentToolWebFetch)
	if !errors.Is(err, ErrRunBudgetExceeded) {
		t.Fatalf("second web_fetch error = %v, want budget exceeded", err)
	}
	if !strings.Contains(err.Error(), "web_fetch call limit") {
		t.Fatalf("error should mention named tool limit: %v", err)
	}
	if run.ToolCallCount != 1 {
		t.Fatalf("tool call count = %d, want only successful reservations counted", run.ToolCallCount)
	}
}

func TestRunContextEnforcesTotalToolBudget(t *testing.T) {
	run := NewRunContext("tools", RunModeSync)
	run.SetRunLimits(RunLimits{MaxReactSteps: 10, MaxToolCalls: 1})

	if err := run.reserveToolCall(tool.AgentToolRead); err != nil {
		t.Fatalf("first tool should be allowed: %v", err)
	}
	err := run.reserveToolCall(tool.AgentToolBash)
	if !errors.Is(err, ErrRunBudgetExceeded) {
		t.Fatalf("second tool error = %v, want budget exceeded", err)
	}
	if !strings.Contains(err.Error(), "total tool call limit") {
		t.Fatalf("error should mention total tool budget: %v", err)
	}
}

func TestRunContextEnforcesModelBudget(t *testing.T) {
	run := NewRunContext("model", RunModeSync)
	run.SetRunLimits(RunLimits{MaxReactSteps: 10, MaxModelCalls: 1})

	if err := run.reserveModelCall(); err != nil {
		t.Fatalf("first model call should be allowed: %v", err)
	}
	err := run.reserveModelCall()
	if !errors.Is(err, ErrRunBudgetExceeded) {
		t.Fatalf("second model call error = %v, want budget exceeded", err)
	}
	if !strings.Contains(err.Error(), "model call limit") {
		t.Fatalf("error should mention model budget: %v", err)
	}
}

func TestWebFetchBudgetMessageIsUserFriendly(t *testing.T) {
	err := RunBudgetError{
		Kind:  "tool",
		Tool:  tool.AgentToolWebFetch,
		Limit: 5,
	}

	toolContent := budgetExceededToolContent(err)
	if !strings.Contains(toolContent, "没有 fetch 到更多有效网页信息") {
		t.Fatalf("tool content should explain fetch failure, got %q", toolContent)
	}
	if strings.Contains(toolContent, "run budget exceeded") {
		t.Fatalf("tool content should not expose internal budget wording: %q", toolContent)
	}

	finalMessage := budgetExceededFinalMessage(err)
	if !strings.Contains(finalMessage, "没有 fetch 到更多有效网页信息") {
		t.Fatalf("final message should explain fetch failure, got %q", finalMessage)
	}
	if strings.Contains(finalMessage, "run budget exceeded") {
		t.Fatalf("final message should not expose internal budget wording: %q", finalMessage)
	}
}
