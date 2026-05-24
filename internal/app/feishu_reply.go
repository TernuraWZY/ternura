package app

import (
	"fmt"
	"strings"

	"ternura/agent"
	"ternura/internal/feishu"
)

const maxFeishuTraceRunes = 4000

func formatFeishuAgentReply(result agent.AgentRunResult) feishu.Reply {
	thinkItems := traceItemsByType(result.Trace, "think")
	toolItems := traceItemsByType(result.Trace, "tool")
	content := strings.TrimSpace(result.Content)
	if len(thinkItems) == 0 && len(toolItems) == 0 {
		return feishu.Reply{Content: content}
	}

	fallback := formatFeishuAgentReplyText(content, thinkItems, toolItems)
	return feishu.Reply{
		Content: fallback,
		Card:    buildFeishuAgentReplyCard(content, thinkItems, toolItems),
	}
}

func formatFeishuAgentReplyText(content string, thinkItems []agent.AgentTraceItem, toolItems []agent.AgentTraceItem) string {
	sections := make([]string, 0, 3)
	if content != "" {
		sections = append(sections, content)
	}
	if len(thinkItems) > 0 || len(toolItems) > 0 {
		sections = append(sections, formatFeishuTraceSection(thinkItems, toolItems))
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n---\n\n"))
}

func traceItemsByType(trace []agent.AgentTraceItem, traceType string) []agent.AgentTraceItem {
	items := make([]agent.AgentTraceItem, 0)
	for _, item := range trace {
		if item.Type == traceType && strings.TrimSpace(item.Content) != "" {
			items = append(items, item)
		}
	}
	return items
}

func formatFeishuTraceSection(thinkItems []agent.AgentTraceItem, toolItems []agent.AgentTraceItem) string {
	sections := []string{"## 过程信息"}
	if len(thinkItems) > 0 {
		sections = append(sections, "### 思考\n"+formatFeishuTraceGroup("思考", thinkItems))
	}
	if len(toolItems) > 0 {
		sections = append(sections, "### 工具调用\n"+formatFeishuTraceGroup("工具调用", toolItems))
	}
	return strings.Join(sections, "\n\n")
}

func formatFeishuTraceGroup(title string, items []agent.AgentTraceItem) string {
	lines := make([]string, 0, len(items)*4)
	for idx, item := range items {
		itemTitle := strings.TrimSpace(item.Title)
		if itemTitle == "" {
			itemTitle = title
		}
		if title == "思考" && strings.EqualFold(itemTitle, "thinking") {
			itemTitle = "思考"
		}
		lines = append(lines,
			fmt.Sprintf("**%d. %s**", idx+1, itemTitle),
			"",
			limitFeishuTraceContent(item.Content),
		)
	}
	return strings.Join(lines, "\n")
}

func buildFeishuAgentReplyCard(content string, thinkItems []agent.AgentTraceItem, toolItems []agent.AgentTraceItem) map[string]any {
	elements := make([]any, 0, 4)
	if content != "" {
		elements = append(elements, map[string]any{
			"tag":     "markdown",
			"content": content,
		})
	}
	if content != "" && (len(thinkItems) > 0 || len(toolItems) > 0) {
		elements = append(elements, map[string]any{"tag": "hr"})
	}
	if len(thinkItems) > 0 {
		elements = append(elements, feishuCollapsiblePanel("思考", len(thinkItems), formatFeishuTraceGroup("思考", thinkItems)))
	}
	if len(toolItems) > 0 {
		elements = append(elements, feishuCollapsiblePanel("工具调用", len(toolItems), formatFeishuTraceGroup("工具调用", toolItems)))
	}

	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"body": map[string]any{
			"elements": elements,
		},
	}
}

func feishuCollapsiblePanel(title string, count int, content string) map[string]any {
	return map[string]any{
		"tag":              "collapsible_panel",
		"expanded":         false,
		"background_color": "grey",
		"header": map[string]any{
			"title": map[string]any{
				"tag":     "markdown",
				"content": fmt.Sprintf("**%s（%d）**", title, count),
			},
			"vertical_align": "center",
			"icon": map[string]any{
				"tag":   "standard_icon",
				"token": "down-small-ccm_outlined",
				"size":  "16px 16px",
			},
			"icon_position":       "right",
			"icon_expanded_angle": -180,
		},
		"border": map[string]any{
			"color":         "grey",
			"corner_radius": "5px",
		},
		"vertical_spacing": "8px",
		"padding":          "8px 8px 8px 8px",
		"elements": []map[string]any{{
			"tag":     "markdown",
			"content": content,
		}},
	}
}

func limitFeishuTraceContent(content string) string {
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if len(runes) <= maxFeishuTraceRunes {
		return content
	}
	return string(runes[:maxFeishuTraceRunes]) + "\n\n...（过程信息较长，已截断）"
}
