package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"ternura/agent"
)

func TestTaskAPIReadsPersistedArtifact(t *testing.T) {
	t.Setenv("TERNURA_API_TOKEN", "")
	store := newSessionStore(filepath.Join(t.TempDir(), "session.json"))
	run := runLifecycle{ID: "run-task-api", StartedAt: time.Now()}
	if err := store.StartRun(run, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishRun(run, "hello", agent.AgentRunResult{Content: "done"}, runStatusSucceeded, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	server := &agentServer{store: store, taskCancels: make(map[string]context.CancelFunc)}

	request := httptest.NewRequest(http.MethodGet, "/api/tasks/"+run.ID, nil)
	request.RemoteAddr = "127.0.0.1:12345"
	response := httptest.NewRecorder()
	server.handleTask(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload taskResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Run.Artifacts) != 1 || payload.Run.Artifacts[0].Content != "done" {
		t.Fatalf("unexpected artifacts: %+v", payload.Run.Artifacts)
	}
}

func TestCancelWaitingApprovalTask(t *testing.T) {
	t.Parallel()
	store := newSessionStore(filepath.Join(t.TempDir(), "session.json"))
	run := runLifecycle{ID: "run-waiting-task", StartedAt: time.Now()}
	if err := store.StartRun(run, "delete it"); err != nil {
		t.Fatal(err)
	}
	result := agent.AgentRunResult{
		Content:      "approval needed",
		CheckpointID: run.ID,
		PendingApproval: &agent.ToolApprovalRequest{
			CheckpointID: run.ID,
			InterruptID:  "interrupt-1",
		},
	}
	if err := store.FinishRun(run, "delete it", result, runStatusWaitingApproval, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	server := &agentServer{store: store, taskCancels: make(map[string]context.CancelFunc)}
	if err := server.cancelTask(run.ID); err != nil {
		t.Fatal(err)
	}
	_, persisted, ok := store.RunByID(run.ID)
	if !ok || persisted.Status != runStatusCancelled {
		t.Fatalf("unexpected cancelled task: %+v", persisted)
	}
}

func TestParseTaskPath(t *testing.T) {
	t.Parallel()

	runID, action, ok := parseTaskPath("/api/tasks/run-123/decision")
	if !ok || runID != "run-123" || action != "decision" {
		t.Fatalf("unexpected parse: %q %q %v", runID, action, ok)
	}
}
