package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestContextBuilderBuildClonesMessagesWithoutRuntimeContext(t *testing.T) {
	input := []*schema.Message{
		schema.SystemMessage("system"),
		schema.UserMessage("hello"),
	}
	builder := NewContextBuilder("system")

	messages, err := builder.Build(context.Background(), nil, input)

	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if len(messages) != len(input) {
		t.Fatalf("messages = %d, want %d", len(messages), len(input))
	}
	if &messages[0] == &input[0] {
		t.Fatalf("message slice was not cloned")
	}
	messages[0] = schema.SystemMessage("changed")
	if input[0].Content != "system" {
		t.Fatalf("input was mutated: %+v", input[0])
	}
}

func TestContextBuilderBuildMergesRuntimeContextIntoSystemMessage(t *testing.T) {
	input := []*schema.Message{
		schema.SystemMessage("system"),
		schema.UserMessage("hello"),
	}
	runCtx := NewRunContext("hello", RunModeSync)
	runCtx.SetContextBlock("memory", "Memory", "User prefers concise replies.")
	builder := NewContextBuilder("system")

	messages, err := builder.Build(context.Background(), runCtx, input)

	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if len(messages) != len(input) {
		t.Fatalf("messages = %d, want %d", len(messages), len(input))
	}
	if messages[0].Role != schema.System {
		t.Fatalf("first message role = %s, want system", messages[0].Role)
	}
	if !strings.Contains(messages[0].Content, "## Memory") {
		t.Fatalf("system message missing runtime context:\n%s", messages[0].Content)
	}
	if strings.Count(messages[0].Content, "system") != 1 {
		t.Fatalf("system prompt should appear once:\n%s", messages[0].Content)
	}
}

func TestContextBuilderBuildPreservesHistoricalToolExchangeBeforeLatestUser(t *testing.T) {
	oldToolCall := schema.AssistantMessage("", []schema.ToolCall{{
		ID: "old-call",
		Function: schema.FunctionCall{
			Name:      "read",
			Arguments: `{"path":"old.txt"}`,
		},
	}})
	currentToolCall := schema.AssistantMessage("", []schema.ToolCall{{
		ID: "current-call",
		Function: schema.FunctionCall{
			Name:      "read",
			Arguments: `{"path":"current.txt"}`,
		},
	}})
	input := []*schema.Message{
		schema.SystemMessage("system"),
		schema.UserMessage("old question"),
		oldToolCall,
		schema.ToolMessage("old huge output", "old-call", schema.WithToolName("read")),
		schema.AssistantMessage("old final answer", nil),
		schema.UserMessage("current question"),
		currentToolCall,
		schema.ToolMessage("current output", "current-call", schema.WithToolName("read")),
	}
	builder := NewContextBuilder("system")

	messages, err := builder.Build(context.Background(), nil, input)

	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if !hasAssistantToolCall(messages, "old-call") {
		t.Fatalf("historical assistant tool call should be preserved: %+v", messages)
	}
	if !containsToolMessage(messages, "old-call", "old huge output") {
		t.Fatalf("historical tool result should be preserved: %+v", messages)
	}
	if !containsAssistantContent(messages, "old final answer") {
		t.Fatalf("historical final assistant answer should be preserved: %+v", messages)
	}
	if !hasAssistantToolCall(messages, "current-call") {
		t.Fatalf("current run assistant tool call should be kept: %+v", messages)
	}
	if !containsToolMessage(messages, "current-call", "current output") {
		t.Fatalf("current run tool result should be kept: %+v", messages)
	}
}

func TestContextBuilderBuildPreservesHistoricalAssistantMessages(t *testing.T) {
	input := []*schema.Message{
		schema.SystemMessage("system"),
		schema.UserMessage("阅读并分析一下当前项目结构"),
		schema.AssistantMessage("已完成项目结构分析。", nil),
		schema.UserMessage("我想继续睡"),
	}
	builder := NewContextBuilder("system")

	messages, err := builder.Build(context.Background(), nil, input)

	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if !containsUserContent(messages, "阅读并分析一下当前项目结构") {
		t.Fatalf("historical user message should be preserved with its answer: %+v", messages)
	}
	if !containsAssistantContent(messages, "已完成项目结构分析。") {
		t.Fatalf("historical assistant answer should be preserved to avoid orphan user history: %+v", messages)
	}
	if !containsUserContent(messages, "我想继续睡") {
		t.Fatalf("latest user message should be preserved: %+v", messages)
	}
}

