package tool

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type ReadTool struct {
	*agentTool
}

func NewReadTool() *ReadTool {
	t := &ReadTool{}
	t.agentTool = newAgentTool(AgentToolRead, "read file content; omit offset and limit to read the entire file, or use zero-based line offset and line limit for a partial read", t.run)
	return t
}

type ReadToolParam struct {
	Path   string `json:"path" jsonschema:"required" jsonschema_description:"the file path to read"`
	Offset int    `json:"offset,omitempty" jsonschema_description:"zero-based line offset; defaults to 0"`
	Limit  int    `json:"limit,omitempty" jsonschema_description:"maximum number of lines to return; 0 reads all remaining lines"`
}

func (t *ReadTool) run(ctx context.Context, p ReadToolParam) (string, error) {
	if p.Offset < 0 {
		return "", fmt.Errorf("offset must be zero or greater")
	}
	if p.Limit < 0 {
		return "", fmt.Errorf("limit must be zero or greater")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	file, err := os.Open(p.Path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return "", err
	}
	if fileInfo.IsDir() {
		return "", fmt.Errorf("path is a directory")
	}

	if p.Offset == 0 && p.Limit == 0 {
		content, err := io.ReadAll(file)
		if err != nil {
			return "", err
		}
		return string(content), nil
	}

	return readFileLines(ctx, file, p.Offset, p.Limit)
}

func readFileLines(ctx context.Context, file io.Reader, offset int, limit int) (string, error) {
	reader := bufio.NewReader(file)
	var selected strings.Builder
	totalLines := 0
	selectedLines := 0

	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			lineIndex := totalLines
			totalLines++
			if lineIndex >= offset && (limit == 0 || selectedLines < limit) {
				selected.WriteString(line)
				selectedLines++
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
	}

	omittedAfter := totalLines - offset - selectedLines
	if omittedAfter < 0 {
		omittedAfter = 0
	}
	if omittedAfter > 0 {
		if selected.Len() > 0 && !strings.HasSuffix(selected.String(), "\n") {
			selected.WriteByte('\n')
		}
		fmt.Fprintf(&selected, "... (%d more lines)", omittedAfter)
	}

	return selected.String(), nil
}
