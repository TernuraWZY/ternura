package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"unicode/utf8"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	einomodel "github.com/cloudwego/eino/components/model"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"ternura/config"
	"ternura/tool"
)

type Agent struct {
	systemPrompt   string
	model          string
	chatModel      einomodel.ToolCallingChatModel
	contextBuilder *ContextBuilder
	messages       []*schema.Message
	tools          map[tool.AgentTool]tool.Tool
	hooks          *HookManager
	runLimits      RunLimits
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

type ConversationMessage struct {
	Role    string `json:"role"`
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

type AgentOption func(*Agent)

func WithHooks(hooks ...Hook) AgentOption {
	return func(a *Agent) {
		a.hooks = NewHookManager(hooks...)
	}
}

func WithHookManager(manager *HookManager) AgentOption {
	return func(a *Agent) {
		if manager == nil {
			a.hooks = NewHookManager()
			return
		}
		a.hooks = manager
	}
}

func WithRunLimits(limits RunLimits) AgentOption {
	return func(a *Agent) {
		a.runLimits = normalizeRunLimits(limits)
	}
}

func NewAgent(modelConf config.ModelConfig, systemPrompt string, tools []tool.Tool, opts ...AgentOption) *Agent {
	chatModel, err := einoopenai.NewChatModel(context.Background(), &einoopenai.ChatModelConfig{
		BaseURL: modelConf.BaseURL,
		APIKey:  modelConf.ApiKey,
		Model:   modelConf.Model,
		ExtraFields: map[string]any{
			"parallel_tool_calls": false,
		},
	})
	if err != nil {
		log.Printf("create Eino OpenAI chat model: %v", err)
	}

	a := Agent{
		systemPrompt:   systemPrompt,
		model:          modelConf.Model,
		chatModel:      chatModel,
		contextBuilder: NewContextBuilder(systemPrompt),
		tools:          make(map[tool.AgentTool]tool.Tool),
		messages:       make([]*schema.Message, 0),
		hooks:          NewHookManager(),
		runLimits:      RunLimitsFromEnv(),
	}
	for _, t := range tools {
		a.tools[t.ToolName()] = t
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&a)
		}
	}
	a.messages = append(a.messages, schema.SystemMessage(systemPrompt))
	return &a
}

func (a *Agent) RestoreConversation(messages []ConversationMessage) {
	a.messages = make([]*schema.Message, 0, len(messages)+1)
	a.messages = append(a.messages, schema.SystemMessage(a.systemPrompt))
	for _, message := range messages {
		switch message.Role {
		case "user":
			a.messages = append(a.messages, schema.UserMessage(message.Content))
		case "assistant":
			a.messages = append(a.messages, schema.AssistantMessage(message.Content, nil))
		}
	}
}

