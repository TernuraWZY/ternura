package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

type ContextBuilder struct {
	systemPrompt                 string
	maxInputRunes                int
	runtimeContextBudgetRunes    int
	conversationBudgetRunes      int
	maxConversationMessages      int
	snipHeadMessages             int
	keepRecentToolResults        int
	toolResultBudgetRunes        int
	toolResultPreviewRunes       int
	toolResultPersistThreshold   int
	compactSummaryThresholdRunes int
	compactSummaryInputRunes     int
	compactSummaryRunes          int
	compactReactiveTailMessages  int
	toolResultsDir               string
	compactTranscriptDir         string
}

const (
	defaultMaxInputRunes                = 50000
	defaultRuntimeContextBudgetRunes    = 12000
	defaultConversationBudgetRunes      = 50000
	defaultMaxConversationMessages      = 50
	defaultSnipHeadMessages             = 3
	defaultKeepRecentToolResults        = 3
	defaultToolResultBudgetRunes        = 200000
	defaultToolResultPreviewRunes       = 2000
	defaultToolResultPersistThreshold   = 30000
	defaultCompactSummaryThresholdRunes = 50000
	defaultCompactSummaryInputRunes     = 80000
	defaultCompactSummaryRunes          = 6000
	defaultCompactReactiveTailMessages  = 5
)

func NewContextBuilder(systemPrompt string) *ContextBuilder {
	return &ContextBuilder{
		systemPrompt:                 systemPrompt,
		maxInputRunes:                defaultMaxInputRunes,
		runtimeContextBudgetRunes:    defaultRuntimeContextBudgetRunes,
		conversationBudgetRunes:      defaultConversationBudgetRunes,
		maxConversationMessages:      defaultMaxConversationMessages,
		snipHeadMessages:             defaultSnipHeadMessages,
		keepRecentToolResults:        defaultKeepRecentToolResults,
		toolResultBudgetRunes:        defaultToolResultBudgetRunes,
		toolResultPreviewRunes:       defaultToolResultPreviewRunes,
		toolResultPersistThreshold:   defaultToolResultPersistThreshold,
		compactSummaryThresholdRunes: defaultCompactSummaryThresholdRunes,
		compactSummaryInputRunes:     defaultCompactSummaryInputRunes,
		compactSummaryRunes:          defaultCompactSummaryRunes,
		compactReactiveTailMessages:  defaultCompactReactiveTailMessages,
		toolResultsDir:               filepath.Join(".task_outputs", "tool-results"),
		compactTranscriptDir:         ".transcripts",
	}
}

func (b *ContextBuilder) Build(ctx context.Context, runCtx *RunContext, input []*schema.Message) ([]*schema.Message, error) {
	messages, err := b.BuildPreCompact(ctx, runCtx, input)
	if err != nil {
		return nil, err
	}
	return b.FinalizeBudget(messages), nil
}

func (b *ContextBuilder) BuildPreCompact(_ context.Context, runCtx *RunContext, input []*schema.Message) ([]*schema.Message, error) {
	messages := cloneMessages(input)
	if b == nil {
		return messages, nil
	}
	messages = b.applyToolResultBudget(messages)
	messages = snipCompactMessages(messages, b.maxConversationMessages, b.snipHeadMessages)
	messages = microCompactToolResults(messages, b.keepRecentToolResults, b.toolResultPreviewRunes)

	runtimeContext := ""
	if runCtx != nil {
		runtimeContext = runCtx.RuntimeContextTextWithBudget(b.runtimeContextBudgetRunes)
	}

	built := make([]*schema.Message, 0, len(messages)+1)
	systemContent := strings.TrimSpace(b.systemPrompt)
	if runtimeContext != "" {
		systemContent = strings.TrimSpace(systemContent + "\n\n" + runtimeContext)
	}
	built = append(built, schema.SystemMessage(systemContent))
	if len(messages) == 0 {
		return built, nil
	}
	if messages[0] != nil && messages[0].Role == schema.System {
		built = append(built, messages[1:]...)
	} else {
		built = append(built, messages...)
	}
	return built, nil
}

