package ternura

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"ternura/tool"
)

type RunMode string

const (
	RunModeSync      RunMode = "sync"
	RunModeStreaming RunMode = "streaming"
)

type RuntimeContextBlock struct {
	Key     string
	Title   string
	Content string
}

type RunContext struct {
	Query          string
	Mode           RunMode
	ModelCallCount int
	ToolCallCount  int
	Metadata       map[string]any

	contextBlocks []RuntimeContextBlock
	disabledTools map[tool.AgentTool]string
}

func NewRunContext(query string, mode RunMode) *RunContext {
	return &RunContext{
		Query:         query,
		Mode:          mode,
		Metadata:      make(map[string]any),
		disabledTools: make(map[tool.AgentTool]string),
	}
}

func (r *RunContext) SetContextBlock(key string, title string, content string) {
	key = strings.TrimSpace(key)
	if key == "" {
		key = fmt.Sprintf("context-%d", len(r.contextBlocks)+1)
	}

	block := RuntimeContextBlock{
		Key:     key,
		Title:   strings.TrimSpace(title),
		Content: strings.TrimSpace(content),
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

func (r *RunContext) ContextBlocks() []RuntimeContextBlock {
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

func (r *RunContext) RuntimeContextText() string {
	if len(r.contextBlocks) == 0 {
		return ""
	}

	sections := []string{"# Runtime Context"}
	for _, block := range r.contextBlocks {
		if strings.TrimSpace(block.Content) == "" {
			continue
		}
		title := block.Title
		if title == "" {
			title = block.Key
		}
		sections = append(sections, "", "## "+title, block.Content)
	}
	if len(sections) == 1 {
		return ""
	}
	return strings.Join(sections, "\n")
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type ToolResult struct {
	Call    ToolCall
	Content string
	Err     error
}

func (r ToolResult) ErrorString() string {
	if r.Err == nil {
		return ""
	}
	return r.Err.Error()
}

type ModelResponse struct {
	Content    string
	RawContent string
	ToolCalls  []ToolCall
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
	AfterModelResponse(ctx context.Context, run *RunContext, response ModelResponse) error
}

type BeforeToolCallHook interface {
	BeforeToolCall(ctx context.Context, run *RunContext, call *ToolCall) error
}

type AfterToolCallHook interface {
	AfterToolCall(ctx context.Context, run *RunContext, result *ToolResult) error
}

type AfterRunHook interface {
	AfterRun(ctx context.Context, run *RunContext, result AgentRunResult, runErr error) error
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

func (m *HookManager) AfterModelResponse(ctx context.Context, run *RunContext, response ModelResponse) error {
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

func (m *HookManager) BeforeToolCall(ctx context.Context, run *RunContext, call *ToolCall) error {
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
