package app

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"ternura/agent"
)

const (
	runtimeStageQueued     = "queued"
	runtimeStageStarted    = "started"
	runtimeStageThinking   = "thinking"
	runtimeStageTool       = "tool"
	runtimeStageCorrecting = "correcting"
	runtimeStageFinishing  = "finishing"
)

type runtimeRunState struct {
	RunID      string
	SessionID  string
	Source     string
	Status     string
	Stage      string
	Detail     string
	Input      string
	StartedAt  time.Time
	UpdatedAt  time.Time
	ModelCalls int
	ToolCalls  int
}

type runtimeRunView struct {
	RunID      string `json:"run_id"`
	SessionID  string `json:"session_id"`
	Source     string `json:"source"`
	Status     string `json:"status"`
	Stage      string `json:"stage"`
	Detail     string `json:"detail,omitempty"`
	Input      string `json:"input"`
	StartedAt  string `json:"started_at"`
	UpdatedAt  string `json:"updated_at"`
	ElapsedMS  int64  `json:"elapsed_ms"`
	ModelCalls int    `json:"model_calls,omitempty"`
	ToolCalls  int    `json:"tool_calls,omitempty"`
}

type runtimeEvent struct {
	Sequence  uint64         `json:"sequence"`
	Type      string         `json:"type"`
	Run       runtimeRunView `json:"run"`
	Timestamp string         `json:"timestamp"`
}

type runtimeMonitor struct {
	mu          sync.RWMutex
	startedAt   time.Time
	active      map[string]runtimeRunState
	subscribers map[uint64]chan runtimeEvent
	nextSubID   uint64
	sequence    uint64
}

func newRuntimeMonitor() *runtimeMonitor {
	return &runtimeMonitor{
		startedAt:   time.Now(),
		active:      make(map[string]runtimeRunState),
		subscribers: make(map[uint64]chan runtimeEvent),
	}
}

func (m *runtimeMonitor) Start(run runLifecycle, sessionID string, source string, input string, stage string) {
	if m == nil || strings.TrimSpace(run.ID) == "" {
		return
	}
	if stage == "" {
		stage = runtimeStageStarted
	}
	now := time.Now()
	state := runtimeRunState{
		RunID:     run.ID,
		SessionID: strings.TrimSpace(sessionID),
		Source:    strings.TrimSpace(source),
		Status:    runStatusRunning,
		Stage:     stage,
		Detail:    runtimeStageDetail(stage),
		Input:     compactRuntimeInput(input),
		StartedAt: run.StartedAt,
		UpdatedAt: now,
	}
	m.mu.Lock()
	m.active[run.ID] = state
	event := m.newEventLocked("run_started", state, now)
	m.publishLocked(event)
	m.mu.Unlock()
}

func (m *runtimeMonitor) Update(runID string, stage string, detail string, metrics agent.RunMetrics) {
	if m == nil || strings.TrimSpace(runID) == "" {
		return
	}
	now := time.Now()
	m.mu.Lock()
	state, ok := m.active[runID]
	if !ok {
		m.mu.Unlock()
		return
	}
	if stage != "" {
		state.Stage = stage
	}
	if strings.TrimSpace(detail) != "" {
		state.Detail = strings.TrimSpace(detail)
	}
	state.ModelCalls = metrics.ModelCalls
	state.ToolCalls = metrics.ToolCalls
	state.UpdatedAt = now
	m.active[runID] = state
	event := m.newEventLocked("run_updated", state, now)
	m.publishLocked(event)
	m.mu.Unlock()
}

func (m *runtimeMonitor) Finish(runID string, status string, result agent.AgentRunResult, runErr error) {
	if m == nil || strings.TrimSpace(runID) == "" {
		return
	}
	now := time.Now()
	m.mu.Lock()
	state, ok := m.active[runID]
	if !ok {
		m.mu.Unlock()
		return
	}
	state.Status = status
	state.Stage = runtimeStageFinishing
	state.Detail = runtimeFinishDetail(status, runErr)
	state.ModelCalls = result.Metrics.ModelCalls
	state.ToolCalls = result.Metrics.ToolCalls
	state.UpdatedAt = now
	delete(m.active, runID)
	event := m.newEventLocked("run_finished", state, now)
	m.publishLocked(event)
	m.mu.Unlock()
}

