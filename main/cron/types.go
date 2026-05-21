package cron

const (
	StoreVersion  = 2
	MaxRunHistory = 20
	MaxSleepMS    = 300_000 // 5 minutes

	ScheduleAt    = "at"
	ScheduleEvery = "every"
	ScheduleCron  = "cron"

	RunStatusOK      = "ok"
	RunStatusError   = "error"
	RunStatusSkipped = "skipped"

	// Legacy API statuses (web UI).
	LegacyScheduled  = "scheduled"
	LegacyRunning    = "running"
	LegacyCompleted  = "completed"
	LegacyCancelled  = "cancelled"
	LegacyFailed     = "failed"
)

// Schedule 定义任务何时触发，对应 nanobot CronSchedule。
type Schedule struct {
	Kind    string `json:"kind"`
	AtMS    int64  `json:"at_ms,omitempty"`
	EveryMS int64  `json:"every_ms,omitempty"`
	Expr    string `json:"expr,omitempty"`
	TZ      string `json:"tz,omitempty"`
}

// Payload 定义到点后执行什么，对应 nanobot CronPayload（精简版：仅 agent_turn + session）。
type Payload struct {
	Message   string `json:"message"`
	SessionID string `json:"session_id"`
	Deliver   bool   `json:"deliver,omitempty"`
}

// RunRecord 单次执行记录。
type RunRecord struct {
	RunAtMS    int64  `json:"run_at_ms"`
	Status     string `json:"status"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
	RunID      string `json:"run_id,omitempty"`
}

// JobState 运行时状态。
type JobState struct {
	NextRunAtMS int64       `json:"next_run_at_ms,omitempty"`
	LastRunAtMS int64       `json:"last_run_at_ms,omitempty"`
	LastStatus  string      `json:"last_status,omitempty"`
	LastError   string      `json:"last_error,omitempty"`
	RunHistory  []RunRecord `json:"run_history,omitempty"`
	Running     bool        `json:"running,omitempty"`
}

// Job 一个 cron 任务。
type Job struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Enabled        bool     `json:"enabled"`
	Schedule       Schedule `json:"schedule"`
	Payload        Payload  `json:"payload"`
	State          JobState `json:"state"`
	CreatedAtMS    int64    `json:"created_at_ms"`
	UpdatedAtMS    int64    `json:"updated_at_ms"`
	DeleteAfterRun bool     `json:"delete_after_run,omitempty"`
}

type storeFile struct {
	Version int   `json:"version"`
	Jobs    []Job `json:"jobs,omitempty"`
}

// AddParams 创建任务参数。
type AddParams struct {
	Name           string
	Message        string
	SessionID      string
	Deliver        bool
	DeleteAfterRun bool
	EverySeconds   int
	CronExpr       string
	TZ             string
	At             string // ISO datetime
	DelaySeconds   int    // 相对延迟，映射为 at
}

// LegacyTask 兼容旧 /api/schedules 与 web UI。
type LegacyTask struct {
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
