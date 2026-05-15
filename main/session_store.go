package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"ternura"
)

const (
	sessionStoreVersion = 2
	defaultSessionPath  = ".ternura/session.json"
)

type persistedMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type persistedRun struct {
	RunID       string                   `json:"run_id"`
	Status      string                   `json:"status"`
	UserMessage string                   `json:"user_message"`
	Content     string                   `json:"content,omitempty"`
	Trace       []ternura.AgentTraceItem `json:"trace,omitempty"`
	RawContent  string                   `json:"raw_content,omitempty"`
	Error       string                   `json:"error,omitempty"`
	StartedAt   string                   `json:"started_at,omitempty"`
	FinishedAt  string                   `json:"finished_at,omitempty"`
	DurationMS  int64                    `json:"duration_ms,omitempty"`
}

type persistedTodo struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

type persistedSession struct {
	SessionID string             `json:"session_id"`
	Title     string             `json:"title"`
	CreatedAt string             `json:"created_at"`
	UpdatedAt string             `json:"updated_at"`
	Messages  []persistedMessage `json:"messages,omitempty"`
	Runs      []persistedRun     `json:"runs,omitempty"`
	Todos     []persistedTodo    `json:"todos,omitempty"`
}

type sessionSnapshot struct {
	Version          int                `json:"version"`
	CurrentSessionID string             `json:"current_session_id,omitempty"`
	Sessions         []persistedSession `json:"sessions,omitempty"`

	LegacyMessages []persistedMessage `json:"messages,omitempty"`
	LegacyRuns     []persistedRun     `json:"runs,omitempty"`
}

