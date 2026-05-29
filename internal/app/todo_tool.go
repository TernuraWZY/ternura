package app

import (
	"context"
	"log"

	"ternura/tool"
)

func newAgentTools(
	updateTodos tool.UpdateTodosFunc,
	remember tool.RememberFunc,
	forgetMemory tool.ForgetMemoryFunc,
	cronTool *tool.CronTool,
) []tool.Tool {
	return []tool.Tool{
		tool.NewReadTool(),
		tool.NewEditTool(),
		tool.NewWriteTool(),
		tool.NewBashTool(),
		tool.NewUpdateTodosTool(updateTodos),
		tool.NewRememberTool(remember),
		tool.NewForgetMemoryTool(forgetMemory),
		cronTool,
		tool.NewWebFetchTool(),
	}
}

func (s *agentServer) updateTodos(ctx context.Context, todos []tool.TodoItem) error {
	return s.updateTodosForSession("")(ctx, todos)
}

func (s *agentServer) updateTodosForSession(sessionID string) tool.UpdateTodosFunc {
	return func(_ context.Context, todos []tool.TodoItem) error {
		return s.replaceTodosForSession(sessionID, todos)
	}
}

func (s *agentServer) replaceTodosForSession(sessionID string, todos []tool.TodoItem) error {
	persisted := make([]persistedTodo, 0, len(todos))
	for _, todo := range todos {
		persisted = append(persisted, persistedTodo{
			ID:      todo.ID,
			Content: todo.Content,
			Status:  todo.Status,
		})
	}

	snapshot, err := s.store.ReplaceTodosForSession(sessionID, persisted)
	if err != nil {
		return err
	}
	targetSessionID := sessionID
	if targetSessionID == "" {
		targetSessionID = snapshot.CurrentSessionID
	}
	if session := findSession(snapshot.Sessions, targetSessionID); session != nil {
		log.Printf("updated %d todos for %s", len(session.Todos), session.SessionID)
	}
	return nil
}

func (s *agentServer) rememberMemory(ctx context.Context, item tool.MemoryItem) (tool.MemoryResult, error) {
	result, err := s.memory.Remember(ctx, item)
	if err != nil {
		return tool.MemoryResult{}, err
	}
	log.Printf("stored long-term memory %s", result.ID)
	return result, nil
}

func (s *agentServer) forgetMemory(ctx context.Context, id string) error {
	if err := s.memory.Forget(ctx, id); err != nil {
		return err
	}
	log.Printf("forgot long-term memory %s", id)
	return nil
}