func TestContextBuilderBuildBudgetsRuntimeContextBlocks(t *testing.T) {
	input := []*schema.Message{
		schema.SystemMessage("system"),
		schema.UserMessage("hello"),
	}
	runCtx := NewRunContext("hello", RunModeSync)
	runCtx.SetContextBlockWithPriority("memory", "Memory", strings.Repeat("M", 140), RuntimeContextPriorityNormal, 90)
	runCtx.SetContextBlockWithPriority("policy", "Tool Policy", "must call cron", RuntimeContextPriorityCritical, 0)
	builder := NewContextBuilder("system")
	builder.runtimeContextBudgetRunes = 140

	messages, err := builder.Build(context.Background(), runCtx, input)

	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	system := messages[0].Content
	if !strings.Contains(system, "## Tool Policy") || !strings.Contains(system, "must call cron") {
		t.Fatalf("critical context should be preserved:\n%s", system)
	}
	if !strings.Contains(system, "## Memory") {
		t.Fatalf("memory context should still be included when it fits:\n%s", system)
	}
	if strings.Count(system, "M") >= 140 || !strings.Contains(system, "truncated") {
		t.Fatalf("memory context was not budgeted:\n%s", system)
	}
	if strings.Index(system, "## Tool Policy") > strings.Index(system, "## Memory") {
		t.Fatalf("higher-priority block should be rendered first:\n%s", system)
	}
}

func TestContextBuilderBuildPrunesOldConversationByBudget(t *testing.T) {
	input := []*schema.Message{
		schema.SystemMessage("system"),
		schema.UserMessage("old user " + strings.Repeat("A", 80)),
		schema.AssistantMessage("old assistant "+strings.Repeat("B", 80), nil),
		schema.UserMessage("current question"),
	}
	builder := NewContextBuilder("system")
	builder.maxInputRunes = 120
	builder.conversationBudgetRunes = 20

	messages, err := builder.Build(context.Background(), nil, input)

	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if !containsUserContent(messages, "current question") {
		t.Fatalf("current user message must be preserved: %+v", messages)
	}
	if containsUserContent(messages, "old user") || containsAssistantContentPrefix(messages, "old assistant") {
		t.Fatalf("old conversation should be pruned by budget: %+v", messages)
	}
}

func TestContextBuilderMicroCompactsOlderCurrentToolResults(t *testing.T) {
	input := []*schema.Message{
		schema.SystemMessage("system"),
		schema.UserMessage("current question"),
		assistantToolCall("call-1", "read"),
		schema.ToolMessage("old output "+strings.Repeat("A", 200), "call-1", schema.WithToolName("read")),
		assistantToolCall("call-2", "read"),
		schema.ToolMessage("recent output 2", "call-2", schema.WithToolName("read")),
		assistantToolCall("call-3", "read"),
		schema.ToolMessage("recent output 3", "call-3", schema.WithToolName("read")),
		assistantToolCall("call-4", "read"),
		schema.ToolMessage("recent output 4", "call-4", schema.WithToolName("read")),
	}
	builder := NewContextBuilder("system")
	builder.toolResultBudgetRunes = 0
	builder.toolResultPreviewRunes = 80
	builder.keepRecentToolResults = 3

	messages, err := builder.Build(context.Background(), nil, input)

	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if containsToolMessage(messages, "call-1", "old output "+strings.Repeat("A", 200)) {
		t.Fatalf("older tool result should be compacted: %+v", messages)
	}
	if !containsToolContentSubstring(messages, "call-1", "[Earlier tool result compacted. Re-run if needed.]") {
		t.Fatalf("older tool result should keep compact placeholder: %+v", messages)
	}
	for _, want := range []string{"recent output 2", "recent output 3", "recent output 4"} {
		if !containsToolContentSubstring(messages, "", want) {
			t.Fatalf("recent tool result %q should be preserved: %+v", want, messages)
		}
	}
}

func TestContextBuilderToolResultBudgetCompactsLargeToolOutputs(t *testing.T) {
	tempDir := t.TempDir()
	input := []*schema.Message{
		schema.SystemMessage("system"),
		schema.UserMessage("current question"),
		schema.AssistantMessage("", []schema.ToolCall{
			{
				ID: "call-large-1",
				Function: schema.FunctionCall{
					Name:      "bash",
					Arguments: `{}`,
				},
			},
			{
				ID: "call-large-2",
				Function: schema.FunctionCall{
					Name:      "bash",
					Arguments: `{}`,
				},
			},
		}),
		schema.ToolMessage("large one "+strings.Repeat("A", 200), "call-large-1", schema.WithToolName("bash")),
		schema.ToolMessage("large two "+strings.Repeat("B", 200), "call-large-2", schema.WithToolName("bash")),
	}
	builder := NewContextBuilder("system")
	builder.toolResultBudgetRunes = 180
	builder.toolResultPreviewRunes = 60
	builder.toolResultPersistThreshold = 60
	builder.toolResultsDir = tempDir

	messages, err := builder.Build(context.Background(), nil, input)

	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if containsToolMessage(messages, "call-large-1", "large one "+strings.Repeat("A", 200)) {
		t.Fatalf("large tool output should be persisted out of model context: %+v", messages)
	}
	if !containsToolContentSubstring(messages, "call-large-1", "<persisted-output>") {
		t.Fatalf("budget compaction marker missing: %+v", messages)
	}
	if !containsToolContentSubstring(messages, "call-large-1", "Preview:\nlarge one") {
		t.Fatalf("persisted output preview missing: %+v", messages)
	}
	for _, id := range []string{"call-large-1", "call-large-2"} {
		if _, err := os.Stat(filepath.Join(tempDir, id+".txt")); err != nil {
			t.Fatalf("persisted output file for %s missing: %v", id, err)
		}
	}
}

