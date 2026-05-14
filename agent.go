package ternura

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"unicode/utf8"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"ternura/shared"
	"ternura/tool"
)

type Agent struct {
	systemPrompt string
	model        string
	client       openai.Client
	messages     []openai.ChatCompletionMessageParamUnion
	tools        map[tool.AgentTool]tool.Tool
}

type AgentRunResult struct {
	Content    string           `json:"content"`
	Trace      []AgentTraceItem `json:"trace,omitempty"`
	RawContent string           `json:"raw_content,omitempty"`
}

type AgentTraceItem struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Content string `json:"content"`
}

type AgentStreamEvent struct {
	Type       string           `json:"type"`
	RunID      string           `json:"run_id,omitempty"`
	Status     string           `json:"status,omitempty"`
	StartedAt  string           `json:"started_at,omitempty"`
	FinishedAt string           `json:"finished_at,omitempty"`
	DurationMS int64            `json:"duration_ms,omitempty"`
	ID         string           `json:"id,omitempty"`
	TraceType  string           `json:"trace_type,omitempty"`
	Title      string           `json:"title,omitempty"`
	Delta      string           `json:"delta,omitempty"`
	Content    string           `json:"content,omitempty"`
	Trace      []AgentTraceItem `json:"trace,omitempty"`
	RawContent string           `json:"raw_content,omitempty"`
	Error      string           `json:"error,omitempty"`
}

func NewAgent(modelConf shared.ModelConfig, systemPrompt string, tools []tool.Tool) *Agent {
	a := Agent{
		systemPrompt: systemPrompt,
		model:        modelConf.Model,
		client:       openai.NewClient(option.WithBaseURL(modelConf.BaseURL), option.WithAPIKey(modelConf.ApiKey)),
		tools:        make(map[tool.AgentTool]tool.Tool),
		messages:     make([]openai.ChatCompletionMessageParamUnion, 0),
	}
	for _, t := range tools {
		a.tools[t.ToolName()] = t
	}
	a.messages = append(a.messages, openai.SystemMessage(systemPrompt))
	return &a
}

func (a *Agent) execute(ctx context.Context, toolName string, argumentsInJSON string) (string, error) {
	t, ok := a.tools[tool.AgentTool(toolName)]
	if !ok {
		return "", errors.New("tool not found")
	}
	return t.Execute(ctx, argumentsInJSON)
}

