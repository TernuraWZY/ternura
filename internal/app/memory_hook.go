package app

import (
	"context"
	"log"
	"strings"

	"ternura/agent"
)

type memoryHook struct {
	store            *memoryStore
	sessionID        func() string
	keywordExtractor activeMemoryKeywordExtractor
}

type memoryHookOption func(*memoryHook)

func withActiveMemoryKeywordExtractor(extractor activeMemoryKeywordExtractor) memoryHookOption {
	return func(h *memoryHook) {
		h.keywordExtractor = extractor
	}
}

func newMemoryHook(store *memoryStore, sessionID func() string, opts ...memoryHookOption) *memoryHook {
	h := &memoryHook{
		store:     store,
		sessionID: sessionID,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
	}
	return h
}

func (h *memoryHook) HookName() string {
	return "active_memory"
}

func (h *memoryHook) BeforeModelCall(ctx context.Context, run *agent.RunContext) error {
	if h == nil || h.store == nil || run == nil {
		return nil
	}
	recall, ok := cachedActiveMemoryRecall(run)
	if ok {
		run.SetContextBlockWithPriority("active-memory", "Active Memory", recall.ContextBlock(), agent.RuntimeContextPriorityNormal, 4000)
		return nil
	}

	sessionID := h.currentSessionID()
	recallQuery, err := h.store.ActiveRecallQueryForQuery(sessionID, run.Query)
	if err != nil {
		log.Printf("build active memory query: %v", err)
		return nil
	}
	h.applyAIKeywordQuery(ctx, &recallQuery)

	recall, err = h.store.ActiveRecall(sessionID, recallQuery)
	if err != nil {
		log.Printf("active memory recall: %v", err)
		return nil
	}
	cacheActiveMemoryRecall(run, recall)
	run.SetContextBlockWithPriority("active-memory", "Active Memory", recall.ContextBlock(), agent.RuntimeContextPriorityNormal, 4000)
	return nil
}

func (h *memoryHook) AfterRun(ctx context.Context, run *agent.RunContext, result agent.AgentRunResult, runErr error) error {
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

const activeMemoryRecallMetadataKey = "active_memory_recall"

func (h *memoryHook) applyAIKeywordQuery(ctx context.Context, recallQuery *activeMemoryRecallQuery) {
	if h == nil || h.keywordExtractor == nil || recallQuery == nil {
		return
	}
	result, err := h.keywordExtractor.ExtractActiveMemoryKeywords(ctx, activeMemoryKeywordInput{
		LatestQuery:   recallQuery.LatestQuery,
		RecallQuery:   recallQuery.Query,
		RecentTurns:   recallQuery.RecentTurns,
		MaxQueryRunes: h.store.activeMemoryMaxQueryRunes,
	})
	if err != nil {
		log.Printf("active memory keyword extraction: %v", err)
		return
	}

	recallQuery.Keywords = normalizeKeywordList(result.Keywords, 8)
	recallQuery.QueryMode = "ai_" + normalizeActiveMemoryQueryMode(result.QueryMode)
	if !result.ShouldRecall {
		recallQuery.SearchQuery = ""
		return
	}

	searchQuery := strings.Join(strings.Fields(result.SearchQuery), " ")
	if searchQuery == "" && len(recallQuery.Keywords) > 0 {
		searchQuery = strings.Join(recallQuery.Keywords, " ")
	}
	recallQuery.SearchQuery = truncateRunes(searchQuery, h.store.activeMemoryMaxQueryRunes)
}

func cachedActiveMemoryRecall(run *agent.RunContext) (activeMemoryRecall, bool) {
	if run == nil || run.Metadata == nil {
		return activeMemoryRecall{}, false
	}
	recall, ok := run.Metadata[activeMemoryRecallMetadataKey].(activeMemoryRecall)
	return recall, ok
}

func cacheActiveMemoryRecall(run *agent.RunContext, recall activeMemoryRecall) {
	if run == nil {
		return
	}
	if run.Metadata == nil {
		run.Metadata = make(map[string]any)
	}
	run.Metadata[activeMemoryRecallMetadataKey] = recall
}
