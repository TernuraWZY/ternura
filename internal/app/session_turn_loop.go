package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"ternura/agent"
	"ternura/internal/feishu"
)

const turnPreemptTimeout = 8 * time.Second

type queuedSessionTurn struct {
	request *sessionTurnRequest
}

func (t queuedSessionTurn) Run() runLifecycle {
	if t.request == nil {
		return runLifecycle{}
	}
	return t.request.run
}

func (t queuedSessionTurn) Wait(ctx context.Context) agentSessionRunOutcome {
	if t.request == nil {
		return agentSessionRunOutcome{Err: errors.New("session turn is not initialized")}
	}
	select {
	case outcome := <-t.request.done:
		return outcome
	case <-ctx.Done():
		t.request.cancel()
		select {
		case outcome := <-t.request.done:
			return outcome
		case <-time.After(2 * time.Second):
			return agentSessionRunOutcome{Run: t.request.run, Err: ctx.Err()}
		}
	}
}

type sessionTurnRequest struct {
	server         *agentServer
	session        *agentSession
	run            runLifecycle
	displayMessage string
	rootPrompt     string
	corrections    []string
	runtimePrompt  string
	runCtx         context.Context
	cancel         context.CancelFunc
	done           chan agentSessionRunOutcome

	resultMu sync.Mutex
	result   agent.AgentRunResult
	runErr   error

	finishOnce sync.Once
	lockMu     sync.Mutex
	locked     bool
}

func newSessionTurnRequest(ctx context.Context, server *agentServer, session *agentSession, message string) *sessionTurnRequest {
	run := session.queueUserRun(message)
	baseCtx := context.Background()
	if ctx != nil {
		baseCtx = context.WithoutCancel(ctx)
	}
	baseCtx = withRuntimeRun(baseCtx, run.ID)
	runCtx, cancel := context.WithCancel(baseCtx)
	return &sessionTurnRequest{
		server:         server,
		session:        session,
		run:            run,
		displayMessage: strings.TrimSpace(message),
		rootPrompt:     strings.TrimSpace(message),
		runtimePrompt:  strings.TrimSpace(message),
		runCtx:         runCtx,
		cancel:         cancel,
		done:           make(chan agentSessionRunOutcome, 1),
	}
}

func (r *sessionTurnRequest) setCorrectionBase(base *sessionTurnRequest) {
	if r == nil || base == nil {
		return
	}
	r.rootPrompt = base.rootPrompt
	r.corrections = append(append([]string(nil), base.corrections...), r.displayMessage)
	r.runtimePrompt = renderRunningCorrectionPrompt(r.rootPrompt, r.corrections)
}

func renderRunningCorrectionPrompt(root string, corrections []string) string {
	if len(corrections) == 0 {
		return strings.TrimSpace(root)
	}
	sections := []string{
		"用户在同一任务运行中补充了要求。请重新审视任务，并以最后一条补充为最高优先级完成同一任务。",
		"原始请求：\n" + strings.TrimSpace(root),
		"运行中补充（越靠后优先级越高）：",
	}
	for idx, correction := range corrections {
		sections = append(sections, fmt.Sprintf("%d. %s", idx+1, strings.TrimSpace(correction)))
	}
	return strings.Join(sections, "\n\n")
}

func (r *sessionTurnRequest) setExecutionResult(result agent.AgentRunResult, err error) {
	r.resultMu.Lock()
	r.result = result
	r.runErr = err
	r.resultMu.Unlock()
}

func (r *sessionTurnRequest) executionResult() (agent.AgentRunResult, error) {
	r.resultMu.Lock()
	defer r.resultMu.Unlock()
	return r.result, r.runErr
}

func (r *sessionTurnRequest) acquireSession() {
	lock := r.server.taskSessionLock(r.session.sessionID)
	lock.Lock()
	r.lockMu.Lock()
	r.locked = true
	r.lockMu.Unlock()
}

func (r *sessionTurnRequest) releaseSession() {
	r.lockMu.Lock()
	if !r.locked {
		r.lockMu.Unlock()
		return
	}
	r.locked = false
	r.lockMu.Unlock()
	r.server.taskSessionLock(r.session.sessionID).Unlock()
}

func (r *sessionTurnRequest) finish(result agent.AgentRunResult, runErr error) {
	r.finishOnce.Do(func() {
		if runErr != nil && strings.TrimSpace(result.Content) == "" {
			result = failedAgentRunResult(result, runErr)
		}
		r.session.finishUserRunWithRuntimePrompt(r.run, r.displayMessage, r.runtimePrompt, result, runErr)
		r.releaseSession()
		r.server.untrackTask(r.run.ID)
		r.cancel()
		r.done <- agentSessionRunOutcome{Run: r.run, Result: result, Err: runErr}
		close(r.done)
	})
}

