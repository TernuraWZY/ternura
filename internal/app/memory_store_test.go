package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"

	"ternura/agent"
	"ternura/tool"
)

func TestMemoryStorePersistsLongTermMemory(t *testing.T) {
	store := newMemoryStore(t.TempDir())

	result, err := store.Remember(context.Background(), tool.MemoryItem{
		Category: tool.MemoryCategoryPreference,
		Content:  "User prefers concise Chinese responses.",
		Source:   "explicit preference",
	})
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if result.ID == "" {
		t.Fatalf("memory id should be set")
	}

	status, err := store.Status("session-test")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.LongTermCount != 1 {
		t.Fatalf("long-term count = %d, want 1", status.LongTermCount)
	}

	contextText, err := store.RuntimeContext("session-test")
	if err != nil {
		t.Fatalf("runtime context: %v", err)
	}
	if !strings.Contains(contextText, result.ID) || !strings.Contains(contextText, "concise Chinese") {
		t.Fatalf("context does not include memory: %q", contextText)
	}

	detail, err := store.Detail("session-test")
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if len(detail.LongTerm) != 1 || detail.LongTerm[0].ID != result.ID {
		t.Fatalf("detail long-term = %+v", detail.LongTerm)
	}
}

func TestMemoryStoreDeduplicatesAndForgetsLongTermMemory(t *testing.T) {
	store := newMemoryStore(t.TempDir())

	first, err := store.Remember(context.Background(), tool.MemoryItem{
		Category: tool.MemoryCategoryPreference,
		Content:  "User prefers concise answers.",
	})
	if err != nil {
		t.Fatalf("remember first: %v", err)
	}
	second, err := store.Remember(context.Background(), tool.MemoryItem{
		Category: tool.MemoryCategoryInstruction,
		Content:  "User prefers concise answers.",
	})
	if err != nil {
		t.Fatalf("remember duplicate: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("duplicate memory id = %q, want %q", second.ID, first.ID)
	}

	if err := store.Forget(context.Background(), first.ID); err != nil {
		t.Fatalf("forget: %v", err)
	}
	status, err := store.Status("session-test")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.LongTermCount != 0 {
		t.Fatalf("long-term count = %d, want 0", status.LongTermCount)
	}
}

func TestMemoryStoreShortTermMemoryRollsBySession(t *testing.T) {
	store := newMemoryStore(t.TempDir())
	store.shortTermTurnLimit = 2

	for _, message := range []string{"first", "second", "third"} {
		if err := store.AppendShortTermTurn("session-test", message, agent.AgentRunResult{Content: "answer " + message}); err != nil {
			t.Fatalf("append short-term turn: %v", err)
		}
	}

	status, err := store.Status("session-test")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if status.ShortTermTurns != 2 {
		t.Fatalf("short-term turns = %d, want 2", status.ShortTermTurns)
	}
	contextText, err := store.RuntimeContext("session-test")
	if err != nil {
		t.Fatalf("runtime context: %v", err)
	}
	if strings.Contains(contextText, "first") || !strings.Contains(contextText, "second") || !strings.Contains(contextText, "third") {
		t.Fatalf("short-term context not rolled correctly: %q", contextText)
	}
}

func TestMemoryStoreSelectsRelevantLongTermMemoriesForContext(t *testing.T) {
	store := newMemoryStore(t.TempDir())
	store.longTermContextLimit = 2
	items := []tool.MemoryItem{
		{Category: tool.MemoryCategoryPreference, Content: "User likes short replies."},
		{Category: tool.MemoryCategoryProject, Content: "Kubernetes deployment uses namespace ternura-prod."},
		{Category: tool.MemoryCategoryFact, Content: "Kubernetes cluster logs are checked with kubectl."},
		{Category: tool.MemoryCategoryFact, Content: "The project uses Go."},
	}
	for _, item := range items {
		if _, err := store.Remember(context.Background(), item); err != nil {
			t.Fatalf("remember: %v", err)
		}
	}

	contextText, err := store.RuntimeContextForQuery("session-test", "帮我看 kubernetes deployment")
	if err != nil {
		t.Fatalf("runtime context: %v", err)
	}
	if !strings.Contains(contextText, "namespace ternura-prod") || !strings.Contains(contextText, "kubectl") {
		t.Fatalf("relevant long-term memories missing:\n%s", contextText)
	}
	if strings.Contains(contextText, "short replies") || strings.Contains(contextText, "The project uses Go") {
		t.Fatalf("irrelevant long-term memories should be left out:\n%s", contextText)
	}
}

func TestMemoryStoreLimitsShortTermTurnsInContext(t *testing.T) {
	store := newMemoryStore(t.TempDir())
	store.shortTermTurnLimit = 10
	store.shortTermContextLimit = 2

	for _, message := range []string{"first", "second", "third", "fourth"} {
		if err := store.AppendShortTermTurn("session-test", message, agent.AgentRunResult{Content: "answer " + message}); err != nil {
			t.Fatalf("append short-term turn: %v", err)
		}
	}

	contextText, err := store.RuntimeContextForQuery("session-test", "continue")
	if err != nil {
		t.Fatalf("runtime context: %v", err)
	}
	if strings.Contains(contextText, "first") || strings.Contains(contextText, "second") {
		t.Fatalf("old short-term turns should be left out:\n%s", contextText)
	}
	if !strings.Contains(contextText, "third") || !strings.Contains(contextText, "fourth") {
		t.Fatalf("recent short-term turns missing:\n%s", contextText)
	}
}

func TestMemoryStoreActiveRecallUsesRecentTurnsForSearchQuery(t *testing.T) {
	store := newMemoryStore(t.TempDir())
	if _, err := store.Remember(context.Background(), tool.MemoryItem{
		Category: tool.MemoryCategoryProject,
		Content:  "Kubernetes deployment uses namespace ternura-prod.",
	}); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if err := store.AppendShortTermTurn("session-test", "帮我看 kubernetes deployment", agent.AgentRunResult{Content: "我会检查部署状态"}); err != nil {
		t.Fatalf("append short-term turn: %v", err)
	}

	recall, err := store.ActiveRecallForQuery("session-test", "现在呢")
	if err != nil {
		t.Fatalf("active recall: %v", err)
	}

	if recall.Status != "ok" {
		t.Fatalf("recall status = %q, want ok", recall.Status)
	}
	if !strings.Contains(recall.SearchQuery, "kubernetes deployment") {
		t.Fatalf("search query should include recent user turn: %q", recall.SearchQuery)
	}
	if !strings.Contains(recall.Summary, "namespace ternura-prod") {
		t.Fatalf("recall summary missing long-term memory:\n%s", recall.Summary)
	}
	if !strings.Contains(recall.Summary, "Relevant recent session context") {
		t.Fatalf("recall summary missing recent context:\n%s", recall.Summary)
	}
	if block := recall.ContextBlock(); !strings.Contains(block, "Untrusted context") || !strings.Contains(block, "Query mode: recent") {
		t.Fatalf("context block missing active-memory metadata:\n%s", block)
	}
}

func TestMemoryStoreActiveRecallReturnsEmptyForUnrelatedQuery(t *testing.T) {
	store := newMemoryStore(t.TempDir())
	if _, err := store.Remember(context.Background(), tool.MemoryItem{
		Category: tool.MemoryCategoryPreference,
		Content:  "User likes short replies.",
	}); err != nil {
		t.Fatalf("remember: %v", err)
	}

	recall, err := store.ActiveRecallForQuery("session-test", "天气怎么样")
	if err != nil {
		t.Fatalf("active recall: %v", err)
	}

	if recall.Status != "no_relevant_memory" {
		t.Fatalf("recall status = %q, want no_relevant_memory", recall.Status)
	}
	if recall.ContextBlock() != "" {
		t.Fatalf("empty recall should not inject context:\n%s", recall.ContextBlock())
	}
}

func TestMemoryStoreActiveRecallDoesNotUseRecentTurnsForNewTopic(t *testing.T) {
	store := newMemoryStore(t.TempDir())
	if _, err := store.Remember(context.Background(), tool.MemoryItem{
		Category: tool.MemoryCategoryProject,
		Content:  "Kubernetes deployment uses namespace ternura-prod.",
	}); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if err := store.AppendShortTermTurn("session-test", "帮我看 kubernetes deployment", agent.AgentRunResult{Content: "我会检查部署状态"}); err != nil {
		t.Fatalf("append short-term turn: %v", err)
	}

	recall, err := store.ActiveRecallForQuery("session-test", "天气怎么样")
	if err != nil {
		t.Fatalf("active recall: %v", err)
	}

	if recall.Status != "no_relevant_memory" {
		t.Fatalf("recall status = %q, want no_relevant_memory; summary:\n%s", recall.Status, recall.Summary)
	}
	if strings.Contains(recall.SearchQuery, "kubernetes deployment") {
		t.Fatalf("new-topic search query should not include previous user turn: %q", recall.SearchQuery)
	}
}

func TestMemoryStoreActiveRecallSkipsLowSignalQuery(t *testing.T) {
	store := newMemoryStore(t.TempDir())
	if _, err := store.Remember(context.Background(), tool.MemoryItem{
		Category: tool.MemoryCategoryPreference,
		Content:  "User likes short replies.",
	}); err != nil {
		t.Fatalf("remember: %v", err)
	}

	recall, err := store.ActiveRecallForQuery("session-test", "你好")
	if err != nil {
		t.Fatalf("active recall: %v", err)
	}

	if recall.Status != "skipped" || !recall.Skipped {
		t.Fatalf("recall = %+v, want skipped", recall)
	}
	if recall.ContextBlock() != "" {
		t.Fatalf("skipped recall should not inject context:\n%s", recall.ContextBlock())
	}
}

func TestMemoryStoreActiveRecallUsesTightLimits(t *testing.T) {
	store := newMemoryStore(t.TempDir())
	for idx := 1; idx <= 6; idx++ {
		if _, err := store.Remember(context.Background(), tool.MemoryItem{
			Category: tool.MemoryCategoryProject,
			Content:  fmt.Sprintf("Redis cache memory fact %d.", idx),
		}); err != nil {
			t.Fatalf("remember %d: %v", idx, err)
		}
	}
	for idx := 1; idx <= 4; idx++ {
		if err := store.AppendShortTermTurn("session-test", fmt.Sprintf("redis cache turn %d", idx), agent.AgentRunResult{Content: "ok"}); err != nil {
			t.Fatalf("append short-term turn %d: %v", idx, err)
		}
	}

	recall, err := store.ActiveRecallForQuery("session-test", "现在呢")
	if err != nil {
		t.Fatalf("active recall: %v", err)
	}

	maxSummaryWithTruncationSuffix := defaultActiveMemoryMaxSummaryRunes + len([]rune("..."))
	if len([]rune(recall.Summary)) > maxSummaryWithTruncationSuffix {
		t.Fatalf("summary length = %d, want <= %d:\n%s", len([]rune(recall.Summary)), maxSummaryWithTruncationSuffix, recall.Summary)
	}
	if got := strings.Count(recall.Summary, "Redis cache memory fact"); got < 3 {
		t.Fatalf("long-term recall count = %d, want at least 3 under tight budget:\n%s", got, recall.Summary)
	}
	if !strings.Contains(recall.Summary, "...") {
		t.Fatalf("summary should show truncation under tight budget:\n%s", recall.Summary)
	}
}

func TestMemoryHookInjectsActiveMemoryBlock(t *testing.T) {
	store := newMemoryStore(t.TempDir())
	if _, err := store.Remember(context.Background(), tool.MemoryItem{
		Category: tool.MemoryCategoryProject,
		Content:  "Kubernetes deployment uses namespace ternura-prod.",
	}); err != nil {
		t.Fatalf("remember: %v", err)
	}
	if err := store.AppendShortTermTurn("session-test", "帮我看 kubernetes deployment", agent.AgentRunResult{Content: "我会检查部署状态"}); err != nil {
		t.Fatalf("append short-term turn: %v", err)
	}
	hook := newMemoryHook(store, func() string { return "session-test" })
	run := agent.NewRunContext("现在呢", agent.RunModeSync)

	if err := hook.BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	rendered := run.RuntimeContextText()
	if !strings.Contains(rendered, "## Active Memory") || !strings.Contains(rendered, "namespace ternura-prod") {
		t.Fatalf("active memory was not injected:\n%s", rendered)
	}
}

func TestMemoryHookUsesAIKeywordExtractorForRecall(t *testing.T) {
	store := newMemoryStore(t.TempDir())
	if _, err := store.Remember(context.Background(), tool.MemoryItem{
		Category: tool.MemoryCategoryProject,
		Content:  "Redis cache TTL is 10 minutes.",
	}); err != nil {
		t.Fatalf("remember: %v", err)
	}
	extractor := &fakeActiveMemoryKeywordExtractor{
		result: activeMemoryKeywordResult{
			ShouldRecall: true,
			QueryMode:    "recent",
			Keywords:     []string{"redis cache", "ttl"},
			SearchQuery:  "redis cache ttl",
		},
	}
	hook := newMemoryHook(store, func() string { return "session-test" }, withActiveMemoryKeywordExtractor(extractor))
	run := agent.NewRunContext("这个缓存呢", agent.RunModeSync)

	if err := hook.BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}
	if err := hook.BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call again: %v", err)
	}

	rendered := run.RuntimeContextText()
	if !strings.Contains(rendered, "Redis cache TTL") ||
		!strings.Contains(rendered, "Query mode: ai_recent") ||
		!strings.Contains(rendered, "Keywords: redis cache, ttl") {
		t.Fatalf("active memory did not use AI keyword recall:\n%s", rendered)
	}
	if extractor.calls != 1 {
		t.Fatalf("keyword extractor calls = %d, want 1", extractor.calls)
	}
}

