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

	TaskStatusScheduled = "scheduled"
	TaskStatusRunning   = "running"
	TaskStatusCompleted = "completed"
	TaskStatusCancelled = "cancelled"
	TaskStatusFailed    = "failed"
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
	Message   string          `json:"message"`
	SessionID string          `json:"session_id"`
	Deliver   bool            `json:"deliver,omitempty"`
	Delivery  *DeliveryTarget `json:"delivery,omitempty"`
}

// DeliveryTarget 记录外部 channel 的回传地址。CLI 本地运行不需要它；
// Feishu 这类外部入口会用它把 cron 触发后的 Agent 回复发回原聊天。
type DeliveryTarget struct {
	Channel       string `json:"channel,omitempty"`
	ReceiveIDType string `json:"receive_id_type,omitempty"`
	ReceiveID     string `json:"receive_id,omitempty"`
	MessageID     string `json:"message_id,omitempty"`
	ThreadID      string `json:"thread_id,omitempty"`
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
	Delivery       *DeliveryTarget
	DeleteAfterRun bool
	EverySeconds   int
	CronExpr       string
	TZ             string
	At             string // ISO datetime
	DelaySeconds   int    // 相对延迟，映射为 at
}
