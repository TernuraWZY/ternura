package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	pendingPlanStoreVersion = 1
	pendingPlanFileName     = "pending_plan.json"
)

type pendingPlan struct {
	Version         int    `json:"version"`
	ID              string `json:"id"`
	SessionID       string `json:"session_id"`
	OriginalMessage string `json:"original_message"`
	Task            string `json:"task"`
	Plan            string `json:"plan"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

type planStore struct {
	root string
}

func newPlanStore(root string) *planStore {
	return &planStore{root: root}
}

func (s *planStore) Pending(sessionID string) (pendingPlan, bool, error) {
	if s == nil || strings.TrimSpace(sessionID) == "" {
		return pendingPlan{}, false, nil
	}
	var plan pendingPlan
	if err := readJSONFile(s.pendingPlanPath(sessionID), &plan); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return pendingPlan{}, false, nil
		}
		return pendingPlan{}, false, err
	}
	if strings.TrimSpace(plan.ID) == "" {
		return pendingPlan{}, false, nil
	}
	return plan, true, nil
}

func (s *planStore) Save(plan pendingPlan) error {
	if s == nil || strings.TrimSpace(plan.SessionID) == "" {
		return nil
	}
	now := time.Now().Format(time.RFC3339Nano)
	if plan.Version == 0 {
		plan.Version = pendingPlanStoreVersion
	}
	if strings.TrimSpace(plan.ID) == "" {
		plan.ID = newPendingPlanID(time.Now())
	}
	if strings.TrimSpace(plan.CreatedAt) == "" {
		plan.CreatedAt = now
	}
	plan.UpdatedAt = now
	return writeJSONAtomic(s.pendingPlanPath(plan.SessionID), plan)
}

func (s *planStore) Clear(sessionID string) error {
	if s == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if err := os.Remove(s.pendingPlanPath(sessionID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *planStore) pendingPlanPath(sessionID string) string {
	return filepath.Join(s.root, sessionsDirName, sessionID, pendingPlanFileName)
}

func newPendingPlanID(now time.Time) string {
	return "plan-" + now.UTC().Format("20060102T150405.000000000")
}