func (b *ContextBuilder) FinalizeBudget(messages []*schema.Message) []*schema.Message {
	if b == nil {
		return messages
	}
	return b.pruneConversationToBudget(messages)
}

func (b *ContextBuilder) NeedsSummaryCompact(messages []*schema.Message) bool {
	if b == nil || b.compactSummaryThresholdRunes <= 0 {
		return false
	}
	return messagesRunes(messages) > b.compactSummaryThresholdRunes
}

func (b *ContextBuilder) pruneConversationToBudget(messages []*schema.Message) []*schema.Message {
	if len(messages) == 0 {
		return messages
	}
	maxInputRunes := b.maxInputRunes
	if maxInputRunes <= 0 || messagesRunes(messages) <= maxInputRunes {
		return messages
	}

	conversationBudget := b.conversationBudgetRunes
	if conversationBudget <= 0 || conversationBudget > maxInputRunes {
		conversationBudget = maxInputRunes
	}

	system := messages[0]
	rest := messages[1:]
	if len(rest) == 0 || messagesRunes(rest) <= conversationBudget {
		return messages
	}

	lastUserIndex := latestUserMessageIndex(rest)
	if lastUserIndex < 0 {
		return append([]*schema.Message{system}, keepTailMessageGroups(rest, conversationBudget)...)
	}

	required := cloneMessages(rest[lastUserIndex:])
	requiredRunes := messagesRunes(required)
	if requiredRunes > conversationBudget {
		return append([]*schema.Message{system}, compactRequiredMessagesToBudget(required, conversationBudget)...)
	}
	remaining := conversationBudget - requiredRunes
	keptPrefix := keepTailMessageGroups(rest[:lastUserIndex], remaining)
	pruned := make([]*schema.Message, 0, 1+len(keptPrefix)+len(required))
	pruned = append(pruned, system)
	pruned = append(pruned, keptPrefix...)
	pruned = append(pruned, required...)
	return pruned
}

func compactRequiredMessagesToBudget(messages []*schema.Message, budgetRunes int) []*schema.Message {
	if len(messages) == 0 || budgetRunes <= 0 {
		return nil
	}
	latestUser := truncateMessageContent(messages[0], budgetRunes)
	remaining := budgetRunes - messageRunes(latestUser)
	if remaining <= 0 || len(messages) == 1 {
		return []*schema.Message{latestUser}
	}
	tail := keepTailMessageGroups(messages[1:], remaining)
	compacted := make([]*schema.Message, 0, 1+len(tail))
	compacted = append(compacted, latestUser)
	compacted = append(compacted, tail...)
	return compacted
}

func keepTailMessageGroups(messages []*schema.Message, budgetRunes int) []*schema.Message {
	if budgetRunes <= 0 || len(messages) == 0 {
		return nil
	}
	groups := messageGroups(messages)
	keptGroups := make([][]*schema.Message, 0, len(groups))
	used := 0
	for idx := len(groups) - 1; idx >= 0; idx-- {
		group := groups[idx]
		groupRunes := messagesRunes(group)
		if used > 0 && used+groupRunes > budgetRunes {
			break
		}
		if used == 0 && groupRunes > budgetRunes {
			keptGroups = append(keptGroups, truncateMessageGroup(group, budgetRunes))
			break
		}
		keptGroups = append(keptGroups, group)
		used += groupRunes
	}
	kept := make([]*schema.Message, 0, len(messages))
	for idx := len(keptGroups) - 1; idx >= 0; idx-- {
		kept = append(kept, keptGroups[idx]...)
	}
	return kept
}

func messageGroups(messages []*schema.Message) [][]*schema.Message {
	groups := make([][]*schema.Message, 0, len(messages))
	for idx := 0; idx < len(messages); idx++ {
		message := messages[idx]
		group := []*schema.Message{message}
		if messageHasToolUse(message) {
			for idx+1 < len(messages) && messages[idx+1] != nil && messages[idx+1].Role == schema.Tool {
				idx++
				group = append(group, messages[idx])
			}
		}
		groups = append(groups, group)
	}
	return groups
}

