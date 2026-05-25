package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"ternura/agent"
	"ternura/tool"
)

func TestMemoryStorePersistsLongTermMemory(t *testing.T) {
	store := newMemoryStore(t.TempDir())

	result, err := store.Remember(context.Background(), tool.MemoryItem{
		Category: tool.MemoryCategoryPreference,
		Content:  "User prefers concise Chinese responses.",
		Source:   "explicit preference",
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if result.ID == "" {
		t.Fatalf("memory id should be set")
	}

	status, err := store.Status("session-test")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.LongTermCount != 1 {
		t.Fatalf("long-term count = %d, want 1", status.LongTermCount)
	}

	contextText, err := store.RuntimeContext("session-test")
	if err != nil {
		t.Fatalf("runtime context: %v", err)
	}
	if !strings.Contains(contextText, result.ID) || !strings.Contains(contextText, "concise Chinese") {
		t.Fatalf("context does not include memory: %q", contextText)
	}

	detail, err := store.Detail("session-test")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if len(detail.LongTerm) != 1 || detail.LongTerm[0].ID != result.ID {
		t.Fatalf("detail long-term = %+v", detail.LongTerm)
	}
}

func TestMemoryStoreDeduplicatesAndForgetsLongTermMemory(t *testing.T) {
	store := newMemoryStore(t.TempDir())

	first, err := store.Remember(context.Background(), tool.MemoryItem{
		Category: tool.MemoryCategoryPreference,
		Content:  "User prefers concise answers.",
	})
	if err != nil {
		t.Fatalf("remember first: %v", err)
	}
	second, err := store.Remember(context.Background(), tool.MemoryItem{
		Category: tool.MemoryCategoryInstruction,
		Content:  "User prefers concise answers.",
	})
	if err != nil {
		t.Fatalf("remember duplicate: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("duplicate memory id = %q, want %q", second.ID, first.ID)
	}

	if err := store.Forget(context.Background(), first.ID); err != nil {
		t.Fatalf("forget: %v", err)
	}
	status, err := store.Status("session-test")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.LongTermCount != 0 {
		t.Fatalf("long-term count = %d, want 0", status.LongTermCount)
	}
}

func TestMemoryStoreShortTermMemoryRollsBySession(t *testing.T) {
	store := newMemoryStore(t.TempDir())
	store.shortTermTurnLimit = 2

	for _, message := range []string{"first", "second", "third"} {
		if err := store.AppendShortTermTurn("session-test", message, agent.AgentRunResult{Content: "answer " + message}); err != nil {
			t.Fatalf("append short-term turn: %v", err)
		}
	}

	status, err := store.Status("session-test")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.ShortTermTurns != 2 {
		t.Fatalf("short-term turns = %d, want 2", status.ShortTermTurns)
	}
	contextText, err := store.RuntimeContext("session-test")
	if err != nil {
		t.Fatalf("runtime context: %v", err)
	}
	if strings.Contains(contextText, "first") || !strings.Contains(contextText, "second") || !strings.Contains(contextText, "third") {
		t.Fatalf("short-term context not rolled correctly: %q", contextText)
	}
}

func TestMemoryStoreCapturesToolMemoryAndRecallsByQuery(t *testing.T) {
	root := t.TempDir()
	store := newMemoryStore(root)
	result := agent.ToolResult{
		Call: schema.ToolCall{
			ID: "call-read",
			Function: schema.FunctionCall{
				Name:      string(tool.AgentToolRead),
				Arguments: `{"path":"agent/context_builder.go"}`,
			},
		},
		Content: "context builder source with tool memory details",
	}

	record, ok, err := store.CaptureToolMemory(context.Background(), "session-test", result)
	if err != nil {
		t.Fatalf("capture tool memory: %v", err)
	}
	if !ok {
		t.Fatal("tool memory should be captured")
	}
	if err := store.AppendToolMemories("session-test", []toolMemoryRecord{record}); err != nil {
		t.Fatalf("append tool memory: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(record.RawRef)))
	if err != nil {
		t.Fatalf("read raw ref: %v", err)
	}
	if string(raw) != result.Content {
		t.Fatalf("raw artifact = %q, want %q", raw, result.Content)
	}

	contextText, err := store.RuntimeContextForQuery("session-test", "context_builder.go 的工具结果")
	if err != nil {
		t.Fatalf("runtime context: %v", err)
	}
	if !strings.Contains(contextText, "Relevant tool memory:") ||
		!strings.Contains(contextText, record.ID) ||
		!strings.Contains(contextText, record.RawRef) ||
		!strings.Contains(contextText, "context_builder.go") {
		t.Fatalf("context missing tool memory:\n%s", contextText)
	}

	unrelated, err := store.RuntimeContextForQuery("session-test", "你好")
	if err != nil {
		t.Fatalf("runtime context unrelated: %v", err)
	}
	if strings.Contains(unrelated, "Relevant tool memory:") {
		t.Fatalf("unrelated query should not recall tool memory:\n%s", unrelated)
	}
}

func TestToolMemoryHookDefersSummaryUntilRunFinishes(t *testing.T) {
	store := newMemoryStore(t.TempDir())
	hook := newToolMemoryHook(store, func() string { return "session-test" })
	run := agent.NewRunContext("read context builder", agent.RunModeSync)
	result := agent.ToolResult{
		Call: schema.ToolCall{
			ID: "call-read",
			Function: schema.FunctionCall{
				Name:      string(tool.AgentToolRead),
				Arguments: `{"path":"agent/context_builder.go"}`,
			},
		},
		Content: "context builder output",
	}

	if err := hook.AfterToolCall(context.Background(), run, &result); err != nil {
		t.Fatalf("after tool call: %v", err)
	}
	before, err := store.RuntimeContextForQuery("session-test", "context_builder.go")
	if err != nil {
		t.Fatalf("runtime context before run finish: %v", err)
	}
	if strings.Contains(before, "Relevant tool memory:") {
		t.Fatalf("tool memory should not be visible before run finishes:\n%s", before)
	}

	if err := hook.AfterRun(context.Background(), run, agent.AgentRunResult{Content: "done"}, nil); err != nil {
		t.Fatalf("after run: %v", err)
	}
	after, err := store.RuntimeContextForQuery("session-test", "context_builder.go")
	if err != nil {
		t.Fatalf("runtime context after run finish: %v", err)
	}
	if !strings.Contains(after, "Relevant tool memory:") {
		t.Fatalf("tool memory should be visible after run finishes:\n%s", after)
	}
}
