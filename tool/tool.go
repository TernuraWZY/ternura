package tool

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/schema"
	einojsonschema "github.com/eino-contrib/jsonschema"
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
	AgentToolCron         AgentTool = "cron"
)

type Tool interface {
	ToolName() AgentTool
	Info(ctx context.Context) (*schema.ToolInfo, error)
	Execute(ctx context.Context, argumentsInJSON string) (string, error)
}

func NewToolInfo(name AgentTool, desc string, params map[string]any) (*schema.ToolInfo, error) {
	info := &schema.ToolInfo{
		Name: string(name),
		Desc: desc,
	}
	if len(params) == 0 {
		return info, nil
	}
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	var js einojsonschema.Schema
	if err := json.Unmarshal(payload, &js); err != nil {
		return nil, err
	}
	info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(&js)
	return info, nil
}
