package tool

import (
	"context"
	"encoding/json"
	"os/exec"
	"runtime"

	"github.com/cloudwego/eino/schema"
)

type BashTool struct{}

func NewBashTool() *BashTool {
	return &BashTool{}
}

type BashToolParam struct {
	Command string `json:"command"`
}

func (t *BashTool) ToolName() AgentTool {
	return AgentToolBash
}

func (t *BashTool) Info(context.Context) (*schema.ToolInfo, error) {
	return NewToolInfo(AgentToolBash, "execute bash command", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "the bash command to execute",
			},
		},
		"required": []string{"command"},
	})
}

func (t *BashTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...Option) (string, error) {
	p := BashToolParam{}
	err := json.Unmarshal([]byte(argumentsInJSON), &p)
	if err != nil {
		return "", err
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// Windows: use cmd.exe to interpret the command line
		cmd = exec.CommandContext(ctx, "cmd", "/C", p.Command)
	} else {
		// Linux/macOS: use POSIX sh (more universal than assuming bash exists)
		cmd = exec.CommandContext(ctx, "sh", "-c", p.Command)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(output), nil
}
