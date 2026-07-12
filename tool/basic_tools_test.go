package tool

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReadToolReadsEntireFileByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")
	want := "first\nsecond\nthird\n"
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := NewReadTool().run(context.Background(), ReadToolParam{Path: path})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestReadToolSupportsOffsetAndLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")
	content := "zero\none\ntwo\nthree\nfour\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := NewReadTool().run(context.Background(), ReadToolParam{
		Path:   path,
		Offset: 1,
		Limit:  2,
	})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := "one\ntwo\n... (2 more lines)"
	if got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestReadToolRejectsNegativeRanges(t *testing.T) {
	tool := NewReadTool()
	if _, err := tool.run(context.Background(), ReadToolParam{Path: "ignored", Offset: -1}); err == nil {
		t.Fatal("negative offset should fail")
	}
	if _, err := tool.run(context.Background(), ReadToolParam{Path: "ignored", Limit: -1}); err == nil {
		t.Fatal("negative limit should fail")
	}
}

func TestWriteToolCreatesParentsAndReportsBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "sample.txt")
	content := "你好\n"

	got, err := NewWriteTool().run(context.Background(), WriteToolParam{Path: path, Content: content})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !strings.Contains(got, "Wrote 7 bytes") || !strings.Contains(got, path) {
		t.Fatalf("result = %q", got)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(raw) != content {
		t.Fatalf("content = %q, want %q", raw, content)
	}
}

func TestEditToolReplacesFirstExactMatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(path, []byte("old old"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	got, err := NewEditTool().run(context.Background(), EditToolParam{
		Path:   path,
		Before: "old",
		After:  "new",
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if !strings.Contains(got, "replaced 1 of 2 matches") {
		t.Fatalf("result = %q", got)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(raw) != "new old" {
		t.Fatalf("content = %q", raw)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat result: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestEditToolFailsWhenTextIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.txt")
	if err := os.WriteFile(path, []byte("unchanged"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := NewEditTool().run(context.Background(), EditToolParam{
		Path:   path,
		Before: "missing",
		After:  "replacement",
	})
	if err == nil || !strings.Contains(err.Error(), "text not found") {
		t.Fatalf("error = %v", err)
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read result: %v", readErr)
	}
	if string(raw) != "unchanged" {
		t.Fatalf("content changed to %q", raw)
	}
}

func TestBashToolKeepsFailureOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell assertion uses POSIX sh")
	}

	_, err := NewBashTool().run(context.Background(), BashToolParam{
		Command: "printf 'useful stderr' >&2; exit 7",
	})
	if err == nil {
		t.Fatal("command should fail")
	}
	if !strings.Contains(err.Error(), "exit status 7") || !strings.Contains(err.Error(), "useful stderr") {
		t.Fatalf("error = %v", err)
	}
}

func TestBasicToolSchemasExposeOptionalControls(t *testing.T) {
	tests := []struct {
		name       string
		tool       Tool
		properties []string
	}{
		{name: "read", tool: NewReadTool(), properties: []string{"path", "offset", "limit"}},
		{name: "bash", tool: NewBashTool(), properties: []string{"command", "timeout_seconds"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := tt.tool.Info(context.Background())
			if err != nil {
				t.Fatalf("info: %v", err)
			}
			schema, err := info.ParamsOneOf.ToJSONSchema()
			if err != nil {
				t.Fatalf("schema: %v", err)
			}
			for _, property := range tt.properties {
				if _, ok := schema.Properties.Get(property); !ok {
					t.Fatalf("property %q missing", property)
				}
			}
		})
	}
}
