package tool

import (
	"context"
	"strings"
	"testing"
)

func TestUpdateTodosToolNormalizesAndCallsUpdater(t *testing.T) {
	var captured []TodoItem
	todosTool := NewUpdateTodosTool(func(_ context.Context, todos []TodoItem) error {
		captured = append([]TodoItem(nil), todos...)
		return nil
	})

	result, err := todosTool.Execute(context.Background(), `{"todos":[{"content":"Inspect current prompt","status":"running"},{"id":"ship","content":"Update README","status":"completed"}]}`)
	if err != nil {
		t.Fatalf("execute update_todos: %v", err)
	}

	if len(captured) != 2 {
		t.Fatalf("captured todos = %d, want 2", len(captured))
	}
	if captured[0].ID != "todo-1" || captured[0].Status != TodoStatusInProgress {
		t.Fatalf("first todo = %+v", captured[0])
	}
	if captured[1].ID != "ship" || captured[1].Status != TodoStatusDone {
		t.Fatalf("second todo = %+v", captured[1])
	}
	if !strings.Contains(result, "Todo list updated (2 items)") {
		t.Fatalf("result = %q", result)
	}
}

func TestUpdateTodosToolRejectsInvalidStatus(t *testing.T) {
	todosTool := NewUpdateTodosTool(nil)

	_, err := todosTool.Execute(context.Background(), `{"todos":[{"content":"Unknown state","status":"later"}]}`)
	if err == nil {
		t.Fatalf("expected invalid status error")
	}
}
