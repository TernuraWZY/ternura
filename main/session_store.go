package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"ternura"
)

const (
	sessionStoreVersion = 1
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

type sessionSnapshot struct {
	Version  int                `json:"version"`
	Messages []persistedMessage `json:"messages,omitempty"`
	Runs     []persistedRun     `json:"runs,omitempty"`
}

type historyResponse struct {
	Runs []persistedRun `json:"runs"`
}

type sessionStore struct {
	mu       sync.Mutex
	path     string
	snapshot sessionSnapshot
}

func newSessionStore(path string) *sessionStore {
	return &sessionStore{
		path: path,
		snapshot: sessionSnapshot{
			Version: sessionStoreVersion,
			Runs:    make([]persistedRun, 0),
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
	if snapshot.Version == 0 {
		snapshot.Version = sessionStoreVersion
	}
	if snapshot.Runs == nil {
		snapshot.Runs = make([]persistedRun, 0)
	}
	s.snapshot = snapshot
	return nil
}

func (s *sessionStore) StartRun(run runLifecycle, userMessage string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.upsertRunLocked(persistedRun{
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

	s.upsertRunLocked(item)
	if status == runStatusSucceeded {
		assistantContent := result.Content
		if assistantContent != "" {
			s.snapshot.Messages = append(s.snapshot.Messages,
				persistedMessage{Role: "user", Content: userMessage},
				persistedMessage{Role: "assistant", Content: assistantContent},
			)
		}
	}
	return s.saveLocked()
}

func (s *sessionStore) Snapshot() sessionSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	return cloneSnapshot(s.snapshot)
}

func (s *sessionStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.snapshot = sessionSnapshot{
		Version: sessionStoreVersion,
		Runs:    make([]persistedRun, 0),
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *sessionStore) upsertRunLocked(run persistedRun) {
	for idx := range s.snapshot.Runs {
		if s.snapshot.Runs[idx].RunID == run.RunID {
			s.snapshot.Runs[idx] = run
			return
		}
	}
	s.snapshot.Runs = append(s.snapshot.Runs, run)
}

func (s *sessionStore) saveLocked() error {
	s.snapshot.Version = sessionStoreVersion
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

func cloneSnapshot(snapshot sessionSnapshot) sessionSnapshot {
	cloned := sessionSnapshot{
		Version:  snapshot.Version,
		Messages: append([]persistedMessage(nil), snapshot.Messages...),
		Runs:     make([]persistedRun, len(snapshot.Runs)),
	}
	for idx, run := range snapshot.Runs {
		cloned.Runs[idx] = run
		cloned.Runs[idx].Trace = append([]ternura.AgentTraceItem(nil), run.Trace...)
	}
	return cloned
}
