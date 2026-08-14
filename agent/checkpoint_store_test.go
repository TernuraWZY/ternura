package agent

import (
	"context"
	"testing"
)

func TestFileCheckPointStoreRoundTrip(t *testing.T) {
	store := NewFileCheckPointStore(t.TempDir())
	ctx := context.Background()
	if err := store.Set(ctx, "run-1", []byte("checkpoint")); err != nil {
		t.Fatalf("set checkpoint: %v", err)
	}
	content, found, err := store.Get(ctx, "run-1")
	if err != nil || !found || string(content) != "checkpoint" {
		t.Fatalf("get checkpoint: content=%q found=%v err=%v", content, found, err)
	}
	if err := store.Delete(ctx, "run-1"); err != nil {
		t.Fatalf("delete checkpoint: %v", err)
	}
	_, found, err = store.Get(ctx, "run-1")
	if err != nil || found {
		t.Fatalf("checkpoint should be deleted: found=%v err=%v", found, err)
	}
}
