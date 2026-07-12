package tool

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

const (
	defaultBashTimeoutSeconds = 120
	maxBashTimeoutSeconds     = 600
	maxBashOutputRunes        = 50000
)

type BashTool struct {
	*agentTool
}

func NewBashTool() *BashTool {
	t := &BashTool{}
	t.agentTool = newAgentTool(AgentToolBash, "execute a shell command with a bounded timeout and return combined stdout and stderr", t.run)
	return t
}

type BashToolParam struct {
	Command        string `json:"command" jsonschema:"required" jsonschema_description:"the shell command to execute"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema_description:"command timeout in seconds; defaults to 120 and is capped at 600"`
}

func (t *BashTool) run(ctx context.Context, p BashToolParam) (string, error) {
	timeoutSeconds := p.TimeoutSeconds
	if timeoutSeconds == 0 {
		timeoutSeconds = defaultBashTimeoutSeconds
	}
	if timeoutSeconds < 0 || timeoutSeconds > maxBashTimeoutSeconds {
		return "", fmt.Errorf("timeout_seconds must be between 1 and %d", maxBashTimeoutSeconds)
	}
	command := strings.TrimSpace(p.Command)
	if command == "" {
		return "", fmt.Errorf("command is required")
	}

	commandCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// Windows: use cmd.exe to interpret the command line
		cmd = exec.CommandContext(commandCtx, "cmd", "/C", command)
	} else {
		// Linux/macOS: use POSIX sh (more universal than assuming bash exists)
		cmd = exec.CommandContext(commandCtx, "sh", "-c", command)
	}

	output, err := cmd.CombinedOutput()
	content := limitBashOutput(string(output))
	if parentErr := ctx.Err(); parentErr != nil {
		if content == "" {
			return "", parentErr
		}
		return "", fmt.Errorf("%w\n\n%s", parentErr, content)
	}
	if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
		if content == "" {
			return "", fmt.Errorf("command timed out after %ds", timeoutSeconds)
		}
		return "", fmt.Errorf("command timed out after %ds\n\n%s", timeoutSeconds, content)
	}
	if err != nil {
		if content == "" {
			return "", fmt.Errorf("command failed: %w", err)
		}
		return "", fmt.Errorf("command failed: %w\n\n%s", err, content)
	}
	if content == "" {
		return "(no output)", nil
	}
	return content, nil
}

func limitBashOutput(content string) string {
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if len(runes) <= maxBashOutputRunes {
		return content
	}
	omitted := len(runes) - maxBashOutputRunes
	return string(runes[:maxBashOutputRunes]) + fmt.Sprintf("\n\n... (%d characters omitted)", omitted)
}
