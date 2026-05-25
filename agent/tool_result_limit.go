package agent

import "fmt"

const (
	maxToolResultContentRunes = 20000
	toolResultHeadRunes       = 12000
	toolResultTailRunes       = 8000
)

func limitToolResultContent(content string) string {
	if content == "" {
		return ""
	}
	runes := []rune(content)
	if len(runes) <= maxToolResultContentRunes {
		return content
	}

	omitted := len(runes) - toolResultHeadRunes - toolResultTailRunes
	if omitted < 0 {
		omitted = len(runes) - maxToolResultContentRunes
	}
	notice := fmt.Sprintf(
		"[tool output truncated: original %d characters, showing first %d and last %d. Narrow the command or read specific files for more detail.]",
		len(runes),
		toolResultHeadRunes,
		toolResultTailRunes,
	)
	return string(runes[:toolResultHeadRunes]) +
		"\n\n" + notice +
		fmt.Sprintf("\n\n... omitted %d characters ...\n\n", omitted) +
		string(runes[len(runes)-toolResultTailRunes:])
}

func limitToolResult(result ToolResult) ToolResult {
	result.Content = limitToolResultContent(result.Content)
	return result
}
