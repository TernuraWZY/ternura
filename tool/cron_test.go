package tool

import (
	"context"
	"strings"
	"testing"
)

func TestCronToolAddRequiresMessage(t *testing.T) {
	tool := NewCronTool(nil, nil, nil)
	out, err := tool.InvokableRun(context.Background(), `{"action":"add"}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "message is required") {
		t.Fatalf("output = %q", out)
	}
}

func TestCronToolAddExecutesCallback(t *testing.T) {
	var got CronAddParams
	tool := NewCronTool(func(_ context.Context, params CronAddParams) (CronAddResult, error) {
		got = params
		return CronAddResult{ID: "cron-test", Name: "test", NextRunAt: "2099-01-01T09:00:00Z"}, nil
	}, nil, nil)

	out, err := tool.InvokableRun(context.Background(), `{"action":"add","message":"remind user","delay_seconds":120}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got.Message != "remind user" || got.DelaySeconds != 120 {
		t.Fatalf("params = %+v", got)
	}
	if !strings.Contains(out, "cron-test") {
		t.Fatalf("output = %q", out)
	}
}

func TestCronToolBlocksNestedAdd(t *testing.T) {
	tool := NewCronTool(func(context.Context, CronAddParams) (CronAddResult, error) {
		t.Fatal("add should not be called")
		return CronAddResult{}, nil
	}, nil, nil)
	tool.SetCronContext(true)
	out, err := tool.InvokableRun(context.Background(), `{"action":"add","message":"nested","delay_seconds":60}`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "cannot schedule new jobs") {
		t.Fatalf("output = %q", out)
	}
}
