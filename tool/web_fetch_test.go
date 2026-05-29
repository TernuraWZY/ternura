package tool

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebFetchToolFetchesHTMLAsReadableText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); !strings.Contains(got, "TernuraAgent") {
			t.Fatalf("user agent = %q", got)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><style>.x{}</style><script>alert(1)</script></head><body><h1>Hello</h1><p>Readable&nbsp;text &amp; links.</p></body></html>`))
	}))
	defer server.Close()

	output, err := NewWebFetchTool().InvokableRun(context.Background(), `{"url":"`+server.URL+`"}`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{
		"Fetched URL: " + server.URL,
		"Status: 200 OK",
		"Content-Type: text/html",
		"Hello",
		"Readable text & links.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "alert(1)") || strings.Contains(output, ".x{}") {
		t.Fatalf("script/style leaked into output:\n%s", output)
	}
}

func TestWebFetchToolRejectsUnsupportedScheme(t *testing.T) {
	_, err := NewWebFetchTool().InvokableRun(context.Background(), `{"url":"file:///etc/passwd"}`)
	if err == nil {
		t.Fatalf("expected unsupported scheme error")
	}
	if strings.Contains(err.Error(), "[LocalFunc]") {
		t.Fatalf("error leaked Eino wrapper: %v", err)
	}
}

func TestWebFetchToolTruncatesContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("abcdef", 100)))
	}))
	defer server.Close()

	output, err := NewWebFetchTool().InvokableRun(context.Background(), `{"url":"`+server.URL+`","max_chars":50}`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(output, "Truncated: true") {
		t.Fatalf("expected truncation marker:\n%s", output)
	}
	if !strings.Contains(output, "[web_fetch truncated]") {
		t.Fatalf("expected text truncation marker:\n%s", output)
	}
}
