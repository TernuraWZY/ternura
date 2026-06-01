package app

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"ternura/agent"
	"ternura/tool"
)

var commandFencePattern = regexp.MustCompile("(?is)```\\s*(?:bash|sh|zsh|shell|terminal|console)\\b|(?m)^\\s*\\$\\s+\\S+")
var pseudoToolCallPattern = regexp.MustCompile(`(?is)<invoke\s+name=["']?([a-zA-Z0-9_:-]+)["']?|</?minimax:tool_call\b|<brief>`)

type toolGroundingGuardHook struct {
	verifier toolGroundingVerifier
}

type toolGroundingDecision struct {
	Block          bool
	Verified       bool
	Reason         string
	MatchedClaims  []string
	RequiredTools  []string
	GroundedTools  []string
	RequireSuccess bool
	Unsupported    []toolGroundingUnsupportedClaim
	VerifierClaims []toolGroundingVerifiedClaim
}

type toolGroundingClaim struct {
	Label          string
	RequiredTools  []tool.AgentTool
	RequireSuccess bool
	Matches        func(content string, lower string) bool
}

type toolGroundingEvidence struct {
	called     map[tool.AgentTool]struct{}
	successful map[tool.AgentTool]struct{}
}

const toolGroundingGuardRetryKey = "tool_grounding_guard.retry"

func newToolGroundingGuardHook(verifier ...toolGroundingVerifier) *toolGroundingGuardHook {
	hook := &toolGroundingGuardHook{}
	if len(verifier) > 0 {
		hook.verifier = verifier[0]
	}
	return hook
}

func (h *toolGroundingGuardHook) HookName() string {
	return "tool_grounding_guard"
}

func (h *toolGroundingGuardHook) FinalizeRun(ctx context.Context, run *agent.RunContext, result *agent.AgentRunResult) error {
	if result == nil || run == nil || strings.TrimSpace(result.Content) == "" {
		return nil
	}

	decision := h.checkToolGrounding(ctx, run, result)
	if !decision.Block {
		return nil
	}

	original := result.Content
	if result.RawContent == "" {
		result.RawContent = original
	}
	if h.requestRetry(run, decision) {
		result.Trace = append(result.Trace, agent.AgentTraceItem{
			Type:    "guard",
			Title:   "Tool grounding guard",
			Content: decision.RetryTraceContent(),
		})
		return nil
	}
	if repaired, ok := h.repairFinalAnswer(ctx, run, result, decision); ok {
		result.Content = repaired
		result.Trace = append(result.Trace, agent.AgentTraceItem{
			Type:    "guard",
			Title:   "Tool grounding repair",
			Content: decision.RepairTraceContent(),
		})
		return nil
	}
	result.Trace = append(result.Trace, agent.AgentTraceItem{
		Type:    "guard",
		Title:   "Tool grounding guard",
		Content: decision.TraceContent(),
	})
	result.Content = decision.UserMessage()
	return nil
}

func (h *toolGroundingGuardHook) repairFinalAnswer(ctx context.Context, run *agent.RunContext, result *agent.AgentRunResult, decision toolGroundingDecision) (string, bool) {
	if h == nil || h.verifier == nil || run == nil || result == nil || len(decision.Unsupported) == 0 {
		return "", false
	}
	repairer, ok := h.verifier.(toolGroundingRepairer)
	if !ok {
		return "", false
	}
	repaired, err := repairer.RepairToolGrounding(ctx, toolGroundingRepairInput{
		UserMessage:       run.Query,
		FinalAnswer:       result.Content,
		ToolEvidence:      currentToolEvidence(run),
		UnsupportedClaims: decision.Unsupported,
	})
	if err != nil {
		return "", false
	}
	repaired = stripToolGroundingRepairReasoning(repaired)
	if repaired == "" || repaired == strings.TrimSpace(result.Content) {
		return "", false
	}
	verification, err := h.verifier.VerifyToolGrounding(ctx, toolGroundingVerificationInput{
		UserMessage:  run.Query,
		FinalAnswer:  repaired,
		ToolEvidence: currentToolEvidence(run),
	})
	if err != nil {
		return "", false
	}
	repairDecision := decisionFromToolGroundingVerification(verification, run)
	if repairDecision.Block {
		return "", false
	}
	return repaired, true
}

