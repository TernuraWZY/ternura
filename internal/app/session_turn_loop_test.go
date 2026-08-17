package app

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ternura/agent"
)

func TestSessionTurnLoopKeepsLatestCorrectionAndCompleteRuntimePrompt(t *testing.T) {
	store := newSessionStore(filepath.Join(t.TempDir(), "session.json"))
	server := &agentServer{
		store:            store,
		runtime:          newRuntimeMonitor(),
		ctx:              context.Background(),
		taskCancels:      make(map[string]context.CancelFunc),
		taskSessionLocks: make(map[string]*sync.Mutex),
	}
	sessionID := "session-turn-loop-test"
	if _, err := store.EnsureSession(sessionID, "Turn Loop Test"); err != nil {
		t.Fatal(err)
	}
	session := server.newAgentSession(sessionID, nil)
	first := newSessionTurnRequest(context.Background(), server, session, "整理上海周末行程")
	second := newSessionTurnRequest(context.Background(), server, session, "不要博物馆")
	second.setCorrectionBase(first)
	latest := newSessionTurnRequest(context.Background(), server, session, "改成适合下雨天")
	latest.setCorrectionBase(second)

	controller := &sessionTurnLoop{server: server, sessionID: sessionID}
	planned, err := controller.genInput(context.Background(), nil, []*sessionTurnRequest{first, second, latest})
	if err != nil {
		t.Fatalf("generate turn input: %v", err)
	}
	if len(planned.Consumed) != 1 || planned.Consumed[0] != latest {
		t.Fatalf("consumed = %+v, want latest correction only", planned.Consumed)
	}
	modelPrompt := planned.Input.Messages[0].Content
	for _, want := range []string{"整理上海周末行程", "不要博物馆", "改成适合下雨天", "越靠后优先级越高"} {
		if !strings.Contains(modelPrompt, want) {
			t.Fatalf("correction prompt missing %q:\n%s", want, modelPrompt)
		}
	}
	latest.finish(agent.AgentRunResult{Content: "已改成雨天行程"}, nil)

	snapshot := store.Snapshot()
	persisted := findSession(snapshot.Sessions, sessionID)
	if persisted == nil || len(persisted.Runs) != 3 {
		t.Fatalf("persisted session = %+v", persisted)
	}
	if persisted.Runs[0].Status != runStatusCancelled || persisted.Runs[1].Status != runStatusCancelled || persisted.Runs[2].Status != runStatusSucceeded {
		t.Fatalf("run statuses = %+v", persisted.Runs)
	}
	if len(persisted.Messages) != 2 || persisted.Messages[0].Role != "user" || persisted.Messages[1].Role != "assistant" {
		t.Fatalf("messages = %+v, want one complete corrected turn", persisted.Messages)
	}
	if persisted.Messages[0].Content != modelPrompt || persisted.Messages[1].Content != "已改成雨天行程" {
		t.Fatalf("persisted corrected turn = %+v", persisted.Messages)
	}
	if persisted.Runs[2].UserMessage != "改成适合下雨天" {
		t.Fatalf("display message should stay user-authored: %+v", persisted.Runs[2])
	}
}
