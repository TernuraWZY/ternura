package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"ternura"
	"ternura/tool"
)

var relativeReminderPattern = regexp.MustCompile(`(?i)(\d+)\s*(秒钟|秒|seconds?|secs?|分钟|分|min(?:ute)?s?|小时|钟头|hours?|hrs?|天|days?)\s*(后|以后|later|from\s+now)?`)

type scheduleShortcut struct {
	Title        string
	Prompt       string
	DelaySeconds int
	DelayLabel   string
}

func parseScheduleShortcut(message string) (scheduleShortcut, bool) {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" || !looksLikeReminderRequest(trimmed) {
		return scheduleShortcut{}, false
	}

	match := relativeReminderPattern.FindStringSubmatchIndex(trimmed)
	if len(match) == 0 {
		return scheduleShortcut{}, false
	}

	delayText := trimmed[match[0]:match[1]]
	amount, err := strconv.Atoi(trimmed[match[2]:match[3]])
	if err != nil || amount <= 0 {
		return scheduleShortcut{}, false
	}
	unit := strings.ToLower(trimmed[match[4]:match[5]])
	delaySeconds := amount * reminderUnitSeconds(unit)
	if delaySeconds <= 0 {
		return scheduleShortcut{}, false
	}

	reminder := reminderContent(trimmed[:match[0]] + " " + trimmed[match[1]:])
	if reminder == "" {
		return scheduleShortcut{}, false
	}

	title := reminder
	if !strings.Contains(title, "提醒") && !strings.Contains(strings.ToLower(title), "remind") {
		title += "提醒"
	}
	return scheduleShortcut{
		Title:        title,
		Prompt:       "提醒用户：" + reminder,
		DelaySeconds: delaySeconds,
		DelayLabel:   strings.TrimSpace(delayText),
	}, true
}

func looksLikeReminderRequest(message string) bool {
	lower := strings.ToLower(message)
	keywords := []string{"提醒", "叫我", "告诉我", "闹钟", "remind", "reminder", "notify", "tell me"}
	for _, keyword := range keywords {
		if strings.Contains(lower, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func reminderUnitSeconds(unit string) int {
	switch unit {
	case "秒", "秒钟", "second", "seconds", "sec", "secs":
		return 1
	case "分钟", "分", "min", "mins", "minute", "minutes":
		return 60
	case "小时", "钟头", "hour", "hours", "hr", "hrs":
		return 3600
	case "天", "day", "days":
		return 86400
	default:
		return 0
	}
}

func reminderContent(content string) string {
	replacer := strings.NewReplacer(
		"提醒我", " ",
		"提醒一下我", " ",
		"提醒一下", " ",
		"提醒", " ",
		"设置", " ",
		"设个", " ",
		"建个", " ",
		"创建", " ",
		"一个", " ",
		"一下", " ",
		"以后", " ",
		"之后", " ",
		"后", " ",
		"到时候", " ",
		"please", " ",
		"remind me to", " ",
		"remind me", " ",
		"reminder", " ",
		"notify me to", " ",
		"notify me", " ",
	)
	cleaned := strings.Join(strings.Fields(replacer.Replace(content)), " ")
	cleaned = strings.Trim(cleaned, " ，。,.;；:：!！?？")
	cleaned = strings.TrimPrefix(cleaned, "得")
	cleaned = strings.TrimPrefix(cleaned, "该")
	cleaned = strings.TrimPrefix(cleaned, "去")
	return strings.TrimSpace(cleaned)
}

func (s *agentServer) tryScheduleShortcut(ctx context.Context, message string) (ternura.AgentRunResult, bool, error) {
	shortcut, ok := parseScheduleShortcut(message)
	if !ok {
		return ternura.AgentRunResult{}, false, nil
	}

	input := tool.ScheduleTaskInput{
		Title:        shortcut.Title,
		Prompt:       shortcut.Prompt,
		DelaySeconds: shortcut.DelaySeconds,
	}
	result, err := s.scheduleTask(ctx, input)
	if err != nil {
		return ternura.AgentRunResult{}, true, err
	}

	args, _ := json.MarshalIndent(input, "", "  ")
	toolResult := fmt.Sprintf("Scheduled task created: [%s] %s at %s", result.ID, result.Title, result.RunAt)
	content := fmt.Sprintf("好的，已设置%s的%s。\n\n任务 ID：`%s`\n到时间后我会在当前会话里提醒你。", shortcut.DelayLabel, result.Title, result.ID)
	return ternura.AgentRunResult{
		Content:    content,
		RawContent: content,
		Trace: []ternura.AgentTraceItem{{
			Type:    "tool",
			Title:   "Tool use: schedule_task",
			Content: scheduleShortcutTrace(string(args), toolResult),
		}},
	}, true, nil
}

func scheduleShortcutTrace(arguments string, result string) string {
	return strings.Join([]string{
		"**Arguments**",
		"",
		"```json",
		arguments,
		"```",
		"",
		"**Result**",
		"",
		"```text",
		result,
		"```",
	}, "\n")
}

func emitScheduleShortcutResult(emit func(ternura.AgentStreamEvent) error, result ternura.AgentRunResult) error {
	for idx, item := range result.Trace {
		if err := emitShortcutTraceItem(emit, fmt.Sprintf("trace-shortcut-%d", idx+1), item); err != nil {
			return err
		}
	}
	if err := emit(ternura.AgentStreamEvent{
		Type:  eventTypeContentDelta,
		Delta: result.Content,
	}); err != nil {
		return err
	}
	return emit(ternura.AgentStreamEvent{
		Type:       "done",
		Content:    result.Content,
		Trace:      result.Trace,
		RawContent: result.RawContent,
	})
}

func emitShortcutTraceItem(emit func(ternura.AgentStreamEvent) error, id string, item ternura.AgentTraceItem) error {
	if err := emit(ternura.AgentStreamEvent{
		Type:      eventTypeTraceStart,
		ID:        id,
		TraceType: item.Type,
		Title:     item.Title,
	}); err != nil {
		return err
	}
	if item.Content != "" {
		if err := emit(ternura.AgentStreamEvent{
			Type:  eventTypeTraceDelta,
			ID:    id,
			Delta: item.Content,
		}); err != nil {
			return err
		}
	}
	return emit(ternura.AgentStreamEvent{
		Type:    eventTypeTraceDone,
		ID:      id,
		Content: item.Content,
	})
}
