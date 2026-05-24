package tool

import (
	"context"
	"encoding/json"
	"os"

	"github.com/cloudwego/eino/schema"
)

type WriteTool struct{}

func NewWriteTool() *WriteTool {
	return &WriteTool{}
}

type WriteToolParam struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (t *WriteTool) ToolName() AgentTool {
	return AgentToolWrite
}

func (t *WriteTool) Info(context.Context) (*schema.ToolInfo, error) {
	return NewToolInfo(AgentToolWrite, "write content to file", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "the file path to write",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "the content to write to the file",
			},
		},
		"required": []string{"path", "content"},
	})
}

func (t *WriteTool) Execute(ctx context.Context, argumentsInJSON string) (string, error) {
	p := WriteToolParam{}
	err := json.Unmarshal([]byte(argumentsInJSON), &p)
	if err != nil {
		return "", err
	}

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
