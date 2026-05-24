package tool

import (
	"context"
	"fmt"
	"strings"
)

const (
	TodoStatusPending    = "pending"
	TodoStatusInProgress = "in_progress"
	TodoStatusDone       = "done"
	TodoStatusBlocked    = "blocked"
	TodoStatusCancelled  = "cancelled"
)

type TodoItem struct {
	ID      string `json:"id,omitempty" jsonschema_description:"Stable short identifier for this todo, such as todo-1."`
	Content string `json:"content" jsonschema:"required" jsonschema_description:"Concrete task step written for the user."`
	Status  string `json:"status" jsonschema:"required,enum=pending,enum=in_progress,enum=done,enum=blocked,enum=cancelled" jsonschema_description:"Current task status."`
}

type UpdateTodosFunc func(ctx context.Context, todos []TodoItem) error

type UpdateTodosTool struct {
	*agentTool
	update UpdateTodosFunc
}

type updateTodosToolParam struct {
	Todos []TodoItem `json:"todos" jsonschema:"required" jsonschema_description:"The complete ordered todo list for the current session. Send an empty array to clear the list."`
}

func NewUpdateTodosTool(update UpdateTodosFunc) *UpdateTodosTool {
	if update == nil {
		update = func(context.Context, []TodoItem) error { return nil }
	}
	t := &UpdateTodosTool{update: update}
	t.agentTool = newAgentTool(
		AgentToolUpdateTodos,
		"Replace the current session todo list with the complete ordered task plan. Use this for multi-step tasks and whenever task status changes.",
		t.run,
	)
	return t
}

func (t *UpdateTodosTool) run(ctx context.Context, p updateTodosToolParam) (string, error) {
	todos, err := normalizeTodoItems(p.Todos)
	if err != nil {
		return "", err
	}
	if err := t.update(ctx, todos); err != nil {
		return "", err
	}

	return formatTodoSummary(todos), nil
}

func normalizeTodoItems(items []TodoItem) ([]TodoItem, error) {
	normalized := make([]TodoItem, 0, len(items))
	seenIDs := make(map[string]int)
	for idx, item := range items {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			return nil, fmt.Errorf("todo %d content is required", idx+1)
		}

		status, err := normalizeTodoStatus(item.Status)
		if err != nil {
			return nil, fmt.Errorf("todo %d status: %w", idx+1, err)
		}

		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = fmt.Sprintf("todo-%d", idx+1)
		}
		id = uniqueTodoID(id, seenIDs)

		normalized = append(normalized, TodoItem{
			ID:      id,
			Content: content,
			Status:  status,
		})
	}
	return normalized, nil
}

func normalizeTodoStatus(status string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "todo", TodoStatusPending:
		return TodoStatusPending, nil
	case "in-progress", "in progress", "doing", "running", TodoStatusInProgress:
		return TodoStatusInProgress, nil
	case "complete", "completed", TodoStatusDone:
		return TodoStatusDone, nil
	case TodoStatusBlocked:
		return TodoStatusBlocked, nil
	case "canceled", TodoStatusCancelled:
		return TodoStatusCancelled, nil
	default:
		return "", fmt.Errorf("must be one of %s, %s, %s, %s, %s", TodoStatusPending, TodoStatusInProgress, TodoStatusDone, TodoStatusBlocked, TodoStatusCancelled)
	}
}

func uniqueTodoID(id string, seenIDs map[string]int) string {
	count := seenIDs[id]
	if count == 0 {
		seenIDs[id] = 1
		return id
	}

	seenIDs[id] = count + 1
	candidate := fmt.Sprintf("%s-%d", id, count+1)
	for seenIDs[candidate] > 0 {
		count++
		seenIDs[id] = count + 1
		candidate = fmt.Sprintf("%s-%d", id, count+1)
	}
	seenIDs[candidate] = 1
	return candidate
}

func formatTodoSummary(todos []TodoItem) string {
	if len(todos) == 0 {
		return "Todo list cleared."
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "Todo list updated (%d items):", len(todos))
	for _, todo := range todos {
		fmt.Fprintf(&builder, "\n- [%s] %s: %s", todo.Status, todo.ID, todo.Content)
	}
	return builder.String()
}
