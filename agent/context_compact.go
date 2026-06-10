package agent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/cloudwego/eino/schema"
)

const compactSummaryPrefix = "[Context compacted summary]"

func (r *einoAgentRun) compactHistoryIfNeeded(ctx context.Context, builder *ContextBuilder, messages []*schema.Message) []*schema.Message {
	if r == nil || builder == nil || !builder.NeedsSummaryCompact(messages) {
		return messages
	}
	if containsCompactSummaryMessage(messages) {
		return messages
	}

	beforeRunes := messagesRunes(messages)
	summary, err := r.summarizeContext(ctx, builder, messages)
	if err != nil {
		log.Printf("context summary compact failed: %v", err)
		r.recordContextCompactTrace(beforeRunes, beforeRunes, "", err)
		return messages
	}
	if strings.TrimSpace(summary) == "" {
		err := fmt.Errorf("empty compact summary")
		log.Printf("context summary compact failed: %v", err)
		r.recordContextCompactTrace(beforeRunes, beforeRunes, "", err)
		return messages
	}

	compacted := compactedSummaryMessages(builder, messages, summary)
	r.recordContextCompactTrace(beforeRunes, messagesRunes(compacted), summary, nil)
	return compacted
}

func (r *einoAgentRun) summarizeContext(ctx context.Context, builder *ContextBuilder, messages []*schema.Message) (string, error) {
	if r == nil || r.agent == nil || r.agent.chatModel == nil {
		return "", fmt.Errorf("chat model is not initialized")
	}
	transcript := renderCompactTranscript(messagesWithoutSystem(messages), builder.compactSummaryInputRunes)
	prompt := fmt.Sprintf(`Summarize this conversation history so the next model call can continue accurately with less context.

Keep only durable, task-relevant facts. Preserve:
- the current user goal and latest explicit instructions
- important decisions and constraints
- files, sessions, job IDs, API endpoints, commands, errors, and tool results that matter
- unfinished work, blockers, and next actions

Do not include hidden chain-of-thought. Do not invent facts. Return concise Markdown only.

Conversation transcript:

%s`, transcript)

	message, err := r.agent.chatModel.Generate(ctx, []*schema.Message{
		schema.SystemMessage("You compact agent conversation context. You do not call tools. You return only a factual Markdown summary."),
		schema.UserMessage(prompt),
	})
	if err != nil {
		return "", err
	}
	if message == nil {
		return "", fmt.Errorf("compact summary model returned nil message")
	}
	if len(message.ToolCalls) > 0 {
		return "", fmt.Errorf("compact summary model returned tool calls instead of summary")
	}
	summary := strings.TrimSpace(message.Content)
	if builder.compactSummaryRunes > 0 {
		summary = trimRunesWithNotice(summary, builder.compactSummaryRunes, "context summary")
	}
	return summary, nil
}

func compactedSummaryMessages(builder *ContextBuilder, messages []*schema.Message, summary string) []*schema.Message {
	if len(messages) == 0 {
		return []*schema.Message{schema.UserMessage(formatCompactSummaryMessage(summary))}
	}

	compacted := make([]*schema.Message, 0, 2+len(messages))
	compacted = append(compacted, messages[0])
	compacted = append(compacted, schema.UserMessage(formatCompactSummaryMessage(summary)))

	rest := removeCompactSummaryMessages(messagesWithoutSystem(messages))
	tailBudget := defaultCompactSummaryTailRunes
	if builder != nil && builder.compactSummaryTailRunes > 0 {
		tailBudget = builder.compactSummaryTailRunes
	}
	compacted = append(compacted, keepTailMessageGroups(rest, tailBudget)...)
	return compacted
}

func formatCompactSummaryMessage(summary string) string {
	return compactSummaryPrefix + "\n\nThe earlier conversation was summarized to keep the model context within budget. Treat the exact recent messages and tool results after this summary as fresher than the summary.\n\n" + strings.TrimSpace(summary)
}

