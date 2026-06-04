package app

import (
	"context"
	"strings"

	"ternura/agent"
)

type sessionSummaryHook struct {
	store     *memoryStore
	sessionID func() string
}

func newSessionSummaryHook(store *memoryStore, sessionID func() string) *sessionSummaryHook {
	return &sessionSummaryHook{
		store:     store,
		sessionID: sessionID,
	}
}

func (h *sessionSummaryHook) HookName() string {
	return "session_summary"
}

func (h *sessionSummaryHook) BeforeModelCall(_ context.Context, run *agent.RunContext) error {
	if h == nil || h.store == nil || h.sessionID == nil || run == nil {
		return nil
	}
	status, err := h.store.Status(h.sessionID())
	if err != nil {
		return nil
	}
	summary := strings.TrimSpace(status.ShortTermSummary)
	if summary == "" {
		run.SetContextBlock("session-summary", "Session Summary", "")
		return nil
	}
	content := strings.Join([]string{
		"Compact summary of prior conversation in this session.",
		"Use it only for continuity; the latest user message has priority.",
		"Do not continue an old task unless the latest user message asks to.",
		"",
		summary,
	}, "\n")
	run.SetContextBlockWithPriority("session-summary", "Session Summary", content, agent.RuntimeContextPriorityLow, 800)
	return nil
}
