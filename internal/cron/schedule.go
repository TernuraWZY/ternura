package cron

import (
	"fmt"
	"strings"
	"time"

	robfigcron "github.com/robfig/cron/v3"
)

func nowMS() int64 {
	return time.Now().UnixMilli()
}

// ComputeNextRun 计算下次触发时间（毫秒）。参考 nanobot _compute_next_run。
func ComputeNextRun(sched Schedule, nowMS int64) int64 {
	switch sched.Kind {
	case ScheduleAt:
		if sched.AtMS > nowMS {
			return sched.AtMS
		}
		return 0
	case ScheduleEvery:
		if sched.EveryMS <= 0 {
			return 0
		}
		return nowMS + sched.EveryMS
	case ScheduleCron:
		if strings.TrimSpace(sched.Expr) == "" {
			return 0
		}
		loc := time.Local
		if tz := strings.TrimSpace(sched.TZ); tz != "" {
			parsed, err := time.LoadLocation(tz)
			if err != nil {
				return 0
			}
			loc = parsed
		}
		parser := robfigcron.NewParser(robfigcron.Minute | robfigcron.Hour | robfigcron.Dom | robfigcron.Month | robfigcron.Dow | robfigcron.Descriptor)
		schedule, err := parser.Parse(sched.Expr)
		if err != nil {
			return 0
		}
		base := time.UnixMilli(nowMS).In(loc)
		next := schedule.Next(base)
		return next.UnixMilli()
	default:
		return 0
	}
}

func buildScheduleFromParams(params AddParams, now time.Time) (Schedule, bool, error) {
	switch {
	case params.EverySeconds > 0:
		return Schedule{
			Kind:    ScheduleEvery,
			EveryMS: int64(params.EverySeconds) * 1000,
		}, false, nil
	case strings.TrimSpace(params.CronExpr) != "":
		if strings.TrimSpace(params.TZ) != "" {
			if _, err := time.LoadLocation(params.TZ); err != nil {
				return Schedule{}, false, fmt.Errorf("unknown timezone %q", params.TZ)
			}
		}
		return Schedule{
			Kind: ScheduleCron,
			Expr: strings.TrimSpace(params.CronExpr),
			TZ:   strings.TrimSpace(params.TZ),
		}, false, nil
	case strings.TrimSpace(params.At) != "":
		at, err := parseAtTime(params.At, now.Location())
		if err != nil {
			return Schedule{}, false, err
		}
		if !at.After(now.Add(-5 * time.Second)) {
			return Schedule{}, false, fmt.Errorf("at must be in the future")
		}
		return Schedule{Kind: ScheduleAt, AtMS: at.UnixMilli()}, true, nil
	case params.DelaySeconds > 0:
		at := now.Add(time.Duration(params.DelaySeconds) * time.Second)
		return Schedule{Kind: ScheduleAt, AtMS: at.UnixMilli()}, true, nil
	default:
		return Schedule{}, false, fmt.Errorf("one of every_seconds, cron_expr, at, or delay_seconds is required")
	}
}

func parseAtTime(raw string, defaultLoc *time.Location) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("at is required")
	}
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			if t.Location() == time.UTC && layout != time.RFC3339Nano && layout != time.RFC3339 {
				t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), defaultLoc)
			}
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid ISO datetime %q", raw)
}

func defaultJobName(message string) string {
	fields := strings.Fields(strings.TrimSpace(message))
	if len(fields) == 0 {
		return "Scheduled task"
	}
	runes := []rune(strings.Join(fields, " "))
	if len(runes) <= 30 {
		return string(runes)
	}
	return string(runes[:30]) + "..."
}

func newJobID(now time.Time) string {
	return fmt.Sprintf("cron-%s", now.UTC().Format("20060102T150405.000000000"))
}
