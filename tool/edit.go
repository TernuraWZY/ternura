package tool

import (
	"context"
	"fmt"
	"os"
	"strings"
)

type EditTool struct {
	*agentTool
}

func NewEditTool() *EditTool {
	t := &EditTool{}
	t.agentTool = newAgentTool(AgentToolEdit, "replace the first exact occurrence of text in a file", t.run)
	return t
}

type EditToolParam struct {
	Path   string `json:"path" jsonschema:"required" jsonschema_description:"the file path to edit"`
	Before string `json:"before" jsonschema:"required" jsonschema_description:"the content to search for"`
	After  string `json:"after" jsonschema:"required" jsonschema_description:"the content to replace with"`
}

func (t *EditTool) run(ctx context.Context, p EditToolParam) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if p.Before == "" {
		return "", fmt.Errorf("before text must not be empty")
	}

	raw, err := os.ReadFile(p.Path)
	if err != nil {
		return "", err
	}
	content := string(raw)
	matches := strings.Count(content, p.Before)
	if matches == 0 {
		return "", fmt.Errorf("text not found in %s", p.Path)
	}

	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(p.Path); statErr == nil {
		mode = info.Mode().Perm()
	}
	replaced := strings.Replace(content, p.Before, p.After, 1)
	if err := writeFileAtomic(p.Path, []byte(replaced), mode); err != nil {
		return "", err
	}
	return fmt.Sprintf("Edited %s (replaced 1 of %d matches)", p.Path, matches), nil
}
