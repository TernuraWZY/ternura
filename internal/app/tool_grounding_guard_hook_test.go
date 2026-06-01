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

func TestToolGroundingGuardUsesVerifierEvidenceContract(t *testing.T) {
	run := agent.NewRunContext("看一下最近三天行情", agent.RunModeSync)
	verifier := &fakeToolGroundingVerifier{
		result: toolGroundingVerification{Claims: []toolGroundingVerifiedClaim{{
			Text:                     "513010 最近三天上涨",
			NeedsCurrentToolEvidence: true,
			Reason:                   "recent market data needs current-run evidence",
			SuggestedTools:           []string{string(tool.AgentToolWebFetch)},
		}}},
	}
	result := agent.AgentRunResult{Content: "513010 最近三天上涨，数据来源腾讯财经。"}

	err := newToolGroundingGuardHook(verifier).FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if verifier.calls != 1 {
		t.Fatalf("verifier calls = %d, want 1", verifier.calls)
	}
	if verifier.lastInput.UserMessage != "看一下最近三天行情" || verifier.lastInput.FinalAnswer != result.Content {
		t.Fatalf("verifier input = %+v", verifier.lastInput)
	}
	policy := run.RequestedToolPolicy()
	if !policy.Required || len(policy.AllowedTools) != 1 || policy.AllowedTools[0] != tool.AgentToolWebFetch {
		t.Fatalf("policy = %+v, want required web_fetch", policy)
	}
	if len(result.Trace) != 1 || !strings.Contains(result.Trace[0].Content, "Unsupported claims") {
		t.Fatalf("guard trace = %+v", result.Trace)
	}
}

func TestToolGroundingGuardAllowsVerifierClaimsThatDoNotNeedCurrentEvidence(t *testing.T) {
	run := agent.NewRunContext("解释一下什么是 ETF", agent.RunModeSync)
	content := "ETF 是一种交易型开放式指数基金。"
	verifier := &fakeToolGroundingVerifier{
		result: toolGroundingVerification{Claims: []toolGroundingVerifiedClaim{{
			Text:                     "ETF 是一种交易型开放式指数基金",
			NeedsCurrentToolEvidence: false,
		}}},
	}
	result := agent.AgentRunResult{Content: content}

	err := newToolGroundingGuardHook(verifier).FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if result.Content != content {
		t.Fatalf("content should be unchanged, got %q", result.Content)
	}
	if len(result.Trace) != 0 {
		t.Fatalf("trace should stay quiet for no-evidence-needed claims: %+v", result.Trace)
	}
	if policy := run.RequestedToolPolicy(); !policy.Empty() {
		t.Fatalf("policy = %+v, want empty", policy)
	}
}

func TestToolGroundingGuardRepairsUnsupportedVerifierClaimsAfterRetry(t *testing.T) {
	run := agent.NewRunContext("看一下 513010", agent.RunModeSync)
	run.Metadata[toolGroundingGuardRetryKey] = true
	verifier := &fakeToolGroundingVerifier{
		results: []toolGroundingVerification{
			{Claims: []toolGroundingVerifiedClaim{{
				Text:                     "基金经理为刘依姗、张湛",
				NeedsCurrentToolEvidence: true,
				Reason:                   "current tool evidence did not include fund manager names",
				SuggestedTools:           []string{string(tool.AgentToolWebFetch)},
			}}},
			{Claims: []toolGroundingVerifiedClaim{{
				Text:                     "本轮工具结果没有覆盖基金经理字段",
				NeedsCurrentToolEvidence: false,
			}}},
		},
		repairResult: "513010 本轮抓到的是行情和净值数据；本轮工具结果没有覆盖基金经理字段。",
	}
	result := agent.AgentRunResult{Content: "513010 当前市价 0.639，基金经理为刘依姗、张湛。"}

	err := newToolGroundingGuardHook(verifier).FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if verifier.calls != 2 {
		t.Fatalf("verifier calls = %d, want 2", verifier.calls)
	}
	if verifier.repairCalls != 1 {
		t.Fatalf("repair calls = %d, want 1", verifier.repairCalls)
	}
	if result.Content != verifier.repairResult {
		t.Fatalf("content = %q, want repaired answer", result.Content)
	}
	if len(result.Trace) != 1 || result.Trace[0].Title != "Tool grounding repair" {
		t.Fatalf("repair trace not appended: %+v", result.Trace)
	}
	if strings.Contains(result.Content, "刘依姗") || strings.Contains(result.Content, "张湛") {
		t.Fatalf("unsupported claim should be removed: %q", result.Content)
	}
}

func TestToolGroundingGuardStripsThinkFromRepairedAnswer(t *testing.T) {
	run := agent.NewRunContext("看一下 MU", agent.RunModeSync)
	run.Metadata[toolGroundingGuardRetryKey] = true
	verifier := &fakeToolGroundingVerifier{
		results: []toolGroundingVerification{
			{Claims: []toolGroundingVerifiedClaim{{
				Text:                     "MU 当前价格 $971",
				NeedsCurrentToolEvidence: true,
				Reason:                   "unsupported market quote",
				SuggestedTools:           []string{string(tool.AgentToolWebFetch)},
			}}},
			{Claims: []toolGroundingVerifiedClaim{{
				Text:                     "本轮没有拿到可靠 MU 行情",
				NeedsCurrentToolEvidence: false,
			}}},
		},
		repairResult: "<think>先删除没证据的价格。</think>\n本轮没有拿到可靠 MU 行情。",
	}
	result := agent.AgentRunResult{Content: "MU 当前价格 $971。"}

	err := newToolGroundingGuardHook(verifier).FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if result.Content != "本轮没有拿到可靠 MU 行情。" {
		t.Fatalf("content = %q, want stripped repaired answer", result.Content)
	}
	if strings.Contains(result.Content, "<think>") {
		t.Fatalf("repaired content should not contain think block: %q", result.Content)
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

func TestToolGroundingGuardBlocksExternalFactWithoutLookupEvidence(t *testing.T) {
	run := agent.NewRunContext("北京天气怎么样", agent.RunModeSync)
	run.Metadata[toolGroundingGuardRetryKey] = true
	result := agent.AgentRunResult{Content: "北京天气：晴，气温 25 度。"}

	err := newToolGroundingGuardHook().FinalizeRun(context.Background(), run, &result)

	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	if !strings.Contains(result.Content, "external lookup result") {
		t.Fatalf("guarded content = %q", result.Content)
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

type fakeToolGroundingVerifier struct {
	result       toolGroundingVerification
	results      []toolGroundingVerification
	err          error
	calls        int
	lastInput    toolGroundingVerificationInput
	repairResult string
	repairErr    error
	repairCalls  int
	lastRepair   toolGroundingRepairInput
}

func (f *fakeToolGroundingVerifier) VerifyToolGrounding(ctx context.Context, input toolGroundingVerificationInput) (toolGroundingVerification, error) {
	idx := f.calls
	f.calls++
	f.lastInput = input
	if len(f.results) > 0 {
		if idx >= len(f.results) {
			idx = len(f.results) - 1
		}
		return f.results[idx], f.err
	}
	return f.result, f.err
}

func (f *fakeToolGroundingVerifier) RepairToolGrounding(ctx context.Context, input toolGroundingRepairInput) (string, error) {
	f.repairCalls++
	f.lastRepair = input
	return f.repairResult, f.repairErr
}
