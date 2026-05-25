package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLimitToolResultContentKeepsShortContentUnchanged(t *testing.T) {
	content := "small output"

	limited := limitToolResultContent(content)

	if limited != content {
		t.Fatalf("limited content = %q, want original", limited)
	}
}

func TestLimitToolResultContentKeepsValidUTF8(t *testing.T) {
	content := strings.Repeat("界", maxToolResultContentRunes+1)

	limited := limitToolResultContent(content)

	if limited == content {
		t.Fatal("content was not limited")
	}
	if !utf8.ValidString(limited) {
		t.Fatal("limited content is not valid UTF-8")
	}
	if !strings.Contains(limited, "[tool output truncated:") {
		t.Fatalf("limited content missing truncation notice:\n%s", limited)
	}
}
