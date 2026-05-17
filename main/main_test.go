package main

import (
	"encoding/json"
	"os"
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
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
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
	session, ok := currentSessionFromSnapshot(snapshot)

	if !ok {
		t.Fatalf("current session not found")
	}
	if len(session.Runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(session.Runs))
	}
	if session.Runs[0].RunID != run.ID || session.Runs[0].Content != "hi" {
		t.Fatalf("run snapshot = %+v", session.Runs[0])
	}
	if len(session.Runs[0].Trace) != 1 || session.Runs[0].Trace[0].Content != "ok" {
		t.Fatalf("trace snapshot = %+v", session.Runs[0].Trace)
	}
	if len(session.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(session.Messages))
	}
	if session.Messages[0].Role != "user" || session.Messages[1].Role != "assistant" {
		t.Fatalf("messages snapshot = %+v", session.Messages)
	}

	if _, err := os.Stat(filepath.Join(dir, sessionIndexFileName)); err != nil {
		t.Fatalf("index file not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, sessionsDirName, session.SessionID, sessionMetaFileName)); err != nil {
		t.Fatalf("session meta file not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, sessionsDirName, session.SessionID, sessionMessagesFileName)); err != nil {
		t.Fatalf("messages file not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, sessionsDirName, session.SessionID, sessionTodosFileName)); err != nil {
		t.Fatalf("todos file not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, sessionsDirName, session.SessionID, sessionRunsDirName, run.ID+sessionRunFileExtension)); err != nil {
		t.Fatalf("run file not written: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("legacy session file should not be written, err=%v", err)
	}
}

func TestSessionStoreNewSessionPreservesPreviousSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	store := newSessionStore(path)
	run := runLifecycle{ID: "run-test-0004", StartedAt: time.Now()}

	if err := store.StartRun(run, "hello"); err != nil {
		t.Fatalf("start run: %v", err)
	}
	firstSnapshot := store.Snapshot()
	firstSessionID := firstSnapshot.CurrentSessionID

	snapshot, err := store.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if snapshot.CurrentSessionID == firstSessionID {
		t.Fatalf("current session id should change")
	}
	if len(snapshot.Sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(snapshot.Sessions))
	}
	firstSession := findSession(snapshot.Sessions, firstSessionID)
	if firstSession == nil || len(firstSession.Runs) != 1 {
		t.Fatalf("first session not preserved: %+v", snapshot.Sessions)
	}
	currentSession, ok := currentSessionFromSnapshot(snapshot)
	if !ok || len(currentSession.Runs) != 0 {
		t.Fatalf("current session = %+v, want empty new session", currentSession)
	}
}

func TestSessionStorePersistsSessionTodos(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	store := newSessionStore(path)

	if _, err := store.ReplaceTodos([]persistedTodo{{
		ID:      "todo-1",
		Content: "Wire update_todos into the agent",
		Status:  "in_progress",
	}}); err != nil {
		t.Fatalf("replace todos: %v", err)
	}

	reloaded := newSessionStore(path)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("load store: %v", err)
	}
	session, ok := currentSessionFromSnapshot(reloaded.Snapshot())
	if !ok {
		t.Fatalf("current session not found")
	}
	if len(session.Todos) != 1 {
		t.Fatalf("todos = %d, want 1", len(session.Todos))
	}
	if session.Todos[0].Content != "Wire update_todos into the agent" {
		t.Fatalf("todo snapshot = %+v", session.Todos[0])
	}

	history := historyFromSnapshot(reloaded.Snapshot())
	if len(history.Sessions) != 1 || len(history.Sessions[0].Todos) != 1 {
		t.Fatalf("history todos = %+v", history.Sessions)
	}
}

func TestSessionStoreMigratesLegacySnapshotFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	startedAt := time.Date(2026, 5, 15, 8, 30, 0, 0, time.UTC)
	finishedAt := startedAt.Add(time.Second)
	legacy := sessionSnapshot{
		Version:          2,
		CurrentSessionID: "session-legacy",
		Sessions: []persistedSession{{
			SessionID: "session-legacy",
			Title:     "Legacy",
			CreatedAt: startedAt.Format(time.RFC3339Nano),
			UpdatedAt: finishedAt.Format(time.RFC3339Nano),
			Messages: []persistedMessage{
				{Role: "user", Content: "legacy question"},
				{Role: "assistant", Content: "legacy answer"},
			},
			Runs: []persistedRun{{
				RunID:       "run-legacy",
				Status:      runStatusSucceeded,
				UserMessage: "legacy question",
				Content:     "legacy answer",
				StartedAt:   startedAt.Format(time.RFC3339Nano),
				FinishedAt:  finishedAt.Format(time.RFC3339Nano),
				DurationMS:  1000,
			}},
			Todos: []persistedTodo{{
				ID:      "todo-legacy",
				Content: "Migrate legacy store",
				Status:  "done",
			}},
		}},
	}
	payload, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatalf("marshal legacy: %v", err)
	}
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	store := newSessionStore(path)
	if err := store.Load(); err != nil {
		t.Fatalf("load migrated store: %v", err)
	}

	snapshot := store.Snapshot()
	session, ok := currentSessionFromSnapshot(snapshot)
	if !ok {
		t.Fatalf("current session not found")
	}
	if session.SessionID != "session-legacy" || len(session.Runs) != 1 || len(session.Messages) != 2 || len(session.Todos) != 1 {
		t.Fatalf("migrated session = %+v", session)
	}
	if _, err := os.Stat(filepath.Join(dir, sessionIndexFileName)); err != nil {
		t.Fatalf("index file not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, sessionsDirName, "session-legacy", sessionRunsDirName, "run-legacy"+sessionRunFileExtension)); err != nil {
		t.Fatalf("run file not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, sessionLegacyBackupName)); err != nil {
		t.Fatalf("legacy backup not written: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("legacy session file should be moved, err=%v", err)
	}
}
