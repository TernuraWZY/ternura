package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	einotool "github.com/cloudwego/eino/components/tool"
	einotoolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
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
	einotool.InvokableTool
	ToolName() AgentTool
}

type Option = einotool.Option

type agentTool struct {
	name      AgentTool
	invokable einotool.InvokableTool
}

func newAgentTool[T any](name AgentTool, desc string, run func(context.Context, T) (string, error)) *agentTool {
	invokable, err := einotoolutils.InferTool[T, string](
		string(name),
		desc,
		run,
		einotoolutils.WithUnmarshalArguments(unmarshalToolArguments[T]),
	)
	if err != nil {
		panic(fmt.Sprintf("infer tool %s: %v", name, err))
	}
	return &agentTool{name: name, invokable: invokable}
}

func (t *agentTool) ToolName() AgentTool {
	return t.name
}

func (t *agentTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.invokable.Info(ctx)
}

func (t *agentTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...Option) (string, error) {
	output, err := t.invokable.InvokableRun(ctx, argumentsInJSON, opts...)
	if err != nil {
		return output, unwrapEinoLocalFuncError(err)
	}
	return output, nil
}

func unmarshalToolArguments[T any](_ context.Context, arguments string) (any, error) {
	var input T
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return nil, err
	}
	return input, nil
}

func unwrapEinoLocalFuncError(err error) error {
	if err == nil || !strings.HasPrefix(err.Error(), "[LocalFunc] ") {
		return err
	}
	if unwrapped := errors.Unwrap(err); unwrapped != nil {
		return unwrapped
	}
	return err
}
