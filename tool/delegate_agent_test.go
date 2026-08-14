package tool

import (
	"context"
	"strings"
	"testing"
)

func TestDelegateAgentToolRunsNamedAgent(t *testing.T) {
	var captured DelegateAgentParams
	delegateTool := NewDelegateAgentTool("delegate", func(_ context.Context, params DelegateAgentParams) (string, error) {
		captured = params
		return "delegated result", nil
	})

	result, err := delegateTool.InvokableRun(context.Background(), `{"agent":" researcher ","task":" inspect context "}`)
	if err != nil {
		t.Fatalf("delegate agent: %v", err)
	}
	if result != "delegated result" || captured.Agent != "researcher" || captured.Task != "inspect context" {
		t.Fatalf("result = %q, params = %+v", result, captured)
	}
}

func TestDelegateAgentToolRejectsEmptyTask(t *testing.T) {
	delegateTool := NewDelegateAgentTool("delegate", func(_ context.Context, params DelegateAgentParams) (string, error) {
		return params.Task, nil
	})

	_, err := delegateTool.InvokableRun(context.Background(), `{"agent":"researcher","task":" "}`)
	if err == nil || !strings.Contains(err.Error(), "task is required") {
		t.Fatalf("error = %v, want task validation", err)
	}
}
