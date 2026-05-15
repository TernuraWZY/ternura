package main

import (
	"context"
	"log"

	"ternura/tool"
)

func newAgentTools(updateTodos tool.UpdateTodosFunc) []tool.Tool {
	return []tool.Tool{
		tool.NewReadTool(),
		tool.NewEditTool(),
		tool.NewWriteTool(),
		tool.NewBashTool(),
		tool.NewUpdateTodosTool(updateTodos),
	}
}

func (s *agentServer) updateTodos(_ context.Context, todos []tool.TodoItem) error {
	persisted := make([]persistedTodo, 0, len(todos))
	for _, todo := range todos {
		persisted = append(persisted, persistedTodo{
			ID:      todo.ID,
			Content: todo.Content,
			Status:  todo.Status,
		})
	}

	snapshot, err := s.store.ReplaceTodos(persisted)
	if err != nil {
		return err
	}
	if session, ok := currentSessionFromSnapshot(snapshot); ok {
		log.Printf("updated %d todos for %s", len(session.Todos), session.SessionID)
	}
	return nil
}