func (h *toolGroundingGuardHook) checkToolGrounding(ctx context.Context, run *agent.RunContext, result *agent.AgentRunResult) toolGroundingDecision {
	heuristic := checkToolGrounding(run, result)
	if heuristic.Block {
		return heuristic
	}
	if h != nil && h.verifier != nil && shouldVerifyToolGrounding(run, result) {
		verification, err := h.verifier.VerifyToolGrounding(ctx, toolGroundingVerificationInput{
			UserMessage:  run.Query,
			FinalAnswer:  result.Content,
			ToolEvidence: currentToolEvidence(run),
		})
		if err == nil {
			decision := decisionFromToolGroundingVerification(verification, run)
			if decision.Verified {
				if decision.HasClaimsNeedingEvidence() {
					result.Trace = append(result.Trace, agent.AgentTraceItem{
						Type:    "guard",
						Title:   "Evidence verifier",
						Content: decision.VerifierTraceContent(),
					})
				}
				return toolGroundingDecision{}
			}
			if decision.Block {
				return decision
			}
			return toolGroundingDecision{}
		}
	}
	return heuristic
}

func (h *toolGroundingGuardHook) requestRetry(run *agent.RunContext, decision toolGroundingDecision) bool {
	if run == nil {
		return false
	}
	if run.Metadata == nil {
		run.Metadata = make(map[string]any)
	}
	if retried, ok := run.Metadata[toolGroundingGuardRetryKey].(bool); ok && retried {
		return false
	}
	if !run.CanCallModel() {
		return false
	}
	required := make([]tool.AgentTool, 0, len(decision.RequiredTools))
	for _, name := range decision.RequiredTools {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		toolName := tool.AgentTool(name)
		if run.CanCallTool(toolName) {
			required = append(required, toolName)
		}
	}
	if len(decision.RequiredTools) > 0 && len(required) == 0 {
		return false
	}
	if len(required) == 0 && !run.CanCallAnyTool() {
		return false
	}
	run.Metadata[toolGroundingGuardRetryKey] = true
	if len(required) == 0 {
		run.SetToolPolicy(agent.RequireAnyTool())
	} else {
		run.SetToolPolicy(agent.ToolPolicy{
			Required:     true,
			AllowedTools: required,
		})
	}
	return true
}

func shouldVerifyToolGrounding(run *agent.RunContext, result *agent.AgentRunResult) bool {
	if run == nil || result == nil {
		return false
	}
	content := strings.TrimSpace(result.Content)
	if content == "" || looksLikeGroundingSafeDisclosure(strings.ToLower(content)) {
		return false
	}
	if len(run.ToolResults()) > 0 {
		return true
	}
	lower := strings.ToLower(content)
	return containsAny(lower,
		"搜索", "查到", "查询", "检索", "调研", "官网", "网页", "页面显示", "数据来源",
		"最近", "最新", "当前", "实时", "行情", "上涨", "下跌", "价格", "天气", "汇率",
		"安装", "执行", "运行", "创建", "删除", "更新", "保存", "已设置", "已完成",
	)
}

