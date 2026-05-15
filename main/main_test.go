package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"ternura"
)

func TestChunkStringByRunesKeepsUTF8Boundaries(t *testing.T) {
	input := "我使用 UTF-8 编码。"
	chunks := chunkStringByRunes(input, 3)

	if got := strings.Join(chunks, ""); got != input {
		t.Fatalf("chunks joined to %q, want %q", got, input)
	}
	for _, chunk := range chunks {
		if !utf8.ValidString(chunk) {
			t.Fatalf("chunk %q is not valid utf-8", chunk)
		}
		if len([]rune(chunk)) > 3 {
			t.Fatalf("chunk %q has more than 3 runes", chunk)
		}
	}
}

func TestChunkStringByRunesFallsBackToSingleRuneChunks(t *testing.T) {
	chunks := chunkStringByRunes("你好", 0)
	if got := strings.Join(chunks, "|"); got != "你|好" {
		t.Fatalf("chunks = %q, want single-rune chunks", got)
	}
}

func TestRunLifecycleFinishEventIncludesTiming(t *testing.T) {
	startedAt := time.Date(2026, 5, 15, 8, 30, 0, 0, time.UTC)
	finishedAt := startedAt.Add(1500 * time.Millisecond)
	run := runLifecycle{ID: "run-test-0001", StartedAt: startedAt}

	event := run.finishEvent(eventTypeRunDone, runStatusSucceeded, finishedAt, nil)

	if event.RunID != run.ID {
		t.Fatalf("event run id = %q, want %q", event.RunID, run.ID)
	}
	if event.Type != eventTypeRunDone || event.Status != runStatusSucceeded {
		t.Fatalf("event type/status = %q/%q", event.Type, event.Status)
	}
	if event.StartedAt == "" || event.FinishedAt == "" {
		t.Fatalf("event should include started_at and finished_at")
	}
	if event.DurationMS != 1500 {
		t.Fatalf("duration = %d, want 1500", event.DurationMS)
	}
}

func TestApplyRunFieldsDecoratesJSONResponse(t *testing.T) {
	startedAt := time.Date(2026, 5, 15, 8, 30, 0, 0, time.UTC)
	finishedAt := startedAt.Add(250 * time.Millisecond)
	run := runLifecycle{ID: "run-test-0002", StartedAt: startedAt}
	resp := chatResponse{Content: "done"}

	applyRunFields(&resp, run, runStatusSucceeded, finishedAt)

	if resp.RunID != run.ID || resp.Status != runStatusSucceeded {
		t.Fatalf("response run fields = %q/%q", resp.RunID, resp.Status)
	}
	if resp.DurationMS != 250 {
		t.Fatalf("response duration = %d, want 250", resp.DurationMS)
	}
}

func TestSessionStorePersistsRunsAndConversation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	store := newSessionStore(path)
	startedAt := time.Date(2026, 5, 15, 8, 30, 0, 0, time.UTC)
	finishedAt := startedAt.Add(2 * time.Second)
	run := runLifecycle{ID: "run-test-0003", StartedAt: startedAt}

	if err := store.StartRun(run, "hello"); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := store.FinishRun(run, "hello", ternura.AgentRunResult{
		Content:    "hi",
		RawContent: "<think>ok</think>hi",
		Trace: []ternura.AgentTraceItem{{
			Type:    "think",
			Title:   "Thinking",
			Content: "ok",
		}},
	}, runStatusSucceeded, finishedAt, nil); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	reloaded := newSessionStore(path)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("load store: %v", err)
	}
	snapshot := reloaded.Snapshot()

	if len(snapshot.Runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(snapshot.Runs))
	}
	if snapshot.Runs[0].RunID != run.ID || snapshot.Runs[0].Content != "hi" {
		t.Fatalf("run snapshot = %+v", snapshot.Runs[0])
	}
	if len(snapshot.Runs[0].Trace) != 1 || snapshot.Runs[0].Trace[0].Content != "ok" {
		t.Fatalf("trace snapshot = %+v", snapshot.Runs[0].Trace)
	}
	if len(snapshot.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(snapshot.Messages))
	}
	if snapshot.Messages[0].Role != "user" || snapshot.Messages[1].Role != "assistant" {
		t.Fatalf("messages snapshot = %+v", snapshot.Messages)
	}
}

func TestSessionStoreClearRemovesPersistedHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	store := newSessionStore(path)
	run := runLifecycle{ID: "run-test-0004", StartedAt: time.Now()}

	if err := store.StartRun(run, "hello"); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("clear store: %v", err)
	}
	if snapshot := store.Snapshot(); len(snapshot.Runs) != 0 || len(snapshot.Messages) != 0 {
		t.Fatalf("snapshot after clear = %+v", snapshot)
	}
}
