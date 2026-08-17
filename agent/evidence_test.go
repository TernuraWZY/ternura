package agent

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func TestEvidenceRecordsFromWebFetchCreatesCitableSource(t *testing.T) {
	finishedAt := time.Date(2026, 8, 17, 9, 30, 0, 0, time.UTC)
	records := evidenceRecordsFromToolResult(ToolResult{
		Call: schema.ToolCall{ID: "call-fetch", Function: schema.FunctionCall{Name: "web_fetch"}},
		Content: strings.Join([]string{
			"Fetched URL: https://example.com/original",
			"Final URL: https://example.com/article",
			"Status: 200 OK",
			"Title: Verified article",
			"",
			"Content:",
			"A concrete fact from the fetched page.",
		}, "\n"),
		FinishedAt: finishedAt,
	})

	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	record := records[0]
	if !record.Citable || record.Kind != "source" || record.URL != "https://example.com/article" {
		t.Fatalf("record = %+v", record)
	}
	if record.Title != "Verified article" || record.Status != "succeeded" {
		t.Fatalf("record metadata = %+v", record)
	}
	if !strings.HasPrefix(record.ContentHash, "sha256:") || record.RetrievedAt != finishedAt.Format(time.RFC3339Nano) {
		t.Fatalf("record integrity metadata = %+v", record)
	}
}

func TestEvidenceRecordsFromWebSearchStayDiscoveryOnly(t *testing.T) {
	records := evidenceRecordsFromToolResult(ToolResult{
		Call: schema.ToolCall{ID: "call-search", Function: schema.FunctionCall{Name: "web_search"}},
		Content: strings.Join([]string{
			"Search query: eino adk",
			"Provider: DuckDuckGo HTML (no API key)",
			"Note: snippets are discovery hints; use web_fetch on a result URL before citing factual claims.",
			"",
			"Results:",
			"1. Eino ADK",
			"   URL: https://example.com/eino",
			"   Snippet: Framework documentation.",
		}, "\n"),
	})

	if len(records) != 1 || records[0].Kind != "discovery" || records[0].Citable {
		t.Fatalf("search evidence = %+v", records)
	}
}

func TestRunContextEvidenceAssignsStableIDsAndRendersLedger(t *testing.T) {
	run := NewRunContext("query", RunModeSync)
	run.recordToolResult(ToolResult{
		Call:    schema.ToolCall{ID: "read-1", Function: schema.FunctionCall{Name: "read"}},
		Content: "file content",
	})
	run.recordToolResult(ToolResult{
		Call: schema.ToolCall{ID: "bash-1", Function: schema.FunctionCall{Name: "bash"}},
		Err:  errors.New("command failed"),
	})

	records := run.Evidence()
	if len(records) != 2 || records[0].ID != "E1" || records[1].ID != "E2" {
		t.Fatalf("evidence IDs = %+v", records)
	}
	contextText := run.EvidenceContextText()
	for _, want := range []string{"[E1]", "file content", "[E2]", "command failed", "not citable"} {
		if !strings.Contains(contextText, want) {
			t.Fatalf("evidence context missing %q:\n%s", want, contextText)
		}
	}
}
