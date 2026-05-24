package cron_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ternura/internal/cron"
)

func TestServiceAddRemoveAndReload(t *testing.T) {
	root := t.TempDir()
	svc := cron.NewService(root)
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	job, err := svc.Add(context.Background(), cron.AddParams{
		Name:           "Build check",
		Message:        "check the build",
		SessionID:      "session-test",
		At:             time.Now().Add(time.Hour).Format(time.RFC3339Nano),
		DeleteAfterRun: true,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if job.ID == "" {
		t.Fatal("expected job id")
	}

	removed, err := svc.Remove(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removed.ID != job.ID {
		t.Fatalf("removed = %+v", removed)
	}

	reloaded := cron.NewService(root)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.List(true)) != 0 {
		t.Fatalf("expected empty jobs after remove, got %+v", reloaded.List(true))
	}
}

func TestServiceClaimDueCompletesOneShot(t *testing.T) {
	root := t.TempDir()
	svc := cron.NewService(root)
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	_, err := svc.Add(context.Background(), cron.AddParams{
		Message:        "run now",
		SessionID:      "session-test",
		DelaySeconds:   1,
		DeleteAfterRun: true,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	claimed, ok, err := svc.ClaimDue(time.Now().Add(2 * time.Second))
	if err != nil {
		t.Fatalf("claim due: %v", err)
	}
	if !ok {
		t.Fatal("expected due job")
	}
	if err := svc.RecordRun(context.Background(), claimed.ID, "run-test", time.Now().Add(-time.Second), nil); err != nil {
		t.Fatalf("record run: %v", err)
	}
	if len(svc.List(true)) != 0 {
		t.Fatalf("one-shot job should be deleted, got %+v", svc.List(true))
	}
}

func TestServiceMigratesLegacySchedulesJSON(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, "schedules.json")
	legacy := `{
  "version": 1,
  "tasks": [{
    "id": "schedule-legacy",
    "title": "legacy task",
    "prompt": "do thing",
    "session_id": "session-1",
    "status": "scheduled",
    "run_at": "2099-01-01T09:00:00+08:00",
    "created_at": "2026-05-21T09:00:00+08:00",
    "updated_at": "2026-05-21T09:00:00+08:00"
  }]
}`
	if err := os.WriteFile(legacyPath, []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	svc := cron.NewService(root)
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	jobs := svc.List(true)
	if len(jobs) != 1 || jobs[0].ID != "schedule-legacy" {
		t.Fatalf("jobs = %+v", jobs)
	}
}
