package app

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"ternura/agent"
)

func TestAgentSessionPersistsUserRunLifecycle(t *testing.T) {
	store := newSessionStore(filepath.Join(t.TempDir(), "session.json"))
	server := &agentServer{store: store}
	sessionID := "session-agent-test"
	if _, err := store.EnsureSession(sessionID, "Agent Session Test"); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	session := server.newAgentSession(sessionID, nil)

	outcome := session.run(context.Background(), agentSessionRunRequest{
		Kind:           agentSessionRunUser,
		DisplayMessage: "hello",
		DirectResult:   &agent.AgentRunResult{Content: "hi"},
	})

	persisted := findSession(store.Snapshot().Sessions, sessionID)
	if persisted == nil {
		t.Fatalf("session not found")
	}
	if len(persisted.Runs) != 1 || persisted.Runs[0].Status != runStatusSucceeded {
		t.Fatalf("runs = %+v, want one succeeded run", persisted.Runs)
	}
	if persisted.Runs[0].RunID != outcome.Run.ID {
		t.Fatalf("run id = %q, want %q", persisted.Runs[0].RunID, outcome.Run.ID)
	}
	if len(persisted.Messages) != 2 ||
		persisted.Messages[0].Role != "user" ||
		persisted.Messages[1].Role != "assistant" {
		t.Fatalf("messages = %+v, want persisted user/assistant pair", persisted.Messages)
	}
}

func TestAgentSessionPersistsResetRunWithoutMessages(t *testing.T) {
	store := newSessionStore(filepath.Join(t.TempDir(), "session.json"))
	server := &agentServer{store: store}
	sessionID := "session-reset-test"
	if _, err := store.EnsureSession(sessionID, "Agent Session Test"); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	session := server.newAgentSession(sessionID, nil)

	session.run(context.Background(), agentSessionRunRequest{
		Kind:           agentSessionRunUser,
		DisplayMessage: "new session",
		DirectResult:   &agent.AgentRunResult{Content: "新会话已开始。"},
		OmitMessages:   true,
	})

	persisted := findSession(store.Snapshot().Sessions, sessionID)
	if persisted == nil {
		t.Fatalf("session not found")
	}
	if len(persisted.Runs) != 1 || persisted.Runs[0].Status != runStatusSucceeded {
		t.Fatalf("runs = %+v, want one succeeded reset run", persisted.Runs)
	}
	if len(persisted.Messages) != 0 {
		t.Fatalf("reset run should not enter model messages: %+v", persisted.Messages)
	}
}

func TestAgentSessionPersistsScheduledRunWithRuntimePrompt(t *testing.T) {
	store := newSessionStore(filepath.Join(t.TempDir(), "session.json"))
	server := &agentServer{store: store}
	sessionID := "session-scheduled-test"
	if _, err := store.EnsureSession(sessionID, "Agent Session Test"); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	session := server.newAgentSession(sessionID, nil)

	outcome := session.run(context.Background(), agentSessionRunRequest{
		Kind:           agentSessionRunScheduled,
		DisplayMessage: "提醒用户：喝水",
		RuntimePrompt:  "[cron job fired]\n提醒用户：喝水",
		DirectResult:   &agent.AgentRunResult{Content: "该喝水了。"},
	})

	persisted := findSession(store.Snapshot().Sessions, sessionID)
	if persisted == nil {
		t.Fatalf("session not found")
	}
	if len(persisted.Runs) != 1 || persisted.Runs[0].RunID != outcome.Run.ID {
		t.Fatalf("runs = %+v, want scheduled run", persisted.Runs)
	}
	if persisted.Runs[0].TriggerKind != runTriggerKindSchedule || persisted.Runs[0].UserMessage != "提醒用户：喝水" {
		t.Fatalf("run metadata = %+v, want schedule display prompt", persisted.Runs[0])
	}
	if len(persisted.Messages) != 2 || persisted.Messages[0].Content != "[cron job fired]\n提醒用户：喝水" {
		t.Fatalf("scheduled runtime prompt not persisted into model history: %+v", persisted.Messages)
	}
}

func TestAgentSessionPersistsFriendlyFailureContent(t *testing.T) {
	store := newSessionStore(filepath.Join(t.TempDir(), "session.json"))
	server := &agentServer{store: store}
	sessionID := "session-failed-test"
	if _, err := store.EnsureSession(sessionID, "Agent Session Test"); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	session := server.newAgentSession(sessionID, nil)

	outcome := session.run(context.Background(), agentSessionRunRequest{
		Kind:           agentSessionRunUser,
		DisplayMessage: "查一下通州一日游",
		DirectResult:   &agent.AgentRunResult{},
		DirectErr:      errors.New("[GraphRunError] exceeds max steps"),
	})

	if outcome.Err == nil {
		t.Fatalf("outcome should keep the original error")
	}
	if !strings.Contains(outcome.Result.Content, "最大步骤数") {
		t.Fatalf("friendly content = %q, want max-step explanation", outcome.Result.Content)
	}
	persisted := findSession(store.Snapshot().Sessions, sessionID)
	if persisted == nil || len(persisted.Runs) != 1 {
		t.Fatalf("session runs = %+v", persisted)
	}
	if persisted.Runs[0].Status != runStatusFailed || !strings.Contains(persisted.Runs[0].Content, "最大步骤数") {
		t.Fatalf("failed run = %+v, want persisted friendly failure", persisted.Runs[0])
	}
}

func TestFormatFeishuOutcomeReturnsFriendlyFailureReply(t *testing.T) {
	outcome := agentSessionRunOutcome{
		Run: runLifecycle{ID: "run-failed"},
		Result: agent.AgentRunResult{
			Content: "这轮 Agent 在工具和推理循环里没有及时收口。",
		},
		Err: errors.New("[GraphRunError] exceeds max steps"),
	}

	reply, err := formatFeishuOutcome(outcome)

	if err != nil {
		t.Fatalf("format outcome: %v", err)
	}
	if !strings.Contains(reply.Content, "没有及时收口") && reply.Card == nil {
		t.Fatalf("reply = %+v, want friendly failure content", reply)
	}
}
