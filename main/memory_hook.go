package main

import (
	"context"
	"log"

	"ternura"
)

type memoryHook struct {
	store     *memoryStore
	sessionID func() string
}

func newMemoryHook(store *memoryStore, sessionID func() string) *memoryHook {
	return &memoryHook{
		store:     store,
		sessionID: sessionID,
	}
}

func (h *memoryHook) HookName() string {
	return "memory"
}

func (h *memoryHook) BeforeModelCall(ctx context.Context, run *ternura.RunContext) error {
	if h == nil || h.store == nil || run == nil {
		return nil
	}
	content, err := h.store.RuntimeContext(h.currentSessionID())
	if err != nil {
		log.Printf("load memory context: %v", err)
		return nil
	}
	run.SetContextBlock("memory", "Memory", content)
	return nil
}

func (h *memoryHook) AfterRun(ctx context.Context, run *ternura.RunContext, result ternura.AgentRunResult, runErr error) error {
	if h == nil || h.store == nil || run == nil || runErr != nil || result.Content == "" {
		return nil
	}
	if err := h.store.AppendShortTermTurn(h.currentSessionID(), run.Query, result); err != nil {
		log.Printf("update short-term memory: %v", err)
	}
	return nil
}

func (h *memoryHook) currentSessionID() string {
	if h == nil || h.sessionID == nil {
		return ""
	}
	return h.sessionID()
}
