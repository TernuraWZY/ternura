package tool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebSearchToolReturnsNormalizedResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "Go context package" {
			t.Fatalf("query = %q", got)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body><div class="results">
<div class="result web-result"><h2><a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fpkg.go.dev%2Fcontext&amp;rut=x">context package - Go Packages</a></h2><a class="result__snippet">Package <b>context</b> carries deadlines and cancellation signals.</a></div>
<div class="result web-result"><h2><a class="result__a" href="https://go.dev/blog/context">Go Concurrency Patterns: Context</a></h2><a class="result__snippet">An introduction to Go context.</a></div>
</div></body></html>`))
	}))
	defer server.Close()

	search := NewWebSearchTool()
	search.endpoint = server.URL
	output, err := search.InvokableRun(context.Background(), `{"query":"Go context package","limit":1}`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{
		"Search query: Go context package",
		"Provider: DuckDuckGo HTML (no API key)",
		"1. context package - Go Packages",
		"URL: https://pkg.go.dev/context",
		"Snippet: Package context carries deadlines and cancellation signals.",
		"snippets are discovery hints",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "Go Concurrency Patterns") {
		t.Fatalf("limit was not applied:\n%s", output)
	}
}

func TestWebSearchToolRequiresQuery(t *testing.T) {
	_, err := NewWebSearchTool().InvokableRun(context.Background(), `{"query":"  "}`)
	if err == nil || !strings.Contains(err.Error(), "query is required") {
		t.Fatalf("error = %v, want required query", err)
	}
}

func TestWebSearchToolReportsNoResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body>No results.</body></html>`))
	}))
	defer server.Close()

	search := NewWebSearchTool()
	search.endpoint = server.URL
	output, err := search.InvokableRun(context.Background(), `{"query":"missing"}`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(output, "Results: none") {
		t.Fatalf("output = %q", output)
	}
}

func TestWebSearchToolAllowsSecurityTermsInResults(t *testing.T) {
	body := `<html><body>
<div class="result"><a class="result__a" href="https://example.com/cloudflare">Cloudflare guide</a><a class="result__snippet">How captcha and access denied pages work.</a></div>
</body></html>`
	if reason := blockedSearchPageReason(body); reason != "" {
		t.Fatalf("ordinary security search result marked blocked: %s", reason)
	}
	results, err := parseDuckDuckGoResults(strings.NewReader(body), 5)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(results) != 1 || results[0].Title != "Cloudflare guide" {
		t.Fatalf("results = %+v", results)
	}
}

func TestWebSearchToolDetectsProviderChallenge(t *testing.T) {
	if reason := blockedSearchPageReason(`<form id="challenge-form">Unfortunately, bots use DuckDuckGo too.</form>`); reason == "" {
		t.Fatal("expected anti-bot challenge")
	}
}