// Run 提供对于单次用户请求 query 的 tool loop，返回本轮结果的输出。Run 会保持当前对话历史，不同主题的对话轮次应该初始化多个 Agent 实例运行。
func (a *Agent) Run(ctx context.Context, query string) (string, error) {
	result, err := a.RunWithTrace(ctx, query)
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

// RunWithTrace 提供单次用户请求 query 的 tool loop，返回最终内容和本轮的 think/tool trace。
func (a *Agent) RunWithTrace(ctx context.Context, query string) (AgentRunResult, error) {
	a.messages = append(a.messages, openai.UserMessage(query))

	result := AgentRunResult{
		Trace: make([]AgentTraceItem, 0),
	}
	for {
		params := a.newChatCompletionParams()

		log.Printf("calling llm model %s...", a.model)
		resp, err := a.client.Chat.Completions.New(ctx, params)
		if err != nil {
			log.Printf("failed to send a new completion request: %v", err)
			return AgentRunResult{}, err
		}
		if len(resp.Choices) == 0 {
			log.Printf("no choices returned, resp: %v", resp)
			return result, nil
		}
		message := resp.Choices[0].Message
		// 拼接 assistant message 到整体消息链中
		a.messages = append(a.messages, message.ToParam())
		appendThinkTrace(&result, message.Content)

		// tool loop 结束，可以返回结果
		if len(message.ToolCalls) == 0 {
			result.RawContent = message.Content
			result.Content = stripThinkBlocks(message.Content)
			break
		}

		for _, toolCall := range message.ToolCalls {
			toolResult, err := a.execute(ctx, toolCall.Function.Name, toolCall.Function.Arguments)
			traceItem := AgentTraceItem{
				Type:  "tool",
				Title: fmt.Sprintf("Tool use: %s", toolCall.Function.Name),
			}
			if err != nil {
				toolResult = err.Error()
				traceItem.Content = formatToolTrace(toolCall.Function.Arguments, toolResult, err)
			} else {
				traceItem.Content = formatToolTrace(toolCall.Function.Arguments, toolResult, nil)
			}
			result.Trace = append(result.Trace, traceItem)
			log.Printf("tool call %s, arguments %s, error: %v", toolCall.Function.Name, toolCall.Function.Arguments, err)
			// 返回 tool message 到整体消息链中
			a.messages = append(a.messages, openai.ToolMessage(toolResult, toolCall.ID))
		}

	}
	return result, nil
}

func (a *Agent) RunStreaming(ctx context.Context, query string, emit func(AgentStreamEvent) error) (AgentRunResult, error) {
	a.messages = append(a.messages, openai.UserMessage(query))

	result := AgentRunResult{
		Trace: make([]AgentTraceItem, 0),
	}
	traceIndex := 0
	newTraceID := func() string {
		traceIndex++
		return fmt.Sprintf("trace-%d", traceIndex)
	}

	for {
		params := a.newChatCompletionParams()
		toolMessages := make([]openai.ChatCompletionMessageParamUnion, 0)
		executedToolCalls := make(map[string]bool)

		log.Printf("streaming llm model %s...", a.model)
		stream := a.client.Chat.Completions.NewStreaming(ctx, params)
		acc := openai.ChatCompletionAccumulator{}
		contentRouter := newStreamingContentRouter(
			func() string { return newTraceID() },
			func(event AgentStreamEvent) error {
				if event.Type == "trace_start" {
					result.Trace = append(result.Trace, AgentTraceItem{
						Type:  event.TraceType,
						Title: event.Title,
					})
				}
				if event.Type == "trace_delta" && len(result.Trace) > 0 {
					result.Trace[len(result.Trace)-1].Content += event.Delta
				}
				if event.Type == "content_delta" {
					result.Content += event.Delta
				}
				return emit(event)
			},
		)

		for stream.Next() {
			chunk := stream.Current()
			if !acc.AddChunk(chunk) {
				return AgentRunResult{}, errors.New("failed to accumulate stream chunk")
			}

			for _, choice := range chunk.Choices {
				if choice.Delta.Content != "" {
					result.RawContent += choice.Delta.Content
					if err := contentRouter.Write(choice.Delta.Content); err != nil {
						return result, err
					}
				}
			}

			if toolCall, ok := acc.JustFinishedToolCall(); ok {
				if err := contentRouter.Flush(); err != nil {
					return result, err
				}
				toolTrace, toolResult := a.executeToolWithTrace(ctx, toolCall.Name, toolCall.Arguments)
				result.Trace = append(result.Trace, toolTrace)
				if err := emitTraceItem(emit, newTraceID(), toolTrace); err != nil {
					return result, err
				}
				log.Printf("tool call %s, arguments %s", toolCall.Name, toolCall.Arguments)
				toolMessages = append(toolMessages, openai.ToolMessage(toolResult, toolCall.ID))
				executedToolCalls[toolCall.ID] = true
			}
		}

		if err := stream.Err(); err != nil {
			log.Printf("failed to stream completion request: %v", err)
			return AgentRunResult{}, err
		}
		if err := contentRouter.Flush(); err != nil {
			return result, err
		}

		if len(acc.Choices) == 0 {
			return result, nil
		}

		message := acc.Choices[0].Message
		a.messages = append(a.messages, message.ToParam())

		if len(message.ToolCalls) == 0 {
			result.Content = strings.TrimSpace(result.Content)
			if err := emit(AgentStreamEvent{
				Type:       "done",
				Content:    result.Content,
				Trace:      result.Trace,
				RawContent: result.RawContent,
			}); err != nil {
				return result, err
			}
			return result, nil
		}

		for _, toolCall := range message.ToolCalls {
			if executedToolCalls[toolCall.ID] {
				continue
			}
			toolTrace, toolResult := a.executeToolWithTrace(ctx, toolCall.Function.Name, toolCall.Function.Arguments)
			result.Trace = append(result.Trace, toolTrace)
			if err := emitTraceItem(emit, newTraceID(), toolTrace); err != nil {
				return result, err
			}
			log.Printf("tool call %s, arguments %s", toolCall.Function.Name, toolCall.Function.Arguments)
			toolMessages = append(toolMessages, openai.ToolMessage(toolResult, toolCall.ID))
		}
		a.messages = append(a.messages, toolMessages...)
	}
}

func (a *Agent) newChatCompletionParams() openai.ChatCompletionNewParams {
	params := openai.ChatCompletionNewParams{
		Model:             a.model,
		Messages:          a.messages,
		Tools:             make([]openai.ChatCompletionToolUnionParam, 0),
		ParallelToolCalls: openai.Bool(false),
	}

	for _, t := range a.tools {
		params.Tools = append(params.Tools, t.Info())
	}
	return params
}

func (a *Agent) executeToolWithTrace(ctx context.Context, name string, arguments string) (AgentTraceItem, string) {
	toolResult, err := a.execute(ctx, name, arguments)
	traceItem := AgentTraceItem{
		Type:  "tool",
		Title: fmt.Sprintf("Tool use: %s", name),
	}
	if err != nil {
		toolResult = err.Error()
		traceItem.Content = formatToolTrace(arguments, toolResult, err)
	} else {
		traceItem.Content = formatToolTrace(arguments, toolResult, nil)
	}
	return traceItem, toolResult
}

func emitTraceItem(emit func(AgentStreamEvent) error, id string, item AgentTraceItem) error {
	if err := emit(AgentStreamEvent{
		Type:      "trace_start",
		ID:        id,
		TraceType: item.Type,
		Title:     item.Title,
	}); err != nil {
		return err
	}
	if item.Content != "" {
		if err := emit(AgentStreamEvent{
			Type:  "trace_delta",
			ID:    id,
			Delta: item.Content,
		}); err != nil {
			return err
		}
	}
	return emit(AgentStreamEvent{
		Type:    "trace_done",
		ID:      id,
		Content: item.Content,
	})
}

func appendThinkTrace(result *AgentRunResult, content string) {
	for _, think := range extractThinkBlocks(content) {
		result.Trace = append(result.Trace, AgentTraceItem{
			Type:    "think",
			Title:   "Thinking",
			Content: think,
		})
	}
}

func extractThinkBlocks(content string) []string {
	blocks := make([]string, 0)
	remaining := content
	for {
		start := strings.Index(remaining, "<think>")
		if start == -1 {
			break
		}
		afterStart := remaining[start+len("<think>"):]
		end := strings.Index(afterStart, "</think>")
		if end == -1 {
			break
		}
		blocks = append(blocks, strings.TrimSpace(afterStart[:end]))
		remaining = afterStart[end+len("</think>"):]
	}
	return blocks
}

func stripThinkBlocks(content string) string {
	var builder strings.Builder
	remaining := content
	for {
		start := strings.Index(remaining, "<think>")
		if start == -1 {
			builder.WriteString(remaining)
			break
		}
		builder.WriteString(remaining[:start])
		afterStart := remaining[start+len("<think>"):]
		end := strings.Index(afterStart, "</think>")
		if end == -1 {
			builder.WriteString(remaining[start:])
			break
		}
		remaining = afterStart[end+len("</think>"):]
	}
	return strings.TrimSpace(builder.String())
}

func formatToolTrace(arguments string, result string, toolErr error) string {
	sections := []string{
		"**Arguments**",
		"",
		"```json",
		formatJSON(arguments),
		"```",
		"",
	}

	if toolErr != nil {
		sections = append(sections,
			"**Error**",
			"",
			"```text",
			result,
			"```",
		)
		return strings.Join(sections, "\n")
	}

	sections = append(sections,
		"**Result**",
		"",
		"```text",
		result,
		"```",
	)
	return strings.Join(sections, "\n")
}

func formatJSON(raw string) string {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	formatted, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return raw
	}
	return string(formatted)
}

