package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"ternura/agent"
	"ternura/internal/cron"
)

func TestStateGuardBlocksUngroundedScheduleIDClaim(t *testing.T) {
	run := agent.NewRunContext("1分钟后提醒我睡觉", agent.RunModeSync)
	result := agent.AgentRunResult{
		Content: "好的，1分钟后我会提醒你该睡觉了。\n\n（任务 ID：`schedule-20260520T163400`）",
	}

	err := newStateGuardHook(nil).FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if !strings.Contains(result.Content, "不会把 `schedule-20260520T163400` 当成有效任务 ID") {
		t.Fatalf("guarded content = %q", result.Content)
	}
	if len(result.Trace) != 1 || result.Trace[0].Type != "guard" {
		t.Fatalf("guard trace not appended: %+v", result.Trace)
	}
}

func TestStateGuardBlocksUngroundedScheduleSuccessWithoutID(t *testing.T) {
	run := agent.NewRunContext("设置1分钟后提醒我睡觉", agent.RunModeSync)
	result := agent.AgentRunResult{
		Content: "好的，已设置1分钟后的睡觉提醒。",
	}

	err := newStateGuardHook(nil).FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if !strings.Contains(result.Content, "没有检测到真实的 `cron` 工具执行结果") {
		t.Fatalf("guarded content = %q", result.Content)
	}
}

func TestStateGuardAllowsVagueScheduleClarification(t *testing.T) {
	run := agent.NewRunContext("等一会告诉我天气", agent.RunModeSync)
	result := agent.AgentRunResult{
		Content: "好的！你想让我多久后告诉你天气？比如 2 分钟后或 5 分钟后？",
	}

	err := newStateGuardHook(nil).FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if result.Content != "好的！你想让我多久后告诉你天气？比如 2 分钟后或 5 分钟后？" {
		t.Fatalf("clarification should not be blocked, got %q", result.Content)
	}
}

func TestStateGuardAllowsKnownScheduleID(t *testing.T) {
	svc := cron.NewService(t.TempDir())
	job, err := svc.Add(context.Background(), cron.AddParams{
		Name:           "睡觉提醒",
		Message:        "提醒用户：该睡觉了",
		SessionID:      "session-test",
		DelaySeconds:   60,
		DeleteAfterRun: true,
	})
	if err != nil {
		t.Fatalf("create cron job: %v", err)
	}
	run := agent.NewRunContext("设置1分钟后提醒我睡觉", agent.RunModeSync)
	content := fmt.Sprintf("好的，已设置1分钟后的睡觉提醒。\n\n任务 ID：`%s`", job.ID)
	result := agent.AgentRunResult{Content: content}

	err = newStateGuardHook(svc).FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if result.Content != content {
		t.Fatalf("content should be unchanged, got %q", result.Content)
	}
}

func TestStateGuardAllowsDiagnosticScheduleMention(t *testing.T) {
	run := agent.NewRunContext("schedule-20260520T163400 这个为啥定时任务又没了", agent.RunModeSync)
	content := "这个 `schedule-20260520T163400` 不在真实的定时任务存储里。"
	result := agent.AgentRunResult{Content: content}

	err := newStateGuardHook(nil).FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if result.Content != content {
		t.Fatalf("content should be unchanged, got %q", result.Content)
	}
}

func TestStateGuardAllowsEnglishDiagnosticScheduleMention(t *testing.T) {
	run := agent.NewRunContext("why did schedule-20260520T163400 disappear?", agent.RunModeSync)
	content := "The scheduled task `schedule-20260520T163400` does not exist in the store."
	result := agent.AgentRunResult{Content: content}

	err := newStateGuardHook(nil).FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if result.Content != content {
		t.Fatalf("content should be unchanged, got %q", result.Content)
	}
}