func truncateMessageGroup(group []*schema.Message, budgetRunes int) []*schema.Message {
	if budgetRunes <= 0 || len(group) == 0 {
		return nil
	}
	truncated := make([]*schema.Message, 0, len(group))
	used := 0
	for _, message := range group {
		remaining := budgetRunes - used
		if remaining <= 0 {
			break
		}
		messageRunes := messageRunes(message)
		if used+messageRunes <= budgetRunes {
			truncated = append(truncated, message)
			used += messageRunes
			continue
		}
		truncated = append(truncated, truncateMessageContent(message, remaining))
		break
	}
	return truncated
}

func messagesRunes(messages []*schema.Message) int {
	total := 0
	for _, message := range messages {
		total += messageRunes(message)
	}
	return total
}

func messageRunes(message *schema.Message) int {
	if message == nil {
		return 0
	}
	total := runeLen(message.Content) + runeLen(string(message.Role)) + runeLen(message.ToolCallID) + runeLen(message.ToolName)
	for _, call := range message.ToolCalls {
		total += runeLen(call.ID)
		total += runeLen(call.Function.Name)
		total += runeLen(call.Function.Arguments)
	}
	return total
}

func truncateMessageContent(message *schema.Message, budgetRunes int) *schema.Message {
	if message == nil || budgetRunes <= 0 || runeLen(message.Content) <= budgetRunes {
		return message
	}
	cloned := *message
	cloned.Content = trimRunesWithNotice(message.Content, budgetRunes, "conversation message")
	return &cloned
}

func (b *ContextBuilder) applyToolResultBudget(messages []*schema.Message) []*schema.Message {
	toolIndexes := latestToolResultIndexes(messages)
	if len(toolIndexes) == 0 || b == nil || b.toolResultBudgetRunes <= 0 {
		return messages
	}
	total := 0
	for _, idx := range toolIndexes {
		total += runeLen(messages[idx].Content)
	}
	if total <= b.toolResultBudgetRunes {
		return messages
	}

	ranked := append([]int(nil), toolIndexes...)
	sortToolIndexesByContentSize(ranked, messages)
	for _, idx := range ranked {
		if total <= b.toolResultBudgetRunes {
			break
		}
		if runeLen(messages[idx].Content) <= b.toolResultPersistThreshold {
			continue
		}
		before := runeLen(messages[idx].Content)
		persistLargeToolOutput(messages[idx], b.toolResultsDir, b.toolResultPreviewRunes)
		total -= before - runeLen(messages[idx].Content)
	}
	return messages
}

func microCompactToolResults(messages []*schema.Message, keepRecent int, previewRunes int) []*schema.Message {
	toolIndexes := toolMessageIndexes(messages)
	if len(toolIndexes) <= keepRecent {
		return messages
	}
	compactUntil := len(toolIndexes) - keepRecent
	for _, idx := range toolIndexes[:compactUntil] {
		if runeLen(messages[idx].Content) <= previewRunes {
			continue
		}
		compactToolMessage(messages[idx], "earlier tool result compacted; re-run or inspect saved run artifacts if needed", previewRunes)
	}
	return messages
}

func toolMessageIndexes(messages []*schema.Message) []int {
	indexes := make([]int, 0)
	for idx, message := range messages {
		if message != nil && message.Role == schema.Tool {
			indexes = append(indexes, idx)
		}
	}
	return indexes
}

func latestToolResultIndexes(messages []*schema.Message) []int {
	if len(messages) == 0 {
		return nil
	}
	end := len(messages) - 1
	if messages[end] == nil || messages[end].Role != schema.Tool {
		return nil
	}
	start := end
	for start > 0 && messages[start-1] != nil && messages[start-1].Role == schema.Tool {
		start--
	}
	return indexesRange(start, end)
}

func indexesRange(start int, end int) []int {
	indexes := make([]int, 0, end-start+1)
	for idx := start; idx <= end; idx++ {
		indexes = append(indexes, idx)
	}
	return indexes
}

func sortToolIndexesByContentSize(indexes []int, messages []*schema.Message) {
	sort.SliceStable(indexes, func(i, j int) bool {
		return runeLen(messages[indexes[i]].Content) > runeLen(messages[indexes[j]].Content)
	})
}

