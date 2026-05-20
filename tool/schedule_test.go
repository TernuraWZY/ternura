package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNormalizeScheduleTaskInput(t *testing.T) {
	now := time.Date(2026, 5, 20, 20, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	input, err := NormalizeScheduleTaskInputAt(ScheduleTaskInput{
		Prompt: "  check the build  ",
		RunAt:  "2026-05-20T23:30:00+08:00",
	}, now)
	if err != nil {
		t.Fatalf("normalize schedule: %v", err)
	}
	if input.Prompt != "check the build" {
		t.Fatalf("prompt = %q", input.Prompt)
	}
	if input.Title != "check the build" {
		t.Fatalf("title = %q", input.Title)
	}
	if !strings.Contains(input.RunAt, "2026-05-20T23:30:00") {
		t.Fatalf("run_at = %q", input.RunAt)
	}
}

func TestNormalizeScheduleTaskInputWithDelaySeconds(t *testing.T) {
	now := time.Date(2026, 5, 21, 9, 0, 0, 0, time.UTC)
	input, err := NormalizeScheduleTaskInputAt(ScheduleTaskInput{
		Prompt:       "remind me to shower",
		DelaySeconds: 120,
	}, now)
	if err != nil {
		t.Fatalf("normalize relative schedule: %v", err)
	}
	if input.RunAt != "2026-05-21T09:02:00Z" {
		t.Fatalf("run_at = %q", input.RunAt)
	}
}

func TestNormalizeScheduleTaskInputRejectsPastRunAt(t *testing.T) {
	now := time.Date(2026, 5, 21, 9, 0, 0, 0, time.UTC)
	_, err := NormalizeScheduleTaskInputAt(ScheduleTaskInput{
		Prompt: "remind me",
		RunAt:  "2026-01-11T16:09:45+08:00",
	}, now)
	if err == nil {
		t.Fatalf("expected past run_at to fail")
	}
}

func TestScheduleTaskToolExecutesCallback(t *testing.T) {
	var got ScheduleTaskInput
	tool := NewScheduleTaskTool(func(_ context.Context, input ScheduleTaskInput) (ScheduleTaskResult, error) {
		got = input
		return ScheduleTaskResult{ID: "schedule-test", Title: input.Title, Prompt: input.Prompt, RunAt: input.RunAt}, nil
	})

	result, err := tool.Execute(context.Background(), `{"title":"Build check","prompt":"check the build","delay_seconds":120}`)
	if err != nil {
		t.Fatalf("execute schedule tool: %v", err)
	}
	if got.Title != "Build check" || got.Prompt != "check the build" {
		t.Fatalf("callback input = %+v", got)
	}
	if !strings.Contains(result, "schedule-test") {
		t.Fatalf("result = %q", result)
	}
}
