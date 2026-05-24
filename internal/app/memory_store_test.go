package app

import (
	"context"
	"strings"
	"testing"

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
