package tool

import (
	"context"

	"github.com/openai/openai-go/v3"
)

type AgentTool string

const (
	AgentToolRead         AgentTool = "read"
	AgentToolWrite        AgentTool = "write"
	AgentToolEdit         AgentTool = "edit"
	AgentToolBash         AgentTool = "bash"
	AgentToolUpdateTodos  AgentTool = "update_todos"
	AgentToolRemember     AgentTool = "remember"
	AgentToolForgetMemory AgentTool = "forget_memory"
	AgentToolScheduleTask AgentTool = "schedule_task"
	AgentToolCancelTask   AgentTool = "cancel_scheduled_task"
)

type Tool interface {
	ToolName() AgentTool
	Info() openai.ChatCompletionToolUnionParam
	Execute(ctx context.Context, argumentsInJSON string) (string, error)
}