func (r *sessionTurnRequest) finishPreempted() {
	result, _ := r.executionResult()
	result.Content = "已收到后续补充，我已在安全点停止这轮，并切换到最新要求。"
	result.RawContent = result.Content
	result.Trace = append(result.Trace, agent.AgentTraceItem{
		Type:    "control",
		Title:   "Run corrected",
		Content: "A newer message preempted this turn at an Eino ADK safe point.",
		Status:  "cancelled",
	})
	r.finish(result, context.Canceled)
}

func (r *sessionTurnRequest) finishSuperseded() {
	result := agent.AgentRunResult{
		Content:    "这条运行中补充已被更新的补充替代，Agent 将以最新要求为准。",
		RawContent: "这条运行中补充已被更新的补充替代，Agent 将以最新要求为准。",
		Trace: []agent.AgentTraceItem{{
			Type:    "control",
			Title:   "Correction superseded",
			Content: "A newer correction replaced this queued turn before model execution.",
			Status:  "cancelled",
		}},
	}
	r.finish(result, context.Canceled)
}

type sessionTurnLoop struct {
	server    *agentServer
	sessionID string
	loop      *adk.TurnLoop[*sessionTurnRequest, *schema.Message]

	mu      sync.Mutex
	active  *sessionTurnRequest
	pending *sessionTurnRequest
}

func newSessionTurnLoop(server *agentServer, sessionID string) *sessionTurnLoop {
	controller := &sessionTurnLoop{server: server, sessionID: sessionID}
	controller.loop = adk.NewTurnLoop(adk.TurnLoopConfig[*sessionTurnRequest, *schema.Message]{
		GenInput:      controller.genInput,
		PrepareAgent:  controller.prepareAgent,
		OnAgentEvents: controller.onAgentEvents,
	})
	controller.loop.Run(server.serverContext())
	go controller.watchExit()
	return controller
}

func (l *sessionTurnLoop) submit(request *sessionTurnRequest) bool {
	l.mu.Lock()
	base := l.pending
	if base == nil {
		base = l.active
	}
	if base != nil {
		request.setCorrectionBase(base)
	}
	l.pending = request
	l.mu.Unlock()

	if base == nil {
		ok, _ := l.loop.Push(request)
		return ok
	}

	base.server.runtime.Update(base.run.ID, runtimeStageCorrecting, "收到新的补充，正在等待安全点切换", agent.RunMetrics{})
	feishu.ReportProgress(base.runCtx, feishu.ProgressUpdate{
		RunID:  base.run.ID,
		Stage:  feishu.ProgressStageFinishing,
		Detail: "收到新的补充，正在安全收口当前步骤",
	})
	ok, _ := l.loop.Push(request, adk.WithPreemptTimeout[*sessionTurnRequest, *schema.Message](adk.AnySafePoint, turnPreemptTimeout))
	return ok
}

func (l *sessionTurnLoop) genInput(_ context.Context, _ *adk.TurnLoop[*sessionTurnRequest, *schema.Message], items []*sessionTurnRequest) (*adk.GenInputResult[*sessionTurnRequest, *schema.Message], error) {
	valid := make([]*sessionTurnRequest, 0, len(items))
	for _, item := range items {
		if item != nil {
			valid = append(valid, item)
		}
	}
	if len(valid) == 0 {
		return nil, errors.New("Eino TurnLoop received no valid session request")
	}
	selected := valid[len(valid)-1]
	for _, superseded := range valid[:len(valid)-1] {
		superseded.finishSuperseded()
	}

	selected.acquireSession()
	l.mu.Lock()
	l.active = selected
	if l.pending == selected {
		l.pending = nil
	}
	l.mu.Unlock()
	selected.server.runtime.Update(selected.run.ID, runtimeStageStarted, "运行已经开始", agent.RunMetrics{})

	return &adk.GenInputResult[*sessionTurnRequest, *schema.Message]{
		RunCtx: selected.runCtx,
		Input: &adk.AgentInput{
			Messages: []*schema.Message{schema.UserMessage(selected.runtimePrompt)},
		},
		Consumed: []*sessionTurnRequest{selected},
	}, nil
}

func (l *sessionTurnLoop) prepareAgent(_ context.Context, _ *adk.TurnLoop[*sessionTurnRequest, *schema.Message], consumed []*sessionTurnRequest) (adk.Agent, error) {
	if len(consumed) != 1 || consumed[0] == nil {
		return nil, errors.New("Eino TurnLoop expected exactly one session request")
	}
	request := consumed[0]
	return agent.NewADKTraceAgent(request.session.agent(), request.run.ID, request.setExecutionResult), nil
}

