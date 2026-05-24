package app

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"ternura/agent"
	"ternura/tool"
)

type currentTimeHook struct{}

func newCurrentTimeHook() *currentTimeHook {
	return &currentTimeHook{}
}

func (h *currentTimeHook) HookName() string {
	return "current_time"
}

func (h *currentTimeHook) BeforeModelCall(_ context.Context, run *agent.RunContext) error {
	if run == nil {
		return nil
	}
	now := time.Now()
	zoneName, offsetSeconds := now.Zone()
	offset := formatUTCOffset(offsetSeconds)
	usage := "Use this time only as runtime metadata."
	if isCronRuntimePrompt(run.Query) {
		usage += " This cron-triggered task is already due; execute its payload now."
	} else {
		usage += " For relative reminders, prefer cron action=add with delay_seconds."
	}
	content := strings.Join([]string{
		fmt.Sprintf("Current time: %s", now.Format(time.RFC3339Nano)),
		fmt.Sprintf("Timezone: %s (%s)", zoneName, offset),
		usage,
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

func (h *scheduleGuidanceHook) BeforeModelCall(_ context.Context, run *agent.RunContext) error {
	if run == nil {
		return nil
	}
	query := strings.TrimSpace(run.Query)
	if isCronRuntimePrompt(query) {
		run.SetContextBlock("schedule-guidance", "Schedule Guidance", "")
		run.SetContextBlock("cron-execution-guidance", "Cron Execution", cronExecutionGuidanceText(unwrapCronRuntimePrompt(query)))
		run.ClearToolChoice()
		return nil
	}

	isCancel := looksLikeScheduleCancelIntent(query)
	isStrong := looksLikeStrongScheduleIntent(query)
	isVague := looksLikeVagueScheduleIntent(query)
	isWeak := !isCancel && !isStrong && !isVague && looksLikeWeakScheduleIntent(query)

	if !isCancel && !isStrong && !isVague && !isWeak {
		run.SetContextBlock("schedule-guidance", "Schedule Guidance", "")
		return nil
	}
	run.SetContextBlock("schedule-guidance", "Schedule Guidance", scheduleGuidanceText(query, isVague))

	// 工具已执行后不再强制 tool_choice，让模型用自然语言收尾。
	if run.ToolCallCount > 0 {
		run.ClearToolChoice()
	}

	// 仅在首轮（工具结果回来之前）强制 tool_choice，避免后续模型被迫继续调工具而无法收尾。
	// 仅在意图非常明确（双门槛或携带 schedule id 的取消请求）时才强制，避免误命中聊天请求。
	if run.ModelCallCount == 1 {
		switch {
		case isCancel && cronJobIDPattern.MatchString(query):
			run.SetToolChoice(agent.ToolChoice{
				Mode: agent.ToolChoiceSpecific,
				Name: tool.AgentToolCron,
			})
		case isStrong:
			run.SetToolChoice(agent.ToolChoice{
				Mode: agent.ToolChoiceSpecific,
				Name: tool.AgentToolCron,
			})
		}
	}
	return nil
}

// looksLikeScheduleGuidanceIntent 保留旧名作为聚合判定，用于外部仅关心"是否调度相关"的场景。
func looksLikeScheduleGuidanceIntent(query string) bool {
	return looksLikeScheduleCancelIntent(query) || looksLikeStrongScheduleIntent(query) || looksLikeWeakScheduleIntent(query)
}

// looksLikeStrongScheduleIntent 严格判定：提醒动词 + 可解析的具体时间（不含纯模糊词如「等一会」）。
func looksLikeStrongScheduleIntent(query string) bool {
	return looksLikeConcreteScheduleIntent(query)
}

// looksLikeWeakScheduleIntent 弱判定：包含明确的调度术语（即便没有时间或动词）。
// 用作 guidance 文本注入的触发，不用于 tool_choice 强制，因此误判代价仅是多一段无关提示。
func looksLikeWeakScheduleIntent(query string) bool {
	lower := strings.ToLower(query)
	keywords := []string{
		"定时", "闹钟", "倒计时", "计时器", "到点", "稍后提醒", "之后提醒", "以后提醒",
		"schedule", "scheduled", "remind", "reminder", "notify", "timer", "alarm", "check back", "follow up",
	}
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

// hasReminderVerb 复用 looksLikeReminderRequest 的动词关键词集。
func hasReminderVerb(query string) bool {
	return looksLikeReminderRequest(query)
}

// timeSignalPattern 同时识别相对时间、绝对时间和模糊未来三类时间信号。
// 这些模式只是 "是否含时间锚点" 的判定，不负责精确解析，精确解析由 LLM 调 cron 工具完成。
var timeSignalPattern = regexp.MustCompile(
	`(?i)(\d+\s*(秒钟|秒|seconds?|secs?|分钟|分|min(?:ute)?s?|小时|钟头|hours?|hrs?|天|days?))` +
		`|(\d{1,2}\s*[:点时]\s*\d{0,2})` +
		`|(今晚|今天晚上|明早|明天早上|明晚|明天晚上|明天|后天|大后天|今天下午|今天上午|下周|下个?月|周[一二三四五六日天])` +
		`|(tomorrow|tonight|next\s+(week|month|monday|tuesday|wednesday|thursday|friday|saturday|sunday))` +
		`|(等下|等一会|等会儿?|稍后|一会儿?|待会儿?|回头|到时候|later|soon)`,
)

// hasTimeSignal 判断 query 中是否含可能的时间锚点（相对/绝对/模糊未来）。
func hasTimeSignal(query string) bool {
	return timeSignalPattern.MatchString(query)
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
	if cronJobIDPattern.MatchString(query) {
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

func scheduleGuidanceText(query string, vague bool) string {
	if looksLikeScheduleCancelIntent(query) {
		return strings.Join([]string{
			"The user appears to be asking to cancel a scheduled job.",
			"- If they gave a job id, call cron with action=remove and that exact job_id.",
			"- If they did not give a clear id, ask for the job id or call cron action=list first.",
			"- Do not claim a job was removed until cron remove succeeds.",
		}, "\n")
	}
	if vague {
		return strings.Join([]string{
			"The user wants a future reminder but the timing is vague (e.g. 等一会 / later / soon).",
			"- Do NOT invent a delay or call cron add yet.",
			"- Ask a brief clarification: how many minutes, or what exact time.",
			"- Do not claim a job is already set.",
		}, "\n")
	}
	return strings.Join([]string{
		"The user appears to be asking for a future reminder, later check, delayed continuation, timer, or scheduled task.",
		"- Creating the schedule is a real side effect: call cron with action=add before saying it is set.",
		"- For relative timing such as '2分钟后', use delay_seconds with action=add.",
		"- For exact wall-clock timing such as 'tomorrow at 9am', use at as ISO datetime with timezone.",
		"- For recurring tasks, use every_seconds or cron_expr (+ optional tz).",
		"- The message field should describe what to do when the job fires.",
		"- After cron add succeeds, report only the id returned by the tool. Never invent a job id.",
		"- If the time is ambiguous, ask a brief clarification instead of guessing.",
		"",
		"Examples:",
		"- '2分钟后提醒我喝水' -> cron({\"action\":\"add\",\"message\":\"提醒用户：喝水\",\"delay_seconds\":120})",
		"- '明天早上9点提醒我开会' -> cron({\"action\":\"add\",\"message\":\"提醒用户：开会\",\"at\":\"<ISO datetime>\"})",
		"- '每天9点检查邮件' -> cron({\"action\":\"add\",\"message\":\"检查邮件并汇报\",\"cron_expr\":\"0 9 * * *\"})",
	}, "\n")
}

func cronExecutionGuidanceText(payload string) string {
	lines := []string{
		"A scheduled cron job has fired. The scheduled time is now.",
		"- Execute the payload now instead of creating another cron or schedule job.",
		"- Treat the payload as the user's saved instruction for this run.",
		"- If the payload is a reminder, deliver the reminder directly.",
		"- Only use tools that are needed to complete the payload.",
	}
	payload = strings.TrimSpace(payload)
	if payload != "" {
		lines = append(lines, "", "Payload:", payload)
	}
	return strings.Join(lines, "\n")
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
