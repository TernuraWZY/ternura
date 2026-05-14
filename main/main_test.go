package main

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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
