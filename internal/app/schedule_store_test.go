package app

import (
	"path/filepath"
	"testing"
	"time"

	"ternura/agent"
)

func TestSessionStoreRunForSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	store := newSessionStore(path)
	firstID := store.CurrentSessionID()
	initialRun := runLifecycle{ID: "run-initial", StartedAt: time.Now()}
	if err := store.StartRun(initialRun, "initial prompt"); err != nil {
		t.Fatalf("start initial run: %v", err)
	}
	snapshot, err := store.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	if firstID == snapshot.CurrentSessionID {
		t.Fatalf("expected a second session")
	}

	run := runLifecycle{ID: "run-scheduled", StartedAt: time.Now()}
	if err := store.StartRunForSession(firstID, run, "scheduled prompt"); err != nil {
		t.Fatalf("start run for session: %v", err)
	}
	if err := store.FinishRunForSession(firstID, run, "scheduled prompt", agent.AgentRunResult{Content: "done"}, runStatusSucceeded, time.Now(), nil); err != nil {
		t.Fatalf("finish run for session: %v", err)
	}
	updated := store.Snapshot()
	first := findSession(updated.Sessions, firstID)
	current := findSession(updated.Sessions, updated.CurrentSessionID)
	if first == nil || len(first.Runs) != 2 || len(first.Messages) != 2 {
		t.Fatalf("first session = %+v", first)
	}
	if current == nil || len(current.Runs) != 0 {
		t.Fatalf("current session should remain untouched: %+v", current)
	}
}

func TestSessionStoreScheduledRunMarksTriggerAndSplitsPrompts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.json")
	store := newSessionStore(path)
	sessionID := store.CurrentSessionID()

	userRun := runLifecycle{ID: "run-user", StartedAt: time.Now()}
	if err := store.StartRunForSession(sessionID, userRun, "帮我看下天气"); err != nil {
		t.Fatalf("start user run: %v", err)
	}
	if err := store.FinishRunForSession(sessionID, userRun, "帮我看下天气", agent.AgentRunResult{Content: "今天晴"}, runStatusSucceeded, time.Now(), nil); err != nil {
		t.Fatalf("finish user run: %v", err)
	}
	preTitleSession := findSession(store.Snapshot().Sessions, sessionID)
	if preTitleSession == nil || preTitleSession.Title == "" {
		t.Fatalf("expected user-initiated run to set session title, got %+v", preTitleSession)
	}
	originalTitle := preTitleSession.Title

	display := "提醒用户：叫我去吃饭"
	runtime := wrapCronRuntimePrompt(display)
	scheduledRun := runLifecycle{ID: "run-scheduled", StartedAt: time.Now()}
	if err := store.StartScheduledRunForSession(sessionID, scheduledRun, display); err != nil {
		t.Fatalf("start scheduled run: %v", err)
	}
	if err := store.FinishScheduledRunForSession(sessionID, scheduledRun, display, runtime, agent.AgentRunResult{Content: "🍽️ 吃饭时间到"}, runStatusSucceeded, time.Now(), nil); err != nil {
		t.Fatalf("finish scheduled run: %v", err)
	}

	session := findSession(store.Snapshot().Sessions, sessionID)
	if session == nil {
		t.Fatalf("session vanished after scheduled run")
	}
	if session.Title != originalTitle {
		t.Fatalf("scheduled run must not overwrite session title: got %q, want %q", session.Title, originalTitle)
	}
	var scheduled *persistedRun
	for idx := range session.Runs {
		if session.Runs[idx].RunID == scheduledRun.ID {
			scheduled = &session.Runs[idx]
			break
		}
	}
	if scheduled == nil {
		t.Fatalf("scheduled run not persisted into session: %+v", session.Runs)
	}
	if scheduled.TriggerKind != runTriggerKindSchedule {
		t.Fatalf("trigger_kind = %q, want %q", scheduled.TriggerKind, runTriggerKindSchedule)
	}
	if scheduled.UserMessage != display {
		t.Fatalf("user_message = %q, want %q", scheduled.UserMessage, display)
	}
	if len(session.Messages) < 2 {
		t.Fatalf("expected at least the user-initiated turn plus the scheduled turn, got %d", len(session.Messages))
	}
	lastUser := session.Messages[len(session.Messages)-2]
	lastAssistant := session.Messages[len(session.Messages)-1]
	if lastUser.Role != "user" || lastUser.Content != runtime {
		t.Fatalf("expected wrapped runtime prompt in user message slot, got role=%q content=%q", lastUser.Role, lastUser.Content)
	}
	if lastAssistant.Role != "assistant" || lastAssistant.Content == "" {
		t.Fatalf("missing assistant reply for scheduled run, got %+v", lastAssistant)
	}
}

func TestNormalizeTriggerKindDefaultsToEmpty(t *testing.T) {
	if got := normalizeTriggerKind(runTriggerKindUser); got != "" {
		t.Fatalf("normalize(user) = %q, want empty for omitempty serialization", got)
	}
	if got := normalizeTriggerKind(runTriggerKindSchedule); got != runTriggerKindSchedule {
		t.Fatalf("normalize(schedule) = %q, want %q", got, runTriggerKindSchedule)
	}
}