func checkToolGrounding(run *agent.RunContext, result *agent.AgentRunResult) toolGroundingDecision {
	content := strings.TrimSpace(result.Content)
	lower := strings.ToLower(content)
	if tools := pseudoToolCallTools(content); len(tools) > 0 {
		return toolGroundingDecision{
			Block:          true,
			Reason:         "unexecuted tool call markup",
			MatchedClaims:  []string{"unexecuted tool call markup"},
			RequiredTools:  agentToolNames(tools),
			GroundedTools:  collectToolGroundingEvidence(run, result).names(),
			RequireSuccess: false,
		}
	}
	if looksLikeGroundingSafeDisclosure(lower) {
		return toolGroundingDecision{}
	}

	evidence := collectToolGroundingEvidence(run, result)
	for _, claim := range toolGroundingClaims() {
		if !claim.Matches(content, lower) {
			continue
		}
		if claim.RequireSuccess {
			if evidence.hasSuccessfulAny(claim.RequiredTools...) {
				continue
			}
		} else if evidence.hasCalledAny(claim.RequiredTools...) {
			continue
		}
		return toolGroundingDecision{
			Block:          true,
			Reason:         claim.Label,
			MatchedClaims:  []string{claim.Label},
			RequiredTools:  agentToolNames(claim.RequiredTools),
			GroundedTools:  evidence.names(),
			RequireSuccess: claim.RequireSuccess,
		}
	}
	return toolGroundingDecision{}
}

func pseudoToolCallTools(content string) []tool.AgentTool {
	matches := pseudoToolCallPattern.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	tools := make([]tool.AgentTool, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		name := strings.TrimSpace(match[1])
		if name != "" {
			tools = append(tools, tool.AgentTool(name))
		}
	}
	if len(tools) == 0 {
		return nil
	}
	return tools
}

func toolGroundingClaims() []toolGroundingClaim {
	return []toolGroundingClaim{
		{
			Label:          "command execution result",
			RequiredTools:  []tool.AgentTool{tool.AgentToolBash},
			RequireSuccess: false,
			Matches: func(content string, lower string) bool {
				if !commandFencePattern.MatchString(content) && !containsAny(lower, "command not found", "命令不存在", "命令未找到") {
					return false
				}
				return containsAny(lower,
					"结果", "输出", "返回", "报错", "失败", "成功", "执行了", "运行了", "已执行", "已运行",
					"command not found", "not found", "version", "installed", "安装完成", "安装成功",
				)
			},
		},
		{
			Label:          "command or installation success",
			RequiredTools:  []tool.AgentTool{tool.AgentToolBash},
			RequireSuccess: true,
			Matches: func(_ string, lower string) bool {
				return containsAny(lower,
					"我执行了", "我运行了", "已经执行", "已执行", "已经运行", "已运行",
					"安装完成", "安装成功", "已经安装", "已安装", "installed successfully",
					"启动成功", "已启动", "重启成功", "已重启",
				)
			},
		},
		{
			Label:          "external lookup result",
			RequiredTools:  []tool.AgentTool{tool.AgentToolWebFetch, tool.AgentToolBash},
			RequireSuccess: true,
			Matches: func(_ string, lower string) bool {
				return containsAny(lower,
					"搜索结果", "查询结果", "我搜索", "我查到", "查到了", "检索到",
					"联网查", "网页显示", "页面显示", "官网显示",
					"天气", "股价", "汇率", "黄金价格", "最新新闻", "实时价格",
				)
			},
		},
		{
			Label:          "local environment fact",
			RequiredTools:  []tool.AgentTool{tool.AgentToolBash},
			RequireSuccess: false,
			Matches: func(_ string, lower string) bool {
				return containsAny(lower, "当前环境", "本机", "path", "python", "pip", "curl", "skillhub", "版本") &&
					containsAny(lower, "不存在", "没有安装", "已安装", "command not found", "version:", "版本是", "版本：", "路径是", "路径：", "位于")
			},
		},
		{
			Label:          "file mutation success",
			RequiredTools:  []tool.AgentTool{tool.AgentToolWrite, tool.AgentToolEdit, tool.AgentToolBash},
			RequireSuccess: true,
			Matches: func(_ string, lower string) bool {
				return containsAny(lower,
					"已写入", "已经写入", "已修改", "已经修改", "已创建文件", "已经创建文件",
					"已删除文件", "已经删除文件", "代码已改", "改好了", "保存到了",
				)
			},
		},
		{
			Label:          "memory mutation success",
			RequiredTools:  []tool.AgentTool{tool.AgentToolRemember, tool.AgentToolForgetMemory},
			RequireSuccess: true,
			Matches: func(_ string, lower string) bool {
				return containsAny(lower,
					"已记住", "已经记住", "我记住了", "已保存记忆", "已经保存记忆",
					"已删除记忆", "已经删除记忆", "已忘记", "已经忘记",
				)
			},
		},
	}
}

