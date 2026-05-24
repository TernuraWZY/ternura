package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
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
	Category string `json:"category"`
	Content  string `json:"content"`
	Source   string `json:"source,omitempty"`
}

type MemoryResult struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Content  string `json:"content"`
}

type RememberFunc func(ctx context.Context, item MemoryItem) (MemoryResult, error)

type ForgetMemoryFunc func(ctx context.Context, id string) error

type RememberTool struct {
	remember RememberFunc
}

type ForgetMemoryTool struct {
	forget ForgetMemoryFunc
}

func NewRememberTool(remember RememberFunc) *RememberTool {
	if remember == nil {
		remember = func(context.Context, MemoryItem) (MemoryResult, error) {
			return MemoryResult{}, nil
		}
	}
	return &RememberTool{remember: remember}
}

func NewForgetMemoryTool(forget ForgetMemoryFunc) *ForgetMemoryTool {
	if forget == nil {
		forget = func(context.Context, string) error { return nil }
	}
	return &ForgetMemoryTool{forget: forget}
}

func (t *RememberTool) ToolName() AgentTool {
	return AgentToolRemember
}

func (t *RememberTool) Info(context.Context) (*schema.ToolInfo, error) {
	return NewToolInfo(
		AgentToolRemember,
		"Store a durable long-term memory about the user, project, or agent behavior. Use only for stable, reusable facts, explicit preferences, or standing instructions; never store secrets or transient chat details.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"category": map[string]any{
					"type":        "string",
					"description": "Memory category.",
					"enum":        []string{MemoryCategoryPreference, MemoryCategoryProfile, MemoryCategoryProject, MemoryCategoryInstruction, MemoryCategoryFact, MemoryCategoryOther},
				},
				"content": map[string]any{
					"type":        "string",
					"description": "Concise durable memory to store. Write it as a standalone sentence.",
				},
				"source": map[string]any{
					"type":        "string",
					"description": "Optional short reason or source, such as explicit user preference.",
				},
			},
			"required":             []string{"category", "content"},
			"additionalProperties": false,
		},
	)
}

func (t *RememberTool) Execute(ctx context.Context, argumentsInJSON string) (string, error) {
	p := MemoryItem{}
	if err := json.Unmarshal([]byte(argumentsInJSON), &p); err != nil {
		return "", err
	}

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

func (t *ForgetMemoryTool) ToolName() AgentTool {
	return AgentToolForgetMemory
}

func (t *ForgetMemoryTool) Info(context.Context) (*schema.ToolInfo, error) {
	return NewToolInfo(
		AgentToolForgetMemory,
		"Delete a long-term memory by id when it is stale, wrong, or the user asks to forget it.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{
					"type":        "string",
					"description": "The memory id to delete, such as memory-20260520T120000000000000.",
				},
			},
			"required":             []string{"id"},
			"additionalProperties": false,
		},
	)
}

func (t *ForgetMemoryTool) Execute(ctx context.Context, argumentsInJSON string) (string, error) {
	p := struct {
		ID string `json:"id"`
	}{}
	if err := json.Unmarshal([]byte(argumentsInJSON), &p); err != nil {
		return "", err
	}
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
