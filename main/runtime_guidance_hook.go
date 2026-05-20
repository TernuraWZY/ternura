package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ternura"
)

type currentTimeHook struct{}

func newCurrentTimeHook() *currentTimeHook {
	return &currentTimeHook{}
}

func (h *currentTimeHook) HookName() string {
	return "current_time"
}

func (h *currentTimeHook) BeforeModelCall(_ context.Context, run *ternura.RunContext) error {
	if run == nil {
		return nil
	}
	now := time.Now()
	zoneName, offsetSeconds := now.Zone()
	offset := formatUTCOffset(offsetSeconds)
	content := strings.Join([]string{
		fmt.Sprintf("Current time: %s", now.Format(time.RFC3339Nano)),
		fmt.Sprintf("Timezone: %s (%s)", zoneName, offset),
		"Use this time only as runtime metadata. For relative reminders, prefer delay_seconds instead of computing run_at.",
	}, "\n")
	run.SetContextBlock("current-time", "Current Time", content)
	return nil
}

type scheduleGuidanceHook struct{}

func newScheduleGuidanceHook() *scheduleGuidanceHook {
	return &scheduleGuidanceHook{}
}

func (h *scheduleGuidanceHook) HookName() string {
	return "schedule_guidance"
}

func (h *scheduleGuidanceHook) BeforeModelCall(_ context.Context, run *ternura.RunContext) error {
	if run == nil {
		return nil
	}
	query := strings.TrimSpace(run.Query)
	if !looksLikeScheduleGuidanceIntent(query) {
		run.SetContextBlock("schedule-guidance", "Schedule Guidance", "")
		return nil
	}
	run.SetContextBlock("schedule-guidance", "Schedule Guidance", scheduleGuidanceText(query))
	return nil
}

func looksLikeScheduleGuidanceIntent(query string) bool {
	lower := strings.ToLower(query)
	if looksLikeScheduleCancelIntent(query) || looksLikeReminderRequest(lower) {
		return true
	}
	keywords := []string{
		"定时", "闹钟", "倒计时", "计时器", "到点", "稍后提醒", "之后提醒", "以后提醒",
		"schedule", "scheduled", "remind", "reminder", "notify", "timer", "alarm", "check back", "follow up",
	}
	for _, keyword := range keywords {
		if strings.Contains(lower, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func looksLikeScheduleCancelIntent(query string) bool {
	lower := strings.ToLower(query)
	cancelWords := []string{"取消", "删除", "移除", "撤销", "停止", "cancel", "remove", "delete", "stop"}
	hasCancelWord := false
	for _, word := range cancelWords {
		if strings.Contains(lower, strings.ToLower(word)) {
			hasCancelWord = true
			break
		}
	}
	if !hasCancelWord {
		return false
	}
	if scheduleIDPattern.MatchString(query) {
		return true
	}
	scheduleWords := []string{"提醒", "定时", "任务", "闹钟", "schedule", "reminder", "timer", "alarm"}
	for _, word := range scheduleWords {
		if strings.Contains(lower, strings.ToLower(word)) {
			return true
		}
	}
	return false
}

func scheduleGuidanceText(query string) string {
	if looksLikeScheduleCancelIntent(query) {
		return strings.Join([]string{
			"The user appears to be asking to cancel a scheduled task.",
			"- If they gave a schedule id, call cancel_scheduled_task with that exact id.",
			"- If they did not give a clear id, ask for the schedule id or list the visible scheduled tasks if available.",
			"- Do not claim a task was cancelled until cancel_scheduled_task succeeds.",
		}, "\n")
	}
	return strings.Join([]string{
		"The user appears to be asking for a future reminder, later check, delayed continuation, timer, or scheduled task.",
		"- Creating the schedule is a real side effect: call schedule_task before saying it is set.",
		"- For relative timing such as '2分钟后', 'in 2 minutes', or 'after 30 seconds', use delay_seconds.",
		"- For exact wall-clock timing such as 'tomorrow at 9am', compute run_at from Current Time and include the timezone offset in RFC3339.",
		"- The prompt field should describe what the agent should do when the task fires, not the scheduling metadata.",
		"- After schedule_task succeeds, report only the id returned by the tool. Never invent a schedule id.",
		"- If the time is ambiguous enough that no safe future instant can be derived, ask a brief clarification instead of guessing.",
		"",
		"Examples:",
		"- '2分钟后提醒我喝水' -> schedule_task({\"title\":\"喝水提醒\",\"prompt\":\"提醒用户：喝水\",\"delay_seconds\":120})",
		"- '明天早上9点提醒我开会' -> schedule_task({\"title\":\"开会提醒\",\"prompt\":\"提醒用户：开会\",\"run_at\":\"<RFC3339 with timezone>\"})",
	}, "\n")
}

func formatUTCOffset(offsetSeconds int) string {
	sign := "+"
	if offsetSeconds < 0 {
		sign = "-"
		offsetSeconds = -offsetSeconds
	}
	hours := offsetSeconds / 3600
	minutes := (offsetSeconds % 3600) / 60
	return fmt.Sprintf("UTC%s%02d:%02d", sign, hours, minutes)
}