func looksLikeGroundingSafeDisclosure(lower string) bool {
	if containsAny(lower,
		"还没有真实执行", "没有真实执行", "尚未执行", "不能确认结果", "无法确认结果",
		"没有对应工具调用", "没有检测到真实", "需要实际调用工具", "我没有联网查询",
		"i have not actually", "i can't verify", "i cannot verify",
	) {
		return true
	}
	if containsAny(lower, "你可以运行", "可以运行以下", "建议运行", "可以执行以下", "you can run", "try running") &&
		!containsAny(lower, "运行结果", "执行结果", "输出如下", "返回如下", "已执行", "已经执行", "安装成功", "已安装") {
		return true
	}
	return false
}

func collectToolGroundingEvidence(run *agent.RunContext, result *agent.AgentRunResult) toolGroundingEvidence {
	evidence := toolGroundingEvidence{
		called:     make(map[tool.AgentTool]struct{}),
		successful: make(map[tool.AgentTool]struct{}),
	}
	if run != nil {
		for _, item := range run.ToolResults() {
			evidence.add(tool.AgentTool(item.Call.Function.Name), item.Error == "")
		}
	}
	if result != nil {
		for _, item := range result.Trace {
			name, ok := toolNameFromTraceTitle(item.Title)
			if !ok {
				continue
			}
			evidence.add(name, !strings.Contains(item.Content, "**Error**"))
		}
	}
	return evidence
}

func (e toolGroundingEvidence) add(name tool.AgentTool, success bool) {
	if strings.TrimSpace(string(name)) == "" {
		return
	}
	e.called[name] = struct{}{}
	if success {
		e.successful[name] = struct{}{}
	}
}

func (e toolGroundingEvidence) hasCalledAny(names ...tool.AgentTool) bool {
	for _, name := range names {
		if _, ok := e.called[name]; ok {
			return true
		}
	}
	return false
}

func (e toolGroundingEvidence) hasSuccessfulAny(names ...tool.AgentTool) bool {
	for _, name := range names {
		if _, ok := e.successful[name]; ok {
			return true
		}
	}
	return false
}

func (e toolGroundingEvidence) names() []string {
	names := make([]string, 0, len(e.called))
	for name := range e.called {
		if name != "" {
			names = append(names, string(name))
		}
	}
	return uniqueStrings(names)
}

func toolNameFromTraceTitle(title string) (tool.AgentTool, bool) {
	const prefix = "Tool use: "
	title = strings.TrimSpace(title)
	if !strings.HasPrefix(title, prefix) {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimPrefix(title, prefix))
	if name == "" {
		return "", false
	}
	return tool.AgentTool(name), true
}

func agentToolNames(names []tool.AgentTool) []string {
	values := make([]string, 0, len(names))
	for _, name := range names {
		if strings.TrimSpace(string(name)) != "" {
			values = append(values, string(name))
		}
	}
	return uniqueStrings(values)
}

func containsAny(content string, phrases ...string) bool {
	for _, phrase := range phrases {
		if strings.Contains(content, strings.ToLower(phrase)) {
			return true
		}
	}
	return false
}

