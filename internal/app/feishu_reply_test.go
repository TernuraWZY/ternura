package app

import (
	"fmt"
	"strings"
	"testing"

	"ternura/agent"
	"ternura/tool"
)

func TestFormatFeishuAgentReplyKeepsPlainContentWhenTraceEmpty(t *testing.T) {
	reply := formatFeishuAgentReply(agent.AgentRunResult{Content: "好的"})

	if reply.Content != "好的" || reply.Card != nil {
		t.Fatalf("reply = %+v, want plain content", reply)
	}
}

func TestFormatFeishuAgentReplyUsesCollapsedEvidenceLedger(t *testing.T) {
	reply := formatFeishuAgentReply(agent.AgentRunResult{
		Content: "结论来自抓取页面 [E1]。",
		Evidence: []agent.EvidenceRecord{{
			ID:      "E1",
			Kind:    "source",
			Tool:    "web_fetch",
			Title:   "官方文档",
			URL:     "https://example.com/docs",
			Excerpt: "页面中的可核验内容。",
			Status:  "succeeded",
			Citable: true,
		}},
	})

	if reply.Card == nil {
		t.Fatal("evidence reply should use an interactive card")
	}
	for _, want := range []string{"证据账本", "[E1]", "官方文档", "https://example.com/docs", "可引用"} {
		if !strings.Contains(reply.Content, want) || !strings.Contains(fmt.Sprint(reply.Card), want) {
			t.Fatalf("evidence reply missing %q: content=%s card=%v", want, reply.Content, reply.Card)
		}
	}
}

func TestFormatFeishuAgentReplyUsesCollapsedTracePanels(t *testing.T) {
	reply := formatFeishuAgentReply(agent.AgentRunResult{
		Content: "完成了",
		Trace: []agent.AgentTraceItem{
			{Type: "think", Title: "Thinking", Content: "需要先确认文件内容。"},
			{Type: "tool", Title: "Tool use: read", Content: "**Arguments**\n\n```json\n{\"path\":\"README.md\"}\n```"},
			{Type: "guard", Title: "Guard", Content: "不应该出现在飞书问答过程里"},
		},
	})

	if reply.Card == nil {
		t.Fatalf("reply should include interactive card")
	}
	for _, want := range []string{"完成了", "## 过程信息", "### 思考", "需要先确认文件内容。", "### 工具调用", "Tool use: read", "README.md"} {
		if !strings.Contains(reply.Content, want) {
			t.Fatalf("fallback reply missing %q:\n%s", want, reply.Content)
		}
	}
	if strings.Contains(reply.Content, "Guard") {
		t.Fatalf("reply should only include think/tool trace:\n%s", reply.Content)
	}

	card, ok := reply.Card.(map[string]any)
	if !ok {
		t.Fatalf("card type = %T", reply.Card)
	}
	body, _ := card["body"].(map[string]any)
	elements, _ := body["elements"].([]any)
	var collapsedPanels int
	for _, element := range elements {
		panel, ok := element.(map[string]any)
		if !ok || panel["tag"] != "collapsible_panel" {
			continue
		}
		collapsedPanels++
		if expanded, _ := panel["expanded"].(bool); expanded {
			t.Fatalf("trace panel should be collapsed by default: %+v", panel)
		}
	}
	if collapsedPanels != 2 {
		t.Fatalf("collapsed panels = %d, want 2; card=%+v", collapsedPanels, card)
	}
}

