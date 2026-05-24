package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/cloudwego/eino/schema"
)

type ReadTool struct{}

func NewReadTool() *ReadTool {
	return &ReadTool{}
}

type ReadToolParam struct {
	Path string `json:"path"`
}

func (t *ReadTool) ToolName() AgentTool {
	return AgentToolRead
}

func (t *ReadTool) Info(context.Context) (*schema.ToolInfo, error) {
	return NewToolInfo(AgentToolRead, "read file content", map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "the file path to read",
			},
		},
		"required": []string{"path"},
	})
}

func (t *ReadTool) Execute(ctx context.Context, argumentsInJSON string) (string, error) {
	p := ReadToolParam{}
	err := json.Unmarshal([]byte(argumentsInJSON), &p)
	if err != nil {
		return "", err
	}

	file, err := os.Open(p.Path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return "", err
	}
	if fileInfo.IsDir() {
		return "", fmt.Errorf("path is a directory")
	}

	content, err := io.ReadAll(file)
	if err != nil {
		return "", err
	}

	return string(content), nil
}
