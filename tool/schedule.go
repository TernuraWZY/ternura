package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
)

type ScheduleTaskInput struct {
	Title        string `json:"title,omitempty"`
	Prompt       string `json:"prompt"`
	RunAt        string `json:"run_at,omitempty"`
	DelaySeconds int    `json:"delay_seconds,omitempty"`
}

type ScheduleTaskResult struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Prompt string `json:"prompt"`
	RunAt  string `json:"run_at"`
}

type ScheduleTaskFunc func(ctx context.Context, input ScheduleTaskInput) (ScheduleTaskResult, error)

type CancelScheduledTaskFunc func(ctx context.Context, id string) error

type ScheduleTaskTool struct {
	schedule ScheduleTaskFunc
}

type CancelScheduledTaskTool struct {
	cancel CancelScheduledTaskFunc
}

func NewScheduleTaskTool(schedule ScheduleTaskFunc) *ScheduleTaskTool {
	if schedule == nil {
		schedule = func(context.Context, ScheduleTaskInput) (ScheduleTaskResult, error) {
			return ScheduleTaskResult{}, nil
		}
	}
	return &ScheduleTaskTool{schedule: schedule}
}

func NewCancelScheduledTaskTool(cancel CancelScheduledTaskFunc) *CancelScheduledTaskTool {
	if cancel == nil {
		cancel = func(context.Context, string) error { return nil }
	}
	return &CancelScheduledTaskTool{cancel: cancel}
}

func (t *ScheduleTaskTool) ToolName() AgentTool {
	return AgentToolScheduleTask
}

func (t *ScheduleTaskTool) Info() openai.ChatCompletionToolUnionParam {
	return openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
		Name: string(AgentToolScheduleTask),
		Description: openai.String(
			"Create a real one-time scheduled background agent run. REQUIRED when the user asks for a reminder, timer, later check, delayed continuation, or future task. Do not claim the task is set until this tool succeeds.",
		),
		Parameters: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{
					"type":        "string",
					"description": "Optional short human-readable title, for example '喝水提醒' or 'standup reminder'.",
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "REQUIRED. The exact instruction the agent should execute when the schedule fires. Describe the reminder/check/task itself; do not include scheduling metadata or a fake schedule id.",
				},
				"run_at": map[string]any{
					"type":        "string",
					"description": "Absolute future time to run, formatted as RFC3339 with timezone, for example 2026-05-20T23:30:00+08:00. Use this only for exact wall-clock times; compute it from Current Time and never use placeholder dates.",
				},
				"delay_seconds": map[string]any{
					"type":        "integer",
					"description": "Relative delay in seconds from now. Prefer this for requests like '2分钟后', 'in 2 minutes', or 'after 30 seconds'. Examples: 2 minutes = 120, 1 hour = 3600.",
				},
			},
			"required":             []string{"prompt"},
			"additionalProperties": false,
		},
	})
}

func (t *ScheduleTaskTool) Execute(ctx context.Context, argumentsInJSON string) (string, error) {
	var input ScheduleTaskInput
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return "", err
	}

	normalized, err := NormalizeScheduleTaskInput(input)
	if err != nil {
		return "", err
	}
	result, err := t.schedule(ctx, normalized)
	if err != nil {
		return "", err
	}
	if result.ID == "" {
		result.ID = "schedule"
	}
	if result.Title == "" {
		result.Title = normalized.Title
	}
	if result.Prompt == "" {
		result.Prompt = normalized.Prompt
	}
	if result.RunAt == "" {
		result.RunAt = normalized.RunAt
	}
	return fmt.Sprintf("Scheduled task created: [%s] %s at %s", result.ID, result.Title, result.RunAt), nil
}

func (t *CancelScheduledTaskTool) ToolName() AgentTool {
	return AgentToolCancelTask
}

func (t *CancelScheduledTaskTool) Info() openai.ChatCompletionToolUnionParam {
	return openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
		Name: string(AgentToolCancelTask),
		Description: openai.String(
			"Cancel a scheduled task by id before it starts.",
		),
		Parameters: openai.FunctionParameters{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "Scheduled task id, such as schedule-20260520T120000000000000.",
				},
			},
			"required":             []string{"id"},
			"additionalProperties": false,
		},
	})
}

func (t *CancelScheduledTaskTool) Execute(ctx context.Context, argumentsInJSON string) (string, error) {
	p := struct {
		ID string `json:"id"`
	}{}
	if err := json.Unmarshal([]byte(argumentsInJSON), &p); err != nil {
		return "", err
	}
	id := strings.TrimSpace(p.ID)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := t.cancel(ctx, id); err != nil {
		return "", err
	}
	return fmt.Sprintf("Scheduled task cancelled: %s", id), nil
}

func NormalizeScheduleTaskInput(input ScheduleTaskInput) (ScheduleTaskInput, error) {
	return NormalizeScheduleTaskInputAt(input, time.Now())
}

func NormalizeScheduleTaskInputAt(input ScheduleTaskInput, now time.Time) (ScheduleTaskInput, error) {
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		return ScheduleTaskInput{}, fmt.Errorf("prompt is required")
	}

	runAtRaw := strings.TrimSpace(input.RunAt)
	if input.DelaySeconds < 0 {
		return ScheduleTaskInput{}, fmt.Errorf("delay_seconds must be positive")
	}

	var runAt time.Time
	switch {
	case runAtRaw != "":
		parsed, err := time.Parse(time.RFC3339Nano, runAtRaw)
		if err != nil {
			return ScheduleTaskInput{}, fmt.Errorf("run_at must be RFC3339 with timezone: %w", err)
		}
		runAt = parsed
	case input.DelaySeconds > 0:
		runAt = now.Add(time.Duration(input.DelaySeconds) * time.Second)
	default:
		return ScheduleTaskInput{}, fmt.Errorf("run_at or positive delay_seconds is required")
	}

	if runAt.Before(now.Add(-5 * time.Second)) {
		return ScheduleTaskInput{}, fmt.Errorf("run_at must be in the future")
	}

	title := strings.Join(strings.Fields(input.Title), " ")
	if title == "" {
		title = defaultScheduleTitle(prompt)
	}
	return ScheduleTaskInput{
		Title:        title,
		Prompt:       prompt,
		RunAt:        runAt.Format(time.RFC3339Nano),
		DelaySeconds: input.DelaySeconds,
	}, nil
}

func defaultScheduleTitle(prompt string) string {
	runes := []rune(strings.Join(strings.Fields(prompt), " "))
	if len(runes) <= 48 {
		return string(runes)
	}
	return string(runes[:48]) + "..."
}
