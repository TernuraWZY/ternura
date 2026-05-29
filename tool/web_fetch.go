package tool

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultWebFetchMaxChars = 12000
	maxWebFetchMaxChars     = 30000
	maxWebFetchReadBytes    = 2 * 1024 * 1024
)

var (
	htmlScriptPattern     = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	htmlStylePattern      = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`)
	htmlNoscriptPattern   = regexp.MustCompile(`(?is)<noscript\b[^>]*>.*?</noscript>`)
	htmlTagPattern        = regexp.MustCompile(`(?s)<[^>]+>`)
	htmlWhitespacePattern = regexp.MustCompile(`[ \t\r\n]+`)
)

type WebFetchTool struct {
	*agentTool
	client *http.Client
}

type WebFetchParam struct {
	URL      string `json:"url" jsonschema:"required" jsonschema_description:"The absolute http or https URL to fetch."`
	MaxChars int    `json:"max_chars,omitempty" jsonschema_description:"Maximum characters to return. Defaults to 12000; capped at 30000."`
}

func NewWebFetchTool() *WebFetchTool {
	t := &WebFetchTool{
		client: &http.Client{Timeout: 15 * time.Second},
	}
	t.agentTool = newAgentTool(
		AgentToolWebFetch,
		"Fetch a specific public HTTP/HTTPS URL using this machine's network and return readable text plus metadata. Use when the user gives a URL or asks to inspect a specific page.",
		t.run,
	)
	return t
}

func (t *WebFetchTool) run(ctx context.Context, params WebFetchParam) (string, error) {
	targetURL, err := normalizeFetchURL(params.URL)
	if err != nil {
		return "", err
	}
	maxChars := normalizeWebFetchMaxChars(params.MaxChars)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,application/json,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("User-Agent", "TernuraAgent/1.0 (+https://github.com/TernuraWZY/ternura)")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, maxWebFetchReadBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	truncatedRead := len(body) > maxWebFetchReadBytes
	if truncatedRead {
		body = body[:maxWebFetchReadBytes]
	}

	contentType := resp.Header.Get("Content-Type")
	text := webFetchBodyToText(body, contentType)
	text, truncatedText := trimWebFetchText(text, maxChars)

	return formatWebFetchOutput(webFetchOutput{
		URL:           targetURL,
		Status:        resp.Status,
		ContentType:   contentType,
		FinalURL:      resp.Request.URL.String(),
		Body:          text,
		ReadTruncated: truncatedRead,
		TextTruncated: truncatedText,
	}), nil
}

type webFetchOutput struct {
	URL           string
	Status        string
	ContentType   string
	FinalURL      string
	Body          string
	ReadTruncated bool
	TextTruncated bool
}

func normalizeFetchURL(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("url is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("url must use http or https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("url host is required")
	}
	return parsed.String(), nil
}

func normalizeWebFetchMaxChars(value int) int {
	if value <= 0 {
		return defaultWebFetchMaxChars
	}
	if value > maxWebFetchMaxChars {
		return maxWebFetchMaxChars
	}
	return value
}

func webFetchBodyToText(body []byte, contentType string) string {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if strings.Contains(mediaType, "html") || looksLikeHTML(body) {
		return htmlToReadableText(body)
	}
	return strings.TrimSpace(string(bytes.ToValidUTF8(body, []byte(" "))))
}

func looksLikeHTML(body []byte) bool {
	prefix := strings.ToLower(string(body[:min(len(body), 512)]))
	return strings.Contains(prefix, "<html") || strings.Contains(prefix, "<!doctype html") || strings.Contains(prefix, "<body")
}

func htmlToReadableText(body []byte) string {
	text := string(bytes.ToValidUTF8(body, []byte(" ")))
	text = htmlScriptPattern.ReplaceAllString(text, " ")
	text = htmlStylePattern.ReplaceAllString(text, " ")
	text = htmlNoscriptPattern.ReplaceAllString(text, " ")
	text = strings.NewReplacer(
		"<br>", "\n",
		"<br/>", "\n",
		"<br />", "\n",
		"</p>", "\n",
		"</div>", "\n",
		"</li>", "\n",
		"&nbsp;", " ",
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
	).Replace(text)
	text = htmlTagPattern.ReplaceAllString(text, " ")
	text = htmlWhitespacePattern.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

func trimWebFetchText(text string, maxChars int) (string, bool) {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= maxChars {
		return text, false
	}
	runes := []rune(text)
	if maxChars <= 20 {
		return string(runes[:maxChars]), true
	}
	return string(runes[:maxChars-20]) + "\n\n[web_fetch truncated]", true
}

func formatWebFetchOutput(output webFetchOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Fetched URL: %s\n", output.URL)
	if output.FinalURL != "" && output.FinalURL != output.URL {
		fmt.Fprintf(&b, "Final URL: %s\n", output.FinalURL)
	}
	fmt.Fprintf(&b, "Status: %s\n", output.Status)
	if output.ContentType != "" {
		fmt.Fprintf(&b, "Content-Type: %s\n", output.ContentType)
	}
	if output.ReadTruncated || output.TextTruncated {
		b.WriteString("Truncated: true\n")
	}
	if output.Body == "" {
		b.WriteString("\nNo readable text returned.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\nContent:\n%s\n", output.Body)
	return b.String()
}
