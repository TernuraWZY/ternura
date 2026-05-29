package cron

func (j Job) Status() string {
	if j.State.Running {
		return TaskStatusRunning
	}
	if !j.Enabled {
		switch j.State.LastStatus {
		case RunStatusError:
			return TaskStatusFailed
		case RunStatusOK:
			return TaskStatusCompleted
		default:
			return TaskStatusCancelled
		}
	}
	return TaskStatusScheduled
}
