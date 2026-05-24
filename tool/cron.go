package tool

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
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
	*agentTool
	add    CronAddFunc
	list   CronListFunc
	remove CronRemoveFunc
	inCron atomic.Bool
}

type cronToolParam struct {
	Action       string `json:"action" jsonschema:"required,enum=add,enum=list,enum=remove" jsonschema_description:"add: create job; list: show jobs; remove: delete job by id"`
	Name         string `json:"name,omitempty" jsonschema_description:"Optional short label, e.g. 'weather reminder'"`
	Message      string `json:"message,omitempty" jsonschema_description:"Required for add. Instruction to execute when the job fires."`
	EverySeconds int    `json:"every_seconds,omitempty" jsonschema_description:"Recurring interval in seconds"`
	CronExpr     string `json:"cron_expr,omitempty" jsonschema_description:"Cron expression such as '0 9 * * *'"`
	TZ           string `json:"tz,omitempty" jsonschema_description:"IANA timezone for cron_expr, e.g. Asia/Shanghai"`
	At           string `json:"at,omitempty" jsonschema_description:"One-time ISO datetime, e.g. 2026-05-22T09:00:00+08:00"`
	DelaySeconds int    `json:"delay_seconds,omitempty" jsonschema_description:"Relative one-time delay in seconds (maps to at)"`
	JobID        string `json:"job_id,omitempty" jsonschema_description:"Required for remove"`
	Deliver      *bool  `json:"deliver,omitempty" jsonschema_description:"Whether to deliver the result to the user (default true)"`
}

func NewCronTool(add CronAddFunc, list CronListFunc, remove CronRemoveFunc) *CronTool {
	t := &CronTool{add: add, list: list, remove: remove}
	t.agentTool = newAgentTool(AgentToolCron, cronToolDescription, t.run)
	return t
}

func (t *CronTool) SetCronContext(active bool) {
	t.inCron.Store(active)
}

const cronToolDescription = "Schedule reminders and recurring tasks. Actions: add, list, remove. " +
	"Use add with message plus exactly one schedule: every_seconds, cron_expr (+ optional tz), or at (ISO datetime). " +
	"For relative delays like '2 minutes', use delay_seconds on add. " +
	"Do not claim a job exists until this tool succeeds."

func (t *CronTool) run(ctx context.Context, params cronToolParam) (string, error) {
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
