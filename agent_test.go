package ternura

import (
	"testing"
	"unicode/utf8"
)

func TestStreamingContentRouterKeepsUTF8Boundaries(t *testing.T) {
	var deltas []string
	router := newStreamingContentRouter(
		func() string { return "trace-1" },
		func(event AgentStreamEvent) error {
			if event.Type == "content_delta" || event.Type == "trace_delta" {
				deltas = append(deltas, event.Delta)
			}
			return nil
		},
	)

	input := "我的回复应该用简洁的方式告诉用户我使用 UTF-8 编码。"
	raw := []byte(input)
	for _, chunk := range []string{string(raw[:17]), string(raw[17:29]), string(raw[29:])} {
		if err := router.Write(chunk); err != nil {
			t.Fatalf("write chunk: %v", err)
		}
	}
	if err := router.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	for _, delta := range deltas {
		if !utf8.ValidString(delta) {
			t.Fatalf("delta is not valid UTF-8: %q", delta)
		}
	}
}
