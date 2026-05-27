package agent

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/schema"
)

type ContextBuilder struct {
	systemPrompt              string
	maxInputRunes             int
	runtimeContextBudgetRunes int
	conversationBudgetRunes   int
}

const (
	defaultMaxInputRunes             = 60000
	defaultRuntimeContextBudgetRunes = 12000
	defaultConversationBudgetRunes   = 45000
)

func NewContextBuilder(systemPrompt string) *ContextBuilder {
	return &ContextBuilder{
		systemPrompt:              systemPrompt,
		maxInputRunes:             defaultMaxInputRunes,
		runtimeContextBudgetRunes: defaultRuntimeContextBudgetRunes,
		conversationBudgetRunes:   defaultConversationBudgetRunes,
	}
}

func (b *ContextBuilder) Build(_ context.Context, runCtx *RunContext, input []*schema.Message) ([]*schema.Message, error) {
	messages := cloneMessages(input)
	if b == nil {
		return messages, nil
	}
	messages = pruneHistoricalToolExchange(messages)

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
	return b.pruneConversationToBudget(built), nil
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
		return append([]*schema.Message{system}, keepTailMessages(rest, conversationBudget)...)
	}

	required := cloneMessages(rest[lastUserIndex:])
	requiredRunes := messagesRunes(required)
	remaining := conversationBudget - requiredRunes
	if remaining <= 0 {
		return append([]*schema.Message{system}, required...)
	}

	keptPrefix := keepTailMessages(rest[:lastUserIndex], remaining)
	pruned := make([]*schema.Message, 0, 1+len(keptPrefix)+len(required))
	pruned = append(pruned, system)
	pruned = append(pruned, keptPrefix...)
	pruned = append(pruned, required...)
	return pruned
}

func keepTailMessages(messages []*schema.Message, budgetRunes int) []*schema.Message {
	if budgetRunes <= 0 || len(messages) == 0 {
		return nil
	}
	kept := make([]*schema.Message, 0, len(messages))
	used := 0
	for idx := len(messages) - 1; idx >= 0; idx-- {
		messageRunes := messageRunes(messages[idx])
		if used > 0 && used+messageRunes > budgetRunes {
			break
		}
		if used == 0 && messageRunes > budgetRunes {
			kept = append(kept, truncateMessageContent(messages[idx], budgetRunes))
			break
		}
		kept = append(kept, messages[idx])
		used += messageRunes
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	return kept
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
	copy(cloned, messages)
	return cloned
}
