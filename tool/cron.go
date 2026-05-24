package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/cloudwego/eino/schema"
)

// CronAddParams 创建 cron 任务。
type CronAddParams struct {
	Name         string `json:"name,omitempty"`
	Message      string `json:"message"`
	EverySeconds int    `json:"every_seconds,omitempty"`
	CronExpr     string `json:"cron_expr,omitempty"`
	TZ           string `json:"tz,omitempty"`
	At           string `json:"at,omitempty"`
	DelaySeconds int    `json:"delay_seconds,omitempty"`
	Deliver      bool   `json:"deliver,omitempty"`
}

// CronAddResult 创建结果。
type CronAddResult struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Message   string `json:"message"`
	NextRunAt string `json:"next_run_at,omitempty"`
}

type CronAddFunc func(ctx context.Context, params CronAddParams) (CronAddResult, error)
type CronListFunc func(ctx context.Context) (string, error)
type CronRemoveFunc func(ctx context.Context, jobID string) (string, error)

type CronTool struct {
	add    CronAddFunc
	list   CronListFunc
	remove CronRemoveFunc
	inCron atomic.Bool
}

func NewCronTool(add CronAddFunc, list CronListFunc, remove CronRemoveFunc) *CronTool {
	return &CronTool{add: add, list: list, remove: remove}
}

func (t *CronTool) SetCronContext(active bool) {
	t.inCron.Store(active)
}

func (t *CronTool) ToolName() AgentTool {
	return AgentToolCron
}

func (t *CronTool) Info(context.Context) (*schema.ToolInfo, error) {
	desc := "Schedule reminders and recurring tasks. Actions: add, list, remove. " +
		"Use add with message plus exactly one schedule: every_seconds, cron_expr (+ optional tz), or at (ISO datetime). " +
		"For relative delays like '2 minutes', use delay_seconds on add. " +
		"Do not claim a job exists until this tool succeeds."
	return NewToolInfo(
		AgentToolCron,
		desc,
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"add", "list", "remove"},
					"description": "add: create job; list: show jobs; remove: delete job by id",
				},
				"name": map[string]any{
					"type":        "string",
					"description": "Optional short label, e.g. 'weather reminder'",
				},
				"message": map[string]any{
					"type":        "string",
					"description": "Required for add. Instruction to execute when the job fires.",
				},
				"every_seconds": map[string]any{
					"type":        "integer",
					"description": "Recurring interval in seconds",
				},
				"cron_expr": map[string]any{
					"type":        "string",
					"description": "Cron expression such as '0 9 * * *'",
				},
				"tz": map[string]any{
					"type":        "string",
					"description": "IANA timezone for cron_expr, e.g. Asia/Shanghai",
				},
				"at": map[string]any{
					"type":        "string",
					"description": "One-time ISO datetime, e.g. 2026-05-22T09:00:00+08:00",
				},
				"delay_seconds": map[string]any{
					"type":        "integer",
					"description": "Relative one-time delay in seconds (maps to at)",
				},
				"job_id": map[string]any{
					"type":        "string",
					"description": "Required for remove",
				},
				"deliver": map[string]any{
					"type":        "boolean",
					"description": "Whether to deliver the result to the user (default true)",
				},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	)
}

func (t *CronTool) Execute(ctx context.Context, argumentsInJSON string) (string, error) {
	var params struct {
		Action       string `json:"action"`
		Name         string `json:"name"`
		Message      string `json:"message"`
		EverySeconds int    `json:"every_seconds"`
		CronExpr     string `json:"cron_expr"`
		TZ           string `json:"tz"`
		At           string `json:"at"`
		DelaySeconds int    `json:"delay_seconds"`
		JobID        string `json:"job_id"`
		Deliver      *bool  `json:"deliver"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &params); err != nil {
		return "", err
	}

	switch strings.TrimSpace(params.Action) {
	case "add":
		if t.inCron.Load() {
			return "Error: cannot schedule new jobs from within a cron job execution", nil
		}
		if strings.TrimSpace(params.Message) == "" {
			return "Error: message is required for action=add", nil
		}
		deliver := true
		if params.Deliver != nil {
			deliver = *params.Deliver
		}
		result, err := t.add(ctx, CronAddParams{
			Name:         params.Name,
			Message:      params.Message,
			EverySeconds: params.EverySeconds,
			CronExpr:     params.CronExpr,
			TZ:           params.TZ,
			At:           params.At,
			DelaySeconds: params.DelaySeconds,
			Deliver:      deliver,
		})
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Created job '%s' (id: %s) next run at %s", result.Name, result.ID, result.NextRunAt), nil
	case "list":
		return t.list(ctx)
	case "remove":
		id := strings.TrimSpace(params.JobID)
		if id == "" {
			return "Error: job_id is required for remove", nil
		}
		return t.remove(ctx, id)
	default:
		return fmt.Sprintf("Unknown action: %s", params.Action), nil
	}
}
