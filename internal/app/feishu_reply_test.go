package app

import (
	"strings"
	"testing"

	"ternura/agent"
)

func TestFormatFeishuAgentReplyKeepsPlainContentWhenTraceEmpty(t *testing.T) {
	reply := formatFeishuAgentReply(agent.AgentRunResult{Content: "好的"})

	if reply.Content != "好的" || reply.Card != nil {
		t.Fatalf("reply = %+v, want plain content", reply)
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