func TestFormatFeishuAgentReplyUsesCollapsedMemoryPanel(t *testing.T) {
	reply := formatFeishuAgentReply(agent.AgentRunResult{
		Content: "我查到了。",
		Trace: []agent.AgentTraceItem{{
			Type:    "memory",
			Title:   "上下文记忆搜索",
			Content: "**Keywords**\n\n`redis`, `ttl`\n\n**Search query**\n\nredis ttl",
		}},
	})

	if reply.Card == nil {
		t.Fatalf("reply should include interactive card")
	}
	for _, want := range []string{"我查到了。", "### 上下文记忆", "redis ttl"} {
		if !strings.Contains(reply.Content, want) {
			t.Fatalf("fallback reply missing %q:\n%s", want, reply.Content)
		}
	}

	card, ok := reply.Card.(map[string]any)
	if !ok {
		t.Fatalf("card type = %T", reply.Card)
	}
	body, _ := card["body"].(map[string]any)
	elements, _ := body["elements"].([]any)
	var memoryPanel map[string]any
	for _, element := range elements {
		panel, ok := element.(map[string]any)
		if !ok || panel["tag"] != "collapsible_panel" {
			continue
		}
		header, _ := panel["header"].(map[string]any)
		title, _ := header["title"].(map[string]any)
		if strings.Contains(fmt.Sprint(title["content"]), "上下文记忆") {
			memoryPanel = panel
		}
	}
	if memoryPanel == nil {
		t.Fatalf("memory panel missing: %+v", card)
	}
	if expanded, _ := memoryPanel["expanded"].(bool); expanded {
		t.Fatalf("memory panel should be collapsed by default: %+v", memoryPanel)
	}
}

func TestFormatFeishuAgentReplyCollapsesThinkWithoutToolTrace(t *testing.T) {
	reply := formatFeishuAgentReply(agent.AgentRunResult{
		Content: "你好！",
		Trace: []agent.AgentTraceItem{{
			Type:    "think",
			Title:   "Thinking",
			Content: "用户只是打了个招呼，简单回应即可。",
		}},
	})

	if reply.Content == "你好！" || reply.Card == nil {
		t.Fatalf("reply should include collapsed think card: %+v", reply)
	}
}

func TestLimitFeishuTraceContentTruncatesLongTrace(t *testing.T) {
	limited := limitFeishuTraceContent(strings.Repeat("a", maxFeishuTraceRunes+10))

	if len([]rune(limited)) <= maxFeishuTraceRunes {
		t.Fatalf("limited trace should include truncation suffix")
	}
	if !strings.Contains(limited, "已截断") {
		t.Fatalf("limited trace missing truncation marker: %q", limited)
	}
}

func TestFormatFeishuAgentReplyRedactsEmailsInTrace(t *testing.T) {
	reply := formatFeishuAgentReply(agent.AgentRunResult{
		Content: "本轮没有拿到有效信息。",
		Trace: []agent.AgentTraceItem{{
			Type:    "tool",
			Title:   "Tool use: web_fetch",
			Content: "举报邮箱：jubao@contact.sohu.com",
		}},
	})

	if strings.Contains(reply.Content, "jubao@contact.sohu.com") {
		t.Fatalf("fallback reply should redact email:\n%s", reply.Content)
	}
	if !strings.Contains(reply.Content, "[email redacted]") {
		t.Fatalf("fallback reply missing redaction marker:\n%s", reply.Content)
	}
	card := fmt.Sprint(reply.Card)
	if strings.Contains(card, "jubao@contact.sohu.com") {
		t.Fatalf("card should redact email:\n%s", card)
	}
}

func TestFormatFeishuAgentReplyUsesApprovalButtons(t *testing.T) {
	reply := formatFeishuAgentReplyForSession(agent.AgentRunResult{
		Content: "reply approve run-1 or reject run-1",
		PendingApproval: &agent.ToolApprovalRequest{
			CheckpointID: "run-1",
			Tool:         tool.AgentToolBash,
			Arguments:    `{"command":"go test ./..."}`,
			Reason:       "将执行本地命令",
		},
	}, "feishu-session")

	card, ok := reply.Card.(map[string]any)
	if !ok {
		t.Fatalf("approval reply should use a card: %+v", reply)
	}
	formatted := fmt.Sprint(card)
	for _, want := range []string{"等待确认", "批准", "拒绝", "approve_tool", "reject_tool", "feishu-session", "run-1"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("approval card missing %q: %s", want, formatted)
		}
	}
	if strings.Contains(formatted, "reply approve") {
		t.Fatalf("card should present native controls instead of text commands: %s", formatted)
	}
	if !strings.Contains(reply.Content, "reply approve") {
		t.Fatalf("fallback content should keep text approval commands: %s", reply.Content)
	}
}
