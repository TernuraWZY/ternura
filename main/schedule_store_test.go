package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"ternura"
	"ternura/tool"
)

func TestScheduleStoreCreateCancelAndReload(t *testing.T) {
	store := newScheduleStore(t.TempDir())
	task, err := store.Create(context.Background(), "session-test", tool.ScheduleTaskInput{
		Title:  "Build check",
		Prompt: "check the build",
		RunAt:  time.Now().Add(time.Hour).Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if task.ID == "" || task.Status != scheduleStatusScheduled {
		t.Fatalf("task = %+v", task)
	}

	cancelled, err := store.Cancel(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("cancel schedule: %v", err)
	}
	if cancelled.Status != scheduleStatusCancelled {
		t.Fatalf("cancelled status = %q", cancelled.Status)
	}

	reloaded := &scheduleStore{path: store.path}
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reload schedules: %v", err)
	}
	tasks := reloaded.Snapshot()
	if len(tasks) != 1 || tasks[0].Status != scheduleStatusCancelled {
		t.Fatalf("reloaded tasks = %+v", tasks)
	}
}

func TestScheduleStoreClaimDueCompletes(t *testing.T) {
	store := newScheduleStore(t.TempDir())
	task := scheduledTask{
		ID:        "schedule-due",
		Title:     "run now",
		Prompt:    "run now",
		SessionID: "session-test",
		Status:    scheduleStatusScheduled,
		RunAt:     time.Now().Add(-time.Minute).Format(time.RFC3339Nano),
		CreatedAt: time.Now().Add(-2 * time.Minute).Format(time.RFC3339Nano),
		UpdatedAt: time.Now().Add(-2 * time.Minute).Format(time.RFC3339Nano),
	}
	store.file.Tasks = append(store.file.Tasks, task)

	claimed, ok, err := store.ClaimDue(time.Now())
	if err != nil {
		t.Fatalf("claim due: %v", err)
	}
	if !ok || claimed.ID != task.ID || claimed.Status != scheduleStatusRunning {
		t.Fatalf("claimed = %+v ok=%v", claimed, ok)
	}

	if err := store.Complete(context.Background(), task.ID, "run-test", nil); err != nil {
		t.Fatalf("complete: %v", err)
	}
	tasks := store.Snapshot()
	if len(tasks) != 1 || tasks[0].Status != scheduleStatusCompleted || tasks[0].LastRunID != "run-test" {
		t.Fatalf("completed tasks = %+v", tasks)
	}
}

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
	if err := store.FinishRunForSession(firstID, run, "scheduled prompt", ternura.AgentRunResult{Content: "done"}, runStatusSucceeded, time.Now(), nil); err != nil {
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
