package mcpruntime

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLoadMissingConfigIsDisabled(t *testing.T) {
	t.Parallel()

	runtime, err := Load(context.Background(), filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(runtime.Tools()) != 0 {
		t.Fatalf("expected no MCP tools, got %d", len(runtime.Tools()))
	}
}

func TestSanitizeName(t *testing.T) {
	t.Parallel()

	if got := sanitizeName("My Server/tool.read"); got != "My_Server_tool_read" {
		t.Fatalf("unexpected sanitized name %q", got)
	}
}