func removeCompactSummaryMessages(messages []*schema.Message) []*schema.Message {
	filtered := make([]*schema.Message, 0, len(messages))
	for _, message := range messages {
		if isCompactSummaryMessage(message) {
			continue
		}
		filtered = append(filtered, message)
	}
	return filtered
}

func containsCompactSummaryMessage(messages []*schema.Message) bool {
	for _, message := range messages {
		if isCompactSummaryMessage(message) {
			return true
		}
	}
	return false
}

func isCompactSummaryMessage(message *schema.Message) bool {
	return message != nil && message.Role == schema.User && strings.HasPrefix(strings.TrimSpace(message.Content), compactSummaryPrefix)
}

func messagesWithoutSystem(messages []*schema.Message) []*schema.Message {
	if len(messages) == 0 {
		return nil
	}
	if messages[0] != nil && messages[0].Role == schema.System {
		return messages[1:]
	}
	return messages
}

func renderCompactTranscript(messages []*schema.Message, budgetRunes int) string {
	if len(messages) == 0 {
		return "(no conversation messages)"
	}

	var builder strings.Builder
	for idx, message := range messages {
		if idx > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(renderCompactMessage(idx+1, message))
	}
	return trimMiddleRunes(builder.String(), budgetRunes, "conversation transcript")
}

func renderCompactMessage(index int, message *schema.Message) string {
	if message == nil {
		return fmt.Sprintf("### Message %d\nrole: nil", index)
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "### Message %d\nrole: %s", index, message.Role)
	if message.ToolName != "" {
		fmt.Fprintf(&builder, "\ntool_name: %s", message.ToolName)
	}
	if message.ToolCallID != "" {
		fmt.Fprintf(&builder, "\ntool_call_id: %s", message.ToolCallID)
	}
	if len(message.ToolCalls) > 0 {
		builder.WriteString("\ntool_calls:")
		for _, call := range message.ToolCalls {
			fmt.Fprintf(&builder, "\n- id: %s\n  name: %s\n  arguments: %s", call.ID, call.Function.Name, strings.TrimSpace(call.Function.Arguments))
		}
	}
	if strings.TrimSpace(message.Content) != "" {
		builder.WriteString("\ncontent:\n")
		builder.WriteString(strings.TrimSpace(message.Content))
	}
	return builder.String()
}

func trimMiddleRunes(content string, limit int, label string) string {
	content = strings.TrimSpace(content)
	if limit <= 0 || runeLen(content) <= limit {
		return content
	}

	notice := fmt.Sprintf("\n\n[%s middle omitted to fit %d characters.]\n\n", label, limit)
	noticeLen := runeLen(notice)
	if limit <= noticeLen+2 {
		return trimRunesWithNotice(content, limit, label)
	}

	remaining := limit - noticeLen
	head := remaining / 2
	tail := remaining - head
	runes := []rune(content)
	return string(runes[:head]) + notice + string(runes[len(runes)-tail:])
}

func (r *einoAgentRun) recordContextCompactTrace(beforeRunes int, afterRunes int, summary string, compactErr error) {
	if r == nil || r.result == nil {
		return
	}

	content := fmt.Sprintf("**Before:** %d chars\n\n**After:** %d chars", beforeRunes, afterRunes)
	if compactErr != nil {
		content += "\n\n**Status:** model summary compact failed; final budget pruning will be used as fallback.\n\n```text\n" + compactErr.Error() + "\n```"
	} else {
		content += "\n\n**Summary**\n\n" + strings.TrimSpace(summary)
	}
	item := AgentTraceItem{
		Type:    "memory",
		Title:   "Context compact",
		Content: content,
	}

	r.mu.Lock()
	r.result.Trace = append(r.result.Trace, item)
	r.mu.Unlock()

	if r.emit != nil {
		if err := emitTraceItem(r.emit, r.newTraceID(), item); err != nil {
			log.Printf("emit context compact trace: %v", err)
		}
	}
}
