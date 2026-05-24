package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Service 管理 cron 任务的持久化与调度，设计参考 nanobot CronService。
type Service struct {
	mu         sync.Mutex
	root       string
	storePath  string
	legacyPath string
	file       storeFile
	running    bool
}

func NewService(root string) *Service {
	cronDir := filepath.Join(root, "cron")
	return &Service{
		root:       root,
		storePath:  filepath.Join(cronDir, "jobs.json"),
		legacyPath: filepath.Join(root, "schedules.json"),
		file: storeFile{
			Version: StoreVersion,
			Jobs:    make([]Job, 0),
		},
	}
}

func (s *Service) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.storePath), 0o755); err != nil {
		return err
	}

	var file storeFile
	err := readJSONFile(s.storePath, &file)
	switch {
	case err == nil:
		s.file = normalizeStore(file)
	case errors.Is(err, os.ErrNotExist):
		if migrateErr := s.migrateLegacyLocked(); migrateErr != nil {
			return migrateErr
		}
	default:
		backup := s.storePath + fmt.Sprintf(".corrupt-%d", time.Now().Unix())
		_ = os.Rename(s.storePath, backup)
		return fmt.Errorf("cron store corrupt, preserved at %s: %w", backup, err)
	}
	return s.saveLocked()
}

func (s *Service) Start() {
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
}

func (s *Service) Stop() {
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
}

func (s *Service) Add(ctx context.Context, params AddParams) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	message := strings.TrimSpace(params.Message)
	if message == "" {
		return Job{}, fmt.Errorf("message is required")
	}
	sessionID := strings.TrimSpace(params.SessionID)
	if sessionID == "" {
		return Job{}, fmt.Errorf("session_id is required")
	}

	now := time.Now()
	schedule, deleteAfter, err := buildScheduleFromParams(params, now)
	if err != nil {
		return Job{}, err
	}
	if params.DeleteAfterRun {
		deleteAfter = true
	}

	name := strings.TrimSpace(params.Name)
	if name == "" {
		name = defaultJobName(message)
	}

	nowMS := now.UnixMilli()
	job := Job{
		ID:       newJobID(now),
		Name:     name,
		Enabled:  true,
		Schedule: schedule,
		Payload: Payload{
			Message:   message,
			SessionID: sessionID,
			Deliver:   params.Deliver,
			Delivery:  cloneDeliveryTarget(params.Delivery),
		},
		State: JobState{
			NextRunAtMS: ComputeNextRun(schedule, nowMS),
		},
		CreatedAtMS:    nowMS,
		UpdatedAtMS:    nowMS,
		DeleteAfterRun: deleteAfter,
	}
	if job.State.NextRunAtMS == 0 {
		return Job{}, fmt.Errorf("schedule does not produce a future run time")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.file.Jobs = append(s.file.Jobs, job)
	if err := s.saveLocked(); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (s *Service) Remove(ctx context.Context, id string) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Job{}, fmt.Errorf("job id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.findJobLocked(id)
	if !ok {
		return Job{}, fmt.Errorf("job %q not found", id)
	}
	if job.State.Running {
		return Job{}, fmt.Errorf("job %q is running and cannot be removed", id)
	}
	out := *job
	s.file.Jobs = removeJobLocked(s.file.Jobs, id)
	if err := s.saveLocked(); err != nil {
		return Job{}, err
	}
	return out, nil
}

func (s *Service) Cancel(ctx context.Context, id string) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Job{}, fmt.Errorf("job id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.findJobLocked(id)
	if !ok {
		return Job{}, fmt.Errorf("job %q not found", id)
	}
	if job.State.Running {
		return Job{}, fmt.Errorf("job %q is running", id)
	}
	if !job.Enabled {
		return Job{}, fmt.Errorf("job %q is already disabled", id)
	}
	nowMS := time.Now().UnixMilli()
	job.Enabled = false
	job.UpdatedAtMS = nowMS
	job.State.NextRunAtMS = 0
	if err := s.saveLocked(); err != nil {
		return Job{}, err
	}
	return *job, nil
}

func (s *Service) List(includeDisabled bool) []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.file.Jobs))
	for _, job := range s.file.Jobs {
		if !includeDisabled && !job.Enabled && !job.State.Running {
			continue
		}
		out = append(out, job)
	}
	sortJobs(out)
	return out
}

func (s *Service) Get(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.findJobLocked(id)
	if !ok {
		return Job{}, false
	}
	return *job, true
}

func (s *Service) LegacySnapshot() []LegacyTask {
	jobs := s.List(true)
	out := make([]LegacyTask, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, job.ToLegacyTask())
	}
	return out
}

func (s *Service) NextWakeDuration(now time.Time) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.nextWakeMSLocked(now.UnixMilli())
	if next == 0 {
		return time.Duration(MaxSleepMS) * time.Millisecond
	}
	delay := next - now.UnixMilli()
	if delay < 0 {
		delay = 0
	}
	if delay > MaxSleepMS {
		delay = MaxSleepMS
	}
	return time.Duration(delay) * time.Millisecond
}

