package app

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"ternura/agent"
)

type createTaskRequest struct {
	SessionID string `json:"session_id,omitempty"`
	Input     string `json:"input"`
}

type taskDecisionRequest struct {
	Approved bool   `json:"approved"`
	Reason   string `json:"reason,omitempty"`
}

type taskResponse struct {
	SessionID string       `json:"session_id"`
	Run       persistedRun `json:"task"`
}

type taskSummary struct {
	SessionID     string                `json:"session_id"`
	RunID         string                `json:"run_id"`
	Status        string                `json:"status"`
	Input         string                `json:"input"`
	Error         string                `json:"error,omitempty"`
	StartedAt     string                `json:"started_at,omitempty"`
	FinishedAt    string                `json:"finished_at,omitempty"`
	DurationMS    int64                 `json:"duration_ms,omitempty"`
	Artifacts     []agent.AgentArtifact `json:"artifacts,omitempty"`
	NeedsApproval bool                  `json:"needs_approval,omitempty"`
	CheckpointID  string                `json:"checkpoint_id,omitempty"`
}

func (s *agentServer) handleTasks(w http.ResponseWriter, r *http.Request) {
	if !authorizeTaskAPI(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.handleTaskList(w, r)
	case http.MethodPost:
		s.handleTaskCreate(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *agentServer) handleTask(w http.ResponseWriter, r *http.Request) {
	if !authorizeTaskAPI(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	runID, action, ok := parseTaskPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch {
	case r.Method == http.MethodGet && action == "":
		sessionID, run, found := s.store.RunByID(runID)
		if !found {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, taskResponse{SessionID: sessionID, Run: run})
	case r.Method == http.MethodPost && action == "cancel":
		if err := s.cancelTask(runID); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		sessionID, run, _ := s.store.RunByID(runID)
		writeJSON(w, http.StatusAccepted, taskResponse{SessionID: sessionID, Run: run})
	case r.Method == http.MethodPost && action == "decision":
		var request taskDecisionRequest
		if err := decodeJSON(w, r, &request); err != nil {
			return
		}
		newRun, err := s.startAsyncTaskDecision(runID, request)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		sessionID, persisted, _ := s.store.RunByID(newRun.ID)
		writeJSON(w, http.StatusAccepted, taskResponse{SessionID: sessionID, Run: persisted})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *agentServer) handleTaskCreate(w http.ResponseWriter, r *http.Request) {
	var request createTaskRequest
	if err := decodeJSON(w, r, &request); err != nil {
		return
	}
	request.Input = strings.TrimSpace(request.Input)
	if request.Input == "" {
		http.Error(w, "input is required", http.StatusBadRequest)
		return
	}
	run, sessionID, err := s.startAsyncTask(request.SessionID, request.Input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	_, persisted, _ := s.store.RunByID(run.ID)
	writeJSON(w, http.StatusAccepted, taskResponse{SessionID: sessionID, Run: persisted})
}

func (s *agentServer) handleTaskList(w http.ResponseWriter, r *http.Request) {
	filter := strings.TrimSpace(r.URL.Query().Get("session_id"))
	snapshot := s.store.Snapshot()
	tasks := make([]taskSummary, 0)
	for _, session := range snapshot.Sessions {
		if filter != "" && session.SessionID != filter {
			continue
		}
		for _, run := range session.Runs {
			tasks = append(tasks, summarizeTask(session.SessionID, run))
		}
	}
	sort.SliceStable(tasks, func(i, j int) bool { return tasks[i].StartedAt > tasks[j].StartedAt })
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (s *agentServer) startAsyncTask(sessionID string, input string) (runLifecycle, string, error) {
	if strings.TrimSpace(sessionID) == "" {
		sessionID = s.store.CurrentSessionID()
	}
	if _, err := s.store.EnsureSession(sessionID, input); err != nil {
		return runLifecycle{}, "", err
	}
	session := s.newAgentSession(sessionID, nil)
	run := newRunLifecycle()
	logRunStart(run)
	if err := s.store.StartRunForSession(sessionID, run, input); err != nil {
		return run, sessionID, err
	}
	s.runtime.Start(run, sessionID, "api", input, runtimeStageQueued)

	runCtx, cancel := context.WithCancel(s.serverContext())
	s.trackTask(run.ID, cancel)
	go func() {
		defer s.untrackTask(run.ID)
		lock := s.taskSessionLock(sessionID)
		lock.Lock()
		defer lock.Unlock()

		executionCtx := withRuntimeRun(runCtx, run.ID)
		s.runtime.Update(run.ID, runtimeStageStarted, "运行已经开始", agent.RunMetrics{})
		result, runErr := session.runWithTrace(executionCtx, input, run.ID)
		if runErr != nil {
			result = failedAgentRunResult(result, runErr)
		}
		session.finishUserRun(run, input, result, runErr)
	}()
	return run, sessionID, nil
}

func (s *agentServer) startAsyncTaskDecision(runID string, decision taskDecisionRequest) (runLifecycle, error) {
	sessionID, pending, ok := s.store.RunByID(runID)
	if !ok || pending.Status != runStatusWaitingApproval || pending.PendingApproval == nil {
		return runLifecycle{}, fmt.Errorf("task %q is not waiting for approval", runID)
	}
	display := "reject " + runID
	if decision.Approved {
		display = "approve " + runID
	}
	run := newRunLifecycle()
	logRunStart(run)
	if err := s.store.StartRunForSession(sessionID, run, display); err != nil {
		return run, err
	}
	s.runtime.Start(run, sessionID, "api", display, runtimeStageQueued)

	runCtx, cancel := context.WithCancel(s.serverContext())
	s.trackTask(run.ID, cancel)
	go func() {
		defer s.untrackTask(run.ID)
		lock := s.taskSessionLock(sessionID)
		lock.Lock()
		defer lock.Unlock()

		session := s.newAgentSession(sessionID, nil)
		executionCtx := withRuntimeRun(runCtx, run.ID)
		s.runtime.Update(run.ID, runtimeStageStarted, "正在恢复审批检查点", agent.RunMetrics{})
		result, runErr := session.agent().ResumeWithTrace(
			executionCtx,
			pending.UserMessage,
			pending.CheckpointID,
			pending.PendingApproval.InterruptID,
			agent.ToolApprovalDecision{Approved: decision.Approved, Reason: decision.Reason},
		)
		if runErr != nil {
			result = failedAgentRunResult(result, runErr)
		}
		resolvedStatus := runStatusRejected
		if decision.Approved {
			resolvedStatus = runStatusApproved
		}
		if err := s.store.ResolvePendingApproval(sessionID, pending.CheckpointID, resolvedStatus); err != nil && runErr == nil {
			runErr = err
		}
		session.finishUserRun(run, display, result, runErr)
	}()
	return run, nil
}

func (s *agentServer) cancelTask(runID string) error {
	s.taskMu.Lock()
	cancel := s.taskCancels[runID]
	s.taskMu.Unlock()
	if cancel != nil {
		cancel()
		return nil
	}
	_, run, ok := s.store.RunByID(runID)
	if !ok {
		return fmt.Errorf("task %q not found", runID)
	}
	if run.Status != runStatusWaitingApproval {
		return fmt.Errorf("task %q is not running", runID)
	}
	if err := s.store.CancelRun(runID, time.Now()); err != nil {
		return err
	}
	if s.checkpoints != nil && run.CheckpointID != "" {
		_ = s.checkpoints.Delete(context.Background(), run.CheckpointID)
	}
	return nil
}

func (s *agentServer) cancelAllTasks() {
	if s == nil {
		return
	}
	s.taskMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.taskCancels))
	for _, cancel := range s.taskCancels {
		cancels = append(cancels, cancel)
	}
	s.taskMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (s *agentServer) trackTask(runID string, cancel context.CancelFunc) {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	s.taskCancels[runID] = cancel
}

func (s *agentServer) untrackTask(runID string) {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	delete(s.taskCancels, runID)
}

func (s *agentServer) taskSessionLock(sessionID string) *sync.Mutex {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	lock := s.taskSessionLocks[sessionID]
	if lock == nil {
		lock = &sync.Mutex{}
		s.taskSessionLocks[sessionID] = lock
	}
	return lock
}

func (s *agentServer) serverContext() context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func (s *agentServer) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":        "Ternura",
		"description": "General-purpose Eino ADK agent with tools, memory, scheduling, skills, and human approval.",
		"protocol":    "ternura-task-api/v1",
		"endpoints": map[string]string{
			"tasks":      "/api/tasks",
			"agent_card": "/api/agent-card",
		},
		"capabilities": map[string]bool{
			"tasks":          true,
			"artifacts":      true,
			"cancellation":   true,
			"human_approval": true,
			"checkpointing":  true,
			"mcp":            s.mcpRuntime != nil && len(s.mcpRuntime.Tools()) > 0,
		},
	})
}

func summarizeTask(sessionID string, run persistedRun) taskSummary {
	return taskSummary{
		SessionID:     sessionID,
		RunID:         run.RunID,
		Status:        run.Status,
		Input:         run.UserMessage,
		Error:         run.Error,
		StartedAt:     run.StartedAt,
		FinishedAt:    run.FinishedAt,
		DurationMS:    run.DurationMS,
		Artifacts:     append([]agent.AgentArtifact(nil), run.Artifacts...),
		NeedsApproval: run.PendingApproval != nil && run.Status == runStatusWaitingApproval,
		CheckpointID:  run.CheckpointID,
	}
}

func parseTaskPath(path string) (runID string, action string, ok bool) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(path, "/api/tasks/"), "/"), "/")
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "run-") {
		return "", "", false
	}
	if len(parts) > 2 {
		return "", "", false
	}
	if len(parts) == 2 {
		action = parts[1]
	}
	return parts[0], action, true
}

func authorizeTaskAPI(r *http.Request) bool {
	expected := strings.TrimSpace(os.Getenv("TERNURA_API_TOKEN"))
	if expected != "" {
		provided := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