type historySession struct {
	SessionID string          `json:"session_id"`
	Title     string          `json:"title"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
	RunCount  int             `json:"run_count"`
	Runs      []persistedRun  `json:"runs,omitempty"`
	Todos     []persistedTodo `json:"todos,omitempty"`
}

type historyResponse struct {
	CurrentSessionID string           `json:"current_session_id"`
	Sessions         []historySession `json:"sessions"`
}

type selectSessionRequest struct {
	SessionID string `json:"session_id"`
}

type sessionStore struct {
	mu       sync.Mutex
	path     string
	snapshot sessionSnapshot
}

func newSessionStore(path string) *sessionStore {
	now := time.Now()
	session := newPersistedSession(now)
	return &sessionStore{
		path: path,
		snapshot: sessionSnapshot{
			Version:          sessionStoreVersion,
			CurrentSessionID: session.SessionID,
			Sessions:         []persistedSession{session},
		},
	}
}

func (s *sessionStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	content, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(content) == 0 {
		return nil
	}

	var snapshot sessionSnapshot
	if err := json.Unmarshal(content, &snapshot); err != nil {
		return err
	}
	s.snapshot = normalizeSnapshot(snapshot)
	return nil
}

func (s *sessionStore) StartRun(run runLifecycle, userMessage string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session := s.currentSessionLocked()
	if isUntitledSession(session.Title) {
		session.Title = sessionTitle(userMessage)
	}
	session.UpdatedAt = run.StartedAt.Format(time.RFC3339Nano)
	upsertRun(session, persistedRun{
		RunID:       run.ID,
		Status:      runStatusRunning,
		UserMessage: userMessage,
		StartedAt:   run.StartedAt.Format(time.RFC3339Nano),
	})
	return s.saveLocked()
}

func (s *sessionStore) FinishRun(run runLifecycle, userMessage string, result ternura.AgentRunResult, status string, finishedAt time.Time, runErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session := s.currentSessionLocked()
	if isUntitledSession(session.Title) {
		session.Title = sessionTitle(userMessage)
	}
	session.UpdatedAt = finishedAt.Format(time.RFC3339Nano)

	item := persistedRun{
		RunID:       run.ID,
		Status:      status,
		UserMessage: userMessage,
		Content:     result.Content,
		Trace:       result.Trace,
		RawContent:  result.RawContent,
		StartedAt:   run.StartedAt.Format(time.RFC3339Nano),
		FinishedAt:  finishedAt.Format(time.RFC3339Nano),
		DurationMS:  durationMillis(run.StartedAt, finishedAt),
	}
	if runErr != nil {
		item.Error = runErr.Error()
	}

	upsertRun(session, item)
	if status == runStatusSucceeded && result.Content != "" {
		session.Messages = append(session.Messages,
			persistedMessage{Role: "user", Content: userMessage},
			persistedMessage{Role: "assistant", Content: result.Content},
		)
	}
	return s.saveLocked()
}

func (s *sessionStore) Snapshot() sessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	return cloneSnapshot(s.snapshot)
}

func (s *sessionStore) SelectSession(sessionID string) (sessionSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.findSessionLocked(sessionID) == nil {
		return sessionSnapshot{}, fmt.Errorf("session %q not found", sessionID)
	}
	s.snapshot.CurrentSessionID = sessionID
	if err := s.saveLocked(); err != nil {
		return sessionSnapshot{}, err
	}
	return cloneSnapshot(s.snapshot), nil
}

func (s *sessionStore) NewSession() (sessionSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session := newPersistedSession(time.Now())
	if current := s.findSessionLocked(s.snapshot.CurrentSessionID); current != nil && !sessionHasContent(*current) {
		*current = session
	} else {
		s.snapshot.Sessions = append(s.snapshot.Sessions, session)
	}
	s.snapshot.CurrentSessionID = session.SessionID
	if err := s.saveLocked(); err != nil {
		return sessionSnapshot{}, err
	}
	return cloneSnapshot(s.snapshot), nil
}

func (s *sessionStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session := newPersistedSession(time.Now())
	s.snapshot = sessionSnapshot{
		Version:          sessionStoreVersion,
		CurrentSessionID: session.SessionID,
		Sessions:         []persistedSession{session},
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *sessionStore) ReplaceTodos(todos []persistedTodo) (sessionSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session := s.currentSessionLocked()
	session.Todos = cloneTodos(todos)
	session.UpdatedAt = time.Now().Format(time.RFC3339Nano)
	if err := s.saveLocked(); err != nil {
		return sessionSnapshot{}, err
	}
	return cloneSnapshot(s.snapshot), nil
}

func (s *sessionStore) currentSessionLocked() *persistedSession {
	if session := s.findSessionLocked(s.snapshot.CurrentSessionID); session != nil {
		return session
	}
	session := newPersistedSession(time.Now())
	s.snapshot.CurrentSessionID = session.SessionID
	s.snapshot.Sessions = append(s.snapshot.Sessions, session)
	return &s.snapshot.Sessions[len(s.snapshot.Sessions)-1]
}

func (s *sessionStore) findSessionLocked(sessionID string) *persistedSession {
	for idx := range s.snapshot.Sessions {
		if s.snapshot.Sessions[idx].SessionID == sessionID {
			return &s.snapshot.Sessions[idx]
		}
	}
	return nil
}

func (s *sessionStore) saveLocked() error {
	s.snapshot = normalizeSnapshot(s.snapshot)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}

	payload, err := json.MarshalIndent(s.snapshot, "", "  ")
	if err != nil {
		return err
	}

	tempPath := s.path + ".tmp"
	if err := os.WriteFile(tempPath, append(payload, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tempPath, s.path)
}

func normalizeSnapshot(snapshot sessionSnapshot) sessionSnapshot {
	now := time.Now()
	if len(snapshot.Sessions) == 0 {
		if len(snapshot.LegacyRuns) > 0 || len(snapshot.LegacyMessages) > 0 {
			session := newPersistedSession(now)
			session.Title = "Recovered session"
			session.Runs = append([]persistedRun(nil), snapshot.LegacyRuns...)
			session.Messages = append([]persistedMessage(nil), snapshot.LegacyMessages...)
			if len(session.Runs) > 0 {
				session.Title = sessionTitle(session.Runs[0].UserMessage)
				if session.Runs[0].StartedAt != "" {
					session.CreatedAt = session.Runs[0].StartedAt
				}
				lastRun := session.Runs[len(session.Runs)-1]
				session.UpdatedAt = firstNonEmpty(lastRun.FinishedAt, lastRun.StartedAt, session.CreatedAt)
			}
			snapshot.Sessions = []persistedSession{session}
			snapshot.CurrentSessionID = session.SessionID
		} else {
			session := newPersistedSession(now)
			snapshot.Sessions = []persistedSession{session}
			snapshot.CurrentSessionID = session.SessionID
		}
	}

	for idx := range snapshot.Sessions {
		session := &snapshot.Sessions[idx]
		if session.SessionID == "" {
			session.SessionID = newSessionID(now.Add(time.Duration(idx) * time.Nanosecond))
		}
		if session.CreatedAt == "" {
			session.CreatedAt = now.Format(time.RFC3339Nano)
		}
		if session.UpdatedAt == "" {
			session.UpdatedAt = session.CreatedAt
		}
		if session.Title == "" && len(session.Runs) > 0 {
			session.Title = sessionTitle(session.Runs[0].UserMessage)
		}
		if session.Title == "" {
			session.Title = "New session"
		}
		if session.Runs == nil {
			session.Runs = make([]persistedRun, 0)
		}
		if session.Todos == nil {
			session.Todos = make([]persistedTodo, 0)
		}
	}
	if snapshot.CurrentSessionID == "" || findSession(snapshot.Sessions, snapshot.CurrentSessionID) == nil {
		snapshot.CurrentSessionID = snapshot.Sessions[len(snapshot.Sessions)-1].SessionID
	}

	snapshot.Version = sessionStoreVersion
	snapshot.LegacyMessages = nil
	snapshot.LegacyRuns = nil
	return snapshot
}

func historyFromSnapshot(snapshot sessionSnapshot) historyResponse {
	resp := historyResponse{
		CurrentSessionID: snapshot.CurrentSessionID,
		Sessions:         make([]historySession, 0, len(snapshot.Sessions)),
	}
	for _, session := range snapshot.Sessions {
		resp.Sessions = append(resp.Sessions, historySession{
			SessionID: session.SessionID,
			Title:     session.Title,
			CreatedAt: session.CreatedAt,
			UpdatedAt: session.UpdatedAt,
			RunCount:  len(session.Runs),
			Runs:      cloneRuns(session.Runs),
			Todos:     cloneTodos(session.Todos),
		})
	}
	return resp
}

func currentSessionFromSnapshot(snapshot sessionSnapshot) (persistedSession, bool) {
	if session := findSession(snapshot.Sessions, snapshot.CurrentSessionID); session != nil {
		return *session, true
	}
	return persistedSession{}, false
}

func findSession(sessions []persistedSession, sessionID string) *persistedSession {
	for idx := range sessions {
		if sessions[idx].SessionID == sessionID {
			return &sessions[idx]
		}
	}
	return nil
}

func upsertRun(session *persistedSession, run persistedRun) {
	for idx := range session.Runs {
		if session.Runs[idx].RunID == run.RunID {
			session.Runs[idx] = run
			return
		}
	}
	session.Runs = append(session.Runs, run)
}

func newPersistedSession(now time.Time) persistedSession {
	timestamp := now.Format(time.RFC3339Nano)
	return persistedSession{
		SessionID: newSessionID(now),
		Title:     "New session",
		CreatedAt: timestamp,
		UpdatedAt: timestamp,
		Runs:      make([]persistedRun, 0),
		Todos:     make([]persistedTodo, 0),
	}
}

func newSessionID(now time.Time) string {
	return fmt.Sprintf("session-%s", now.UTC().Format("20060102T150405.000000000"))
}

func sessionTitle(message string) string {
	const maxRunes = 32
	runes := []rune(message)
	if len(runes) == 0 {
		return "New session"
	}
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + "..."
}

func isUntitledSession(title string) bool {
	return title == "" || title == "New session"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func cloneSnapshot(snapshot sessionSnapshot) sessionSnapshot {
	cloned := sessionSnapshot{
		Version:          snapshot.Version,
		CurrentSessionID: snapshot.CurrentSessionID,
		Sessions:         make([]persistedSession, len(snapshot.Sessions)),
	}
	for idx, session := range snapshot.Sessions {
		cloned.Sessions[idx] = session
		cloned.Sessions[idx].Messages = append([]persistedMessage(nil), session.Messages...)
		cloned.Sessions[idx].Runs = cloneRuns(session.Runs)
		cloned.Sessions[idx].Todos = cloneTodos(session.Todos)
	}
	return cloned
}

func cloneRuns(runs []persistedRun) []persistedRun {
	cloned := make([]persistedRun, len(runs))
	for idx, run := range runs {
		cloned[idx] = run
		cloned[idx].Trace = append([]ternura.AgentTraceItem(nil), run.Trace...)
	}
	return cloned
}

func cloneTodos(todos []persistedTodo) []persistedTodo {
	cloned := make([]persistedTodo, len(todos))
	copy(cloned, todos)
	return cloned
}

func sessionHasContent(session persistedSession) bool {
	return len(session.Runs) > 0 || len(session.Messages) > 0 || len(session.Todos) > 0
}
