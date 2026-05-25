package agent

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/schema"
)

type ContextBuilder struct {
	systemPrompt string
}

func NewContextBuilder(systemPrompt string) *ContextBuilder {
	return &ContextBuilder{
		systemPrompt: systemPrompt,
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
		runtimeContext = runCtx.RuntimeContextText()
	}
	if runtimeContext == "" {
		return messages, nil
	}

	built := make([]*schema.Message, 0, len(messages)+1)
	systemContent := strings.TrimSpace(b.systemPrompt + "\n\n" + runtimeContext)
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
