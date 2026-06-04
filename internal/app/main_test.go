package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ternura/agent"
)

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
	if err := store.FinishRun(run, "hello", agent.AgentRunResult{
		Content:    "hi",
		RawContent: "<think>ok</think>hi",
		Trace: []agent.AgentTraceItem{{
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
	if len(session.Runs[0].ModelInput) != 0 {
		t.Fatalf("model input should be empty for this result: %+v", session.Runs[0].ModelInput)
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

func TestSessionStorePersistsModelInputSnapshots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	store := newSessionStore(path)
	run := runLifecycle{ID: "run-model-input", StartedAt: time.Now()}

	if err := store.StartRun(run, "hello"); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := store.FinishRun(run, "hello", agent.AgentRunResult{
		Content: "hi",
		ModelInput: []agent.ModelInputSnapshot{{
			Call: 1,
			Messages: []agent.ModelInputMessage{{
				Role:    "user",
				Content: "hello",
			}},
			TotalRunes: 5,
		}},
	}, runStatusSucceeded, time.Now(), nil); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	reloaded := newSessionStore(path)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("load store: %v", err)
	}
	session, ok := currentSessionFromSnapshot(reloaded.Snapshot())
	if !ok || len(session.Runs) != 1 {
		t.Fatalf("session snapshot = %+v", reloaded.Snapshot())
	}
	if len(session.Runs[0].ModelInput) != 1 || session.Runs[0].ModelInput[0].Messages[0].Content != "hello" {
		t.Fatalf("model input snapshot not persisted: %+v", session.Runs[0].ModelInput)
	}
}

func TestSessionStoreSanitizesConversationMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	store := newSessionStore(path)
	firstRun := runLifecycle{ID: "run-clean-history", StartedAt: time.Now()}
	secondRun := runLifecycle{ID: "run-omit-history", StartedAt: time.Now()}

	if err := store.StartRun(firstRun, "hello"); err != nil {
		t.Fatalf("start first run: %v", err)
	}
	if err := store.FinishRun(firstRun, "hello", agent.AgentRunResult{
		Content: "<think>secret</think>\nclean answer",
	}, runStatusSucceeded, time.Now(), nil); err != nil {
		t.Fatalf("finish first run: %v", err)
	}
	if err := store.StartRun(secondRun, "guard"); err != nil {
		t.Fatalf("start second run: %v", err)
	}
	if err := store.FinishRun(secondRun, "guard", agent.AgentRunResult{
		Content: "我拦截了这次回复：没有本轮工具证据支撑。",
	}, runStatusSucceeded, time.Now(), nil); err != nil {
		t.Fatalf("finish second run: %v", err)
	}

	session, ok := currentSessionFromSnapshot(store.Snapshot())
	if !ok {
		t.Fatalf("current session not found")
	}
	if len(session.Messages) != 3 {
		t.Fatalf("messages = %d, want first user/assistant plus second user: %+v", len(session.Messages), session.Messages)
	}
	if session.Messages[1].Content != "clean answer" {
		t.Fatalf("assistant history should strip think content: %+v", session.Messages)
	}
	if session.Messages[2].Role != "user" || session.Messages[2].Content != "guard" {
		t.Fatalf("guard assistant should be omitted from history: %+v", session.Messages)
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

func TestSessionStoreLoadMarksRunningRunsInterrupted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	store := newSessionStore(path)
	run := runLifecycle{ID: "run-interrupted", StartedAt: time.Now().Add(-time.Minute)}
	if err := store.StartRun(run, "still running"); err != nil {
		t.Fatalf("start run: %v", err)
	}

	reloaded := newSessionStore(path)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("load store: %v", err)
	}
	session, ok := currentSessionFromSnapshot(reloaded.Snapshot())
	if !ok || len(session.Runs) != 1 {
		t.Fatalf("session snapshot = %+v", reloaded.Snapshot())
	}
	loadedRun := session.Runs[0]
	if loadedRun.Status != runStatusFailed ||
		!strings.Contains(loadedRun.Error, "interrupted") ||
		loadedRun.FinishedAt == "" ||
		loadedRun.DurationMS <= 0 {
		t.Fatalf("running run should be marked interrupted: %+v", loadedRun)
	}
}

func TestSessionStoreResetSessionClearsExistingSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.json")
	store := newSessionStore(path)
	sessionID := "feishu-test"
	if _, err := store.EnsureSession(sessionID, "Feishu Test"); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	run := runLifecycle{ID: "run-reset-test", StartedAt: time.Now()}
	if err := store.StartRunForSession(sessionID, run, "hello"); err != nil {
		t.Fatalf("start run: %v", err)
	}
	if err := store.FinishRunForSession(sessionID, run, "hello", agent.AgentRunResult{Content: "hi"}, runStatusSucceeded, time.Now(), nil); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, sessionsDirName, sessionID, sessionRunsDirName, run.ID+sessionRunFileExtension)); err != nil {
		t.Fatalf("run file not written before reset: %v", err)
	}

	snapshot, err := store.ResetSession(sessionID, "Feishu Test")
	if err != nil {
		t.Fatalf("reset session: %v", err)
	}
	session := findSession(snapshot.Sessions, sessionID)
	if session == nil {
		t.Fatalf("reset session missing from snapshot: %+v", snapshot.Sessions)
	}
	if len(session.Runs) != 0 || len(session.Messages) != 0 || len(session.Todos) != 0 {
		t.Fatalf("session should be empty after reset: %+v", session)
	}
	if _, err := os.Stat(filepath.Join(dir, sessionsDirName, sessionID, sessionRunsDirName, run.ID+sessionRunFileExtension)); !os.IsNotExist(err) {
		t.Fatalf("old run file should be removed, err=%v", err)
	}

	resetRun := runLifecycle{ID: "run-reset-confirm", StartedAt: time.Now()}
	if err := store.StartRunForSession(sessionID, resetRun, "new session"); err != nil {
		t.Fatalf("start reset run: %v", err)
	}
	if err := store.FinishRunForSessionWithoutMessages(sessionID, resetRun, "new session", agent.AgentRunResult{Content: "new session started"}, runStatusSucceeded, time.Now(), nil); err != nil {
		t.Fatalf("finish reset run: %v", err)
	}
	snapshot = store.Snapshot()
	session = findSession(snapshot.Sessions, sessionID)
	if session == nil || len(session.Runs) != 1 {
		t.Fatalf("reset confirmation run should be recorded only as a run: %+v", session)
	}
	if len(session.Messages) != 0 {
		t.Fatalf("reset confirmation should not enter model messages: %+v", session.Messages)
	}
}

