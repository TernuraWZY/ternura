package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"ternura/agent"
	"ternura/internal/cron"
	"ternura/tool"
)

const cronRuntimePromptPrefix = "[cron job fired]"

type cronRunner struct {
	server *agentServer
}

func newCronRunner(server *agentServer) *cronRunner {
	return &cronRunner{server: server}
}

func (r *cronRunner) Run(ctx context.Context) {
	r.server.cron.Start()
	defer r.server.cron.Stop()

	for {
		if err := r.runDue(ctx); err != nil {
			log.Printf("cron runner: %v", err)
		}
		delay := r.server.cron.NextWakeDuration(time.Now())
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			stopTimer(timer)
			return
		case <-r.server.cronWake:
			stopTimer(timer)
		case <-timer.C:
		}
	}
}

func (r *cronRunner) runDue(ctx context.Context) error {
	for {
		job, ok, err := r.server.cron.ClaimDue(time.Now())
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		r.server.runCronJob(ctx, job)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

func (s *agentServer) wakeCronRunner() {
	if s == nil || s.cronWake == nil {
		return
	}
	select {
	case s.cronWake <- struct{}{}:
	default:
	}
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

func wrapCronRuntimePrompt(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return cronRuntimePromptPrefix
	}
	return cronRuntimePromptPrefix + "\n" + message
}

func isCronRuntimePrompt(query string) bool {
	return strings.HasPrefix(strings.TrimSpace(query), cronRuntimePromptPrefix)
}

func unwrapCronRuntimePrompt(query string) string {
	query = strings.TrimSpace(query)
	if !strings.HasPrefix(query, cronRuntimePromptPrefix) {
		return query
	}
	return strings.TrimSpace(strings.TrimPrefix(query, cronRuntimePromptPrefix))
}

func (s *agentServer) runCronJob(ctx context.Context, job cron.Job) {
	s.mu.Lock()
	defer s.mu.Unlock()

	run := newRunLifecycle()
	display := strings.TrimSpace(job.Payload.Message)
	log.Printf("cron job %s started run %s for session %s", job.ID, run.ID, job.Payload.SessionID)
	logRunStart(run)

	if err := s.store.StartScheduledRunForSession(job.Payload.SessionID, run, display); err != nil {
		finished := time.Now()
		logRunFinish(run, runStatusFailed, finished)
		if completeErr := s.cron.RecordRun(context.Background(), job.ID, run.ID, run.StartedAt, err); completeErr != nil {
			log.Printf("complete failed cron job %s: %v", job.ID, completeErr)
		}
		return
	}

	runtimePrompt := wrapCronRuntimePrompt(display)
	cronTool := tool.NewCronTool(s.cronAddForSession(job.Payload.SessionID), s.cronList, s.cronRemove)
	cronTool.SetCronContext(true)
	agent := s.newAgentForSessionWithCron(job.Payload.SessionID, cronTool)
	started := run.StartedAt
	result, err := agent.RunWithTrace(ctx, runtimePrompt)
	finished := time.Now()

	status := runStatusSucceeded
	if err != nil {
		status = runStatusFailed
	}
	logRunFinish(run, status, finished)
	if persistErr := s.store.FinishScheduledRunForSession(job.Payload.SessionID, run, display, runtimePrompt, result, status, finished, err); persistErr != nil {
		log.Printf("persist cron run %s: %v", run.ID, persistErr)
	}
	if completeErr := s.cron.RecordRun(context.Background(), job.ID, run.ID, started, err); completeErr != nil {
		log.Printf("complete cron job %s: %v", job.ID, completeErr)
	}
	if err == nil && job.Payload.Deliver && job.Payload.Delivery != nil {
		s.deliverCronResult(ctx, job, result)
	}

	if job.Payload.SessionID == s.store.CurrentSessionID() {
		s.resetAgentFromSnapshot(s.store.Snapshot())
	}
}

func (s *agentServer) cronAdd(ctx context.Context, params tool.CronAddParams) (tool.CronAddResult, error) {
	return s.cronAddForSession("")(ctx, params)
}

func (s *agentServer) cronAddForSession(sessionID string) tool.CronAddFunc {
	return s.cronAddForSessionWithDelivery(sessionID, nil)
}

func (s *agentServer) cronAddForSessionWithDelivery(sessionID string, delivery *cron.DeliveryTarget) tool.CronAddFunc {
	return func(ctx context.Context, params tool.CronAddParams) (tool.CronAddResult, error) {
		targetSessionID := sessionID
		if targetSessionID == "" {
			targetSessionID = s.store.CurrentSessionID()
		}
		job, err := s.cron.Add(ctx, cron.AddParams{
			Name:           params.Name,
			Message:        params.Message,
			SessionID:      targetSessionID,
			Deliver:        params.Deliver,
			Delivery:       delivery,
			EverySeconds:   params.EverySeconds,
			CronExpr:       params.CronExpr,
			TZ:             params.TZ,
			At:             params.At,
			DelaySeconds:   params.DelaySeconds,
			DeleteAfterRun: params.DelaySeconds > 0 || strings.TrimSpace(params.At) != "",
		})
		if err != nil {
			return tool.CronAddResult{}, err
		}
		nextRun := ""
		if job.State.NextRunAtMS > 0 {
			nextRun = time.UnixMilli(job.State.NextRunAtMS).Format(time.RFC3339Nano)
		}
		log.Printf("cron job %s for %s next %s", job.ID, job.Payload.SessionID, nextRun)
		s.wakeCronRunner()
		return tool.CronAddResult{
			ID:        job.ID,
			Name:      job.Name,
			Message:   job.Payload.Message,
			NextRunAt: nextRun,
		}, nil
	}
}

func (s *agentServer) cronList(ctx context.Context) (string, error) {
	_ = ctx
	jobs := s.cron.List(true)
	if len(jobs) == 0 {
		return "No scheduled jobs.", nil
	}
	lines := make([]string, 0, len(jobs)+1)
	lines = append(lines, "Scheduled jobs:")
	for _, job := range jobs {
		timing := formatCronTiming(job)
		line := fmt.Sprintf("- %s (id: %s, %s, status: %s)", job.Name, job.ID, timing, job.Status())
		if job.State.NextRunAtMS > 0 {
			line += fmt.Sprintf(", next: %s", time.UnixMilli(job.State.NextRunAtMS).Format(time.RFC3339))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

func (s *agentServer) cronRemove(ctx context.Context, jobID string) (string, error) {
	job, err := s.cron.Remove(ctx, jobID)
	if err != nil {
		return "", err
	}
	log.Printf("removed cron job %s", job.ID)
	s.wakeCronRunner()
	return fmt.Sprintf("Removed job %s", job.ID), nil
}

func formatCronTiming(job cron.Job) string {
	switch job.Schedule.Kind {
	case cron.ScheduleEvery:
		sec := job.Schedule.EveryMS / 1000
		return fmt.Sprintf("every %ds", sec)
	case cron.ScheduleCron:
		if job.Schedule.TZ != "" {
			return fmt.Sprintf("cron: %s (%s)", job.Schedule.Expr, job.Schedule.TZ)
		}
		return fmt.Sprintf("cron: %s", job.Schedule.Expr)
	case cron.ScheduleAt:
		return fmt.Sprintf("at %s", time.UnixMilli(job.Schedule.AtMS).Format(time.RFC3339))
	default:
		return job.Schedule.Kind
	}
}

func (s *agentServer) newAgentForSession(sessionID string) *agent.Agent {
	return s.newAgentForSessionWithCron(sessionID, tool.NewCronTool(s.cronAddForSession(sessionID), s.cronList, s.cronRemove))
}

func (s *agentServer) newAgentForSessionWithCron(sessionID string, cronTool *tool.CronTool) *agent.Agent {
	sessionAgent := newAgentFromSkillRegistry(s.modelConf, s.newSkillRegistry(sessionID, cronTool))

	snapshot := s.store.Snapshot()
	if session := findSession(snapshot.Sessions, sessionID); session != nil && len(session.Messages) > 0 {
		restoreAgentConversation(sessionAgent, session.Messages)
	}
	return sessionAgent
}
