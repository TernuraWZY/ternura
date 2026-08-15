package app

import (
	"context"
	"sync"
	"testing"

	"ternura/internal/feishu"
)

func TestHandleFeishuCardActionCancelsTrackedRun(t *testing.T) {
	runCtx, cancel := context.WithCancel(context.Background())
	server := &agentServer{
		taskCancels:      make(map[string]context.CancelFunc),
		taskSessionLocks: make(map[string]*sync.Mutex),
	}
	server.trackTask("run-active", cancel)

	response, err := server.handleFeishuCardAction(context.Background(), feishu.CardAction{
		Value: map[string]any{"action": "cancel_run", "run_id": "run-active"},
	})
	if err != nil {
		t.Fatalf("handle cancel action: %v", err)
	}
	select {
	case <-runCtx.Done():
	default:
		t.Fatal("tracked run context was not cancelled")
	}
	if response.ToastType != "success" || response.Card == nil {
		t.Fatalf("cancel response = %+v", response)
	}
}
