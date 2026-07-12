package agent

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/cloudwego/eino/schema"

	"ternura/tool"
)

type RunMode string

const (
	RunModeSync      RunMode = "sync"
	RunModeStreaming RunMode = "streaming"
)

type RuntimeContextBlock struct {
	Key         string
	Title       string
	Content     string
	Priority    int
	BudgetRunes int
}

const (
	RuntimeContextPriorityCritical = 1000
	RuntimeContextPriorityHigh     = 800
	RuntimeContextPriorityNormal   = 500
	RuntimeContextPriorityLow      = 200
)

// ToolPolicy 描述本轮模型调用的工具使用策略。
// 它不直接执行工具，只影响 Eino ReAct 暴露哪些工具，以及是否要求模型先调用工具。
type ToolPolicy struct {
	Required     bool
	AllowedTools []tool.AgentTool
}

func RequireAnyTool() ToolPolicy {
	return ToolPolicy{Required: true}
}

func RequireTool(name tool.AgentTool) ToolPolicy {
	if strings.TrimSpace(string(name)) == "" {
		return ToolPolicy{}
	}
	return ToolPolicy{
		Required:     true,
		AllowedTools: []tool.AgentTool{name},
	}
}

type RunContext struct {
	Query          string
	Mode           RunMode
	ModelCallCount int
	ToolCallCount  int
	Metadata       map[string]any

	contextBlocks  []RuntimeContextBlock
	disabledTools  map[tool.AgentTool]string
	toolResults    []ToolExecution
	toolPolicy     ToolPolicy
	toolPolicyMu   sync.RWMutex
	runLimits      RunLimits
	toolCallCounts map[tool.AgentTool]int
}

func NewRunContext(query string, mode RunMode) *RunContext {
	return &RunContext{
		Query:         query,
		Mode:          mode,
		Metadata:      make(map[string]any),
		disabledTools: make(map[tool.AgentTool]string),
	}
}

func (r *RunContext) SetRunLimits(limits RunLimits) {
	if r == nil {
		return
	}
	r.runLimits = normalizeRunLimits(limits)
	if r.toolCallCounts == nil {
		r.toolCallCounts = make(map[tool.AgentTool]int)
	}
}

func (r *RunContext) RunLimits() RunLimits {
	if r == nil {
		return RunLimits{}
	}
	return r.runLimits
}

func (r *RunContext) reserveModelCall() error {
	if r == nil {
		return nil
	}
	limit := r.runLimits.MaxModelCalls
	if limit > 0 && r.ModelCallCount >= limit {
		return RunBudgetError{Kind: "model", Limit: limit}
	}
	r.ModelCallCount++
	return nil
}

func (r *RunContext) reserveToolCall(name tool.AgentTool) error {
	if r == nil {
		return nil
	}
	if r.toolCallCounts == nil {
		r.toolCallCounts = make(map[tool.AgentTool]int)
	}
	if limit := r.runLimits.MaxToolCalls; limit > 0 && r.ToolCallCount >= limit {
		return RunBudgetError{Kind: "tool", Limit: limit}
	}
	if limit := r.runLimits.MaxToolCallsByName[name]; limit > 0 && r.toolCallCounts[name] >= limit {
		return RunBudgetError{Kind: "tool", Tool: name, Limit: limit}
	}
	r.ToolCallCount++
	r.toolCallCounts[name]++
	return nil
}

func (r *RunContext) CanCallModel() bool {
	if r == nil {
		return true
	}
	limit := r.runLimits.MaxModelCalls
	return limit <= 0 || r.ModelCallCount < limit
}

func (r *RunContext) CanCallTool(name tool.AgentTool) bool {
	if r == nil {
		return true
	}
	if limit := r.runLimits.MaxToolCalls; limit > 0 && r.ToolCallCount >= limit {
		return false
	}
	if limit := r.runLimits.MaxToolCallsByName[name]; limit > 0 && r.toolCallCounts[name] >= limit {
		return false
	}
	return true
}

func (r *RunContext) CanCallAnyTool(names ...tool.AgentTool) bool {
	if r == nil {
		return true
	}
	if limit := r.runLimits.MaxToolCalls; limit > 0 && r.ToolCallCount >= limit {
		return false
	}
	if len(names) == 0 {
		return true
	}
	for _, name := range names {
		if r.CanCallTool(name) {
			return true
		}
	}
	return false
}

func (r *RunContext) ToolCallCountFor(name tool.AgentTool) int {
	if r == nil || r.toolCallCounts == nil {
		return 0
	}
	return r.toolCallCounts[name]
}