func TestMemoryHookSummarizesRecallBeforeInjection(t *testing.T) {
	store := newMemoryStore(t.TempDir())
	if _, err := store.Remember(context.Background(), tool.MemoryItem{
		Category: tool.MemoryCategoryProfile,
		Content:  "用户是一名程序员",
	}); err != nil {
		t.Fatalf("remember profile: %v", err)
	}
	if _, err := store.Remember(context.Background(), tool.MemoryItem{
		Category: tool.MemoryCategoryProject,
		Content:  "SkillHub command is not available on PATH.",
	}); err != nil {
		t.Fatalf("remember project: %v", err)
	}
	extractor := &fakeActiveMemoryKeywordExtractor{
		result: activeMemoryKeywordResult{
			ShouldRecall: true,
			QueryMode:    "recent",
			Keywords:     []string{"skillhub", "verification"},
			SearchQuery:  "skillhub verification",
		},
	}
	summarizer := &fakeActiveMemorySummarizer{
		result: activeMemorySummaryResult{Summary: "SkillHub 当前不在 PATH 中；需要用真实 bash 结果确认。"},
	}
	hook := newMemoryHook(
		store,
		func() string { return "session-test" },
		withActiveMemoryKeywordExtractor(extractor),
		withActiveMemorySummarizer(summarizer),
	)
	run := agent.NewRunContext("你帮我看看现在 skill Hub 安装成功了吗", agent.RunModeSync)

	if err := hook.BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	rendered := run.RuntimeContextText()
	if !strings.Contains(rendered, "SkillHub 当前不在 PATH") {
		t.Fatalf("summarized memory missing:\n%s", rendered)
	}
	if strings.Contains(rendered, "用户是一名程序员") {
		t.Fatalf("raw noisy memory should not be injected:\n%s", rendered)
	}
	if summarizer.calls != 1 {
		t.Fatalf("summarizer calls = %d, want 1", summarizer.calls)
	}
	if !strings.Contains(summarizer.lastInput.RecallCandidate, "SkillHub command is not available") {
		t.Fatalf("summarizer candidate missing raw recall: %+v", summarizer.lastInput)
	}
}

