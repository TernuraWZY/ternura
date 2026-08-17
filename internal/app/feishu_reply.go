package app

import (
	"fmt"
	"regexp"
	"strings"

	"ternura/agent"
	"ternura/internal/feishu"
)

const maxFeishuTraceRunes = 4000

var feishuEmailPattern = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)

func formatFeishuAgentReply(result agent.AgentRunResult) feishu.Reply {
	return formatFeishuAgentReplyForSession(result, "")
}

func formatFeishuAgentReplyForSession(result agent.AgentRunResult, sessionID string) feishu.Reply {
	memoryItems := traceItemsByType(result.Trace, "memory")
	thinkItems := traceItemsByType(result.Trace, "think")
	toolItems := traceItemsByType(result.Trace, "tool")
	evidenceItems := sanitizeEvidenceForFeishu(result.Evidence)
	fallbackContent := sanitizeFeishuReplyText(strings.TrimSpace(result.Content))
	cardContent := fallbackContent
	if result.PendingApproval != nil {
		cardContent = formatFeishuApprovalSummary(*result.PendingApproval)
	}
	memoryItems = sanitizeTraceItemsForFeishu(memoryItems)
	thinkItems = sanitizeTraceItemsForFeishu(thinkItems)
	toolItems = sanitizeTraceItemsForFeishu(toolItems)
	if result.PendingApproval == nil && len(memoryItems) == 0 && len(thinkItems) == 0 && len(toolItems) == 0 && len(evidenceItems) == 0 {
		return feishu.Reply{Content: fallbackContent}
	}

	fallback := formatFeishuAgentReplyText(fallbackContent, memoryItems, thinkItems, toolItems, evidenceItems)
	return feishu.Reply{
		Content: fallback,
		Card:    buildFeishuAgentReplyCard(cardContent, memoryItems, thinkItems, toolItems, evidenceItems, result.PendingApproval, sessionID),
	}
}

