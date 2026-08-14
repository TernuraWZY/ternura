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

func TestWebFetchToolUsesReadabilityForArticleMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html><head>
<title>A practical agent article</title>
<meta property="og:site_name" content="Ternura Notes">
<meta name="author" content="Ternura Team">
</head><body>
<nav>Home Products Pricing Sign in</nav>
<article><h1>A practical agent article</h1>
<p>Reliable agents separate web discovery from source reading. Search results help locate useful pages, but the agent must inspect the original source before making factual claims.</p>
<p>A readable-content extractor removes navigation, advertisements, and repeated page chrome. This gives the model a cleaner context while keeping the final source URL visible.</p>
<p>The fallback path still matters because documentation pages and small websites do not always look like conventional news articles.</p>
</article><footer>Copyright and unrelated links</footer>
</body></html>`))
	}))
	defer server.Close()

	output, err := NewWebFetchTool().InvokableRun(context.Background(), `{"url":"`+server.URL+`"}`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{
		"Extraction: readability",
		"Title: A practical agent article",
		"Byline: Ternura Team",
		"Site: Ternura Notes",
		"Reliable agents separate web discovery",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "Home Products Pricing Sign in") {
		t.Fatalf("navigation leaked into readability output:\n%s", output)
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

func TestWebFetchToolMarksNonSuccessStatusUnusable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing page", http.StatusNotFound)
	}))
	defer server.Close()

	output, err := NewWebFetchTool().InvokableRun(context.Background(), `{"url":"`+server.URL+`"}`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{
		"Status: 404 Not Found",
		"Usable: false",
		"non-success HTTP status",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestWebFetchToolRejectsSearchResultURL(t *testing.T) {
	output, err := NewWebFetchTool().InvokableRun(context.Background(), `{"url":"https://www.google.com/search?q=ternura"}`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(output, "Usable: false") || !strings.Contains(output, "search result pages are not supported") {
		t.Fatalf("search URL should be marked unusable:\n%s", output)
	}
}

func TestWebFetchToolMarksCaptchaPageUnusable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body>正在进行安全验证，请完成验证码。</body></html>`))
	}))
	defer server.Close()

	output, err := NewWebFetchTool().InvokableRun(context.Background(), `{"url":"`+server.URL+`"}`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(output, "Usable: false") || !strings.Contains(output, "captcha") {
		t.Fatalf("captcha page should be marked unusable:\n%s", output)
	}
}
