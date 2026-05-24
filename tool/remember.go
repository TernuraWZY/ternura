package tool

import (
	"context"
	"fmt"
	"strings"
)

const (
	MemoryCategoryPreference  = "preference"
	MemoryCategoryProfile     = "profile"
	MemoryCategoryProject     = "project"
	MemoryCategoryInstruction = "instruction"
	MemoryCategoryFact        = "fact"
	MemoryCategoryOther       = "other"
)

type MemoryItem struct {
	Category string `json:"category" jsonschema:"required,enum=preference,enum=profile,enum=project,enum=instruction,enum=fact,enum=other" jsonschema_description:"Memory category."`
	Content  string `json:"content" jsonschema:"required" jsonschema_description:"Concise durable memory to store. Write it as a standalone sentence."`
	Source   string `json:"source,omitempty" jsonschema_description:"Optional short reason or source, such as explicit user preference."`
}

type MemoryResult struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Content  string `json:"content"`
}

type RememberFunc func(ctx context.Context, item MemoryItem) (MemoryResult, error)

type ForgetMemoryFunc func(ctx context.Context, id string) error

type RememberTool struct {
	*agentTool
	remember RememberFunc
}

type ForgetMemoryTool struct {
	*agentTool
	forget ForgetMemoryFunc
}

type forgetMemoryToolParam struct {
	ID string `json:"id" jsonschema:"required" jsonschema_description:"The memory id to delete, such as memory-20260520T120000000000000."`
}

func NewRememberTool(remember RememberFunc) *RememberTool {
	if remember == nil {
		remember = func(context.Context, MemoryItem) (MemoryResult, error) {
			return MemoryResult{}, nil
		}
	}
	t := &RememberTool{remember: remember}
	t.agentTool = newAgentTool(
		AgentToolRemember,
		"Store a durable long-term memory about the user, project, or agent behavior. Use only for stable, reusable facts, explicit preferences, or standing instructions; never store secrets or transient chat details.",
		t.run,
	)
	return t
}

func NewForgetMemoryTool(forget ForgetMemoryFunc) *ForgetMemoryTool {
	if forget == nil {
		forget = func(context.Context, string) error { return nil }
	}
	t := &ForgetMemoryTool{forget: forget}
	t.agentTool = newAgentTool(
		AgentToolForgetMemory,
		"Delete a long-term memory by id when it is stale, wrong, or the user asks to forget it.",
		t.run,
	)
	return t
}

func (t *RememberTool) run(ctx context.Context, p MemoryItem) (string, error) {
	item, err := NormalizeMemoryItem(p)
	if err != nil {
		return "", err
	}
	result, err := t.remember(ctx, item)
	if err != nil {
		return "", err
	}
	if result.ID == "" {
		result.ID = "memory"
	}
	if result.Category == "" {
		result.Category = item.Category
	}
	if result.Content == "" {
		result.Content = item.Content
	}
	return fmt.Sprintf("Memory stored: [%s] %s (%s)", result.ID, result.Content, result.Category), nil
}

func (t *ForgetMemoryTool) run(ctx context.Context, p forgetMemoryToolParam) (string, error) {
	id := strings.TrimSpace(p.ID)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := t.forget(ctx, id); err != nil {
		return "", err
	}
	return fmt.Sprintf("Memory forgotten: %s", id), nil
}

func NormalizeMemoryItem(item MemoryItem) (MemoryItem, error) {
	content := strings.Join(strings.Fields(item.Content), " ")
	if content == "" {
		return MemoryItem{}, fmt.Errorf("content is required")
	}
	category, err := normalizeMemoryCategory(item.Category)
	if err != nil {
		return MemoryItem{}, err
	}
	source := strings.Join(strings.Fields(item.Source), " ")
	return MemoryItem{
		Category: category,
		Content:  content,
		Source:   source,
	}, nil
}

func normalizeMemoryCategory(category string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "", MemoryCategoryOther:
		return MemoryCategoryOther, nil
	case MemoryCategoryPreference:
		return MemoryCategoryPreference, nil
	case MemoryCategoryProfile:
		return MemoryCategoryProfile, nil
	case MemoryCategoryProject:
		return MemoryCategoryProject, nil
	case MemoryCategoryInstruction:
		return MemoryCategoryInstruction, nil
	case MemoryCategoryFact:
		return MemoryCategoryFact, nil
	default:
		return "", fmt.Errorf("category must be one of %s, %s, %s, %s, %s, %s", MemoryCategoryPreference, MemoryCategoryProfile, MemoryCategoryProject, MemoryCategoryInstruction, MemoryCategoryFact, MemoryCategoryOther)
	}
}
