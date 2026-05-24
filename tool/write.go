package tool

import (
	"context"
	"os"
)

type WriteTool struct {
	*agentTool
}

func NewWriteTool() *WriteTool {
	t := &WriteTool{}
	t.agentTool = newAgentTool(AgentToolWrite, "write content to file", t.run)
	return t
}

type WriteToolParam struct {
	Path    string `json:"path" jsonschema:"required" jsonschema_description:"the file path to write"`
	Content string `json:"content" jsonschema:"required" jsonschema_description:"the content to write to the file"`
}

func (t *WriteTool) run(ctx context.Context, p WriteToolParam) (string, error) {
	file, err := os.OpenFile(p.Path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return "", err
	}
	defer file.Close()

	_, err = file.WriteString(p.Content)
	if err != nil {
		return "", err
	}

	return "", nil
}