func (r *RunContext) BudgetContextText() string {
	if r == nil {
		return ""
	}
	limits := r.runLimits
	parts := []string{
		"This run has a bounded execution budget. Prefer direct progress over broad exploration.",
		formatBudgetLine("Model calls", r.ModelCallCount, limits.MaxModelCalls),
		formatBudgetLine("Tool calls", r.ToolCallCount, limits.MaxToolCalls),
	}
	if limit := limits.MaxToolCallsByName[tool.AgentToolWebFetch]; limit > 0 {
		parts = append(parts, formatBudgetLine("web_fetch calls", r.ToolCallCountFor(tool.AgentToolWebFetch), limit))
	}
	parts = append(parts, "If a tool returns a budget-exceeded message, stop calling tools and answer with the evidence already available. Clearly say what was not verified.")
	return strings.Join(parts, "\n")
}

func formatBudgetLine(label string, used int, limit int) string {
	if limit <= 0 {
		return fmt.Sprintf("- %s used: %d.", label, used)
	}
	remaining := limit - used
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("- %s used: %d/%d; remaining: %d.", label, used, limit, remaining)
}

// SetToolPolicy 写入本轮模型调用的工具使用策略。
// hook 通常只在 ModelCallCount == 1（首轮）设置 Required，工具返回后 agent loop 会清空策略。
func (r *RunContext) SetToolPolicy(policy ToolPolicy) {
	if r == nil {
		return
	}
	r.toolPolicyMu.Lock()
	defer r.toolPolicyMu.Unlock()
	r.toolPolicy = normalizeToolPolicy(policy)
}

// ClearToolPolicy 清空工具使用策略，回到默认的 auto 行为。
func (r *RunContext) ClearToolPolicy() {
	if r == nil {
		return
	}
	r.toolPolicyMu.Lock()
	defer r.toolPolicyMu.Unlock()
	r.toolPolicy = ToolPolicy{}
}

// RequestedToolPolicy 返回当前 RunContext 持有的工具使用策略，未设置时返回零值。
func (r *RunContext) RequestedToolPolicy() ToolPolicy {
	if r == nil {
		return ToolPolicy{}
	}
	r.toolPolicyMu.RLock()
	defer r.toolPolicyMu.RUnlock()
	return r.toolPolicy
}

func normalizeToolPolicy(policy ToolPolicy) ToolPolicy {
	if len(policy.AllowedTools) == 0 {
		if !policy.Required {
			return ToolPolicy{}
		}
		return ToolPolicy{Required: true}
	}

	allowed := make([]tool.AgentTool, 0, len(policy.AllowedTools))
	seen := make(map[tool.AgentTool]struct{}, len(policy.AllowedTools))
	for _, name := range policy.AllowedTools {
		if strings.TrimSpace(string(name)) == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		allowed = append(allowed, name)
	}
	if len(allowed) == 0 {
		return ToolPolicy{}
	}
	return ToolPolicy{
		Required:     policy.Required,
		AllowedTools: allowed,
	}
}

func (p ToolPolicy) Empty() bool {
	return !p.Required && len(p.AllowedTools) == 0
}

func (r *RunContext) SetContextBlock(key string, title string, content string) {
	r.SetContextBlockWithPriority(key, title, content, RuntimeContextPriorityNormal, 0)
}

func (r *RunContext) SetContextBlockWithPriority(key string, title string, content string, priority int, budgetRunes int) {
	if r == nil {
		return
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = fmt.Sprintf("context-%d", len(r.contextBlocks)+1)
	}
	if priority == 0 {
		priority = RuntimeContextPriorityNormal
	}

	block := RuntimeContextBlock{
		Key:         key,
		Title:       strings.TrimSpace(title),
		Content:     strings.TrimSpace(content),
		Priority:    priority,
		BudgetRunes: budgetRunes,
	}
	for idx, existing := range r.contextBlocks {
		if existing.Key == key {
			if block.Content == "" {
				r.contextBlocks = append(r.contextBlocks[:idx], r.contextBlocks[idx+1:]...)
				return
			}
			r.contextBlocks[idx] = block
			return
		}
	}
	if block.Content != "" {
		r.contextBlocks = append(r.contextBlocks, block)
	}
}

func (r *RunContext) AddContextBlock(title string, content string) {
	r.SetContextBlock("", title, content)
}

func (r *RunContext) AddContextBlockWithPriority(title string, content string, priority int, budgetRunes int) {
	r.SetContextBlockWithPriority("", title, content, priority, budgetRunes)
}

func (r *RunContext) ContextBlocks() []RuntimeContextBlock {
	if r == nil {
		return nil
	}
	blocks := make([]RuntimeContextBlock, len(r.contextBlocks))
	copy(blocks, r.contextBlocks)
	return blocks
}

func (r *RunContext) DisableTool(name tool.AgentTool, reason string) {
	if name == "" {
		return
	}
	if r.disabledTools == nil {
		r.disabledTools = make(map[tool.AgentTool]string)
	}
	r.disabledTools[name] = strings.TrimSpace(reason)
}

