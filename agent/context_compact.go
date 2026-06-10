package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

const compactSummaryPrefix = "[Compacted]"

func (r *einoAgentRun) compactHistoryIfNeeded(ctx context.Context, builder *ContextBuilder, messages []*schema.Message) []*schema.Message {
	if r == nil || builder == nil || (!builder.NeedsSummaryCompact(messages) && !containsCompactToolResult(messages)) {
		return messages
	}
	if containsCompactSummaryMessage(messages) {
		return messages
	}

	beforeRunes := messagesRunes(messages)
	summary, transcriptPath, err := r.summarizeContext(ctx, builder, messages)
	if err != nil {
		log.Printf("context summary compact failed: %v", err)
		r.recordContextCompactTrace(beforeRunes, beforeRunes, "", transcriptPath, err)
		return messages
	}
	if strings.TrimSpace(summary) == "" {
		err := fmt.Errorf("empty compact summary")
		log.Printf("context summary compact failed: %v", err)
		r.recordContextCompactTrace(beforeRunes, beforeRunes, "", transcriptPath, err)
		return messages
	}

	compacted := compactedHistoryMessages(messages, summary)
	r.recordContextCompactTrace(beforeRunes, messagesRunes(compacted), summary, transcriptPath, nil)
	return compacted
}

func (r *einoAgentRun) reactiveCompactHistory(ctx context.Context, builder *ContextBuilder, messages []*schema.Message) []*schema.Message {
	if r == nil || builder == nil || containsCompactSummaryMessage(messages) {
		return messages
	}
	beforeRunes := messagesRunes(messages)
	summary, transcriptPath, err := r.summarizeContext(ctx, builder, messages)
	if err != nil {
		log.Printf("reactive context compact failed: %v", err)
		r.recordContextCompactTrace(beforeRunes, beforeRunes, "", transcriptPath, err)
		return messages
	}
	compacted := reactiveCompactedMessages(builder, messages, summary)
	r.recordContextCompactTrace(beforeRunes, messagesRunes(compacted), summary, transcriptPath, nil)
	return compacted
}

func (r *einoAgentRun) summarizeContext(ctx context.Context, builder *ContextBuilder, messages []*schema.Message) (string, string, error) {
	if r == nil || r.agent == nil || r.agent.chatModel == nil {
		return "", "", fmt.Errorf("chat model is not initialized")
	}
	transcriptPath := writeCompactTranscript(messages, builder.compactTranscriptDir)
	transcript := renderCompactTranscript(messagesWithoutSystem(messages), builder.compactSummaryInputRunes)
	prompt := fmt.Sprintf(`Summarize this coding-agent conversation so work can continue.

Preserve:
1. current goal
2. key findings/decisions
3. files read/changed
4. remaining work
5. user constraints

Be compact but concrete. Do not include hidden chain-of-thought. Do not invent facts. Return concise Markdown only.

Conversation transcript:

%s`, transcript)

	message, err := r.agent.chatModel.Generate(ctx, []*schema.Message{
		schema.SystemMessage("You compact agent conversation context. You do not call tools. You return only a factual Markdown summary."),
		schema.UserMessage(prompt),
	})
	if err != nil {
		return "", transcriptPath, err
	}
	if message == nil {
		return "", transcriptPath, fmt.Errorf("compact summary model returned nil message")
	}
	if len(message.ToolCalls) > 0 {
		return "", transcriptPath, fmt.Errorf("compact summary model returned tool calls instead of summary")
	}
	summary := strings.TrimSpace(message.Content)
	if builder.compactSummaryRunes > 0 {
		summary = trimRunesWithNotice(summary, builder.compactSummaryRunes, "context summary")
	}
	return summary, transcriptPath, nil
}

func compactedHistoryMessages(messages []*schema.Message, summary string) []*schema.Message {
	if len(messages) == 0 {
		return []*schema.Message{schema.UserMessage(formatCompactSummaryMessage(summary))}
	}
	if messages[0] != nil && messages[0].Role == schema.System {
		return []*schema.Message{messages[0], schema.UserMessage(formatCompactSummaryMessage(summary))}
	}
	return []*schema.Message{schema.UserMessage(formatCompactSummaryMessage(summary))}
}

func reactiveCompactedMessages(builder *ContextBuilder, messages []*schema.Message, summary string) []*schema.Message {
	compacted := compactedHistoryMessages(messages, summary)
	rest := removeCompactSummaryMessages(messagesWithoutSystem(messages))
	tailCount := defaultCompactReactiveTailMessages
	if builder != nil && builder.compactReactiveTailMessages > 0 {
		tailCount = builder.compactReactiveTailMessages
	}
	tailStart := len(rest) - tailCount
	if tailStart < 0 {
		tailStart = 0
	}
	tailStart = adjustTailStartForToolPair(rest, tailStart)
	return append(compacted, rest[tailStart:]...)
}

func formatCompactSummaryMessage(summary string) string {
	return compactSummaryPrefix + "\n\n" + strings.TrimSpace(summary)
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

func containsCompactToolResult(messages []*schema.Message) bool {
	for _, message := range messages {
		if message != nil && message.Role == schema.Tool && message.ToolName == "compact" {
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

func writeCompactTranscript(messages []*schema.Message, dir string) string {
	if len(messages) == 0 {
		return ""
	}
	if strings.TrimSpace(dir) == "" {
		dir = ".transcripts"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, fmt.Sprintf("transcript_%d.jsonl", time.Now().Unix()))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		path = filepath.Join(dir, fmt.Sprintf("transcript_%d.jsonl", time.Now().UnixNano()))
		file, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return ""
		}
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, message := range messages {
		if err := encoder.Encode(message); err != nil {
			return path
		}
	}
	return path
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

func (r *einoAgentRun) recordContextCompactTrace(beforeRunes int, afterRunes int, summary string, transcriptPath string, compactErr error) {
	if r == nil || r.result == nil {
		return
	}

	content := fmt.Sprintf("**Before:** %d chars\n\n**After:** %d chars", beforeRunes, afterRunes)
	if strings.TrimSpace(transcriptPath) != "" {
		content += "\n\n**Transcript:** `" + transcriptPath + "`"
	}
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
