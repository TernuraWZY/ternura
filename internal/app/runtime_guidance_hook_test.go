package app

import (
	"context"
	"strings"
	"testing"

	"ternura/agent"
	"ternura/tool"
)

func TestCurrentTimeHookAddsRuntimeContext(t *testing.T) {
	run := agent.NewRunContext("hello", agent.RunModeSync)

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

func TestCurrentTimeHookMarksCronRuntimeAsDueNow(t *testing.T) {
	run := agent.NewRunContext(wrapCronRuntimePrompt("提醒用户：喝水"), agent.RunModeSync)

	if err := newCurrentTimeHook().BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	rendered := run.RuntimeContextText()
	for _, want := range []string{"## Current Time", "cron-triggered task is already due"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("cron runtime context missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "delay_seconds") {
		t.Fatalf("cron runtime should not receive create-schedule timing guidance:\n%s", rendered)
	}
}

func TestScheduleGuidanceHookAddsReminderGuidance(t *testing.T) {
	run := agent.NewRunContext("2分钟后提醒我喝水", agent.RunModeSync)

	if err := newScheduleGuidanceHook().BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	rendered := run.RuntimeContextText()
	for _, want := range []string{"## Schedule Guidance", "cron", "delay_seconds", "Never invent"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("schedule guidance missing %q:\n%s", want, rendered)
		}
	}
}

func TestScheduleGuidanceHookTreatsCronRuntimeAsExecution(t *testing.T) {
	run := agent.NewRunContext(wrapCronRuntimePrompt("提醒用户：喝水"), agent.RunModeSync)
	run.ModelCallCount = 1

	if err := newScheduleGuidanceHook().BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	rendered := run.RuntimeContextText()
	for _, want := range []string{"## Cron Execution", "scheduled time is now", "提醒用户：喝水"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("cron execution guidance missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Creating the schedule") {
		t.Fatalf("cron runtime should not receive create-schedule guidance:\n%s", rendered)
	}
	if choice := run.RequestedToolChoice(); choice.Mode != "" {
		t.Fatalf("cron runtime should not force schedule tool choice, got %+v", choice)
	}
}

func TestScheduleGuidanceHookAddsCancelGuidance(t *testing.T) {
	run := agent.NewRunContext("取消 schedule-20260520T120000 这个提醒", agent.RunModeSync)

	if err := newScheduleGuidanceHook().BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	rendered := run.RuntimeContextText()
	for _, want := range []string{"action=remove", "Do not claim"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("cancel guidance missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Creating the schedule") {
		t.Fatalf("cancel request should not receive create guidance:\n%s", rendered)
	}
}

func TestScheduleGuidanceHookIgnoresOrdinaryFutureQuestion(t *testing.T) {
	run := agent.NewRunContext("明天天气怎么样", agent.RunModeSync)

	if err := newScheduleGuidanceHook().BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	if rendered := run.RuntimeContextText(); strings.Contains(rendered, "Schedule Guidance") {
		t.Fatalf("ordinary future question should not receive schedule guidance:\n%s", rendered)
	}
}

func TestScheduleGuidanceHookForcesScheduleToolOnStrongIntent(t *testing.T) {
	run := agent.NewRunContext("2分钟后提醒我喝水", agent.RunModeSync)
	run.ModelCallCount = 1

	if err := newScheduleGuidanceHook().BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	choice := run.RequestedToolChoice()
	if choice.Mode != agent.ToolChoiceSpecific || choice.Name != tool.AgentToolCron {
		t.Fatalf("expected cron forced, got %+v", choice)
	}
}

func TestScheduleGuidanceHookForcesCancelToolWhenIDPresent(t *testing.T) {
	run := agent.NewRunContext("帮我取消 schedule-20260520T120000 这个提醒", agent.RunModeSync)
	run.ModelCallCount = 1

	if err := newScheduleGuidanceHook().BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	choice := run.RequestedToolChoice()
	if choice.Mode != agent.ToolChoiceSpecific || choice.Name != tool.AgentToolCron {
		t.Fatalf("expected cron forced for cancel with id, got %+v", choice)
	}
}

func TestScheduleGuidanceHookDoesNotForceCancelWithoutID(t *testing.T) {
	run := agent.NewRunContext("帮我取消那个提醒", agent.RunModeSync)
	run.ModelCallCount = 1

	if err := newScheduleGuidanceHook().BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	if choice := run.RequestedToolChoice(); choice.Mode != "" {
		t.Fatalf("cancel without id should not force tool choice, got %+v", choice)
	}
	if rendered := run.RuntimeContextText(); !strings.Contains(rendered, "action=remove") {
		t.Fatalf("cancel guidance text should still be injected:\n%s", rendered)
	}
}

func TestScheduleGuidanceHookDoesNotForceToolOnPureChatRequest(t *testing.T) {
	cases := []string{
		"告诉我 1+1等于几",
		"叫我大哥",
		"提醒一下，我有个想法",
		"明天天气怎么样",
		"remind me about React hooks",
	}
	for _, query := range cases {
		t.Run(query, func(t *testing.T) {
			run := agent.NewRunContext(query, agent.RunModeSync)
			run.ModelCallCount = 1

			if err := newScheduleGuidanceHook().BeforeModelCall(context.Background(), run); err != nil {
				t.Fatalf("before model call: %v", err)
			}

			if choice := run.RequestedToolChoice(); choice.Mode != "" {
				t.Fatalf("query %q should not force tool choice, got %+v", query, choice)
			}
		})
	}
}

func TestScheduleGuidanceHookDoesNotForceToolAfterFirstModelCall(t *testing.T) {
	run := agent.NewRunContext("2分钟后提醒我喝水", agent.RunModeSync)
	run.ModelCallCount = 2

	if err := newScheduleGuidanceHook().BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	if choice := run.RequestedToolChoice(); choice.Mode != "" {
		t.Fatalf("subsequent model calls should not be forced, got %+v", choice)
	}
}

func TestHasTimeSignal(t *testing.T) {
	positives := []string{
		"2分钟后",
		"in 30 seconds",
		"明天早上9点",
		"今晚",
		"after 1 hour",
		"等一会",
		"稍后",
		"回头",
		"周三",
		"tomorrow",
		"9:30",
	}
	for _, q := range positives {
		if !hasTimeSignal(q) {
			t.Errorf("expected time signal for %q", q)
		}
	}

	negatives := []string{
		"1+1等于几",
		"帮我写个 Go 程序",
		"今天天气",
	}
	for _, q := range negatives {
		if hasTimeSignal(q) {
			t.Errorf("did not expect time signal for %q", q)
		}
	}
}

func TestLooksLikeStrongScheduleIntent(t *testing.T) {
	positives := []string{
		"2分钟后提醒我喝水",
		"明天9点叫我开会",
		"remind me in 30 minutes to stretch",
	}
	for _, q := range positives {
		if !looksLikeStrongScheduleIntent(q) {
			t.Errorf("expected strong schedule intent for %q", q)
		}
	}

	negatives := []string{
		"告诉我 1+1等于几",
		"叫我大哥",
		"15分钟后开会",
		"今天天气怎么样",
		"等一会告诉我天气",
		"稍后告诉我结果",
	}
	for _, q := range negatives {
		if looksLikeStrongScheduleIntent(q) {
			t.Errorf("did not expect strong schedule intent for %q", q)
		}
	}
}

func TestLooksLikeVagueScheduleIntent(t *testing.T) {
	if !looksLikeVagueScheduleIntent("等一会告诉我天气") {
		t.Fatal("expected vague schedule intent")
	}
	if looksLikeVagueScheduleIntent("2分钟后提醒我喝水") {
		t.Fatal("concrete schedule must not be vague")
	}
	if looksLikeVagueScheduleIntent("告诉我1+1等于几") {
		t.Fatal("plain question must not be vague schedule")
	}
}

func TestLooksLikeConcreteScheduleIntent(t *testing.T) {
	if !looksLikeConcreteScheduleIntent("2分钟后提醒我喝水") {
		t.Fatal("expected concrete schedule intent")
	}
	if looksLikeConcreteScheduleIntent("等一会告诉我天气") {
		t.Fatal("vague schedule must not be concrete")
	}
}

func TestScheduleGuidanceHookAddsVagueTimingGuidance(t *testing.T) {
	run := agent.NewRunContext("等一会告诉我天气", agent.RunModeSync)
	run.ModelCallCount = 1

	if err := newScheduleGuidanceHook().BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	rendered := run.RuntimeContextText()
	for _, want := range []string{"timing is vague", "Do NOT invent a delay", "Do not claim a job is already set"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("vague guidance missing %q:\n%s", want, rendered)
		}
	}
	if choice := run.RequestedToolChoice(); choice.Mode != "" {
		t.Fatalf("vague timing should not force tool choice, got %+v", choice)
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
