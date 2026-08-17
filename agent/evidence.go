package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ternura/tool"
)

const evidenceExcerptRunes = 1200

type EvidenceRecord struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Tool        string `json:"tool"`
	Title       string `json:"title,omitempty"`
	URL         string `json:"url,omitempty"`
	Excerpt     string `json:"excerpt,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
	Status      string `json:"status"`
	Citable     bool   `json:"citable"`
	RetrievedAt string `json:"retrieved_at,omitempty"`
	ToolCallID  string `json:"tool_call_id,omitempty"`
}

func evidenceRecordsFromToolResult(result ToolResult) []EvidenceRecord {
	name := tool.AgentTool(strings.TrimSpace(result.Call.Function.Name))
	if name == tool.AgentToolCompact {
		return nil
	}

	base := EvidenceRecord{
		Tool:        string(name),
		ToolCallID:  result.Call.ID,
		Status:      "succeeded",
		ContentHash: evidenceContentHash(result.Content),
		RetrievedAt: evidenceTime(result.FinishedAt),
	}
	if result.Err != nil {
		base.Kind = "execution"
		base.Title = "Failed tool call: " + string(name)
		base.Status = "failed"
		base.Excerpt = evidenceExcerpt(result.ErrorString())
		return []EvidenceRecord{base}
	}
	if strings.HasPrefix(result.Content, "Tool call ") && strings.Contains(result.Content, " was rejected:") {
		base.Kind = "action"
		base.Title = "Rejected tool call: " + string(name)
		base.Status = "rejected"
		base.Excerpt = evidenceExcerpt(result.Content)
		return []EvidenceRecord{base}
	}

	switch name {
	case tool.AgentToolWebSearch:
		return webSearchEvidence(base, result.Content)
	case tool.AgentToolWebFetch:
		return webFetchEvidence(base, result.Content)
	case tool.AgentToolWrite, tool.AgentToolEdit, tool.AgentToolUpdateTodos,
		tool.AgentToolRemember, tool.AgentToolForgetMemory, tool.AgentToolCron:
		base.Kind = "action"
		base.Title = "Tool action: " + string(name)
		base.Citable = strings.TrimSpace(result.Content) != ""
	default:
		base.Kind = "observation"
		base.Title = "Tool observation: " + string(name)
		base.Citable = strings.TrimSpace(result.Content) != ""
	}
	base.Excerpt = evidenceExcerpt(result.Content)
	if base.Excerpt == "" {
		base.Status = "empty"
		base.Citable = false
	}
	return []EvidenceRecord{base}
}

func webFetchEvidence(base EvidenceRecord, content string) []EvidenceRecord {
	fields, body := parseWebFetchOutput(content)
	base.Kind = "source"
	base.Title = strings.TrimSpace(fields["Title"])
	if base.Title == "" {
		base.Title = "Fetched web source"
	}
	base.URL = strings.TrimSpace(fields["Final URL"])
	if base.URL == "" {
		base.URL = strings.TrimSpace(fields["Fetched URL"])
	}
	base.Excerpt = evidenceExcerpt(body)
	base.Citable = base.URL != "" && base.Excerpt != "" &&
		!strings.EqualFold(strings.TrimSpace(fields["Usable"]), "false") &&
		strings.HasPrefix(strings.TrimSpace(fields["Status"]), "2")
	if !base.Citable {
		base.Status = "unusable"
		if reason := strings.TrimSpace(fields["Failure reason"]); reason != "" {
			base.Excerpt = evidenceExcerpt(reason)
		} else if base.Excerpt == "" {
			base.Excerpt = evidenceExcerpt(content)
		}
	}
	return []EvidenceRecord{base}
}

func parseWebFetchOutput(content string) (map[string]string, string) {
	fields := make(map[string]string)
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	bodyStart := -1
	for idx, line := range lines {
		if strings.TrimSpace(line) == "Content:" {
			bodyStart = idx + 1
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	body := ""
	if bodyStart >= 0 && bodyStart < len(lines) {
		body = strings.TrimSpace(strings.Join(lines[bodyStart:], "\n"))
	}
	return fields, body
}

func webSearchEvidence(base EvidenceRecord, content string) []EvidenceRecord {
	query := ""
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > 0 {
		query = strings.TrimSpace(strings.TrimPrefix(lines[0], "Search query:"))
	}

	records := make([]EvidenceRecord, 0)
	for idx := 0; idx < len(lines); idx++ {
		title, ok := numberedSearchTitle(lines[idx])
		if !ok {
			continue
		}
		record := base
		record.Kind = "discovery"
		record.Title = title
		record.Citable = false
		for idx++; idx < len(lines); idx++ {
			line := strings.TrimSpace(lines[idx])
			if _, next := numberedSearchTitle(lines[idx]); next {
				idx--
				break
			}
			switch {
			case strings.HasPrefix(line, "URL:"):
				record.URL = strings.TrimSpace(strings.TrimPrefix(line, "URL:"))
			case strings.HasPrefix(line, "Snippet:"):
				record.Excerpt = evidenceExcerpt(strings.TrimSpace(strings.TrimPrefix(line, "Snippet:")))
			}
		}
		records = append(records, record)
	}
	if len(records) > 0 {
		return records
	}
	base.Kind = "discovery"
	base.Title = "Web search"
	if query != "" {
		base.Title += ": " + query
	}
	base.Excerpt = evidenceExcerpt(content)
	base.Citable = false
	if strings.Contains(content, "Results: none") {
		base.Status = "empty"
	}
	return []EvidenceRecord{base}
}

func numberedSearchTitle(line string) (string, bool) {
	line = strings.TrimSpace(line)
	dot := strings.Index(line, ". ")
	if dot <= 0 {
		return "", false
	}
	if _, err := strconv.Atoi(line[:dot]); err != nil {
		return "", false
	}
	title := strings.TrimSpace(line[dot+2:])
	return title, title != ""
}

func evidenceContentHash(content string) string {
	if content == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func evidenceTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}

func evidenceExcerpt(content string) string {
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if len(runes) <= evidenceExcerptRunes {
		return content
	}
	return string(runes[:evidenceExcerptRunes]) + "\n\n[evidence excerpt truncated]"
}

func renderEvidenceContext(records []EvidenceRecord) string {
	if len(records) == 0 {
		return ""
	}
	lines := []string{
		"This ledger contains tool outputs captured during the current run.",
		"Ground factual claims in citable entries and cite their IDs inline, for example [E1].",
		"Discovery entries are leads only; fetch the page before using them as factual support.",
	}
	for _, record := range records {
		label := record.Kind
		if record.Citable {
			label += ", citable"
		} else {
			label += ", not citable"
		}
		line := fmt.Sprintf("- [%s] (%s, %s) %s", record.ID, label, record.Status, record.Title)
		if record.URL != "" {
			line += "\n  URL: " + record.URL
		}
		if record.Excerpt != "" {
			line += "\n  Excerpt: " + record.Excerpt
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
