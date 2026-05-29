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
	summarizer       activeMemorySummarizer
}

type memoryHookOption func(*memoryHook)

func withActiveMemoryKeywordExtractor(extractor activeMemoryKeywordExtractor) memoryHookOption {
	return func(h *memoryHook) {
		h.keywordExtractor = extractor
	}
}

func withActiveMemorySummarizer(summarizer activeMemorySummarizer) memoryHookOption {
	return func(h *memoryHook) {
		h.summarizer = summarizer
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
		run.SetContextBlockWithPriority("active-memory", "Active Memory", recall.ContextBlock(), agent.RuntimeContextPriorityNormal, 1000)
		return nil
	}

	sessionID := h.currentSessionID()
	recallQuery, err := h.store.ActiveRecallQueryForQuery(sessionID, run.Query)
	if err != nil {
		log.Printf("build active memory query: %v", err)
		return nil
	}
	if recallQuery.ShouldRecall {
		h.applyAIKeywordQuery(ctx, &recallQuery)
	}

	recall, err = h.store.ActiveRecall(sessionID, recallQuery)
	if err != nil {
		log.Printf("active memory recall: %v", err)
		return nil
	}
	h.applyAISummary(ctx, run, &recall)
	cacheActiveMemoryRecall(run, recall)
	run.SetContextBlockWithPriority("active-memory", "Active Memory", recall.ContextBlock(), agent.RuntimeContextPriorityNormal, 1000)
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

func (h *memoryHook) FinalizeRun(ctx context.Context, run *agent.RunContext, result *agent.AgentRunResult) error {
	if h == nil || run == nil || result == nil {
		return nil
	}
	recall, ok := cachedActiveMemoryRecall(run)
	if !ok {
		return nil
	}
	content := formatActiveMemoryTrace(recall)
	if content == "" {
		return nil
	}
	result.Trace = append(result.Trace, agent.AgentTraceItem{
		Type:    "memory",
		Title:   "上下文记忆搜索",
		Content: content,
	})
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
		recallQuery.ShouldRecall = false
		recallQuery.Keywords = nil
		recallQuery.SearchQuery = ""
		return
	}

	recallQuery.ShouldRecall = true
	searchQuery := strings.Join(strings.Fields(result.SearchQuery), " ")
	if searchQuery == "" && len(recallQuery.Keywords) > 0 {
		searchQuery = strings.Join(recallQuery.Keywords, " ")
	}
	recallQuery.SearchQuery = truncateRunes(searchQuery, h.store.activeMemoryMaxQueryRunes)
}

func (h *memoryHook) applyAISummary(ctx context.Context, run *agent.RunContext, recall *activeMemoryRecall) {
	if h == nil || h.summarizer == nil || h.store == nil || run == nil || recall == nil {
		return
	}
	candidate := strings.TrimSpace(recall.RawSummary)
	if candidate == "" {
		candidate = strings.TrimSpace(recall.Summary)
	}
	if candidate == "" {
		return
	}
	result, err := h.summarizer.SummarizeActiveMemory(ctx, activeMemorySummaryInput{
		LatestQuery:     run.Query,
		QueryMode:       recall.QueryMode,
		SearchQuery:     recall.SearchQuery,
		Keywords:        recall.Keywords,
		RecallCandidate: candidate,
		MaxSummaryRunes: h.store.activeMemoryMaxSummaryRunes,
	})
	if err != nil {
		log.Printf("active memory summarization: %v", err)
		return
	}
	summary := strings.TrimSpace(result.Summary)
	if summary == "" {
		recall.Summary = ""
		recall.Summarized = true
		recall.Status = "no_relevant_memory"
		return
	}
	recall.Summary = truncateRunes(summary, h.store.activeMemoryMaxSummaryRunes)
	recall.Summarized = true
}

func formatActiveMemoryTrace(recall activeMemoryRecall) string {
	summary := strings.TrimSpace(recall.Summary)
	searchQuery := strings.TrimSpace(recall.SearchQuery)
	if summary == "" && searchQuery == "" && len(recall.Keywords) == 0 {
		return ""
	}

	sections := make([]string, 0, 12)
	if recall.Status != "" {
		sections = append(sections, "**状态**", "", "`"+recall.Status+"`", "")
	}
	if recall.QueryMode != "" {
		sections = append(sections, "**Query mode**", "", "`"+recall.QueryMode+"`", "")
	}
	if recall.Summarized {
		sections = append(sections, "**Summary mode**", "", "`ai_summary`", "")
	}
	if len(recall.Keywords) > 0 {
		sections = append(sections, "**Keywords**", "", "`"+strings.Join(recall.Keywords, "`, `")+"`", "")
	}
	if searchQuery != "" {
		sections = append(sections, "**Search query**", "", searchQuery, "")
	}
	if summary != "" {
		sections = append(sections, "**召回内容**", "", summary)
	}
	if summary == "" {
		sections = append(sections, "**召回内容**", "", "未命中相关记忆。")
	}
	return strings.TrimSpace(strings.Join(sections, "\n"))
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