func (r *RunContext) EnableTool(name tool.AgentTool) {
	delete(r.disabledTools, name)
}

func (r *RunContext) ToolDisabled(name tool.AgentTool) (string, bool) {
	reason, ok := r.disabledTools[name]
	return reason, ok
}

func (r *RunContext) ToolResults() []ToolExecution {
	if r == nil || len(r.toolResults) == 0 {
		return nil
	}
	results := make([]ToolExecution, len(r.toolResults))
	copy(results, r.toolResults)
	return results
}

func (r *RunContext) recordToolResult(result ToolResult) {
	if r == nil {
		return
	}
	r.toolResults = append(r.toolResults, ToolExecution{
		Call:    result.Call,
		Content: result.Content,
		Error:   result.ErrorString(),
	})
}

func (r *RunContext) RuntimeContextText() string {
	return renderRuntimeContext(r.ContextBlocks(), 0)
}

func (r *RunContext) RuntimeContextTextWithBudget(budgetRunes int) string {
	return renderRuntimeContext(r.ContextBlocks(), budgetRunes)
}

func renderRuntimeContext(blocks []RuntimeContextBlock, budgetRunes int) string {
	if len(blocks) == 0 {
		return ""
	}

	type indexedBlock struct {
		block RuntimeContextBlock
		index int
	}
	indexed := make([]indexedBlock, 0, len(blocks))
	for idx, block := range blocks {
		if strings.TrimSpace(block.Content) == "" {
			continue
		}
		if block.Priority == 0 {
			block.Priority = RuntimeContextPriorityNormal
		}
		indexed = append(indexed, indexedBlock{block: block, index: idx})
	}
	if len(indexed) == 0 {
		return ""
	}
	sort.SliceStable(indexed, func(i, j int) bool {
		if indexed[i].block.Priority != indexed[j].block.Priority {
			return indexed[i].block.Priority > indexed[j].block.Priority
		}
		return indexed[i].index < indexed[j].index
	})

	sections := []string{"# Runtime Context"}
	for _, item := range indexed {
		block := item.block
		title := block.Title
		if title == "" {
			title = block.Key
		}
		content := trimRunesWithNotice(block.Content, block.BudgetRunes, "runtime context block")
		section := "\n\n## " + title + "\n" + content
		if budgetRunes > 0 && runeLen(strings.Join(sections, ""))+runeLen(section) > budgetRunes {
			remaining := budgetRunes - runeLen(strings.Join(sections, ""))
			if remaining <= runeLen("\n\n## "+title+"\n") {
				if block.Priority < RuntimeContextPriorityCritical {
					continue
				}
				remaining = runeLen("\n\n## " + title + "\n")
			}
			contentBudget := remaining - runeLen("\n\n## "+title+"\n")
			if contentBudget <= 0 {
				contentBudget = 1
			}
			section = "\n\n## " + title + "\n" + trimRunesWithNotice(content, contentBudget, "runtime context budget")
		}
		if budgetRunes > 0 && runeLen(strings.Join(sections, ""))+runeLen(section) > budgetRunes && block.Priority < RuntimeContextPriorityCritical {
			continue
		}
		sections = append(sections, section)
	}
	if len(sections) == 1 {
		return ""
	}
	return strings.Join(sections, "")
}

func trimRunesWithNotice(content string, limit int, label string) string {
	content = strings.TrimSpace(content)
	if limit <= 0 || runeLen(content) <= limit {
		return content
	}
	notice := fmt.Sprintf("\n\n[%s truncated to %d characters.]", label, limit)
	noticeLen := runeLen(notice)
	if limit <= noticeLen {
		return string([]rune(content)[:maxInt(1, limit)])
	}
	return string([]rune(content)[:limit-noticeLen]) + notice
}

