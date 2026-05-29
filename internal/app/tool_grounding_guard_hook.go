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

type toolGroundingGuardHook struct{}

type toolGroundingDecision struct {
	Block          bool
	Reason         string
	MatchedClaims  []string
	RequiredTools  []string
	GroundedTools  []string
	RequireSuccess bool
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

func newToolGroundingGuardHook() *toolGroundingGuardHook {
	return &toolGroundingGuardHook{}
}

func (h *toolGroundingGuardHook) HookName() string {
	return "tool_grounding_guard"
}

func (h *toolGroundingGuardHook) FinalizeRun(_ context.Context, run *agent.RunContext, result *agent.AgentRunResult) error {
	if result == nil || run == nil || strings.TrimSpace(result.Content) == "" {
		return nil
	}

	decision := checkToolGrounding(run, result)
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
	result.Trace = append(result.Trace, agent.AgentTraceItem{
		Type:    "guard",
		Title:   "Tool grounding guard",
		Content: decision.TraceContent(),
	})
	result.Content = decision.UserMessage()
	return nil
}

func (h *toolGroundingGuardHook) requestRetry(run *agent.RunContext, decision toolGroundingDecision) bool {
	if run == nil || len(decision.RequiredTools) == 0 {
		return false
	}
	if run.Metadata == nil {
		run.Metadata = make(map[string]any)
	}
	if retried, ok := run.Metadata[toolGroundingGuardRetryKey].(bool); ok && retried {
		return false
	}
	required := make([]tool.AgentTool, 0, len(decision.RequiredTools))
	for _, name := range decision.RequiredTools {
		name = strings.TrimSpace(name)
		if name != "" {
			required = append(required, tool.AgentTool(name))
		}
	}
	if len(required) == 0 {
		return false
	}
	run.Metadata[toolGroundingGuardRetryKey] = true
	run.SetToolPolicy(agent.ToolPolicy{
		Required:     true,
		AllowedTools: required,
	})
	return true
}

func checkToolGrounding(run *agent.RunContext, result *agent.AgentRunResult) toolGroundingDecision {
	content := strings.TrimSpace(result.Content)
	lower := strings.ToLower(content)
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

func (d toolGroundingDecision) TraceContent() string {
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