func persistLargeToolOutput(message *schema.Message, dir string, previewRunes int) {
	if message == nil || message.Role != schema.Tool || strings.HasPrefix(message.Content, "<persisted-output>") {
		return
	}
	output := message.Content
	if strings.TrimSpace(dir) == "" {
		dir = filepath.Join(".task_outputs", "tool-results")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		compactToolMessage(message, "persist large output failed", previewRunes)
		return
	}
	id := strings.TrimSpace(message.ToolCallID)
	if id == "" {
		id = fmt.Sprintf("tool-%d", time.Now().UnixNano())
	}
	path := filepath.Join(dir, id+".txt")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if writeErr := os.WriteFile(path, []byte(output), 0o600); writeErr != nil {
			compactToolMessage(message, "persist large output failed", previewRunes)
			return
		}
	}
	preview := strings.TrimSpace(output)
	if previewRunes > 0 && runeLen(preview) > previewRunes {
		preview = string([]rune(preview)[:previewRunes])
	}
	message.Content = fmt.Sprintf("<persisted-output>\nFull output: %s\nPreview:\n%s\n</persisted-output>", path, preview)
}

func compactToolMessage(message *schema.Message, reason string, previewRunes int) {
	if message == nil || message.Role != schema.Tool || strings.HasPrefix(message.Content, "[Earlier tool result compacted.") {
		return
	}
	message.Content = "[Earlier tool result compacted. Re-run if needed.]"
}

func snipCompactMessages(messages []*schema.Message, maxMessages int, headMessages int) []*schema.Message {
	if maxMessages <= 0 || len(messages) <= maxMessages {
		return messages
	}
	if headMessages < 0 {
		headMessages = 0
	}
	if headMessages >= maxMessages {
		headMessages = maxMessages / 2
	}
	tailMessages := maxMessages - headMessages - 1
	if tailMessages <= 0 {
		return messages[len(messages)-maxMessages:]
	}
	headEnd := minInt(headMessages, len(messages))
	tailStart := len(messages) - tailMessages
	if headEnd > 0 && messageHasToolUse(messages[headEnd-1]) {
		for headEnd < len(messages) && messages[headEnd] != nil && messages[headEnd].Role == schema.Tool {
			headEnd++
		}
	}
	tailStart = adjustTailStartForToolPair(messages, tailStart)
	if headEnd >= tailStart {
		return messages
	}
	snipped := tailStart - headEnd
	placeholder := schema.UserMessage(fmt.Sprintf("[context compacted: %d middle messages omitted]", snipped))
	compacted := make([]*schema.Message, 0, len(messages)-snipped+1)
	compacted = append(compacted, messages[:headEnd]...)
	compacted = append(compacted, placeholder)
	compacted = append(compacted, messages[tailStart:]...)
	return compacted
}

func adjustTailStartForToolPair(messages []*schema.Message, tailStart int) int {
	if tailStart <= 0 || tailStart >= len(messages) || messages[tailStart] == nil || messages[tailStart].Role != schema.Tool {
		return tailStart
	}
	idx := tailStart
	for idx > 0 && messages[idx] != nil && messages[idx].Role == schema.Tool {
		idx--
	}
	if messageHasToolUse(messages[idx]) {
		return idx
	}
	return tailStart
}

func messageHasToolUse(message *schema.Message) bool {
	return message != nil && message.Role == schema.Assistant && len(message.ToolCalls) > 0
}

func latestUserMessageIndex(messages []*schema.Message) int {
	for idx := len(messages) - 1; idx >= 0; idx-- {
		if messages[idx] != nil && messages[idx].Role == schema.User {
			return idx
		}
	}
	return -1
}

func cloneMessages(messages []*schema.Message) []*schema.Message {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]*schema.Message, len(messages))
	for idx, message := range messages {
		if message == nil {
			continue
		}
		item := *message
		if len(message.ToolCalls) > 0 {
			item.ToolCalls = append([]schema.ToolCall(nil), message.ToolCalls...)
		}
		cloned[idx] = &item
	}
	return cloned
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}
