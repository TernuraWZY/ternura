package agent

import (
	"context"
	"fmt"
	"strings"

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
	compactSummaryThresholdRunes int
	compactSummaryInputRunes     int
	compactSummaryRunes          int
	compactSummaryTailRunes      int
}

const (
	defaultMaxInputRunes                = 60000
	defaultRuntimeContextBudgetRunes    = 12000
	defaultConversationBudgetRunes      = 45000
	defaultMaxConversationMessages      = 50
	defaultSnipHeadMessages             = 3
	defaultKeepRecentToolResults        = 3
	defaultToolResultBudgetRunes        = 24000
	defaultToolResultPreviewRunes       = 1200
	defaultCompactSummaryThresholdRunes = 52000
	defaultCompactSummaryInputRunes     = 90000
	defaultCompactSummaryRunes          = 6000
	defaultCompactSummaryTailRunes      = 12000
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
		compactSummaryThresholdRunes: defaultCompactSummaryThresholdRunes,
		compactSummaryInputRunes:     defaultCompactSummaryInputRunes,
		compactSummaryRunes:          defaultCompactSummaryRunes,
		compactSummaryTailRunes:      defaultCompactSummaryTailRunes,
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
	messages = pruneHistoricalToolExchange(messages)
	messages = sanitizeHistoricalAssistantMessages(messages)
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
	toolIndexes := toolMessageIndexes(messages)
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

	protectedFrom := len(toolIndexes) - b.keepRecentToolResults
	if protectedFrom < 0 {
		protectedFrom = 0
	}
	for pos, idx := range toolIndexes {
		if total <= b.toolResultBudgetRunes {
			break
		}
		if pos >= protectedFrom {
			continue
		}
		before := runeLen(messages[idx].Content)
		compactToolMessage(messages[idx], "older tool result compacted by total tool-result budget", b.toolResultPreviewRunes)
		total -= before - runeLen(messages[idx].Content)
	}
	for pos := len(toolIndexes) - 1; pos >= 0 && total > b.toolResultBudgetRunes; pos-- {
		idx := toolIndexes[pos]
		before := runeLen(messages[idx].Content)
		compactToolMessage(messages[idx], "tool result compacted by total tool-result budget", b.toolResultPreviewRunes)
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

func compactToolMessage(message *schema.Message, reason string, previewRunes int) {
	if message == nil || message.Role != schema.Tool || strings.HasPrefix(message.Content, "[Tool result compacted:") {
		return
	}
	originalRunes := runeLen(message.Content)
	preview := strings.TrimSpace(message.Content)
	if previewRunes > 0 && runeLen(preview) > previewRunes {
		preview = trimRunesWithNotice(preview, previewRunes, "tool result preview")
	}
	message.Content = fmt.Sprintf(
		"[Tool result compacted: %s. tool=%s call_id=%s original_chars=%d.]\n\nPreview:\n%s",
		strings.TrimSpace(reason),
		message.ToolName,
		message.ToolCallID,
		originalRunes,
		preview,
	)
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

func pruneHistoricalToolExchange(messages []*schema.Message) []*schema.Message {
	lastUserIndex := latestUserMessageIndex(messages)
	if lastUserIndex <= 0 {
		return messages
	}

	pruned := make([]*schema.Message, 0, len(messages))
	for idx, message := range messages {
		if idx < lastUserIndex && isToolExchangeMessage(message) {
			continue
		}
		pruned = append(pruned, message)
	}
	return pruned
}

func sanitizeHistoricalAssistantMessages(messages []*schema.Message) []*schema.Message {
	lastUserIndex := latestUserMessageIndex(messages)
	if lastUserIndex <= 0 {
		return messages
	}
	sanitized := make([]*schema.Message, 0, len(messages))
	for idx, message := range messages {
		if message == nil || idx >= lastUserIndex || message.Role != schema.Assistant {
			sanitized = append(sanitized, message)
			continue
		}
	}
	return sanitized
}

func latestUserMessageIndex(messages []*schema.Message) int {
	for idx := len(messages) - 1; idx >= 0; idx-- {
		if messages[idx] != nil && messages[idx].Role == schema.User {
			return idx
		}
	}
	return -1
}

func isToolExchangeMessage(message *schema.Message) bool {
	if message == nil {
		return false
	}
	return message.Role == schema.Tool || len(message.ToolCalls) > 0
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