func formatFeishuAgentReplyText(content string, memoryItems []agent.AgentTraceItem, thinkItems []agent.AgentTraceItem, toolItems []agent.AgentTraceItem, evidenceItems []agent.EvidenceRecord) string {
	sections := make([]string, 0, 3)
	if content != "" {
		sections = append(sections, content)
	}
	if len(memoryItems) > 0 || len(thinkItems) > 0 || len(toolItems) > 0 {
		sections = append(sections, formatFeishuTraceSection(memoryItems, thinkItems, toolItems))
	}
	if len(evidenceItems) > 0 {
		sections = append(sections, "## 证据账本\n\n"+formatFeishuEvidenceGroup(evidenceItems))
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

func formatFeishuTraceSection(memoryItems []agent.AgentTraceItem, thinkItems []agent.AgentTraceItem, toolItems []agent.AgentTraceItem) string {
	sections := []string{"## 过程信息"}
	if len(memoryItems) > 0 {
		sections = append(sections, "### 上下文记忆\n"+formatFeishuTraceGroup("上下文记忆", memoryItems))
	}
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

func buildFeishuAgentReplyCard(content string, memoryItems []agent.AgentTraceItem, thinkItems []agent.AgentTraceItem, toolItems []agent.AgentTraceItem, evidenceItems []agent.EvidenceRecord, approval *agent.ToolApprovalRequest, sessionID string) map[string]any {
	elements := make([]any, 0, 6)
	if content != "" {
		elements = append(elements, map[string]any{
			"tag":     "markdown",
			"content": content,
		})
	}
	if content != "" && (len(memoryItems) > 0 || len(thinkItems) > 0 || len(toolItems) > 0 || len(evidenceItems) > 0) {
		elements = append(elements, map[string]any{"tag": "hr"})
	}
	if len(memoryItems) > 0 {
		elements = append(elements, feishuCollapsiblePanel("上下文记忆", len(memoryItems), formatFeishuTraceGroup("上下文记忆", memoryItems)))
	}
	if len(thinkItems) > 0 {
		elements = append(elements, feishuCollapsiblePanel("思考", len(thinkItems), formatFeishuTraceGroup("思考", thinkItems)))
	}
	if len(toolItems) > 0 {
		elements = append(elements, feishuCollapsiblePanel("工具调用", len(toolItems), formatFeishuTraceGroup("工具调用", toolItems)))
	}
	if len(evidenceItems) > 0 {
		elements = append(elements, feishuCollapsiblePanel("证据账本", len(evidenceItems), formatFeishuEvidenceGroup(evidenceItems)))
	}
	if approval != nil {
		elements = append(elements, feishuApprovalActions(*approval, sessionID))
	}

	card := map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"body": map[string]any{
			"elements": elements,
		},
	}
	if approval != nil {
		card["header"] = map[string]any{
			"title":    map[string]string{"tag": "plain_text", "content": "等待确认"},
			"template": "orange",
		}
	}
	return card
}

func formatFeishuEvidenceGroup(records []agent.EvidenceRecord) string {
	sections := make([]string, 0, len(records))
	for _, record := range records {
		title := strings.TrimSpace(record.Title)
		if title == "" {
			title = record.Tool
		}
		lines := []string{fmt.Sprintf("**[%s] %s**", record.ID, title)}
		kind := record.Kind
		if record.Citable {
			kind += " · 可引用"
		} else {
			kind += " · 仅供发现/审计"
		}
		lines = append(lines, fmt.Sprintf("`%s` · `%s` · `%s`", record.Tool, kind, record.Status))
		if record.URL != "" {
			lines = append(lines, record.URL)
		}
		if record.Excerpt != "" {
			lines = append(lines, "", limitFeishuTraceContent(record.Excerpt))
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}
	return strings.Join(sections, "\n\n")
}

func formatFeishuApprovalSummary(approval agent.ToolApprovalRequest) string {
	reason := strings.TrimSpace(approval.Reason)
	if reason == "" {
		reason = "这个工具调用会产生外部副作用。"
	}
	arguments := strings.TrimSpace(approval.Arguments)
	if arguments == "" {
		arguments = "{}"
	}
	return strings.Join([]string{
		"**需要确认后才能继续执行**",
		"",
		"- 工具：`" + string(approval.Tool) + "`",
		"- 原因：" + sanitizeFeishuReplyText(reason),
		"- 参数：`" + limitFeishuTraceContent(arguments) + "`",
	}, "\n")
}

func feishuApprovalActions(approval agent.ToolApprovalRequest, sessionID string) map[string]any {
	baseValue := map[string]any{
		"session_id":    sessionID,
		"checkpoint_id": approval.CheckpointID,
	}
	button := func(text string, buttonType string, action string) map[string]any {
		value := make(map[string]any, len(baseValue)+1)
		for key, item := range baseValue {
			value[key] = item
		}
		value["action"] = action
		return map[string]any{
			"tag":   "button",
			"text":  map[string]string{"tag": "plain_text", "content": text},
			"type":  buttonType,
			"width": "fill",
			"behaviors": []any{map[string]any{
				"type":  "callback",
				"value": value,
			}},
		}
	}
	return map[string]any{
		"tag":                "column_set",
		"flex_mode":          "none",
		"horizontal_spacing": "8px",
		"columns": []any{
			map[string]any{
				"tag": "column", "width": "weighted", "weight": 1,
				"elements": []any{button("批准", "primary", "approve_tool")},
			},
			map[string]any{
				"tag": "column", "width": "weighted", "weight": 1,
				"elements": []any{button("拒绝", "danger", "reject_tool")},
			},
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
	content = sanitizeFeishuReplyText(strings.TrimSpace(content))
	runes := []rune(content)
	if len(runes) <= maxFeishuTraceRunes {
		return content
	}
	return string(runes[:maxFeishuTraceRunes]) + "\n\n...（过程信息较长，已截断）"
}

func sanitizeTraceItemsForFeishu(items []agent.AgentTraceItem) []agent.AgentTraceItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]agent.AgentTraceItem, 0, len(items))
	for _, item := range items {
		item.Content = sanitizeFeishuReplyText(item.Content)
		out = append(out, item)
	}
	return out
}

func sanitizeEvidenceForFeishu(records []agent.EvidenceRecord) []agent.EvidenceRecord {
	if len(records) == 0 {
		return nil
	}
	out := make([]agent.EvidenceRecord, len(records))
	copy(out, records)
	for idx := range out {
		out[idx].Title = sanitizeFeishuReplyText(out[idx].Title)
		out[idx].Excerpt = sanitizeFeishuReplyText(out[idx].Excerpt)
	}
	return out
}

func sanitizeFeishuReplyText(content string) string {
	return feishuEmailPattern.ReplaceAllString(content, "[email redacted]")
}