func runeLen(value string) int {
	return len([]rune(value))
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

type ToolResult struct {
	Call    schema.ToolCall
	Content string
	Err     error
}

type ToolExecution struct {
	Call    schema.ToolCall
	Content string
	Error   string
}

func (r ToolResult) ErrorString() string {
	if r.Err == nil {
		return ""
	}
	return r.Err.Error()
}

type Hook interface{}

type NamedHook interface {
	HookName() string
}

type BeforeRunHook interface {
	BeforeRun(ctx context.Context, run *RunContext) error
}

type AfterUserMessageHook interface {
	AfterUserMessage(ctx context.Context, run *RunContext) error
}

type BeforeModelCallHook interface {
	BeforeModelCall(ctx context.Context, run *RunContext) error
}

type AfterModelResponseHook interface {
	AfterModelResponse(ctx context.Context, run *RunContext, response *schema.Message) error
}

type BeforeToolCallHook interface {
	BeforeToolCall(ctx context.Context, run *RunContext, call *schema.ToolCall) error
}

type AfterToolCallHook interface {
	AfterToolCall(ctx context.Context, run *RunContext, result *ToolResult) error
}

type AfterRunHook interface {
	AfterRun(ctx context.Context, run *RunContext, result AgentRunResult, runErr error) error
}

type FinalizeRunHook interface {
	FinalizeRun(ctx context.Context, run *RunContext, result *AgentRunResult) error
}

type HookManager struct {
	hooks []Hook
}

func NewHookManager(hooks ...Hook) *HookManager {
	manager := &HookManager{}
	manager.Append(hooks...)
	return manager
}

func (m *HookManager) Append(hooks ...Hook) {
	if m == nil {
		return
	}
	for _, hook := range hooks {
		if hook != nil {
			m.hooks = append(m.hooks, hook)
		}
	}
}

func (m *HookManager) Hooks() []Hook {
	if m == nil {
		return nil
	}
	hooks := make([]Hook, len(m.hooks))
	copy(hooks, m.hooks)
	return hooks
}

func (m *HookManager) BeforeRun(ctx context.Context, run *RunContext) error {
	if m == nil {
		return nil
	}
	for _, hook := range m.hooks {
		if typed, ok := hook.(BeforeRunHook); ok {
			if err := typed.BeforeRun(ctx, run); err != nil {
				return wrapHookError(hook, "BeforeRun", err)
			}
		}
	}
	return nil
}

func (m *HookManager) AfterUserMessage(ctx context.Context, run *RunContext) error {
	if m == nil {
		return nil
	}
	for _, hook := range m.hooks {
		if typed, ok := hook.(AfterUserMessageHook); ok {
			if err := typed.AfterUserMessage(ctx, run); err != nil {
				return wrapHookError(hook, "AfterUserMessage", err)
			}
		}
	}
	return nil
}

func (m *HookManager) BeforeModelCall(ctx context.Context, run *RunContext) error {
	if m == nil {
		return nil
	}
	for _, hook := range m.hooks {
		if typed, ok := hook.(BeforeModelCallHook); ok {
			if err := typed.BeforeModelCall(ctx, run); err != nil {
				return wrapHookError(hook, "BeforeModelCall", err)
			}
		}
	}
	return nil
}

func (m *HookManager) AfterModelResponse(ctx context.Context, run *RunContext, response *schema.Message) error {
	if m == nil {
		return nil
	}
	for _, hook := range m.hooks {
		if typed, ok := hook.(AfterModelResponseHook); ok {
			if err := typed.AfterModelResponse(ctx, run, response); err != nil {
				return wrapHookError(hook, "AfterModelResponse", err)
			}
		}
	}
	return nil
}

func (m *HookManager) BeforeToolCall(ctx context.Context, run *RunContext, call *schema.ToolCall) error {
	if m == nil {
		return nil
	}
	for _, hook := range m.hooks {
		if typed, ok := hook.(BeforeToolCallHook); ok {
			if err := typed.BeforeToolCall(ctx, run, call); err != nil {
				return wrapHookError(hook, "BeforeToolCall", err)
			}
		}
	}
	return nil
}

func (m *HookManager) AfterToolCall(ctx context.Context, run *RunContext, result *ToolResult) error {
	if m == nil {
		return nil
	}
	for _, hook := range m.hooks {
		if typed, ok := hook.(AfterToolCallHook); ok {
			if err := typed.AfterToolCall(ctx, run, result); err != nil {
				return wrapHookError(hook, "AfterToolCall", err)
			}
		}
	}
	return nil
}

func (m *HookManager) AfterRun(ctx context.Context, run *RunContext, result AgentRunResult, runErr error) error {
	if m == nil {
		return nil
	}
	for _, hook := range m.hooks {
		if typed, ok := hook.(AfterRunHook); ok {
			if err := typed.AfterRun(ctx, run, result, runErr); err != nil {
				return wrapHookError(hook, "AfterRun", err)
			}
		}
	}
	return nil
}

func (m *HookManager) FinalizeRun(ctx context.Context, run *RunContext, result *AgentRunResult) error {
	if m == nil {
		return nil
	}
	for _, hook := range m.hooks {
		if typed, ok := hook.(FinalizeRunHook); ok {
			if err := typed.FinalizeRun(ctx, run, result); err != nil {
				return wrapHookError(hook, "FinalizeRun", err)
			}
		}
	}
	return nil
}

func wrapHookError(hook Hook, phase string, err error) error {
	return fmt.Errorf("hook %s %s: %w", hookName(hook), phase, err)
}

func hookName(hook Hook) string {
	if named, ok := hook.(NamedHook); ok {
		if name := strings.TrimSpace(named.HookName()); name != "" {
			return name
		}
	}
	t := reflect.TypeOf(hook)
	if t == nil {
		return "unknown"
	}
	return t.String()
}