func (s *Service) ClaimDue(now time.Time) (Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.file = normalizeStore(s.file)
	nowMS := now.UnixMilli()
	bestIdx := -1
	var bestNext int64
	for idx, job := range s.file.Jobs {
		if !job.Enabled || job.State.Running {
			continue
		}
		if job.State.NextRunAtMS == 0 || job.State.NextRunAtMS > nowMS {
			continue
		}
		if bestIdx == -1 || job.State.NextRunAtMS < bestNext {
			bestIdx = idx
			bestNext = job.State.NextRunAtMS
		}
	}
	if bestIdx == -1 {
		return Job{}, false, s.saveLocked()
	}

	s.file.Jobs[bestIdx].State.Running = true
	s.file.Jobs[bestIdx].UpdatedAtMS = nowMS
	if err := s.saveLocked(); err != nil {
		return Job{}, false, err
	}
	return s.file.Jobs[bestIdx], true, nil
}

func (s *Service) RecordRun(ctx context.Context, id, runID string, started time.Time, runErr error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.findJobLocked(id)
	if !ok {
		return fmt.Errorf("job %q not found", id)
	}

	now := time.Now()
	nowMS := now.UnixMilli()
	startMS := started.UnixMilli()
	duration := nowMS - startMS
	if duration < 0 {
		duration = 0
	}

	record := RunRecord{
		RunAtMS:    startMS,
		DurationMS: duration,
		RunID:      strings.TrimSpace(runID),
	}
	if runErr != nil {
		record.Status = RunStatusError
		record.Error = runErr.Error()
		job.State.LastStatus = RunStatusError
		job.State.LastError = runErr.Error()
	} else {
		record.Status = RunStatusOK
		job.State.LastStatus = RunStatusOK
		job.State.LastError = ""
	}
	job.State.LastRunAtMS = startMS
	job.State.Running = false
	job.UpdatedAtMS = nowMS
	job.State.RunHistory = append(job.State.RunHistory, record)
	if len(job.State.RunHistory) > MaxRunHistory {
		job.State.RunHistory = job.State.RunHistory[len(job.State.RunHistory)-MaxRunHistory:]
	}

	if job.Schedule.Kind == ScheduleAt {
		if job.DeleteAfterRun {
			s.file.Jobs = removeJobLocked(s.file.Jobs, id)
			return s.saveLocked()
		}
		job.Enabled = false
		job.State.NextRunAtMS = 0
		return s.saveLocked()
	}

	next := ComputeNextRun(job.Schedule, nowMS)
	if next == 0 {
		job.Enabled = false
		job.State.NextRunAtMS = 0
	} else {
		job.State.NextRunAtMS = next
	}
	return s.saveLocked()
}

func (s *Service) findJobLocked(id string) (*Job, bool) {
	for idx := range s.file.Jobs {
		if s.file.Jobs[idx].ID == id {
			return &s.file.Jobs[idx], true
		}
	}
	return nil, false
}

func (s *Service) nextWakeMSLocked(nowMS int64) int64 {
	var next int64
	for _, job := range s.file.Jobs {
		if !job.Enabled || job.State.Running || job.State.NextRunAtMS == 0 {
			continue
		}
		if job.State.NextRunAtMS <= nowMS {
			return nowMS
		}
		if next == 0 || job.State.NextRunAtMS < next {
			next = job.State.NextRunAtMS
		}
	}
	return next
}

func normalizeStore(file storeFile) storeFile {
	if file.Version == 0 {
		file.Version = StoreVersion
	}
	if file.Jobs == nil {
		file.Jobs = make([]Job, 0)
	}
	nowMS := time.Now().UnixMilli()
	for idx := range file.Jobs {
		job := &file.Jobs[idx]
		if job.State.Running {
			job.State.Running = false
			job.State.LastStatus = RunStatusError
			job.State.LastError = "interrupted while the server was stopped"
		}
		if job.Enabled && job.State.NextRunAtMS == 0 {
			job.State.NextRunAtMS = ComputeNextRun(job.Schedule, nowMS)
		}
		if job.CreatedAtMS == 0 {
			job.CreatedAtMS = nowMS
		}
		if job.UpdatedAtMS == 0 {
			job.UpdatedAtMS = job.CreatedAtMS
		}
	}
	return file
}

func cloneDeliveryTarget(target *DeliveryTarget) *DeliveryTarget {
	if target == nil {
		return nil
	}
	cloned := *target
	cloned.Channel = strings.TrimSpace(cloned.Channel)
	cloned.ReceiveIDType = strings.TrimSpace(cloned.ReceiveIDType)
	cloned.ReceiveID = strings.TrimSpace(cloned.ReceiveID)
	cloned.MessageID = strings.TrimSpace(cloned.MessageID)
	cloned.ThreadID = strings.TrimSpace(cloned.ThreadID)
	if cloned.Channel == "" || cloned.ReceiveID == "" {
		return nil
	}
	return &cloned
}

func removeJobLocked(jobs []Job, id string) []Job {
	out := jobs[:0]
	for _, job := range jobs {
		if job.ID != id {
			out = append(out, job)
		}
	}
	if out == nil {
		return make([]Job, 0)
	}
	return out
}

func sortJobs(jobs []Job) {
	sort.SliceStable(jobs, func(i, j int) bool {
		a, b := jobs[i], jobs[j]
		if a.Enabled != b.Enabled {
			return a.Enabled
		}
		if a.State.NextRunAtMS == b.State.NextRunAtMS {
			return a.CreatedAtMS > b.CreatedAtMS
		}
		if a.State.NextRunAtMS == 0 {
			return false
		}
		if b.State.NextRunAtMS == 0 {
			return true
		}
		return a.State.NextRunAtMS < b.State.NextRunAtMS
	})
}

func (s *Service) saveLocked() error {
	s.file.Version = StoreVersion
	sortJobs(s.file.Jobs)
	return writeJSONAtomic(s.storePath, s.file)
}

func readJSONFile(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
