package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type WriteTool struct {
	*agentTool
}

func NewWriteTool() *WriteTool {
	t := &WriteTool{}
	t.agentTool = newAgentTool(AgentToolWrite, "write content to a file, creating parent directories when needed", t.run)
	return t
}

type WriteToolParam struct {
	Path    string `json:"path" jsonschema:"required" jsonschema_description:"the file path to write"`
	Content string `json:"content" jsonschema:"required" jsonschema_description:"the content to write to the file"`
}

func (t *WriteTool) run(ctx context.Context, p WriteToolParam) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o755); err != nil {
		return "", err
	}
	if err := writeFileAtomic(p.Path, []byte(p.Content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Wrote %d bytes to %s", len([]byte(p.Content)), p.Path), nil
}
