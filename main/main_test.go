package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChunkStringByRunesKeepsUTF8Boundaries(t *testing.T) {
	input := "我使用 UTF-8 编码。"
	chunks := chunkStringByRunes(input, 3)

	if got := strings.Join(chunks, ""); got != input {
		t.Fatalf("chunks joined to %q, want %q", got, input)
	}
	for _, chunk := range chunks {
		if !utf8.ValidString(chunk) {
			t.Fatalf("chunk %q is not valid utf-8", chunk)
		}
		if len([]rune(chunk)) > 3 {
			t.Fatalf("chunk %q has more than 3 runes", chunk)
		}
	}
}

func TestChunkStringByRunesFallsBackToSingleRuneChunks(t *testing.T) {
	chunks := chunkStringByRunes("你好", 0)
	if got := strings.Join(chunks, "|"); got != "你|好" {
		t.Fatalf("chunks = %q, want single-rune chunks", got)
	}
}
