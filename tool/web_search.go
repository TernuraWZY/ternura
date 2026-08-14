package tool

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	defaultWebSearchLimit    = 5
	maxWebSearchLimit        = 10
	maxWebSearchReadBytes    = 1024 * 1024
	defaultWebSearchTimeout  = 8 * time.Second
	defaultWebSearchEndpoint = "https://html.duckduckgo.com/html/"
)

type WebSearchTool struct {
	*agentTool
	client   *http.Client
	endpoint string
}

type WebSearchParam struct {
	Query string `json:"query" jsonschema:"required" jsonschema_description:"The search query. Include precise names, dates, or constraints when useful."`
	Limit int    `json:"limit,omitempty" jsonschema_description:"Maximum number of results to return. Defaults to 5; capped at 10."`
}

type webSearchResult struct {
	Title   string
	URL     string
	Snippet string
}

func NewWebSearchTool() *WebSearchTool {
	t := &WebSearchTool{
		client:   &http.Client{Timeout: webSearchTimeoutFromEnv()},
		endpoint: defaultWebSearchEndpoint,
	}
	t.agentTool = newAgentTool(
		AgentToolWebSearch,
		"Search the public web without an API key and return result titles, URLs, and snippets. Search snippets are discovery hints; fetch a result URL before treating it as factual evidence.",
		t.run,
	)
	return t
}

func (t *WebSearchTool) run(ctx context.Context, params WebSearchParam) (string, error) {
	query := strings.TrimSpace(params.Query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}
	limit := params.Limit
	if limit <= 0 {
		limit = defaultWebSearchLimit
	}
	if limit > maxWebSearchLimit {
		limit = maxWebSearchLimit
	}

	endpoint, err := url.Parse(t.endpoint)
	if err != nil {
		return "", fmt.Errorf("parse search endpoint: %w", err)
	}
	values := endpoint.Query()
	values.Set("q", query)
	endpoint.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.7")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; TernuraAgent/1.0; +https://github.com/TernuraWZY/ternura)")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("search provider returned %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWebSearchReadBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxWebSearchReadBytes {
		return "", fmt.Errorf("search response exceeded %d bytes", maxWebSearchReadBytes)
	}
	if reason := blockedSearchPageReason(string(body)); reason != "" {
		return "", fmt.Errorf("search provider blocked the request: %s", reason)
	}

	results, err := parseDuckDuckGoResults(strings.NewReader(string(body)), limit)
	if err != nil {
		return "", err
	}
	return formatWebSearchOutput(query, results), nil
}

func blockedSearchPageReason(body string) string {
	lower := strings.ToLower(body)
	if containsAny(lower,
		"id=\"challenge-form\"",
		"class=\"anomaly-modal",
		"unfortunately, bots use duckduckgo too",
		"please complete the following challenge",
	) {
		return "DuckDuckGo returned an anti-bot challenge"
	}
	return ""
}

func parseDuckDuckGoResults(r io.Reader, limit int) ([]webSearchResult, error) {
	doc, err := html.Parse(r)
	if err != nil {
		return nil, fmt.Errorf("parse search results: %w", err)
	}
	results := make([]webSearchResult, 0, limit)
	forEachHTMLNode(doc, func(node *html.Node) bool {
		if len(results) >= limit || node.Type != html.ElementNode || !hasHTMLClass(node, "result") {
			return len(results) < limit
		}
		titleNode := findHTMLDescendantByClass(node, "result__a")
		if titleNode == nil {
			return true
		}
		title := htmlNodeText(titleNode)
		rawURL := htmlAttr(titleNode, "href")
		resultURL := normalizeDuckDuckGoResultURL(rawURL)
		if title == "" || resultURL == "" {
			return true
		}
		snippet := ""
		if snippetNode := findHTMLDescendantByClass(node, "result__snippet"); snippetNode != nil {
			snippet = htmlNodeText(snippetNode)
		}
		results = append(results, webSearchResult{Title: title, URL: resultURL, Snippet: snippet})
		return len(results) < limit
	})
	return results, nil
}

func forEachHTMLNode(node *html.Node, visit func(*html.Node) bool) bool {
	if node == nil || !visit(node) {
		return false
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if !forEachHTMLNode(child, visit) {
			return false
		}
	}
	return true
}

func findHTMLDescendantByClass(node *html.Node, className string) *html.Node {
	var found *html.Node
	forEachHTMLNode(node, func(candidate *html.Node) bool {
		if candidate.Type == html.ElementNode && hasHTMLClass(candidate, className) {
			found = candidate
			return false
		}
		return true
	})
	return found
}

func hasHTMLClass(node *html.Node, className string) bool {
	for _, class := range strings.Fields(htmlAttr(node, "class")) {
		if class == className {
			return true
		}
	}
	return false
}

func htmlAttr(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func htmlNodeText(node *html.Node) string {
	var parts []string
	forEachHTMLNode(node, func(candidate *html.Node) bool {
		if candidate.Type == html.TextNode {
			if text := strings.TrimSpace(candidate.Data); text != "" {
				parts = append(parts, text)
			}
		}
		return true
	})
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

func normalizeDuckDuckGoResultURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if strings.HasPrefix(rawURL, "//") {
		rawURL = "https:" + rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return ""
	}
	if strings.EqualFold(parsed.Hostname(), "duckduckgo.com") && parsed.Path == "/l/" {
		rawURL = parsed.Query().Get("uddg")
		parsed, err = url.Parse(rawURL)
		if err != nil {
			return ""
		}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return ""
	}
	return parsed.String()
}

func formatWebSearchOutput(query string, results []webSearchResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Search query: %s\nProvider: DuckDuckGo HTML (no API key)\n", query)
	if len(results) == 0 {
		b.WriteString("Results: none\n")
		return b.String()
	}
	b.WriteString("Note: snippets are discovery hints; use web_fetch on a result URL before citing factual claims.\n\nResults:\n")
	for i, result := range results {
		fmt.Fprintf(&b, "%d. %s\n   URL: %s\n", i+1, result.Title, result.URL)
		if result.Snippet != "" {
			fmt.Fprintf(&b, "   Snippet: %s\n", result.Snippet)
		}
	}
	return b.String()
}

func webSearchTimeoutFromEnv() time.Duration {
	value := strings.TrimSpace(os.Getenv("TERNURA_WEB_SEARCH_TIMEOUT"))
	if value == "" {
		return defaultWebSearchTimeout
	}
	if duration, err := time.ParseDuration(value); err == nil && duration > 0 {
		return duration
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return defaultWebSearchTimeout
}
