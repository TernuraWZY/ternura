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
	if policy := run.RequestedToolPolicy(); !policy.Empty() {
		t.Fatalf("cron runtime should not require schedule tool, got %+v", policy)
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

func TestScheduleGuidanceHookRequiresScheduleToolOnStrongIntent(t *testing.T) {
	run := agent.NewRunContext("2分钟后提醒我喝水", agent.RunModeSync)
	run.ModelCallCount = 1

	if err := newScheduleGuidanceHook().BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	policy := run.RequestedToolPolicy()
	if !policy.Required || len(policy.AllowedTools) != 1 || policy.AllowedTools[0] != tool.AgentToolCron {
		t.Fatalf("expected cron required, got %+v", policy)
	}
}

func TestScheduleGuidanceHookRequiresCancelToolWhenIDPresent(t *testing.T) {
	run := agent.NewRunContext("帮我取消 schedule-20260520T120000 这个提醒", agent.RunModeSync)
	run.ModelCallCount = 1

	if err := newScheduleGuidanceHook().BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	policy := run.RequestedToolPolicy()
	if !policy.Required || len(policy.AllowedTools) != 1 || policy.AllowedTools[0] != tool.AgentToolCron {
		t.Fatalf("expected cron required for cancel with id, got %+v", policy)
	}
}

func TestScheduleGuidanceHookDoesNotForceCancelWithoutID(t *testing.T) {
	run := agent.NewRunContext("帮我取消那个提醒", agent.RunModeSync)
	run.ModelCallCount = 1

	if err := newScheduleGuidanceHook().BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	if policy := run.RequestedToolPolicy(); !policy.Empty() {
		t.Fatalf("cancel without id should not require tool, got %+v", policy)
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

			if policy := run.RequestedToolPolicy(); !policy.Empty() {
				t.Fatalf("query %q should not require tool, got %+v", query, policy)
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

	if policy := run.RequestedToolPolicy(); !policy.Empty() {
		t.Fatalf("subsequent model calls should not require tool, got %+v", policy)
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
	if policy := run.RequestedToolPolicy(); !policy.Empty() {
		t.Fatalf("vague timing should not require tool, got %+v", policy)
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
