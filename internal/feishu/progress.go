package feishu

import "context"

type ProgressStage string

const (
	ProgressStageStarted   ProgressStage = "started"
	ProgressStageThinking  ProgressStage = "thinking"
	ProgressStageTool      ProgressStage = "tool"
	ProgressStageFinishing ProgressStage = "finishing"
)

type ProgressUpdate struct {
	RunID     string
	Stage     ProgressStage
	Detail    string
	ToolCalls int
}

type progressReporterKey struct{}

func withProgressReporter(ctx context.Context, reporter func(ProgressUpdate)) context.Context {
	if reporter == nil {
		return ctx
	}
	return context.WithValue(ctx, progressReporterKey{}, reporter)
}

func ReportProgress(ctx context.Context, update ProgressUpdate) {
	if ctx == nil {
		return
	}
	reporter, _ := ctx.Value(progressReporterKey{}).(func(ProgressUpdate))
	if reporter != nil {
		reporter(update)
	}
}
