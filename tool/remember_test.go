package tool

import (
	"context"
	"strings"
	"testing"
)

func TestRememberToolStoresNormalizedMemory(t *testing.T) {
	var captured MemoryItem
	tool := NewRememberTool(func(_ context.Context, item MemoryItem) (MemoryResult, error) {
		captured = item
		return MemoryResult{
			ID:       "memory-test",
			Category: item.Category,
			Content:  item.Content,
		}, nil
	})

	result, err := tool.Execute(context.Background(), `{"category":"preference","content":"  prefers concise answers  ","source":" user said so "}`)
	if err != nil {
		t.Fatalf("execute remember: %v", err)
	}
	if captured.Category != MemoryCategoryPreference || captured.Content != "prefers concise answers" || captured.Source != "user said so" {
		t.Fatalf("captured memory = %+v", captured)
	}
	if !strings.Contains(result, "memory-test") {
		t.Fatalf("result = %q, want memory id", result)
	}
}

func TestForgetMemoryToolRequiresID(t *testing.T) {
	tool := NewForgetMemoryTool(nil)
	if _, err := tool.Execute(context.Background(), `{}`); err == nil {
		t.Fatalf("expected missing id error")
	}
}