func TestMessageRequestsNewSession(t *testing.T) {
	cases := []string{"new session", "New   Chat!", "reset session", "新会话。", "重新开始"}
	for _, input := range cases {
		if !messageRequestsNewSession(input) {
			t.Fatalf("messageRequestsNewSession(%q) = false, want true", input)
		}
	}
	if messageRequestsNewSession("继续搞一下") {
		t.Fatal("non-reset message should not request new session")
	}
}

func TestCleanConversationForRestoreDropsNoiseAndCapsHistory(t *testing.T) {
	persisted := []persistedMessage{
		{Role: "system", Content: "ignored"},
		{Role: "user", Content: "old user"},
		{Role: "assistant", Content: "<think>secret</think>\nold clean answer"},
		{Role: "assistant", Content: "我拦截了这次回复：没有本轮工具证据支撑。"},
		{Role: "user", Content: "middle user"},
		{Role: "assistant", Content: "middle answer"},
		{Role: "user", Content: "latest user"},
		{Role: "assistant", Content: "latest answer"},
	}

	cleaned := cleanConversationForRestore(persisted, 4)

	if len(cleaned) != 3 {
		t.Fatalf("cleaned messages = %d, want user-only history: %+v", len(cleaned), cleaned)
	}
	combined := ""
	for _, message := range cleaned {
		combined += message.Role + ":" + message.Content + "\n"
	}
	for _, blocked := range []string{"system", "<think>", "secret", "我拦截了这次回复"} {
		if strings.Contains(combined, blocked) {
			t.Fatalf("restore conversation still contains blocked content %q:\n%s", blocked, combined)
		}
	}
	if strings.Contains(combined, "assistant:") {
		t.Fatalf("assistant history should not be restored into model context:\n%s", combined)
	}
	if !strings.Contains(combined, "latest user") {
		t.Fatalf("latest clean history should be preserved:\n%s", combined)
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