func (a *Agent) ensureChatModel() error {
	if a.chatModel == nil {
		return errors.New("chat model is not initialized")
	}
	return nil
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
func (a *Agent) RunWithTrace(ctx context.Context, query string) (result AgentRunResult, runErr error) {
	runCtx := NewRunContext(query, RunModeSync)
	runCtx.SetRunLimits(a.runLimits)
	if err := a.hooks.BeforeRun(ctx, runCtx); err != nil {
		return result, err
	}
	defer func() {
		if err := a.hooks.AfterRun(ctx, runCtx, result, runErr); err != nil && runErr == nil {
			runErr = err
		}
	}()

	a.messages = append(a.messages, schema.UserMessage(query))
	if err := a.hooks.AfterUserMessage(ctx, runCtx); err != nil {
		return result, err
	}

	result = AgentRunResult{
		Trace: make([]AgentTraceItem, 0),
	}
	for {
		runtime, err := a.newEinoAgentRun(ctx, runCtx, &result, nil)
		if err != nil {
			if errors.Is(err, ErrRunBudgetExceeded) {
				result.Content = budgetExceededFinalMessage(err)
				result.Trace = append(result.Trace, budgetExceededTrace(err))
				return result, nil
			}
			return result, err
		}
		message, err := runtime.Generate(ctx)
		if err != nil {
			if errors.Is(err, ErrRunBudgetExceeded) {
				result.Content = budgetExceededFinalMessage(err)
				result.Trace = append(result.Trace, budgetExceededTrace(err))
				return result, nil
			}
			return AgentRunResult{}, err
		}
		if message == nil {
			return result, nil
		}
		if runtime.RetryIgnoredToolPolicy(ctx) {
			continue
		}
		result.RawContent = message.Content
		result.Content = stripThinkBlocks(message.Content)
		if err := a.hooks.FinalizeRun(ctx, runCtx, &result); err != nil {
			return result, err
		}
		if a.retryFinalizationToolPolicy(ctx, runCtx) {
			continue
		}
		break
	}
	return result, nil
}

func (a *Agent) RunStreaming(ctx context.Context, query string, emit func(AgentStreamEvent) error) (result AgentRunResult, runErr error) {
	runCtx := NewRunContext(query, RunModeStreaming)
	runCtx.SetRunLimits(a.runLimits)
	if err := a.hooks.BeforeRun(ctx, runCtx); err != nil {
		return result, err
	}
	defer func() {
		if err := a.hooks.AfterRun(ctx, runCtx, result, runErr); err != nil && runErr == nil {
			runErr = err
		}
	}()

	a.messages = append(a.messages, schema.UserMessage(query))
	if err := a.hooks.AfterUserMessage(ctx, runCtx); err != nil {
		return result, err
	}

	result = AgentRunResult{
		Trace: make([]AgentTraceItem, 0),
	}

	for {
		runtime, err := a.newEinoAgentRun(ctx, runCtx, &result, emit)
		if err != nil {
			if errors.Is(err, ErrRunBudgetExceeded) {
				result.Content = budgetExceededFinalMessage(err)
				result.Trace = append(result.Trace, budgetExceededTrace(err))
				return emitBudgetExceededDone(emit, result, result.Content)
			}
			return result, err
		}
		message, err := runtime.Stream(ctx)
		if err != nil {
			if errors.Is(err, ErrRunBudgetExceeded) {
				delta := budgetExceededFinalMessage(err)
				if strings.TrimSpace(result.Content) == "" {
					result.Content = delta
				} else {
					delta = "\n\n" + budgetExceededFinalMessage(err)
					result.Content += delta
				}
				result.Trace = append(result.Trace, budgetExceededTrace(err))
				return emitBudgetExceededDone(emit, result, delta)
			}
			return result, err
		}
		if message == nil {
			return result, nil
		}
		if runtime.RetryIgnoredToolPolicy(ctx) {
			continue
		}
		result.Content = strings.TrimSpace(result.Content)
		if err := a.hooks.FinalizeRun(ctx, runCtx, &result); err != nil {
			return result, err
		}
		if a.retryFinalizationToolPolicy(ctx, runCtx) {
			result.Content = ""
			result.RawContent = ""
			continue
		}
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
}

func budgetExceededFinalMessage(err error) string {
	var budgetErr RunBudgetError
	if errors.As(err, &budgetErr) {
		switch {
		case budgetErr.Kind == "tool" && budgetErr.Tool == tool.AgentToolWebFetch:
			return fmt.Sprintf("这轮没有 fetch 到更多有效网页信息，`web_fetch` 已达到本轮上限（%d 次）。\n\n我已经停止继续抓取，避免一直等待。可以换一个更具体的网址或问题继续查。", budgetErr.Limit)
		case budgetErr.Kind == "tool":
			return fmt.Sprintf("这轮工具调用已经达到上限（%d 次），我已停止继续调用工具。\n\n目前没有拿到足够的新证据继续推进，请缩小问题范围后再试。", budgetErr.Limit)
		case budgetErr.Kind == "model":
			return fmt.Sprintf("这轮模型调用已经达到上限（%d 次），我已停止继续运行。\n\n当前没有足够稳定的结果可继续展开，请缩小问题范围后再试。", budgetErr.Limit)
		}
	}
	return "这轮运行预算已经耗尽，我已停止继续调用模型或工具，避免单轮交互继续拉长。请缩小问题范围后再试。"
}

func budgetExceededToolContent(err error) string {
	var budgetErr RunBudgetError
	if errors.As(err, &budgetErr) {
		switch {
		case budgetErr.Kind == "tool" && budgetErr.Tool == tool.AgentToolWebFetch:
			return fmt.Sprintf("没有 fetch 到更多有效网页信息：web_fetch 已达到本轮上限（%d 次）。请停止继续抓取网页，基于已经拿到的可用证据回答；如果已有网页结果不可用，请明确告诉用户没有 fetch 到有效信息。", budgetErr.Limit)
		case budgetErr.Kind == "tool":
			return fmt.Sprintf("工具调用已达到本轮上限（%d 次）。请停止继续调用工具，基于已有证据回答，并明确说明哪些信息没有验证。", budgetErr.Limit)
		case budgetErr.Kind == "model":
			return fmt.Sprintf("模型调用已达到本轮上限（%d 次）。请基于已有证据给出简短结论，并明确说明哪些信息没有验证。", budgetErr.Limit)
		}
	}
	return "本轮运行预算已耗尽。请停止继续调用工具，基于已有证据回答，并明确说明哪些信息没有验证。"
}

func budgetExceededTrace(err error) AgentTraceItem {
	return AgentTraceItem{
		Type:    "budget",
		Title:   "Run budget",
		Content: budgetExceededToolContent(err),
	}
}

func emitBudgetExceededDone(emit func(AgentStreamEvent) error, result AgentRunResult, delta string) (AgentRunResult, error) {
	if emit == nil {
		return result, nil
	}
	if strings.TrimSpace(delta) != "" {
		if err := emit(AgentStreamEvent{
			Type:    "content_delta",
			Delta:   delta,
			Content: result.Content,
		}); err != nil {
			return result, err
		}
	}
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

func (a *Agent) toolsForRun(runCtx *RunContext) []einotool.BaseTool {
	tools, available := a.enabledToolsForRun(runCtx)
	policy := effectiveToolPolicy(runCtx, available)
	if len(policy.AllowedTools) > 0 {
		filtered := make([]einotool.BaseTool, 0, len(policy.AllowedTools))
		for _, name := range policy.AllowedTools {
			filtered = append(filtered, available[name])
		}
		return filtered
	}
	return tools
}

func (a *Agent) enabledToolsForRun(runCtx *RunContext) ([]einotool.BaseTool, map[tool.AgentTool]einotool.BaseTool) {
	names := make([]string, 0, len(a.tools))
	for name := range a.tools {
		names = append(names, string(name))
	}
	sort.Strings(names)

	tools := make([]einotool.BaseTool, 0, len(names))
	available := make(map[tool.AgentTool]einotool.BaseTool, len(names))
	for _, rawName := range names {
		name := tool.AgentTool(rawName)
		if runCtx != nil {
			if _, disabled := runCtx.ToolDisabled(name); disabled {
				continue
			}
		}
		t := a.tools[name]
		tools = append(tools, t)
		available[name] = t
	}
	return tools, available
}

func effectiveToolPolicy(runCtx *RunContext, available map[tool.AgentTool]einotool.BaseTool) ToolPolicy {
	if runCtx == nil {
		return ToolPolicy{}
	}
	policy := runCtx.RequestedToolPolicy()
	if policy.Empty() {
		return ToolPolicy{}
	}
	if len(policy.AllowedTools) == 0 {
		if policy.Required && len(available) == 0 {
			return ToolPolicy{}
		}
		return policy
	}

	allowed := make([]tool.AgentTool, 0, len(policy.AllowedTools))
	for _, name := range policy.AllowedTools {
		if _, ok := available[name]; ok {
			allowed = append(allowed, name)
		}
	}
	if len(allowed) == 0 {
		return ToolPolicy{}
	}
	policy.AllowedTools = allowed
	return policy
}

func toolTraceFromResult(result ToolResult) AgentTraceItem {
	return AgentTraceItem{
		Type:    "tool",
		Title:   fmt.Sprintf("Tool use: %s", result.Call.Function.Name),
		Content: formatToolTrace(result.Call.Function.Arguments, result.Content, result.Err),
	}
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

const ignoredToolPolicyRetryKey = "ignored_tool_policy_retry"
const finalizationToolPolicyRetryKey = "finalization_tool_policy_retry"

// retryIgnoredToolPolicy 在模型无视 Required 工具策略直接文本回复时，追加一次 nudge 并重试。
func (a *Agent) retryIgnoredToolPolicy(_ context.Context, runCtx *RunContext, policy ToolPolicy) bool {
	if runCtx == nil || !policy.Required {
		return false
	}
	if runCtx.Metadata != nil {
		if retried, ok := runCtx.Metadata[ignoredToolPolicyRetryKey].(bool); ok && retried {
			return false
		}
	}
	if runCtx.Metadata == nil {
		runCtx.Metadata = make(map[string]any)
	}
	runCtx.Metadata[ignoredToolPolicyRetryKey] = true
	runCtx.SetToolPolicy(policy)
	a.messages = append(a.messages, schema.UserMessage(
		"You must call the required tool before claiming the action succeeded. Call it now with valid arguments.",
	))
	return true
}

// retryFinalizationToolPolicy lets a FinalizeRun hook recover from an ungrounded
// final answer by setting a required tool policy and asking ReAct to continue.
func (a *Agent) retryFinalizationToolPolicy(_ context.Context, runCtx *RunContext) bool {
	if runCtx == nil {
		return false
	}
	policy := runCtx.RequestedToolPolicy()
	if !policy.Required {
		return false
	}
	if runCtx.Metadata != nil {
		if retried, ok := runCtx.Metadata[finalizationToolPolicyRetryKey].(bool); ok && retried {
			return false
		}
	}
	if runCtx.Metadata == nil {
		runCtx.Metadata = make(map[string]any)
	}
	runCtx.Metadata[finalizationToolPolicyRetryKey] = true
	runCtx.SetToolPolicy(policy)
	a.messages = append(a.messages, schema.UserMessage(
		"Your previous final answer claimed a real tool result without grounded evidence. Do not write a command block as text. Call the required tool now with valid arguments, then answer from the actual tool result.",
	))
	return true
}