func (m *runtimeMonitor) Active() []runtimeRunView {
	if m == nil {
		return nil
	}
	now := time.Now()
	m.mu.RLock()
	runs := make([]runtimeRunView, 0, len(m.active))
	for _, state := range m.active {
		runs = append(runs, runtimeRunViewFromState(state, now))
	}
	m.mu.RUnlock()
	sort.SliceStable(runs, func(i, j int) bool {
		return runs[i].StartedAt > runs[j].StartedAt
	})
	return runs
}

func (m *runtimeMonitor) StartedAt() time.Time {
	if m == nil {
		return time.Time{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.startedAt
}

func (m *runtimeMonitor) Subscribe() (<-chan runtimeEvent, func()) {
	if m == nil {
		closed := make(chan runtimeEvent)
		close(closed)
		return closed, func() {}
	}
	m.mu.Lock()
	m.nextSubID++
	id := m.nextSubID
	channel := make(chan runtimeEvent, 32)
	m.subscribers[id] = channel
	m.mu.Unlock()
	return channel, func() {
		m.mu.Lock()
		if current, ok := m.subscribers[id]; ok {
			delete(m.subscribers, id)
			close(current)
		}
		m.mu.Unlock()
	}
}

func (m *runtimeMonitor) newEventLocked(eventType string, state runtimeRunState, now time.Time) runtimeEvent {
	m.sequence++
	return runtimeEvent{
		Sequence:  m.sequence,
		Type:      eventType,
		Run:       runtimeRunViewFromState(state, now),
		Timestamp: now.Format(time.RFC3339Nano),
	}
}

func (m *runtimeMonitor) publishLocked(event runtimeEvent) {
	for _, subscriber := range m.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func runtimeRunViewFromState(state runtimeRunState, now time.Time) runtimeRunView {
	elapsed := now.Sub(state.StartedAt).Milliseconds()
	if elapsed < 0 {
		elapsed = 0
	}
	return runtimeRunView{
		RunID:      state.RunID,
		SessionID:  state.SessionID,
		Source:     state.Source,
		Status:     state.Status,
		Stage:      state.Stage,
		Detail:     state.Detail,
		Input:      state.Input,
		StartedAt:  state.StartedAt.Format(time.RFC3339Nano),
		UpdatedAt:  state.UpdatedAt.Format(time.RFC3339Nano),
		ElapsedMS:  elapsed,
		ModelCalls: state.ModelCalls,
		ToolCalls:  state.ToolCalls,
	}
}

func runtimeStageDetail(stage string) string {
	switch stage {
	case runtimeStageQueued:
		return "等待当前会话中的前一个任务结束"
	case runtimeStageThinking:
		return "模型正在思考并决定下一步"
	case runtimeStageTool:
		return "正在执行工具"
	case runtimeStageCorrecting:
		return "收到新的补充，正在安全切换"
	case runtimeStageFinishing:
		return "正在整理最终结果"
	default:
		return "运行已经开始"
	}
}

func runtimeFinishDetail(status string, runErr error) string {
	switch status {
	case runStatusSucceeded:
		return "运行已完成"
	case runStatusWaitingApproval:
		return "等待用户审批"
	case runStatusCancelled:
		return "运行已取消"
	case runStatusFailed:
		if runErr != nil {
			return "运行失败：" + compactRuntimeInput(runErr.Error())
		}
		return "运行失败"
	default:
		return status
	}
}

func compactRuntimeInput(input string) string {
	input = strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
	const maxRunes = 240
	runes := []rune(input)
	if len(runes) <= maxRunes {
		return input
	}
	return string(runes[:maxRunes]) + "..."
}

type runtimeRunContextKey struct{}

func withRuntimeRun(ctx context.Context, runID string) context.Context {
	return context.WithValue(ctx, runtimeRunContextKey{}, strings.TrimSpace(runID))
}

func runtimeRunID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(runtimeRunContextKey{}).(string)
	return strings.TrimSpace(value)
}
