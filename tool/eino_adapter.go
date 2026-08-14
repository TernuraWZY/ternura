package tool

import (
	"context"
	"fmt"
	"strings"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type ApprovalAwareTool interface {
	ApprovalRequirement() (required bool, reason string)
}

type einoToolAdapter struct {
	name           AgentTool
	originalName   string
	invokable      einotool.InvokableTool
	info           *schema.ToolInfo
	approval       bool
	approvalReason string
}

func AdaptEinoTool(ctx context.Context, exposedName string, base einotool.BaseTool, approval bool, approvalReason string) (Tool, error) {
	invokable, ok := base.(einotool.InvokableTool)
	if !ok {
		return nil, fmt.Errorf("Eino tool %q is not invokable", exposedName)
	}
	info, err := base.Info(ctx)
	if err != nil {
		return nil, err
	}
	name := AgentTool(strings.TrimSpace(exposedName))
	if name == "" {
		name = AgentTool(info.Name)
	}
	adaptedInfo := *info
	adaptedInfo.Name = string(name)
	return &einoToolAdapter{
		name:           name,
		originalName:   info.Name,
		invokable:      invokable,
		info:           &adaptedInfo,
		approval:       approval,
		approvalReason: strings.TrimSpace(approvalReason),
	}, nil
}

func (t *einoToolAdapter) ToolName() AgentTool {
	return t.name
}

func (t *einoToolAdapter) Info(context.Context) (*schema.ToolInfo, error) {
	copyInfo := *t.info
	return &copyInfo, nil
}

func (t *einoToolAdapter) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...Option) (string, error) {
	return t.invokable.InvokableRun(ctx, argumentsInJSON, opts...)
}

func (t *einoToolAdapter) ApprovalRequirement() (bool, string) {
	return t.approval, t.approvalReason
}
