package app

import (
	"context"
	"log"
	"strings"
	"time"

	"ternura/agent"
	"ternura/tool"
)

type agentSession struct {
	server    *agentServer
	sessionID string
	cronTool  *tool.CronTool
}

type agentSessionRunKind string

const (
	agentSessionRunUser      agentSessionRunKind = runTriggerKindUser
	agentSessionRunScheduled agentSessionRunKind = runTriggerKindSchedule
)

type agentSessionRunRequest struct {
	Kind           agentSessionRunKind
	DisplayMessage string
	RuntimePrompt  string
	DirectResult   *agent.AgentRunResult
	DirectErr      error
	OmitMessages   bool
}

type agentSessionRunOutcome struct {
	Run    runLifecycle
	Result agent.AgentRunResult
	Err    error
}

func (s *agentServer) newAgentSession(sessionID string, cronTool *tool.CronTool) *agentSession {
	return &agentSession{
		server:    s,
		sessionID: sessionID,
		cronTool:  cronTool,
	}
}

func (s *agentSession) agent() *agent.Agent {
	sessionAgent := newAgentFromSkillRegistry(s.server.modelConf, s.server.newSkillRegistry(s.sessionID, s.cronTool))

	snapshot := s.server.store.Snapshot()
	if session := findSession(snapshot.Sessions, s.sessionID); session != nil && len(session.Messages) > 0 {
		_ = restoreAgentConversation(sessionAgent, session.Messages)
	}
	return sessionAgent
}

func (s *agentSession) runWithTrace(ctx context.Context, runtimePrompt string) (agent.AgentRunResult, error) {
	return s.agent().RunWithTrace(ctx, runtimePrompt)
}

func (s *agentSession) run(ctx context.Context, request agentSessionRunRequest) agentSessionRunOutcome {
	request = normalizeAgentSessionRunRequest(request)
	run, err := s.startRun(request)
	if err != nil {
		return agentSessionRunOutcome{Run: run, Err: err}
	}

	result := agent.AgentRunResult{}
	runErr := request.DirectErr
	if request.DirectResult != nil {
		result = *request.DirectResult
	} else {
		result, runErr = s.runWithTrace(ctx, request.RuntimePrompt)
	}
	if runErr != nil {
		result = failedAgentRunResult(result, runErr)
	}
	s.finishRun(run, request, result, runErr)
	return agentSessionRunOutcome{
		Run:    run,
		Result: result,
		Err:    runErr,
	}
}

func normalizeAgentSessionRunRequest(request agentSessionRunRequest) agentSessionRunRequest {
	if request.Kind == "" {
		request.Kind = agentSessionRunUser
	}
	request.DisplayMessage = strings.TrimSpace(request.DisplayMessage)
	request.RuntimePrompt = strings.TrimSpace(request.RuntimePrompt)
	if request.RuntimePrompt == "" {
		request.RuntimePrompt = request.DisplayMessage
	}
	return request
}

func failedAgentRunResult(result agent.AgentRunResult, err error) agent.AgentRunResult {
	if strings.TrimSpace(result.Content) != "" {
		return result
	}
	message := "这轮 Agent 没有顺利完成，我已经停止继续运行，避免它继续循环。"
	if err != nil {
		detail := strings.TrimSpace(err.Error())
		switch {
		case strings.Contains(detail, "exceeds max steps"):
			message = "这轮 Agent 在工具和推理循环里没有及时收口，已经达到最大步骤数，所以我停止了继续运行。\n\n可以把问题缩小一点，或者直接指定需要查询的信息，我会重新跑一轮。"
		case detail != "":
			message += "\n\n错误信息：`" + detail + "`"
		}
	}
	result.Content = message
	result.RawContent = message
	result.Trace = append(result.Trace, agent.AgentTraceItem{
		Type:    "error",
		Title:   "Run failed",
		Content: message,
	})
	return result
}

func (s *agentSession) startRun(request agentSessionRunRequest) (runLifecycle, error) {
	switch request.Kind {
	case agentSessionRunScheduled:
		return s.startScheduledRun(request.DisplayMessage)
	default:
		return s.startUserRun(request.DisplayMessage), nil
	}
}

func (s *agentSession) finishRun(run runLifecycle, request agentSessionRunRequest, result agent.AgentRunResult, runErr error) {
	switch {
	case request.Kind == agentSessionRunScheduled:
		s.finishScheduledRun(run, request.DisplayMessage, request.RuntimePrompt, result, runErr)
	case request.OmitMessages:
		s.finishUserRunWithoutMessages(run, request.DisplayMessage, result)
	default:
		s.finishUserRun(run, request.DisplayMessage, result, runErr)
	}
}

func (s *agentSession) startUserRun(message string) runLifecycle {
	run := newRunLifecycle()
	logRunStart(run)
	if err := s.server.store.StartRunForSession(s.sessionID, run, message); err != nil {
		log.Printf("persist run start %s: %v", run.ID, err)
	}
	return run
}

func (s *agentSession) startScheduledRun(displayPrompt string) (runLifecycle, error) {
	run := newRunLifecycle()
	logRunStart(run)
	if err := s.server.store.StartScheduledRunForSession(s.sessionID, run, displayPrompt); err != nil {
		finished := time.Now()
		logRunFinish(run, runStatusFailed, finished)
		return run, err
	}
	return run, nil
}

func (s *agentSession) finishUserRun(run runLifecycle, message string, result agent.AgentRunResult, runErr error) {
	finished := time.Now()
	status := runStatusSucceeded
	if runErr != nil {
		status = runStatusFailed
	}
	logRunFinish(run, status, finished)
	if err := s.server.store.FinishRunForSession(s.sessionID, run, message, result, status, finished, runErr); err != nil {
		log.Printf("persist run %s: %v", run.ID, err)
	}
}

func (s *agentSession) finishUserRunWithoutMessages(run runLifecycle, message string, result agent.AgentRunResult) {
	finished := time.Now()
	logRunFinish(run, runStatusSucceeded, finished)
	if err := s.server.store.FinishRunForSessionWithoutMessages(s.sessionID, run, message, result, runStatusSucceeded, finished, nil); err != nil {
		log.Printf("persist run %s: %v", run.ID, err)
	}
}

func (s *agentSession) finishScheduledRun(run runLifecycle, displayPrompt string, runtimePrompt string, result agent.AgentRunResult, runErr error) {
	finished := time.Now()
	status := runStatusSucceeded
	if runErr != nil {
		status = runStatusFailed
	}
	logRunFinish(run, status, finished)
	if err := s.server.store.FinishScheduledRunForSession(s.sessionID, run, displayPrompt, runtimePrompt, result, status, finished, runErr); err != nil {
		log.Printf("persist cron run %s: %v", run.ID, err)
	}
}