func TestMemoryHookAddsActiveMemoryTrace(t *testing.T) {
	store := newMemoryStore(t.TempDir())
	if _, err := store.Remember(context.Background(), tool.MemoryItem{
		Category: tool.MemoryCategoryProject,
		Content:  "Redis cache TTL is 10 minutes.",
	}); err != nil {
		t.Fatalf("remember: %v", err)
	}
	extractor := &fakeActiveMemoryKeywordExtractor{
		result: activeMemoryKeywordResult{
			ShouldRecall: true,
			QueryMode:    "recent",
			Keywords:     []string{"redis", "ttl"},
			SearchQuery:  "redis ttl",
		},
	}
	hook := newMemoryHook(store, func() string { return "session-test" }, withActiveMemoryKeywordExtractor(extractor))
	run := agent.NewRunContext("这个缓存呢", agent.RunModeSync)
	result := agent.AgentRunResult{Content: "缓存是 10 分钟。"}

	if err := hook.BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}
	if err := hook.FinalizeRun(context.Background(), run, &result); err != nil {
		t.Fatalf("finalize run: %v", err)
	}

	if len(result.Trace) != 1 {
		t.Fatalf("trace length = %d, want 1: %+v", len(result.Trace), result.Trace)
	}
	item := result.Trace[0]
	if item.Type != "memory" || item.Title != "上下文记忆搜索" {
		t.Fatalf("trace item = %+v, want memory trace", item)
	}
	for _, want := range []string{"ai_recent", "redis", "ttl", "redis ttl", "Redis cache TTL"} {
		if !strings.Contains(item.Content, want) {
			t.Fatalf("memory trace missing %q:\n%s", want, item.Content)
		}
	}
}

