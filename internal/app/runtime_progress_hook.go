package app

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/schema"

	"ternura/agent"
	"ternura/internal/feishu"
)

type runtimeProgressHook struct {
	monitor *runtimeMonitor
}

func newRuntimeProgressHook(monitor *runtimeMonitor) *runtimeProgressHook {
	return &runtimeProgressHook{monitor: monitor}
}

func (h *runtimeProgressHook) HookName() string {
	return "runtime_progress"
}

func (h *runtimeProgressHook) BeforeModelCall(ctx context.Context, run *agent.RunContext) error {
	metrics := run.RunMetrics()
	detail := "正在思考并整理下一步"
	feishu.ReportProgress(ctx, feishu.ProgressUpdate{
		Stage:     feishu.ProgressStageThinking,
		Detail:    detail,
		ToolCalls: metrics.ToolCalls,
	})
	h.update(ctx, runtimeStageThinking, detail, metrics)
	return nil
}

func (h *runtimeProgressHook) BeforeToolCall(ctx context.Context, run *agent.RunContext, call *schema.ToolCall) error {
	name := "工具"
	if call != nil && call.Function.Name != "" {
		name = "`" + call.Function.Name + "`"
	}
	detail := fmt.Sprintf("正在调用 %s", name)
	metrics := run.RunMetrics()
	feishu.ReportProgress(ctx, feishu.ProgressUpdate{
		Stage:     feishu.ProgressStageTool,
		Detail:    detail,
		ToolCalls: metrics.ToolCalls,
	})
	h.update(ctx, runtimeStageTool, detail, metrics)
	return nil
}

func (h *runtimeProgressHook) AfterToolCall(ctx context.Context, run *agent.RunContext, result *agent.ToolResult) error {
	detail := "工具执行完成，正在整理结果"
	if result != nil && result.Err != nil {
		detail = "工具返回了错误，正在调整处理方式"
	}
	metrics := run.RunMetrics()
	feishu.ReportProgress(ctx, feishu.ProgressUpdate{
		Stage:     feishu.ProgressStageFinishing,
		Detail:    detail,
		ToolCalls: metrics.ToolCalls,
	})
	h.update(ctx, runtimeStageFinishing, detail, metrics)
	return nil
}

func (h *runtimeProgressHook) update(ctx context.Context, stage string, detail string, metrics agent.RunMetrics) {
	if h == nil || h.monitor == nil {
		return
	}
	h.monitor.Update(runtimeRunID(ctx), stage, detail, metrics)
}