func TestContextBuilderSnipCompactPreservesToolUsePairAtTailBoundary(t *testing.T) {
	input := []*schema.Message{
		schema.SystemMessage("system"),
		schema.UserMessage("head"),
		schema.UserMessage("middle 1"),
		schema.UserMessage("middle 2"),
		assistantToolCall("tail-call", "read"),
		schema.ToolMessage("tail output", "tail-call", schema.WithToolName("read")),
		schema.UserMessage("latest question"),
	}
	builder := NewContextBuilder("system")
	builder.maxConversationMessages = 5
	builder.conversationBudgetRunes = 10000
	builder.toolResultBudgetRunes = 0

	messages, err := builder.Build(context.Background(), nil, input)

	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if containsToolMessage(messages, "tail-call", "tail output") && !hasAssistantToolCall(messages, "tail-call") {
		t.Fatalf("snip compaction left an orphan tool result: %+v", messages)
	}
	if !containsUserContent(messages, "latest question") {
		t.Fatalf("latest user message should be preserved: %+v", messages)
	}
}

func TestContextBuilderBudgetKeepsLatestUserAndAvoidsOrphanToolResult(t *testing.T) {
	input := []*schema.Message{
		schema.SystemMessage("system"),
		schema.UserMessage("current question"),
		assistantToolCall("call-big", "read"),
		schema.ToolMessage(strings.Repeat("T", 500), "call-big", schema.WithToolName("read")),
	}
	builder := NewContextBuilder("system")
	builder.maxInputRunes = 120
	builder.conversationBudgetRunes = 40
	builder.toolResultBudgetRunes = 0

	messages, err := builder.Build(context.Background(), nil, input)

	if err != nil {
		t.Fatalf("build context: %v", err)
	}
	if !containsUserContent(messages, "current question") {
		t.Fatalf("latest user message should be preserved: %+v", messages)
	}
	if containsToolContentSubstring(messages, "call-big", "") && !hasAssistantToolCall(messages, "call-big") {
		t.Fatalf("budget pruning left an orphan tool result: %+v", messages)
	}
}

func assistantToolCall(id string, name string) *schema.Message {
	return schema.AssistantMessage("", []schema.ToolCall{{
		ID: id,
		Function: schema.FunctionCall{
			Name:      name,
			Arguments: `{}`,
		},
	}})
}

func hasAssistantToolCall(messages []*schema.Message, callID string) bool {
	for _, message := range messages {
		if message == nil || message.Role != schema.Assistant {
			continue
		}
		for _, call := range message.ToolCalls {
			if call.ID == callID {
				return true
			}
		}
	}
	return false
}

func containsToolContentSubstring(messages []*schema.Message, callID string, content string) bool {
	for _, message := range messages {
		if message == nil || message.Role != schema.Tool {
			continue
		}
		if callID != "" && message.ToolCallID != callID {
			continue
		}
		if strings.Contains(message.Content, content) {
			return true
		}
	}
	return false
}

func containsAssistantContent(messages []*schema.Message, content string) bool {
	for _, message := range messages {
		if message != nil && message.Role == schema.Assistant && message.Content == content {
			return true
		}
	}
	return false
}

func containsAssistantContentPrefix(messages []*schema.Message, content string) bool {
	for _, message := range messages {
		if message != nil && message.Role == schema.Assistant && strings.HasPrefix(message.Content, content) {
			return true
		}
	}
	return false
}

func containsAssistantContentSubstring(messages []*schema.Message, content string) bool {
	for _, message := range messages {
		if message != nil && message.Role == schema.Assistant && strings.Contains(message.Content, content) {
			return true
		}
	}
	return false
}

func containsUserContent(messages []*schema.Message, content string) bool {
	for _, message := range messages {
		if message != nil && message.Role == schema.User && strings.Contains(message.Content, content) {
			return true
		}
	}
	return false
}
