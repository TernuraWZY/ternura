package main

import (
	"context"
	"log"
	"time"

	"ternura"
)

const defaultSchedulePollInterval = time.Second

type scheduleRunner struct {
	server   *agentServer
	interval time.Duration
}

func newScheduleRunner(server *agentServer) *scheduleRunner {
	return &scheduleRunner{
		server:   server,
		interval: defaultSchedulePollInterval,
	}
}

func (r *scheduleRunner) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		if err := r.runDue(ctx); err != nil {
			log.Printf("schedule runner: %v", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *scheduleRunner) runDue(ctx context.Context) error {
	for {
		task, ok, err := r.server.schedules.ClaimDue(time.Now())
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		r.server.runScheduledTask(ctx, task)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

func (s *agentServer) runScheduledTask(ctx context.Context, task scheduledTask) {
	s.mu.Lock()
	defer s.mu.Unlock()

	run := newRunLifecycle()
	log.Printf("scheduled task %s started run %s for session %s", task.ID, run.ID, task.SessionID)
	logRunStart(run)

	if err := s.store.StartRunForSession(task.SessionID, run, task.Prompt); err != nil {
		finished := time.Now()
		logRunFinish(run, runStatusFailed, finished)
		if completeErr := s.schedules.Complete(context.Background(), task.ID, run.ID, err); completeErr != nil {
			log.Printf("complete failed schedule %s: %v", task.ID, completeErr)
		}
		return
	}

	agent := s.newAgentForSession(task.SessionID)
	result, err := agent.RunWithTrace(ctx, task.Prompt)
	finished := time.Now()

	status := runStatusSucceeded
	if err != nil {
		status = runStatusFailed
	}
	logRunFinish(run, status, finished)
	if persistErr := s.store.FinishRunForSession(task.SessionID, run, task.Prompt, result, status, finished, err); persistErr != nil {
		log.Printf("persist scheduled run %s: %v", run.ID, persistErr)
	}
	if completeErr := s.schedules.Complete(context.Background(), task.ID, run.ID, err); completeErr != nil {
		log.Printf("complete schedule %s: %v", task.ID, completeErr)
	}

	if task.SessionID == s.store.CurrentSessionID() {
		s.resetAgentFromSnapshot(s.store.Snapshot())
	}
}

func (s *agentServer) newAgentForSession(sessionID string) *ternura.Agent {
	agent := ternura.NewAgent(
		s.modelConf,
		ternura.TernuraAgentSystemPrompt,
		newAgentTools(
			s.updateTodosForSession(sessionID),
			s.rememberMemory,
			s.forgetMemory,
			s.scheduleTaskForSession(sessionID),
			s.cancelScheduledTask,
		),
		ternura.WithHooks(
			newCurrentTimeHook(),
			newMemoryHook(s.memory, func() string { return sessionID }),
			newScheduleGuidanceHook(),
			newStateGuardHook(s.schedules),
		),
	)

	snapshot := s.store.Snapshot()
	if session := findSession(snapshot.Sessions, sessionID); session != nil && len(session.Messages) > 0 {
		restoreAgentConversation(agent, session.Messages)
	}
	return agent
}
