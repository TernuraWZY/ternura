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

func TestServiceIgnoresLegacySchedulesJSON(t *testing.T) {
	root := t.TempDir()
	legacyPath := filepath.Join(root, "schedules.json")
	if err := os.WriteFile(legacyPath, []byte(`[
		{
			"id": "legacy-1",
			"name": "Legacy check",
			"message": "this should not be migrated",
			"session_id": "session-legacy",
			"status": "scheduled",
			"next_run_at_ms": 32503680000000,
			"delete_after_run": true
		}
	]`), 0o644); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	svc := cron.NewService(root)
	if err := svc.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	jobs := svc.List(true)
	if len(jobs) != 0 {
		t.Fatalf("jobs = %+v", jobs)
	}
}
