package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ternura"
)

const (
	sessionStoreVersion     = 3
	defaultSessionPath      = ".ternura/session.json"
	sessionIndexFileName    = "index.json"
	sessionLegacyBackupName = "session.legacy.json"
	sessionsDirName         = "sessions"
	sessionMetaFileName     = "meta.json"
	sessionMessagesFileName = "messages.json"
	sessionTodosFileName    = "todos.json"
	sessionRunsDirName      = "runs"
	sessionRunFileExtension = ".json"
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

type sessionIndex struct {
	Version          int                 `json:"version"`
	CurrentSessionID string              `json:"current_session_id,omitempty"`
	Sessions         []sessionIndexEntry `json:"sessions,omitempty"`
}

type sessionIndexEntry struct {
	SessionID    string `json:"session_id"`
	Title        string `json:"title"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	RunCount     int    `json:"run_count"`
	MessageCount int    `json:"message_count,omitempty"`
	TodoCount    int    `json:"todo_count,omitempty"`
}

type sessionMeta struct {
	Version      int      `json:"version"`
	SessionID    string   `json:"session_id"`
	Title        string   `json:"title"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
	RunCount     int      `json:"run_count"`
	MessageCount int      `json:"message_count,omitempty"`
	TodoCount    int      `json:"todo_count,omitempty"`
	RunIDs       []string `json:"run_ids,omitempty"`
}

type sessionMessagesFile struct {
	Version  int                `json:"version"`
	Messages []persistedMessage `json:"messages,omitempty"`
}

type sessionTodosFile struct {
	Version int             `json:"version"`
	Todos   []persistedTodo `json:"todos,omitempty"`
}

type historySession struct {
	SessionID string          `json:"session_id"`
	Title     string          `json:"title"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
	RunCount  int             `json:"run_count"`
	LastRun   *persistedRun   `json:"last_run,omitempty"`
	Runs      []persistedRun  `json:"runs,omitempty"`
	Todos     []persistedTodo `json:"todos,omitempty"`
}

type historyResponse struct {
	CurrentSessionID string           `json:"current_session_id"`
	Sessions         []historySession `json:"sessions"`
}

type sessionDetailResponse struct {
	CurrentSessionID string         `json:"current_session_id"`
	Session          historySession `json:"session"`
}

type selectSessionRequest struct {
	SessionID string `json:"session_id"`
}

type sessionStore struct {
	mu        sync.Mutex
	path      string
	root      string
	indexPath string
	snapshot  sessionSnapshot
}

func newSessionStore(path string) *sessionStore {
	now := time.Now()
	session := newPersistedSession(now)
	root := filepath.Dir(path)
	return &sessionStore{
		path:      path,
		root:      root,
		indexPath: filepath.Join(root, sessionIndexFileName),
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

	splitSnapshot, err := s.loadSplitLocked()
	if err == nil {
		s.snapshot = normalizeSnapshot(splitSnapshot)
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}

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
	if err := s.saveLocked(); err != nil {
		return err
	}
	if err := s.archiveLegacyLocked(); err != nil {
		return err
	}
	return nil
}

func (s *sessionStore) StartRun(run runLifecycle, userMessage string) error {
	return s.StartRunForSession("", run, userMessage)
}

func (s *sessionStore) StartRunForSession(sessionID string, run runLifecycle, userMessage string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.sessionForRunLocked(sessionID)
	if err != nil {
		return err
	}
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
	return s.FinishRunForSession("", run, userMessage, result, status, finishedAt, runErr)
}

func (s *sessionStore) FinishRunForSession(sessionID string, run runLifecycle, userMessage string, result ternura.AgentRunResult, status string, finishedAt time.Time, runErr error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.sessionForRunLocked(sessionID)
	if err != nil {
		return err
	}
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

func (s *sessionStore) CurrentSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.snapshot.CurrentSessionID
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
	for _, path := range []string{s.indexPath, s.path, s.legacyBackupPath()} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.RemoveAll(s.sessionsPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.saveLocked()
}

func (s *sessionStore) ReplaceTodos(todos []persistedTodo) (sessionSnapshot, error) {
	return s.ReplaceTodosForSession("", todos)
}

func (s *sessionStore) ReplaceTodosForSession(sessionID string, todos []persistedTodo) (sessionSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.sessionForRunLocked(sessionID)
	if err != nil {
		return sessionSnapshot{}, err
	}
	session.Todos = cloneTodos(todos)
	session.UpdatedAt = time.Now().Format(time.RFC3339Nano)
	if err := s.saveLocked(); err != nil {
		return sessionSnapshot{}, err
	}
	return cloneSnapshot(s.snapshot), nil
}

func (s *sessionStore) sessionForRunLocked(sessionID string) (*persistedSession, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return s.currentSessionLocked(), nil
	}
	if session := s.findSessionLocked(sessionID); session != nil {
		return session, nil
	}
	return nil, fmt.Errorf("session %q not found", sessionID)
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
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}

	activeSessions := make(map[string]struct{}, len(s.snapshot.Sessions))
	for _, session := range s.snapshot.Sessions {
		activeSessions[session.SessionID] = struct{}{}
		if err := s.writeSessionLocked(session); err != nil {
			return err
		}
	}
	if err := s.cleanupSessionDirsLocked(activeSessions); err != nil {
		return err
	}

	return writeJSONAtomic(s.indexPath, indexFromSnapshot(s.snapshot))
}

func (s *sessionStore) loadSplitLocked() (sessionSnapshot, error) {
	var index sessionIndex
	if err := readJSONFile(s.indexPath, &index); err != nil {
		return sessionSnapshot{}, err
	}

	snapshot := sessionSnapshot{
		Version:          index.Version,
		CurrentSessionID: index.CurrentSessionID,
		Sessions:         make([]persistedSession, 0, len(index.Sessions)),
	}
	for _, entry := range index.Sessions {
		session, err := s.loadSessionLocked(entry)
		if err != nil {
			return sessionSnapshot{}, err
		}
		snapshot.Sessions = append(snapshot.Sessions, session)
	}
	return normalizeSnapshot(snapshot), nil
}

func (s *sessionStore) loadSessionLocked(entry sessionIndexEntry) (persistedSession, error) {
	meta := sessionMeta{
		Version:      sessionStoreVersion,
		SessionID:    entry.SessionID,
		Title:        entry.Title,
		CreatedAt:    entry.CreatedAt,
		UpdatedAt:    entry.UpdatedAt,
		RunCount:     entry.RunCount,
		MessageCount: entry.MessageCount,
		TodoCount:    entry.TodoCount,
	}
	if err := readJSONFile(s.sessionMetaPath(entry.SessionID), &meta); err != nil && !errors.Is(err, os.ErrNotExist) {
		return persistedSession{}, err
	}

	messages, err := s.loadMessagesLocked(entry.SessionID)
	if err != nil {
		return persistedSession{}, err
	}
	todos, err := s.loadTodosLocked(entry.SessionID)
	if err != nil {
		return persistedSession{}, err
	}
	runs, err := s.loadRunsLocked(entry.SessionID, meta.RunIDs)
	if err != nil {
		return persistedSession{}, err
	}

	return persistedSession{
		SessionID: firstNonEmpty(meta.SessionID, entry.SessionID),
		Title:     firstNonEmpty(meta.Title, entry.Title),
		CreatedAt: firstNonEmpty(meta.CreatedAt, entry.CreatedAt),
		UpdatedAt: firstNonEmpty(meta.UpdatedAt, entry.UpdatedAt),
		Messages:  messages,
		Runs:      runs,
		Todos:     todos,
	}, nil
}

func (s *sessionStore) loadMessagesLocked(sessionID string) ([]persistedMessage, error) {
	var wrapper sessionMessagesFile
	if err := readJSONFile(s.sessionMessagesPath(sessionID), &wrapper); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return append([]persistedMessage(nil), wrapper.Messages...), nil
}

func (s *sessionStore) loadTodosLocked(sessionID string) ([]persistedTodo, error) {
	var wrapper sessionTodosFile
	if err := readJSONFile(s.sessionTodosPath(sessionID), &wrapper); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return cloneTodos(wrapper.Todos), nil
}

func (s *sessionStore) loadRunsLocked(sessionID string, runIDs []string) ([]persistedRun, error) {
	ids := append([]string(nil), runIDs...)
	if len(ids) == 0 {
		entries, err := os.ReadDir(s.sessionRunsPath(sessionID))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), sessionRunFileExtension) {
				continue
			}
			ids = append(ids, strings.TrimSuffix(entry.Name(), sessionRunFileExtension))
		}
		sort.Strings(ids)
	}

	runs := make([]persistedRun, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, runID := range ids {
		if runID == "" {
			continue
		}
		if _, ok := seen[runID]; ok {
			continue
		}
		seen[runID] = struct{}{}

		var run persistedRun
		if err := readJSONFile(s.sessionRunPath(sessionID, runID), &run); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		if run.RunID == "" {
			run.RunID = runID
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (s *sessionStore) writeSessionLocked(session persistedSession) error {
	sessionDir := s.sessionPath(session.SessionID)
	runsDir := s.sessionRunsPath(session.SessionID)
	if err := os.MkdirAll(runsDir, 0o700); err != nil {
		return err
	}

	runIDs := make([]string, 0, len(session.Runs))
	activeRuns := make(map[string]struct{}, len(session.Runs))
	for _, run := range session.Runs {
		if run.RunID == "" {
			continue
		}
		runIDs = append(runIDs, run.RunID)
		activeRuns[run.RunID] = struct{}{}
		if err := writeJSONAtomic(s.sessionRunPath(session.SessionID, run.RunID), run); err != nil {
			return err
		}
	}
	if err := s.cleanupRunFilesLocked(session.SessionID, activeRuns); err != nil {
		return err
	}

	meta := sessionMeta{
		Version:      sessionStoreVersion,
		SessionID:    session.SessionID,
		Title:        session.Title,
		CreatedAt:    session.CreatedAt,
		UpdatedAt:    session.UpdatedAt,
		RunCount:     len(session.Runs),
		MessageCount: len(session.Messages),
		TodoCount:    len(session.Todos),
		RunIDs:       runIDs,
	}
	if err := writeJSONAtomic(filepath.Join(sessionDir, sessionMetaFileName), meta); err != nil {
		return err
	}
	if err := writeJSONAtomic(filepath.Join(sessionDir, sessionMessagesFileName), sessionMessagesFile{
		Version:  sessionStoreVersion,
		Messages: session.Messages,
	}); err != nil {
		return err
	}
	return writeJSONAtomic(filepath.Join(sessionDir, sessionTodosFileName), sessionTodosFile{
		Version: sessionStoreVersion,
		Todos:   session.Todos,
	})
}

func (s *sessionStore) cleanupSessionDirsLocked(active map[string]struct{}) error {
	entries, err := os.ReadDir(s.sessionsPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, ok := active[entry.Name()]; ok {
			continue
		}
		if err := os.RemoveAll(filepath.Join(s.sessionsPath(), entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (s *sessionStore) cleanupRunFilesLocked(sessionID string, active map[string]struct{}) error {
	entries, err := os.ReadDir(s.sessionRunsPath(sessionID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), sessionRunFileExtension) {
			continue
		}
		runID := strings.TrimSuffix(entry.Name(), sessionRunFileExtension)
		if _, ok := active[runID]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(s.sessionRunsPath(sessionID), entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *sessionStore) archiveLegacyLocked() error {
	if s.path == "" || s.path == s.indexPath {
		return nil
	}
	if _, err := os.Stat(s.path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	backupPath := s.legacyBackupPath()
	if _, err := os.Stat(backupPath); err == nil {
		return os.Remove(s.path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(s.path, backupPath)
}

func (s *sessionStore) sessionsPath() string {
	return filepath.Join(s.root, sessionsDirName)
}

func (s *sessionStore) sessionPath(sessionID string) string {
	return filepath.Join(s.sessionsPath(), sessionID)
}

func (s *sessionStore) sessionMetaPath(sessionID string) string {
	return filepath.Join(s.sessionPath(sessionID), sessionMetaFileName)
}

func (s *sessionStore) sessionMessagesPath(sessionID string) string {
	return filepath.Join(s.sessionPath(sessionID), sessionMessagesFileName)
}

func (s *sessionStore) sessionTodosPath(sessionID string) string {
	return filepath.Join(s.sessionPath(sessionID), sessionTodosFileName)
}

func (s *sessionStore) sessionRunsPath(sessionID string) string {
	return filepath.Join(s.sessionPath(sessionID), sessionRunsDirName)
}

func (s *sessionStore) sessionRunPath(sessionID string, runID string) string {
	return filepath.Join(s.sessionRunsPath(sessionID), runID+sessionRunFileExtension)
}

func (s *sessionStore) legacyBackupPath() string {
	return filepath.Join(s.root, sessionLegacyBackupName)
}

func indexFromSnapshot(snapshot sessionSnapshot) sessionIndex {
	index := sessionIndex{
		Version:          sessionStoreVersion,
		CurrentSessionID: snapshot.CurrentSessionID,
		Sessions:         make([]sessionIndexEntry, 0, len(snapshot.Sessions)),
	}
	for _, session := range snapshot.Sessions {
		index.Sessions = append(index.Sessions, sessionIndexEntry{
			SessionID:    session.SessionID,
			Title:        session.Title,
			CreatedAt:    session.CreatedAt,
			UpdatedAt:    session.UpdatedAt,
			RunCount:     len(session.Runs),
			MessageCount: len(session.Messages),
			TodoCount:    len(session.Todos),
		})
	}
	return index
}

func readJSONFile(path string, target any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(content) == 0 {
		return os.ErrNotExist
	}
	return json.Unmarshal(content, target)
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}

	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, append(payload, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
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
		resp.Sessions = append(resp.Sessions, historySessionFromSession(session, false))
	}
	return resp
}

func sessionDetailFromSnapshot(snapshot sessionSnapshot, sessionID string) (sessionDetailResponse, error) {
	if sessionID == "" {
		sessionID = snapshot.CurrentSessionID
	}
	session := findSession(snapshot.Sessions, sessionID)
	if session == nil {
		return sessionDetailResponse{}, fmt.Errorf("session %q not found", sessionID)
	}
	return sessionDetailResponse{
		CurrentSessionID: snapshot.CurrentSessionID,
		Session:          historySessionFromSession(*session, true),
	}, nil
}

func historySessionFromSession(session persistedSession, includeRuns bool) historySession {
	item := historySession{
		SessionID: session.SessionID,
		Title:     session.Title,
		CreatedAt: session.CreatedAt,
		UpdatedAt: session.UpdatedAt,
		RunCount:  len(session.Runs),
		Todos:     cloneTodos(session.Todos),
	}
	if len(session.Runs) > 0 {
		lastRun := summarizeHistoryRun(session.Runs[len(session.Runs)-1])
		item.LastRun = &lastRun
	}
	if includeRuns {
		item.Runs = cloneRuns(session.Runs)
	}
	return item
}

func summarizeHistoryRun(run persistedRun) persistedRun {
	run.Trace = nil
	run.RawContent = ""
	return run
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
