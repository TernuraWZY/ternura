package app

import (
	"context"
	"log"

	"ternura/agent"
)

const pendingToolMemoriesKey = "tool_memories.pending"

type toolMemoryHook struct {
	store     *memoryStore
	sessionID func() string
}

func newToolMemoryHook(store *memoryStore, sessionID func() string) *toolMemoryHook {
	return &toolMemoryHook{
		store:     store,
		sessionID: sessionID,
	}
}

func (h *toolMemoryHook) HookName() string {
	return "tool_memory"
}

func (h *toolMemoryHook) AfterToolCall(ctx context.Context, run *agent.RunContext, result *agent.ToolResult) error {
	if h == nil || h.store == nil || run == nil || result == nil {
		return nil
	}
	record, ok, err := h.store.CaptureToolMemory(ctx, h.currentSessionID(), *result)
	if err != nil {
		log.Printf("capture tool memory: %v", err)
		return nil
	}
	if !ok {
		return nil
	}
	pending := pendingToolMemories(run)
	pending = append(pending, record)
	run.Metadata[pendingToolMemoriesKey] = pending
	return nil
}

func (h *toolMemoryHook) AfterRun(_ context.Context, run *agent.RunContext, _ agent.AgentRunResult, runErr error) error {
	if h == nil || h.store == nil || run == nil || runErr != nil {
		return nil
	}
	records := pendingToolMemories(run)
	if len(records) == 0 {
		return nil
	}
	if err := h.store.AppendToolMemories(h.currentSessionID(), records); err != nil {
		log.Printf("append tool memories: %v", err)
	}
	delete(run.Metadata, pendingToolMemoriesKey)
	return nil
}

func (h *toolMemoryHook) currentSessionID() string {
	if h == nil || h.sessionID == nil {
		return ""
	}
	return h.sessionID()
}

func pendingToolMemories(run *agent.RunContext) []toolMemoryRecord {
	if run == nil || run.Metadata == nil {
		return nil
	}
	records, _ := run.Metadata[pendingToolMemoriesKey].([]toolMemoryRecord)
	return records
}
