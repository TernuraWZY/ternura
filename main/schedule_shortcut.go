package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"ternura"
	"ternura/main/cron"
	"ternura/tool"
)

// tryScheduleShortcut 对「N分钟后提醒我X」这类明确句式直接创建 cron，不依赖 LLM。
func (s *agentServer) tryScheduleShortcut(ctx context.Context, message string) (ternura.AgentRunResult, bool, error) {
	return s.tryScheduleShortcutForSession(ctx, message, s.store.CurrentSessionID(), nil)
}

func (s *agentServer) tryScheduleShortcutForSession(ctx context.Context, message string, sessionID string, delivery *cron.DeliveryTarget) (ternura.AgentRunResult, bool, error) {
	params, ok := parseRelativeScheduleShortcut(message)
	if !ok {
		return ternura.AgentRunResult{}, false, nil
	}
	params.SessionID = sessionID
	params.Delivery = delivery
	job, err := s.cron.Add(ctx, params)
	if err != nil {
		content := fmt.Sprintf("我识别到你想设置定时任务，但参数有问题（%s）。能再具体说一下时间吗？", err.Error())
		return ternura.AgentRunResult{Content: content, RawContent: content}, true, nil
	}
	s.wakeCronRunner()
	delayLabel := cronDelayLabel(job.State.NextRunAtMS)
	content := fmt.Sprintf("好的，已设置%s的「%s」。\n\n任务 ID：`%s`\n到时间后我会在当前会话里提醒你。", delayLabel, job.Name, job.ID)
	args, _ := json.MarshalIndent(map[string]any{
		"action":        "add",
		"message":       params.Message,
		"delay_seconds": params.DelaySeconds,
	}, "", "  ")
	traceContent := fmt.Sprintf("**Arguments**\n\n```json\n%s\n```\n\n**Result**\n\n```text\nCreated job '%s' (id: %s)\n```", args, job.Name, job.ID)
	return ternura.AgentRunResult{
		Content:    content,
		RawContent: content,
		Trace: []ternura.AgentTraceItem{{
			Type:    "tool",
			Title:   "Tool use: " + string(tool.AgentToolCron),
			Content: traceContent,
		}},
	}, true, nil
}

func cronDelayLabel(nextRunMS int64) string {
	if nextRunMS <= 0 {
		return ""
	}
	delta := time.Until(time.UnixMilli(nextRunMS))
	if delta <= 0 {
		return ""
	}
	switch {
	case delta < 90*time.Second:
		return fmt.Sprintf("%d 秒后", int(delta.Seconds()+0.5))
	case delta < 90*time.Minute:
		return fmt.Sprintf("%d 分钟后", int(delta.Minutes()+0.5))
	case delta < 36*time.Hour:
		return fmt.Sprintf("%d 小时后", int(delta.Hours()+0.5))
	default:
		return "在 " + time.UnixMilli(nextRunMS).Local().Format("2006-01-02 15:04")
	}
}

func emitScheduleShortcutResult(emit func(ternura.AgentStreamEvent) error, result ternura.AgentRunResult) error {
	for idx, item := range result.Trace {
		id := fmt.Sprintf("trace-shortcut-%d", idx+1)
		if err := emit(ternura.AgentStreamEvent{Type: "trace_start", ID: id, TraceType: item.Type, Title: item.Title}); err != nil {
			return err
		}
		if item.Content != "" {
			if err := emit(ternura.AgentStreamEvent{Type: "trace_delta", ID: id, Delta: item.Content}); err != nil {
				return err
			}
		}
		if err := emit(ternura.AgentStreamEvent{Type: "trace_done", ID: id, Content: item.Content}); err != nil {
			return err
		}
	}
	if result.Content != "" {
		return emit(ternura.AgentStreamEvent{Type: "content", Content: result.Content})
	}
	return nil
}
