package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ternura/tool"
)

const (
	scheduleStoreVersion = 1
	scheduleFileName     = "schedules.json"

	scheduleStatusScheduled = "scheduled"
	scheduleStatusRunning   = "running"
	scheduleStatusCompleted = "completed"
	scheduleStatusCancelled = "cancelled"
	scheduleStatusFailed    = "failed"
)

type scheduleStore struct {
	mu   sync.Mutex
	path string
	file scheduleFile
}

type scheduleFile struct {
	Version int             `json:"version"`
	Tasks   []scheduledTask `json:"tasks,omitempty"`
}

type scheduledTask struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Prompt     string `json:"prompt"`
	SessionID  string `json:"session_id"`
	Status     string `json:"status"`
	RunAt      string `json:"run_at"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	LastRunID  string `json:"last_run_id,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

type scheduleCreateRequest struct {
	Title        string `json:"title,omitempty"`
	Prompt       string `json:"prompt"`
	RunAt        string `json:"run_at,omitempty"`
	DelaySeconds int    `json:"delay_seconds,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
}

type scheduleCancelRequest struct {
	ID string `json:"id"`
}

type schedulesResponse struct {
	CurrentSessionID string          `json:"current_session_id"`
	Tasks            []scheduledTask `json:"tasks"`
}

func newScheduleStore(root string) *scheduleStore {
	return &scheduleStore{
		path: filepath.Join(root, scheduleFileName),
		file: scheduleFile{
			Version: scheduleStoreVersion,
			Tasks:   make([]scheduledTask, 0),
		},
	}
}

func (s *scheduleStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var file scheduleFile
	if err := readJSONFile(s.path, &file); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	s.file = normalizeScheduleFile(file, time.Now())
	return s.saveLocked()
}

func (s *scheduleStore) Create(ctx context.Context, sessionID string, input tool.ScheduleTaskInput) (scheduledTask, error) {
	select {
	case <-ctx.Done():
		return scheduledTask{}, ctx.Err()
	default:
	}

	normalized, err := tool.NormalizeScheduleTaskInput(input)
	if err != nil {
		return scheduledTask{}, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return scheduledTask{}, fmt.Errorf("session_id is required")
	}

	runAt, err := time.Parse(time.RFC3339Nano, normalized.RunAt)
	if err != nil {
		return scheduledTask{}, err
	}

	now := time.Now()
	timestamp := now.Format(time.RFC3339Nano)
	task := scheduledTask{
		ID:        newScheduleID(now),
		Title:     normalized.Title,
		Prompt:    normalized.Prompt,
		SessionID: sessionID,
		Status:    scheduleStatusScheduled,
		RunAt:     runAt.Format(time.RFC3339Nano),
		CreatedAt: timestamp,
		UpdatedAt: timestamp,
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.file = normalizeScheduleFile(s.file, now)
	s.file.Tasks = append(s.file.Tasks, task)
	if err := s.saveLocked(); err != nil {
		return scheduledTask{}, err
	}
	return task, nil
}

func (s *scheduleStore) Cancel(ctx context.Context, id string) (scheduledTask, error) {
	select {
	case <-ctx.Done():
		return scheduledTask{}, ctx.Err()
	default:
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return scheduledTask{}, fmt.Errorf("schedule id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.findTaskLocked(id)
	if !ok {
		return scheduledTask{}, fmt.Errorf("schedule %q not found", id)
	}
	if task.Status != scheduleStatusScheduled {
		return scheduledTask{}, fmt.Errorf("schedule %q is %s and cannot be cancelled", id, task.Status)
	}
	now := time.Now().Format(time.RFC3339Nano)
	task.Status = scheduleStatusCancelled
	task.UpdatedAt = now
	task.FinishedAt = now
	if err := s.saveLocked(); err != nil {
		return scheduledTask{}, err
	}
	return *task, nil
}

func (s *scheduleStore) Snapshot() []scheduledTask {
	s.mu.Lock()
	defer s.mu.Unlock()

	return sortedScheduleTasks(s.file.Tasks)
}

func (s *scheduleStore) ClaimDue(now time.Time) (scheduledTask, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.file = normalizeScheduleFile(s.file, now)
	bestIdx := -1
	var bestRunAt time.Time
	for idx, task := range s.file.Tasks {
		if task.Status != scheduleStatusScheduled {
			continue
		}
		runAt, err := time.Parse(time.RFC3339Nano, task.RunAt)
		if err != nil {
			s.file.Tasks[idx].Status = scheduleStatusFailed
			s.file.Tasks[idx].LastError = "invalid run_at: " + err.Error()
			s.file.Tasks[idx].UpdatedAt = now.Format(time.RFC3339Nano)
			continue
		}
		if runAt.After(now) {
			continue
		}
		if bestIdx == -1 || runAt.Before(bestRunAt) {
			bestIdx = idx
			bestRunAt = runAt
		}
	}
	if bestIdx == -1 {
		if err := s.saveLocked(); err != nil {
			return scheduledTask{}, false, err
		}
		return scheduledTask{}, false, nil
	}

	timestamp := now.Format(time.RFC3339Nano)
	s.file.Tasks[bestIdx].Status = scheduleStatusRunning
	s.file.Tasks[bestIdx].StartedAt = timestamp
	s.file.Tasks[bestIdx].UpdatedAt = timestamp
	s.file.Tasks[bestIdx].LastError = ""
	if err := s.saveLocked(); err != nil {
		return scheduledTask{}, false, err
	}
	return s.file.Tasks[bestIdx], true, nil
}

func (s *scheduleStore) Complete(ctx context.Context, id string, runID string, runErr error) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.findTaskLocked(id)
	if !ok {
		return fmt.Errorf("schedule %q not found", id)
	}
	now := time.Now().Format(time.RFC3339Nano)
	task.LastRunID = strings.TrimSpace(runID)
	task.FinishedAt = now
	task.UpdatedAt = now
	if runErr != nil {
		task.Status = scheduleStatusFailed
		task.LastError = runErr.Error()
	} else {
		task.Status = scheduleStatusCompleted
		task.LastError = ""
	}
	return s.saveLocked()
}

func (s *scheduleStore) findTaskLocked(id string) (*scheduledTask, bool) {
	for idx := range s.file.Tasks {
		if s.file.Tasks[idx].ID == id {
			return &s.file.Tasks[idx], true
		}
	}
	return nil, false
}

func (s *scheduleStore) saveLocked() error {
	s.file.Version = scheduleStoreVersion
	sort.SliceStable(s.file.Tasks, func(i, j int) bool {
		return s.file.Tasks[i].RunAt < s.file.Tasks[j].RunAt
	})
	return writeJSONAtomic(s.path, s.file)
}

func normalizeScheduleFile(file scheduleFile, now time.Time) scheduleFile {
	if file.Version == 0 {
		file.Version = scheduleStoreVersion
	}
	if file.Tasks == nil {
		file.Tasks = make([]scheduledTask, 0)
	}
	timestamp := now.Format(time.RFC3339Nano)
	for idx := range file.Tasks {
		task := &file.Tasks[idx]
		task.ID = strings.TrimSpace(task.ID)
		task.Title = strings.TrimSpace(task.Title)
		task.Prompt = strings.TrimSpace(task.Prompt)
		task.SessionID = strings.TrimSpace(task.SessionID)
		if task.Status == "" {
			task.Status = scheduleStatusScheduled
		}
		if task.Status == scheduleStatusRunning {
			task.Status = scheduleStatusFailed
			task.LastError = "interrupted while the server was stopped"
			if task.FinishedAt == "" {
				task.FinishedAt = timestamp
			}
		}
		if task.CreatedAt == "" {
			task.CreatedAt = timestamp
		}
		if task.UpdatedAt == "" {
			task.UpdatedAt = task.CreatedAt
		}
	}
	return file
}

func sortedScheduleTasks(tasks []scheduledTask) []scheduledTask {
	sorted := append([]scheduledTask(nil), tasks...)
	if sorted == nil {
		return make([]scheduledTask, 0)
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Status == scheduleStatusScheduled && sorted[j].Status != scheduleStatusScheduled {
			return true
		}
		if sorted[i].Status != scheduleStatusScheduled && sorted[j].Status == scheduleStatusScheduled {
			return false
		}
		if sorted[i].RunAt == sorted[j].RunAt {
			return sorted[i].CreatedAt > sorted[j].CreatedAt
		}
		return sorted[i].RunAt < sorted[j].RunAt
	})
	return sorted
}

func newScheduleID(now time.Time) string {
	return fmt.Sprintf("schedule-%s", now.UTC().Format("20060102T150405.000000000"))
}
