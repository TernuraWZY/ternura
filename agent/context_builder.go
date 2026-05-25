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
	if messages[0].Role == schema.System {
		built = append(built, messages[1:]...)
	} else {
		built = append(built, messages...)
	}
	return built, nil
}

func cloneMessages(messages []*schema.Message) []*schema.Message {
	if len(messages) == 0 {
		return nil
	}
	cloned := make([]*schema.Message, len(messages))
	copy(cloned, messages)
	return cloned
}
