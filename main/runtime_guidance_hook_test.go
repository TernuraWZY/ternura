package main

import (
	"context"
	"strings"
	"testing"

	"ternura"
)

func TestCurrentTimeHookAddsRuntimeContext(t *testing.T) {
	run := ternura.NewRunContext("hello", ternura.RunModeSync)

	if err := newCurrentTimeHook().BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	rendered := run.RuntimeContextText()
	for _, want := range []string{"## Current Time", "Current time:", "Timezone:", "delay_seconds"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("runtime context missing %q:\n%s", want, rendered)
		}
	}
}

func TestScheduleGuidanceHookAddsReminderGuidance(t *testing.T) {
	run := ternura.NewRunContext("2分钟后提醒我喝水", ternura.RunModeSync)

	if err := newScheduleGuidanceHook().BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	rendered := run.RuntimeContextText()
	for _, want := range []string{"## Schedule Guidance", "schedule_task", "delay_seconds", "Never invent"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("schedule guidance missing %q:\n%s", want, rendered)
		}
	}
}

func TestScheduleGuidanceHookAddsCancelGuidance(t *testing.T) {
	run := ternura.NewRunContext("取消 schedule-20260520T120000 这个提醒", ternura.RunModeSync)

	if err := newScheduleGuidanceHook().BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	rendered := run.RuntimeContextText()
	for _, want := range []string{"cancel_scheduled_task", "Do not claim"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("cancel guidance missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Creating the schedule") {
		t.Fatalf("cancel request should not receive create guidance:\n%s", rendered)
	}
}

func TestScheduleGuidanceHookIgnoresOrdinaryFutureQuestion(t *testing.T) {
	run := ternura.NewRunContext("明天天气怎么样", ternura.RunModeSync)

	if err := newScheduleGuidanceHook().BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	if rendered := run.RuntimeContextText(); strings.Contains(rendered, "Schedule Guidance") {
		t.Fatalf("ordinary future question should not receive schedule guidance:\n%s", rendered)
	}
}

func TestFormatUTCOffset(t *testing.T) {
	tests := map[int]string{
		28800:  "UTC+08:00",
		-18000: "UTC-05:00",
		19800:  "UTC+05:30",
	}
	for offset, want := range tests {
		if got := formatUTCOffset(offset); got != want {
			t.Fatalf("formatUTCOffset(%d) = %q, want %q", offset, got, want)
		}
	}
}
