package app

import (
	"context"
	"strings"
	"testing"

	"ternura/agent"
	"ternura/tool"
)

func TestToolGroundingGuardRetriesCommandResultWithoutBashEvidence(t *testing.T) {
	run := agent.NewRunContext("帮我安装 skillhub", agent.RunModeSync)
	result := agent.AgentRunResult{
		Content: "我执行了：\n\n```bash\ncurl -fsSL https://skillhub.cn/install/skillhub.md | bash\n```\n\n安装完成。",
	}

	err := newToolGroundingGuardHook().FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	policy := run.RequestedToolPolicy()
	if !policy.Required || len(policy.AllowedTools) != 1 || policy.AllowedTools[0] != tool.AgentToolBash {
		t.Fatalf("policy = %+v, want required bash", policy)
	}
	if len(result.Trace) != 1 || result.Trace[0].Type != "guard" {
		t.Fatalf("guard trace not appended: %+v", result.Trace)
	}
	if !strings.Contains(result.Trace[0].Content, "Automatically retrying") {
		t.Fatalf("guard trace should record retry action: %+v", result.Trace)
	}
}

func TestToolGroundingGuardBlocksInstallClaimEvenWhenOnlyWebFetchRan(t *testing.T) {
	run := agent.NewRunContext("帮我安装 skillhub", agent.RunModeSync)
	run.Metadata[toolGroundingGuardRetryKey] = true
	result := agent.AgentRunResult{
		Content: "我执行了安装脚本，SkillHub 已安装。",
		Trace: []agent.AgentTraceItem{{
			Type:    "tool",
			Title:   "Tool use: web_fetch",
			Content: "**Result**\n\n```text\ninstall instructions\n```",
		}},
	}

	err := newToolGroundingGuardHook().FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if !strings.Contains(result.Content, "`bash` 工具证据") {
		t.Fatalf("guarded content = %q", result.Content)
	}
}

func TestToolGroundingGuardAllowsAdviceWithoutClaimingExecution(t *testing.T) {
	run := agent.NewRunContext("怎么安装 skillhub", agent.RunModeSync)
	content := "你可以运行以下命令安装：\n\n```bash\ncurl -fsSL https://skillhub.cn/install/skillhub.md | bash\n```"
	result := agent.AgentRunResult{Content: content}

	err := newToolGroundingGuardHook().FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if result.Content != content {
		t.Fatalf("content should be unchanged, got %q", result.Content)
	}
}

func TestToolGroundingGuardAllowsCommandResultWithBashEvidence(t *testing.T) {
	run := agent.NewRunContext("查一下 python 版本", agent.RunModeSync)
	content := "本机 Python 版本是 3.9.6。"
	result := agent.AgentRunResult{
		Content: content,
		Trace: []agent.AgentTraceItem{{
			Type:    "tool",
			Title:   "Tool use: bash",
			Content: "**Result**\n\n```text\nPython 3.9.6\n```",
		}},
	}

	err := newToolGroundingGuardHook().FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if result.Content != content {
		t.Fatalf("content should be unchanged, got %q", result.Content)
	}
}

func TestToolGroundingGuardDoesNotPoliceExternalFactWithoutLookupEvidence(t *testing.T) {
	run := agent.NewRunContext("北京天气怎么样", agent.RunModeSync)
	content := "北京天气：晴，气温 25 度。"
	result := agent.AgentRunResult{Content: content}

	err := newToolGroundingGuardHook().FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if result.Content != content {
		t.Fatalf("content should be unchanged, got %q", result.Content)
	}
	if policy := run.RequestedToolPolicy(); !policy.Empty() {
		t.Fatalf("policy = %+v, want empty", policy)
	}
}

func TestToolGroundingGuardBlocksSuccessfulInstallClaimAfterFailedBash(t *testing.T) {
	run := agent.NewRunContext("安装 skillhub", agent.RunModeSync)
	run.Metadata[toolGroundingGuardRetryKey] = true
	result := agent.AgentRunResult{
		Content: "SkillHub 已安装成功。",
		Trace: []agent.AgentTraceItem{{
			Type:    "tool",
			Title:   "Tool use: bash",
			Content: "**Error**\n\n```text\ncommand failed\n```",
		}},
	}

	err := newToolGroundingGuardHook().FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if !strings.Contains(result.Content, "command or installation success") {
		t.Fatalf("guarded content = %q", result.Content)
	}
}

func TestToolGroundingGuardRetriesPseudoToolCallMarkup(t *testing.T) {
	run := agent.NewRunContext("你再看", agent.RunModeSync)
	result := agent.AgentRunResult{
		Content: "让我实际搜一下：\n\n<brief>\n<invoke name=\"web_fetch\">\n<parameter name=\"url\">https://example.com</parameter>\n</invoke>\n</minimax:tool_call>",
	}

	err := newToolGroundingGuardHook().FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	policy := run.RequestedToolPolicy()
	if !policy.Required || len(policy.AllowedTools) != 1 || policy.AllowedTools[0] != tool.AgentToolWebFetch {
		t.Fatalf("policy = %+v, want required web_fetch", policy)
	}
	if len(result.Trace) != 1 || !strings.Contains(result.Trace[0].Content, "unexecuted tool call markup") {
		t.Fatalf("guard trace should explain pseudo tool call: %+v", result.Trace)
	}
}

func TestToolGroundingGuardBlocksPseudoToolCallMarkupAfterRetry(t *testing.T) {
	run := agent.NewRunContext("你再看", agent.RunModeSync)
	run.Metadata[toolGroundingGuardRetryKey] = true
	result := agent.AgentRunResult{
		Content: "让我实际搜一下：\n\n<brief>\n<invoke name=\"web_fetch\">\n<parameter name=\"url\">https://example.com</parameter>\n</invoke>\n</minimax:tool_call>",
	}

	err := newToolGroundingGuardHook().FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if strings.Contains(result.Content, "<invoke") || strings.Contains(result.Content, "minimax:tool_call") {
		t.Fatalf("pseudo tool call should not be returned to user: %q", result.Content)
	}
	if !strings.Contains(result.Content, "工具调用文本") || !strings.Contains(result.Content, "没有真正执行工具") {
		t.Fatalf("blocked content should explain pseudo tool call: %q", result.Content)
	}
}
