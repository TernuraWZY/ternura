package tool

import (
	"context"
	"os"
	"strings"
)

type EditTool struct {
	*agentTool
}

func NewEditTool() *EditTool {
	t := &EditTool{}
	t.agentTool = newAgentTool(AgentToolEdit, "edit content in file", t.run)
	return t
}

type EditToolParam struct {
	Path   string `json:"path" jsonschema:"required" jsonschema_description:"the file path to edit"`
	Before string `json:"before" jsonschema:"required" jsonschema_description:"the content to search for"`
	After  string `json:"after" jsonschema:"required" jsonschema_description:"the content to replace with"`
}

func (t *EditTool) run(ctx context.Context, p EditToolParam) (string, error) {
	raw, err := os.ReadFile(p.Path)
	if err != nil {
		return "", err
	}

	backupPath := p.Path + ".bak"
	err = os.WriteFile(backupPath, raw, 0644)
	if err != nil {
		return "", err
	}

	replaced := strings.ReplaceAll(string(raw), p.Before, p.After)

	err = os.WriteFile(p.Path, []byte(replaced), 0644)
	if err != nil {
		os.Rename(backupPath, p.Path)
		return "", err
	}

	os.Remove(backupPath)
	return "", nil
}
