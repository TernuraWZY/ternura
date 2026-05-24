package tool

import (
	"context"
	"fmt"
	"io"
	"os"
)

type ReadTool struct {
	*agentTool
}

func NewReadTool() *ReadTool {
	t := &ReadTool{}
	t.agentTool = newAgentTool(AgentToolRead, "read file content", t.run)
	return t
}

type ReadToolParam struct {
	Path string `json:"path" jsonschema:"required" jsonschema_description:"the file path to read"`
}

func (t *ReadTool) run(ctx context.Context, p ReadToolParam) (string, error) {
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