func (d toolGroundingDecision) UserMessage() string {
	if d.Reason == "unexecuted tool call markup" {
		required := strings.Join(d.RequiredTools, "` / `")
		if required == "" {
			required = "工具"
		}
		return fmt.Sprintf("我拦截了这次回复：它生成了 `%s` 的工具调用文本，但这只是普通文本，并没有真正执行工具。\n\n我不会把这类伪工具调用发给你。请重新发起这个请求，我会先实际调用工具；如果没有拿到有效信息，会直接说明没有 fetch 到有效结果。", required)
	}
	if len(d.Unsupported) > 0 {
		required := strings.Join(d.RequiredTools, "` / `")
		if required == "" {
			required = "合适的"
		}
		return fmt.Sprintf("我拦截了这次回复：它仍然包含没有本轮工具证据支撑的 claim。已调用的工具不等于每个结论都有证据，问题出在部分具体说法没有被当前工具结果覆盖。\n\n需要补充或重新调用 `%s` 工具后，再只基于工具结果作答。", required)
	}
	required := strings.Join(d.RequiredTools, "` / `")
	if required == "" {
		required = "工具"
	}
	return fmt.Sprintf("我拦截了这次回复：它看起来在汇报真实的 `%s`，但本轮没有找到对应的 `%s` 工具证据。\n\n为了避免把模型猜测当成事实，我不会继续发送原回答。请重新发起这个请求，我会先实际调用工具，再基于工具结果给你结论。", d.Reason, required)
}

func (d toolGroundingDecision) RetryTraceContent() string {
	content := d.TraceContent()
	if content != "" {
		content += "\n\n"
	}
	return content + "**Action**\n\nAutomatically retrying this run with a required tool policy instead of returning the ungrounded final answer."
}

func (d toolGroundingDecision) RepairTraceContent() string {
	content := d.TraceContent()
	if content != "" {
		content += "\n\n"
	}
	return content + "**Action**\n\nRepaired the final answer by removing or qualifying unsupported claims instead of blocking the whole reply."
}

func (d toolGroundingDecision) VerifierTraceContent() string {
	if len(d.VerifierClaims) == 0 {
		return "Evidence verifier found no claims requiring current tool evidence."
	}
	lines := []string{"**Evidence verifier**", ""}
	for _, claim := range d.VerifierClaims {
		text := strings.TrimSpace(claim.Text)
		if text == "" {
			continue
		}
		lines = append(lines,
			"- "+text,
			fmt.Sprintf("  - needs current tool evidence: %t", claim.NeedsCurrentToolEvidence),
			"  - evidence refs: "+strings.Join(claim.EvidenceRefs, ", "),
		)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (d toolGroundingDecision) HasClaimsNeedingEvidence() bool {
	for _, claim := range d.VerifierClaims {
		if claim.NeedsCurrentToolEvidence {
			return true
		}
	}
	return false
}

func (d toolGroundingDecision) TraceContent() string {
	if len(d.Unsupported) > 0 {
		lines := []string{
			"**Reason**",
			"",
			"The evidence verifier found final-answer claims that require current-run tool evidence, but their evidence refs do not point to a successful current tool call.",
			"",
			"**Unsupported claims**",
			"",
		}
		for _, claim := range d.Unsupported {
			lines = append(lines, "- "+claim.Text)
			if claim.Reason != "" {
				lines = append(lines, "  - reason: "+claim.Reason)
			}
			if len(claim.EvidenceRefs) > 0 {
				lines = append(lines, "  - evidence refs: "+strings.Join(claim.EvidenceRefs, ", "))
			}
		}
		lines = append(lines, "", "**Grounded tools in this run**", "")
		if len(d.GroundedTools) == 0 {
			lines = append(lines, "None")
		} else {
			lines = append(lines, "`"+strings.Join(d.GroundedTools, "`, `")+"`")
		}
		return strings.TrimSpace(strings.Join(lines, "\n"))
	}
	sections := []string{
		"**Reason**",
		"",
		fmt.Sprintf("Final answer looked like a grounded tool result claim: `%s`.", d.Reason),
		"",
		"**Required evidence**",
		"",
		"`" + strings.Join(d.RequiredTools, "`, `") + "`",
		"",
		"**Require successful tool result**",
		"",
		fmt.Sprintf("%t", d.RequireSuccess),
		"",
		"**Grounded tools in this run**",
		"",
		"None",
	}
	if len(d.GroundedTools) > 0 {
		sections[len(sections)-1] = "`" + strings.Join(d.GroundedTools, "`, `") + "`"
	}
	return strings.TrimSpace(strings.Join(sections, "\n"))
}
