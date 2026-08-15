package app

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/schema"

	"ternura/agent"
	"ternura/internal/feishu"
)

type feishuProgressHook struct{}

func newFeishuProgressHook() *feishuProgressHook {
	return &feishuProgressHook{}
}

func (h *feishuProgressHook) HookName() string {
	return "feishu_progress"
}

func (h *feishuProgressHook) BeforeModelCall(ctx context.Context, run *agent.RunContext) error {
	feishu.ReportProgress(ctx, feishu.ProgressUpdate{
		Stage:     feishu.ProgressStageThinking,
		Detail:    "正在思考并整理下一步",
		ToolCalls: run.RunMetrics().ToolCalls,
	})
	return nil
}

func (h *feishuProgressHook) BeforeToolCall(ctx context.Context, run *agent.RunContext, call *schema.ToolCall) error {
	name := "工具"
	if call != nil && call.Function.Name != "" {
		name = "`" + call.Function.Name + "`"
	}
	feishu.ReportProgress(ctx, feishu.ProgressUpdate{
		Stage:     feishu.ProgressStageTool,
		Detail:    fmt.Sprintf("正在调用 %s", name),
		ToolCalls: run.RunMetrics().ToolCalls,
	})
	return nil
}

func (h *feishuProgressHook) AfterToolCall(ctx context.Context, run *agent.RunContext, result *agent.ToolResult) error {
	detail := "工具执行完成，正在整理结果"
	if result != nil && result.Err != nil {
		detail = "工具返回了错误，正在调整处理方式"
	}
	feishu.ReportProgress(ctx, feishu.ProgressUpdate{
		Stage:     feishu.ProgressStageFinishing,
		Detail:    detail,
		ToolCalls: run.RunMetrics().ToolCalls,
	})
	return nil
}