func (l *sessionTurnLoop) onAgentEvents(_ context.Context, turn *adk.TurnContext[*sessionTurnRequest, *schema.Message], events *adk.AsyncIterator[*adk.AgentEvent]) error {
	var eventErr error
	for {
		event, ok := events.Next()
		if !ok {
			break
		}
		if event != nil && event.Err != nil {
			eventErr = event.Err
		}
	}
	if turn == nil || len(turn.Consumed) != 1 || turn.Consumed[0] == nil {
		return nil
	}
	request := turn.Consumed[0]
	result, runErr := request.executionResult()
	if runErr == nil {
		runErr = eventErr
	}
	preempted := channelClosed(turn.Preempted)
	if preempted {
		request.finishPreempted()
	} else {
		if isADKCancellation(runErr) || errors.Is(request.runCtx.Err(), context.Canceled) {
			runErr = context.Canceled
			result.Content = "这轮任务已取消。"
			result.RawContent = result.Content
		} else if runErr != nil {
			result = failedAgentRunResult(result, runErr)
		}
		request.finish(result, runErr)
	}

	l.mu.Lock()
	if l.active == request {
		l.active = nil
	}
	l.mu.Unlock()
	return nil
}

func isADKCancellation(err error) bool {
	if err == nil {
		return false
	}
	var cancelErr *adk.CancelError
	return errors.As(err, &cancelErr) || errors.Is(err, context.Canceled)
}

func channelClosed(channel <-chan struct{}) bool {
	if channel == nil {
		return false
	}
	select {
	case <-channel:
		return true
	default:
		return false
	}
}

func (l *sessionTurnLoop) stop(cause string) {
	if l == nil || l.loop == nil {
		return
	}
	l.loop.Stop(
		adk.WithGracefulTimeout(5*time.Second),
		adk.WithSkipCheckpoint(),
		adk.WithStopCause(cause),
	)
	l.loop.Wait()
}

func (l *sessionTurnLoop) watchExit() {
	exit := l.loop.Wait()
	err := exit.ExitReason
	if err == nil {
		err = context.Canceled
	}
	for _, request := range append(exit.InterruptedItems, exit.UnhandledItems...) {
		if request != nil {
			request.finish(agent.AgentRunResult{}, err)
		}
	}
	l.server.forgetSessionTurnLoop(l.sessionID, l)
}

func (l *sessionTurnLoop) busy() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active != nil || l.pending != nil
}

func (s *agentServer) enqueueSessionTurn(ctx context.Context, session *agentSession, message string) (queuedSessionTurn, error) {
	request := newSessionTurnRequest(ctx, s, session, message)
	s.trackTask(request.run.ID, request.cancel)
	controller := s.sessionTurnLoop(session.sessionID)
	if !controller.submit(request) {
		request.finish(agent.AgentRunResult{}, errors.New("session turn loop is stopped"))
		return queuedSessionTurn{request: request}, errors.New("session turn loop is stopped")
	}
	return queuedSessionTurn{request: request}, nil
}

func (s *agentServer) sessionTurnLoop(sessionID string) *sessionTurnLoop {
	s.turnLoopMu.Lock()
	defer s.turnLoopMu.Unlock()
	if existing := s.turnLoops[sessionID]; existing != nil {
		return existing
	}
	created := newSessionTurnLoop(s, sessionID)
	s.turnLoops[sessionID] = created
	return created
}

func (s *agentServer) sessionTurnLoopBusy(sessionID string) bool {
	s.turnLoopMu.Lock()
	loop := s.turnLoops[sessionID]
	s.turnLoopMu.Unlock()
	return loop != nil && loop.busy()
}

func (s *agentServer) forgetSessionTurnLoop(sessionID string, loop *sessionTurnLoop) {
	s.turnLoopMu.Lock()
	defer s.turnLoopMu.Unlock()
	if s.turnLoops[sessionID] == loop {
		delete(s.turnLoops, sessionID)
	}
}

func (s *agentServer) stopSessionTurnLoop(sessionID string, cause string) {
	s.turnLoopMu.Lock()
	loop := s.turnLoops[sessionID]
	if loop != nil {
		delete(s.turnLoops, sessionID)
	}
	s.turnLoopMu.Unlock()
	if loop != nil {
		loop.stop(cause)
	}
}

func (s *agentServer) stopAllSessionTurnLoops() {
	s.turnLoopMu.Lock()
	loops := make([]*sessionTurnLoop, 0, len(s.turnLoops))
	for _, loop := range s.turnLoops {
		loops = append(loops, loop)
	}
	s.turnLoops = make(map[string]*sessionTurnLoop)
	s.turnLoopMu.Unlock()
	for _, loop := range loops {
		loop.stop("server shutdown")
	}
}
