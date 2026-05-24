package tool

import (
	"context"
	"os/exec"
	"runtime"
)

type BashTool struct {
	*agentTool
}

func NewBashTool() *BashTool {
	t := &BashTool{}
	t.agentTool = newAgentTool(AgentToolBash, "execute bash command", t.run)
	return t
}

type BashToolParam struct {
	Command string `json:"command" jsonschema:"required" jsonschema_description:"the bash command to execute"`
}

func (t *BashTool) run(ctx context.Context, p BashToolParam) (string, error) {
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
