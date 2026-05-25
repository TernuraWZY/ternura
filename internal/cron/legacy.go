package cron

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

// ToLegacyTask keeps the old schedule projection for migrated data and guard logic.
func (j Job) ToLegacyTask() LegacyTask {
	nextMS := j.State.NextRunAtMS
	if nextMS == 0 && j.Schedule.Kind == ScheduleAt {
		nextMS = j.Schedule.AtMS
	}
	runAt := ""
	if nextMS > 0 {
		runAt = time.UnixMilli(nextMS).Format(time.RFC3339Nano)
	} else if j.State.LastRunAtMS > 0 {
		runAt = time.UnixMilli(j.State.LastRunAtMS).Format(time.RFC3339Nano)
	}

	task := LegacyTask{
		ID:        j.ID,
		Title:     j.Name,
		Prompt:    j.Payload.Message,
		SessionID: j.Payload.SessionID,
		Status:    j.legacyStatus(),
		RunAt:     runAt,
		CreatedAt: time.UnixMilli(j.CreatedAtMS).Format(time.RFC3339Nano),
		UpdatedAt: time.UnixMilli(j.UpdatedAtMS).Format(time.RFC3339Nano),
		LastError: strings.TrimSpace(j.State.LastError),
	}
	if j.State.LastRunAtMS > 0 {
		task.StartedAt = time.UnixMilli(j.State.LastRunAtMS).Format(time.RFC3339Nano)
	}
	if !j.Enabled && j.State.LastRunAtMS > 0 {
		task.FinishedAt = time.UnixMilli(j.UpdatedAtMS).Format(time.RFC3339Nano)
	}
	if len(j.State.RunHistory) > 0 {
		last := j.State.RunHistory[len(j.State.RunHistory)-1]
		if last.RunID != "" {
			task.LastRunID = last.RunID
		}
	}
	return task
}

func (j Job) legacyStatus() string {
	if j.State.Running {
		return LegacyRunning
	}
	if !j.Enabled {
		switch j.State.LastStatus {
		case RunStatusError:
			return LegacyFailed
		case RunStatusOK:
			return LegacyCompleted
		default:
			return LegacyCancelled
		}
	}
	return LegacyScheduled
}

type legacyScheduleFile struct {
	Version int                  `json:"version"`
	Tasks   []legacyScheduleTask `json:"tasks,omitempty"`
}

type legacyScheduleTask struct {
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

func (s *Service) migrateLegacyLocked() error {
	data, err := os.ReadFile(s.legacyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var legacy legacyScheduleFile
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}
	nowMS := time.Now().UnixMilli()
	jobs := make([]Job, 0, len(legacy.Tasks))
	for _, task := range legacy.Tasks {
		job, ok := jobFromLegacyTask(task, nowMS)
		if ok {
			jobs = append(jobs, job)
		}
	}
	s.file = storeFile{Version: StoreVersion, Jobs: jobs}
	return nil
}

func jobFromLegacyTask(task legacyScheduleTask, nowMS int64) (Job, bool) {
	runAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(task.RunAt))
	if err != nil {
		return Job{}, false
	}
	createdMS := parseLegacyTimeMS(task.CreatedAt, nowMS)
	updatedMS := parseLegacyTimeMS(task.UpdatedAt, createdMS)

	job := Job{
		ID:      strings.TrimSpace(task.ID),
		Name:    strings.TrimSpace(task.Title),
		Enabled: task.Status == LegacyScheduled,
		Schedule: Schedule{
			Kind: ScheduleAt,
			AtMS: runAt.UnixMilli(),
		},
		Payload: Payload{
			Message:   strings.TrimSpace(task.Prompt),
			SessionID: strings.TrimSpace(task.SessionID),
			Deliver:   true,
		},
		CreatedAtMS:    createdMS,
		UpdatedAtMS:    updatedMS,
		DeleteAfterRun: true,
	}
	if job.ID == "" || job.Payload.Message == "" || job.Payload.SessionID == "" {
		return Job{}, false
	}
	if job.Name == "" {
		job.Name = defaultJobName(job.Payload.Message)
	}

	switch task.Status {
	case LegacyScheduled:
		job.Enabled = true
		job.State.NextRunAtMS = runAt.UnixMilli()
	case LegacyRunning:
		job.Enabled = false
		job.State.LastStatus = RunStatusError
		job.State.LastError = "interrupted while migrating legacy schedule"
	case LegacyCompleted:
		job.Enabled = false
		job.State.LastStatus = RunStatusOK
	case LegacyFailed:
		job.Enabled = false
		job.State.LastStatus = RunStatusError
		job.State.LastError = task.LastError
	default:
		job.Enabled = false
	}

	if task.LastRunID != "" && len(job.State.RunHistory) == 0 {
		job.State.RunHistory = []RunRecord{{
			RunAtMS: parseLegacyTimeMS(task.StartedAt, updatedMS),
			Status:  job.State.LastStatus,
			RunID:   task.LastRunID,
			Error:   task.LastError,
		}}
	}
	return job, true
}

func parseLegacyTimeMS(raw string, fallback int64) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return fallback
	}
	return t.UnixMilli()
}