type streamingContentRouter struct {
	pending    string
	inThink    bool
	traceID    string
	contentBuf strings.Builder
	traceBuf   strings.Builder
	newTraceID func() string
	emit       func(AgentStreamEvent) error
}

func newStreamingContentRouter(newTraceID func() string, emit func(AgentStreamEvent) error) *streamingContentRouter {
	return &streamingContentRouter{
		newTraceID: newTraceID,
		emit:       emit,
	}
}

func (r *streamingContentRouter) Write(delta string) error {
	r.pending += delta
	return r.drain(false)
}

func (r *streamingContentRouter) Flush() error {
	return r.drain(true)
}

func (r *streamingContentRouter) drain(flush bool) error {
	for r.pending != "" {
		if r.inThink {
			end := strings.Index(r.pending, "</think>")
			if end >= 0 {
				if end > 0 {
					delta := r.traceBuf.String() + r.pending[:end]
					r.traceBuf.Reset()
					if err := r.emit(AgentStreamEvent{Type: "trace_delta", ID: r.traceID, Delta: delta}); err != nil {
						return err
					}
				}
				if err := r.emit(AgentStreamEvent{Type: "trace_done", ID: r.traceID}); err != nil {
					return err
				}
				r.pending = r.pending[end+len("</think>"):]
				r.inThink = false
				r.traceID = ""
				continue
			}

			emitLength := len(r.pending)
			if !flush {
				emitLength = safeEmitLength(r.pending, "</think>")
			}
			if emitLength == 0 {
				return nil
			}
			r.traceBuf.WriteString(r.pending[:emitLength])
			delta := r.traceBuf.String()
			r.traceBuf.Reset()
			if err := r.emit(AgentStreamEvent{Type: "trace_delta", ID: r.traceID, Delta: delta}); err != nil {
				return err
			}
			r.pending = r.pending[emitLength:]
			continue
		}

		start := strings.Index(r.pending, "<think>")
		if start >= 0 {
			if start > 0 {
				delta := r.contentBuf.String() + r.pending[:start]
				r.contentBuf.Reset()
				if err := r.emit(AgentStreamEvent{Type: "content_delta", Delta: delta}); err != nil {
					return err
				}
			}
			r.pending = r.pending[start+len("<think>"):]
			r.inThink = true
			r.traceID = r.newTraceID()
			if err := r.emit(AgentStreamEvent{
				Type:      "trace_start",
				ID:        r.traceID,
				TraceType: "think",
				Title:     "Thinking",
			}); err != nil {
				return err
			}
			continue
		}

		emitLength := len(r.pending)
		if !flush {
			emitLength = safeEmitLength(r.pending, "<think>")
		}
		if emitLength == 0 {
			return nil
		}
		r.contentBuf.WriteString(r.pending[:emitLength])
		delta := r.contentBuf.String()
		r.contentBuf.Reset()
		if err := r.emit(AgentStreamEvent{Type: "content_delta", Delta: delta}); err != nil {
			return err
		}
		r.pending = r.pending[emitLength:]
	}
	if flush && r.inThink {
		if r.traceBuf.Len() > 0 {
			if err := r.emit(AgentStreamEvent{Type: "trace_delta", ID: r.traceID, Delta: r.traceBuf.String()}); err != nil {
				return err
			}
			r.traceBuf.Reset()
		}
		if err := r.emit(AgentStreamEvent{Type: "trace_done", ID: r.traceID}); err != nil {
			return err
		}
		r.inThink = false
		r.traceID = ""
	}
	if flush && r.contentBuf.Len() > 0 {
		if err := r.emit(AgentStreamEvent{Type: "content_delta", Delta: r.contentBuf.String()}); err != nil {
			return err
		}
		r.contentBuf.Reset()
	}
	return nil
}

func safeEmitLength(content string, marker string) int {
	maxKeep := len(marker) - 1
	if len(content) <= maxKeep {
		return 0
	}
	return lastCompleteUTF8Boundary(content, len(content)-maxKeep)
}

func lastCompleteUTF8Boundary(content string, limit int) int {
	if limit > len(content) {
		limit = len(content)
	}
	for limit > 0 && !utf8.ValidString(content[:limit]) {
		limit--
	}
	return limit
}
