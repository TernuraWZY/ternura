package tool

import (
	"context"
	"fmt"
	"strings"
)

type DelegateAgentParams struct {
	Agent string `json:"agent" jsonschema:"required" jsonschema_description:"name of the delegated agent to run"`
	Task  string `json:"task" jsonschema:"required" jsonschema_description:"self-contained task for the delegated agent"`
}

type DelegateAgentFunc func(context.Context, DelegateAgentParams) (string, error)

type DelegateAgentTool struct {
	*agentTool
}

func NewDelegateAgentTool(description string, delegate DelegateAgentFunc) *DelegateAgentTool {
	description = strings.TrimSpace(description)
	if description == "" {
		description = "delegate a focused task to a named specialist agent and return its final result"
	}
	t := &DelegateAgentTool{}
	t.agentTool = newAgentTool(AgentToolDelegateAgent, description, func(ctx context.Context, params DelegateAgentParams) (string, error) {
		params.Agent = strings.TrimSpace(params.Agent)
		params.Task = strings.TrimSpace(params.Task)
		if params.Agent == "" {
			return "", fmt.Errorf("agent is required")
		}
		if params.Task == "" {
			return "", fmt.Errorf("task is required")
		}
		if delegate == nil {
			return "", fmt.Errorf("delegated agent runtime is unavailable")
		}
		return delegate(ctx, params)
	})
	return t
}
