package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"ternura/internal/feishu"
)

type runtimeCounts struct {
	ActiveRuns       int `json:"active_runs"`
	PendingApprovals int `json:"pending_approvals"`
	Sessions         int `json:"sessions"`
	CronJobs         int `json:"cron_jobs"`
	Skills           int `json:"skills"`
	Tools            int `json:"tools"`
	MCPTools         int `json:"mcp_tools"`
}

type runtimeMemoryStatus struct {
	CurrentSessionID string `json:"current_session_id"`
	LongTermCount    int    `json:"long_term_count"`
	ShortTermTurns   int    `json:"short_term_turns"`
	ToolMemoryCount  int    `json:"tool_memory_count"`
}

type runtimeOverview struct {
	Status           string                   `json:"status"`
	StartedAt        string                   `json:"started_at"`
	UptimeSeconds    int64                    `json:"uptime_seconds"`
	Model            string                   `json:"model"`
	Provider         string                   `json:"provider"`
	ContextWindow    int                      `json:"context_window"`
	Feishu           feishu.ConnectionStatus  `json:"feishu"`
	Counts           runtimeCounts            `json:"counts"`
	Memory           runtimeMemoryStatus      `json:"memory"`
	ActiveRuns       []runtimeRunView         `json:"active_runs"`
	PendingApprovals []pendingApprovalSummary `json:"pending_approvals"`
}

type pendingApprovalSummary struct {
	Task      taskSummary `json:"task"`
	Tool      string      `json:"tool"`
	Reason    string      `json:"reason"`
	Arguments string      `json:"arguments"`
}

type sessionSummary struct {
	SessionID    string `json:"session_id"`
	Title        string `json:"title"`
	Current      bool   `json:"current"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	MessageCount int    `json:"message_count"`
	RunCount     int    `json:"run_count"`
	TodoCount    int    `json:"todo_count"`
	LastStatus   string `json:"last_status,omitempty"`
}

func (s *agentServer) handleRuntime(w http.ResponseWriter, r *http.Request) {
	if !authorizeTaskAPI(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.runtimeOverview())
}

func (s *agentServer) runtimeOverview() runtimeOverview {
	now := time.Now()
	startedAt := s.runtime.StartedAt()
	active := s.runtime.Active()
	snapshot := s.store.Snapshot()
	pending := make([]pendingApprovalSummary, 0)
	for _, session := range snapshot.Sessions {
		for _, run := range session.Runs {
			if run.Status == runStatusWaitingApproval && run.PendingApproval != nil {
				pending = append(pending, pendingApprovalSummary{
					Task:      summarizeTask(session.SessionID, run),
					Tool:      string(run.PendingApproval.Tool),
					Reason:    compactAdminText(run.PendingApproval.Reason, 360),
					Arguments: compactAdminText(run.PendingApproval.Arguments, 1200),
				})
			}
		}
	}
	sort.SliceStable(pending, func(i, j int) bool {
		return pending[i].Task.StartedAt > pending[j].Task.StartedAt
	})

	cronJobs := 0
	if s.cron != nil {
		cronJobs = len(s.cron.List(false))
	}
	mcpTools := 0
	if s.mcpRuntime != nil {
		mcpTools = len(s.mcpRuntime.Tools())
	}
	skillCount, toolCount := s.runtimeInventory()
	memoryStatus := runtimeMemoryStatus{CurrentSessionID: snapshot.CurrentSessionID}
	if s.memory != nil && snapshot.CurrentSessionID != "" {
		if current, err := s.memory.Status(snapshot.CurrentSessionID); err == nil {
			memoryStatus.LongTermCount = current.LongTermCount
			memoryStatus.ShortTermTurns = current.ShortTermTurns
			memoryStatus.ToolMemoryCount = current.ToolMemoryCount
		}
	}
	uptime := int64(0)
	startedAtText := ""
	if !startedAt.IsZero() {
		startedAtText = startedAt.Format(time.RFC3339Nano)
		uptime = int64(now.Sub(startedAt).Seconds())
		if uptime < 0 {
			uptime = 0
		}
	}
	feishuStatus := feishu.ConnectionStatus{}
	if s.feishu != nil {
		feishuStatus = s.feishu.Status()
	}
	return runtimeOverview{
		Status:        "ok",
		StartedAt:     startedAtText,
		UptimeSeconds: uptime,
		Model:         s.modelConf.Model,
		Provider:      runtimeProviderName(),
		ContextWindow: s.modelConf.ContextWindow,
		Feishu:        feishuStatus,
		Counts: runtimeCounts{
			ActiveRuns:       len(active),
			PendingApprovals: len(pending),
			Sessions:         len(snapshot.Sessions),
			CronJobs:         cronJobs,
			Skills:           skillCount,
			Tools:            toolCount,
			MCPTools:         mcpTools,
		},
		Memory:           memoryStatus,
		ActiveRuns:       active,
		PendingApprovals: pending,
	}
}

func (s *agentServer) handleRuntimeEvents(w http.ResponseWriter, r *http.Request) {
	if !authorizeTaskAPI(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = fmt.Fprint(w, "retry: 2000\n\n")
	flusher.Flush()

	events, unsubscribe := s.runtime.Subscribe()
	defer unsubscribe()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(event)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "id: %d\nevent: runtime\ndata: %s\n\n", event.Sequence, payload)
			flusher.Flush()
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func (s *agentServer) handleSessions(w http.ResponseWriter, r *http.Request) {
	if !authorizeTaskAPI(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	snapshot := s.store.Snapshot()
	sessions := make([]sessionSummary, 0, len(snapshot.Sessions))
	for _, session := range snapshot.Sessions {
		sessions = append(sessions, summarizeSession(session, session.SessionID == snapshot.CurrentSessionID))
	}
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].UpdatedAt > sessions[j].UpdatedAt })
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func summarizeSession(session persistedSession, current bool) sessionSummary {
	lastStatus := ""
	if len(session.Runs) > 0 {
		lastStatus = session.Runs[len(session.Runs)-1].Status
	}
	return sessionSummary{
		SessionID:    session.SessionID,
		Title:        session.Title,
		Current:      current,
		CreatedAt:    session.CreatedAt,
		UpdatedAt:    session.UpdatedAt,
		MessageCount: len(session.Messages),
		RunCount:     len(session.Runs),
		TodoCount:    len(session.Todos),
		LastStatus:   lastStatus,
	}
}

func (s *agentServer) handleCronJobs(w http.ResponseWriter, r *http.Request) {
	if !authorizeTaskAPI(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cron == nil {
		writeJSON(w, http.StatusOK, map[string]any{"jobs": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": s.cron.List(true)})
}

func runtimeProviderName() string {
	provider := strings.TrimSpace(os.Getenv("LLM_PROVIDER"))
	if provider == "" {
		return "openai"
	}
	return strings.ToLower(provider)
}

func compactAdminText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}
