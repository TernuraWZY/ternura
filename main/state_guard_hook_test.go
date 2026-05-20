package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"ternura"
	"ternura/tool"
)

func TestStateGuardBlocksUngroundedScheduleIDClaim(t *testing.T) {
	run := ternura.NewRunContext("1分钟后提醒我睡觉", ternura.RunModeSync)
	result := ternura.AgentRunResult{
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
	run := ternura.NewRunContext("设置1分钟后提醒我睡觉", ternura.RunModeSync)
	result := ternura.AgentRunResult{
		Content: "好的，1分钟后我会提醒你该睡觉了。",
	}

	err := newStateGuardHook(nil).FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if !strings.Contains(result.Content, "没有检测到真实的 `schedule_task` 工具执行结果") {
		t.Fatalf("guarded content = %q", result.Content)
	}
}

func TestStateGuardAllowsKnownScheduleID(t *testing.T) {
	store := newScheduleStore(t.TempDir())
	task, err := store.Create(context.Background(), "session-test", tool.ScheduleTaskInput{
		Title:        "睡觉提醒",
		Prompt:       "提醒用户：该睡觉了",
		DelaySeconds: 60,
	})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	run := ternura.NewRunContext("设置1分钟后提醒我睡觉", ternura.RunModeSync)
	content := fmt.Sprintf("好的，已设置1分钟后的睡觉提醒。\n\n任务 ID：`%s`", task.ID)
	result := ternura.AgentRunResult{Content: content}

	err = newStateGuardHook(store).FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if result.Content != content {
		t.Fatalf("content should be unchanged, got %q", result.Content)
	}
}

func TestStateGuardAllowsDiagnosticScheduleMention(t *testing.T) {
	run := ternura.NewRunContext("schedule-20260520T163400 这个为啥定时任务又没了", ternura.RunModeSync)
	content := "这个 `schedule-20260520T163400` 不在真实的定时任务存储里。"
	result := ternura.AgentRunResult{Content: content}

	err := newStateGuardHook(nil).FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if result.Content != content {
		t.Fatalf("content should be unchanged, got %q", result.Content)
	}
}

func TestStateGuardAllowsEnglishDiagnosticScheduleMention(t *testing.T) {
	run := ternura.NewRunContext("why did schedule-20260520T163400 disappear?", ternura.RunModeSync)
	content := "The scheduled task `schedule-20260520T163400` does not exist in the store."
	result := ternura.AgentRunResult{Content: content}

	err := newStateGuardHook(nil).FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if result.Content != content {
		t.Fatalf("content should be unchanged, got %q", result.Content)
	}
}