func TestMemoryHookSkipsTraceWhenRecallIsNotUseful(t *testing.T) {
	store := newMemoryStore(t.TempDir())
	extractor := &fakeActiveMemoryKeywordExtractor{
		result: activeMemoryKeywordResult{
			ShouldRecall: false,
			QueryMode:    "latest",
		},
	}
	hook := newMemoryHook(store, func() string { return "session-test" }, withActiveMemoryKeywordExtractor(extractor))
	run := agent.NewRunContext("你好", agent.RunModeSync)
	result := agent.AgentRunResult{Content: "你好！"}

	if err := hook.BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}
	if err := hook.FinalizeRun(context.Background(), run, &result); err != nil {
		t.Fatalf("finalize run: %v", err)
	}

	if len(result.Trace) != 0 {
		t.Fatalf("trace length = %d, want 0: %+v", len(result.Trace), result.Trace)
	}
	if extractor.calls != 0 {
		t.Fatalf("keyword extractor should be skipped for low-signal query, calls = %d", extractor.calls)
	}
}

func TestMemoryStoreCapturesToolMemoryAndRecallsByQuery(t *testing.T) {
	root := t.TempDir()
	store := newMemoryStore(root)
	result := agent.ToolResult{
		Call: schema.ToolCall{
			ID: "call-read",
			Function: schema.FunctionCall{
				Name:      string(tool.AgentToolRead),
				Arguments: `{"path":"agent/context_builder.go"}`,
			},
		},
		Content: "context builder source with tool memory details",
	}

	record, ok, err := store.CaptureToolMemory(context.Background(), "session-test", result)
	if err != nil {
		t.Fatalf("capture tool memory: %v", err)
	}
	if !ok {
		t.Fatal("tool memory should be captured")
	}
	if err := store.AppendToolMemories("session-test", []toolMemoryRecord{record}); err != nil {
		t.Fatalf("append tool memory: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(record.RawRef)))
	if err != nil {
		t.Fatalf("read raw ref: %v", err)
	}
	if string(raw) != result.Content {
		t.Fatalf("raw artifact = %q, want %q", raw, result.Content)
	}

	contextText, err := store.RuntimeContextForQuery("session-test", "context_builder.go 的工具结果")
	if err != nil {
		t.Fatalf("runtime context: %v", err)
	}
	if !strings.Contains(contextText, "Relevant tool memory:") ||
		!strings.Contains(contextText, record.ID) ||
		!strings.Contains(contextText, record.RawRef) ||
		!strings.Contains(contextText, "context_builder.go") {
		t.Fatalf("context missing tool memory:\n%s", contextText)
	}

	unrelated, err := store.RuntimeContextForQuery("session-test", "你好")
	if err != nil {
		t.Fatalf("runtime context unrelated: %v", err)
	}
	if strings.Contains(unrelated, "Relevant tool memory:") {
		t.Fatalf("unrelated query should not recall tool memory:\n%s", unrelated)
	}
}

func TestMemoryStoreResetSessionClearsShortTermAndToolArtifacts(t *testing.T) {
	root := t.TempDir()
	store := newMemoryStore(root)
	sessionID := "session-test"
	result := agent.ToolResult{
		Call: schema.ToolCall{
			ID: "call-read",
			Function: schema.FunctionCall{
				Name:      string(tool.AgentToolRead),
				Arguments: `{"path":"README.md"}`,
			},
		},
		Content: "remembered tool output",
	}
	record, ok, err := store.CaptureToolMemory(context.Background(), sessionID, result)
	if err != nil {
		t.Fatalf("capture tool memory: %v", err)
	}
	if !ok {
		t.Fatal("tool memory should be captured")
	}
	if err := store.AppendToolMemories(sessionID, []toolMemoryRecord{record}); err != nil {
		t.Fatalf("append tool memory: %v", err)
	}
	if err := store.AppendShortTermTurn(sessionID, "hello", agent.AgentRunResult{Content: "hi"}); err != nil {
		t.Fatalf("append short-term turn: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(record.RawRef))); err != nil {
		t.Fatalf("tool artifact missing before reset: %v", err)
	}

	if err := store.ResetSession(sessionID); err != nil {
		t.Fatalf("reset memory session: %v", err)
	}
	if _, err := os.Stat(store.shortTermPath(sessionID)); !os.IsNotExist(err) {
		t.Fatalf("short-term memory should be removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(record.RawRef))); !os.IsNotExist(err) {
		t.Fatalf("tool artifact should be removed, err=%v", err)
	}
}

func TestToolMemoryHookDefersSummaryUntilRunFinishes(t *testing.T) {
	store := newMemoryStore(t.TempDir())
	hook := newToolMemoryHook(store, func() string { return "session-test" })
	run := agent.NewRunContext("read context builder", agent.RunModeSync)
	result := agent.ToolResult{
		Call: schema.ToolCall{
			ID: "call-read",
			Function: schema.FunctionCall{
				Name:      string(tool.AgentToolRead),
				Arguments: `{"path":"agent/context_builder.go"}`,
			},
		},
		Content: "context builder output",
	}

	if err := hook.AfterToolCall(context.Background(), run, &result); err != nil {
		t.Fatalf("after tool call: %v", err)
	}
	before, err := store.RuntimeContextForQuery("session-test", "context_builder.go")
	if err != nil {
		t.Fatalf("runtime context before run finish: %v", err)
	}
	if strings.Contains(before, "Relevant tool memory:") {
		t.Fatalf("tool memory should not be visible before run finishes:\n%s", before)
	}

	if err := hook.AfterRun(context.Background(), run, agent.AgentRunResult{Content: "done"}, nil); err != nil {
		t.Fatalf("after run: %v", err)
	}
	after, err := store.RuntimeContextForQuery("session-test", "context_builder.go")
	if err != nil {
		t.Fatalf("runtime context after run finish: %v", err)
	}
	if !strings.Contains(after, "Relevant tool memory:") {
		t.Fatalf("tool memory should be visible after run finishes:\n%s", after)
	}
}

type fakeActiveMemoryKeywordExtractor struct {
	result activeMemoryKeywordResult
	err    error
	calls  int
}

func (f *fakeActiveMemoryKeywordExtractor) ExtractActiveMemoryKeywords(ctx context.Context, input activeMemoryKeywordInput) (activeMemoryKeywordResult, error) {
	f.calls++
	return f.result, f.err
}

type fakeActiveMemorySummarizer struct {
	result    activeMemorySummaryResult
	err       error
	calls     int
	lastInput activeMemorySummaryInput
}

func (f *fakeActiveMemorySummarizer) SummarizeActiveMemory(ctx context.Context, input activeMemorySummaryInput) (activeMemorySummaryResult, error) {
	f.calls++
	f.lastInput = input
	return f.result, f.err
}
