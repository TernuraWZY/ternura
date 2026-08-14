package app

import (
	"testing"

	"ternura/agent"
)

func TestEvaluateCase(t *testing.T) {
	t.Parallel()

	failures := evaluateCase(evalCase{
		ExpectContains:    []string{"answer"},
		ExpectNotContains: []string{"invented"},
		RequireTools:      []string{"read"},
		MaxModelCalls:     2,
		MaxToolCalls:      1,
	}, agent.AgentRunResult{
		Content: "answer",
		Trace: []agent.AgentTraceItem{{
			Type:  "tool",
			Title: "Tool use: read",
		}},
		Metrics: agent.RunMetrics{ModelCalls: 2, ToolCalls: 1},
	})
	if len(failures) != 0 {
		t.Fatalf("unexpected failures: %v", failures)
	}
}
