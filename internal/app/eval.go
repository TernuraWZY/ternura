package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"ternura/agent"
)

type evalCase struct {
	Name              string   `json:"name"`
	Input             string   `json:"input"`
	ExpectContains    []string `json:"expect_contains,omitempty"`
	ExpectNotContains []string `json:"expect_not_contains,omitempty"`
	RequireTools      []string `json:"require_tools,omitempty"`
	MaxModelCalls     int      `json:"max_model_calls,omitempty"`
	MaxToolCalls      int      `json:"max_tool_calls,omitempty"`
	TimeoutSeconds    int      `json:"timeout_seconds,omitempty"`
}

type evalCaseResult struct {
	Name       string           `json:"name"`
	Passed     bool             `json:"passed"`
	Failures   []string         `json:"failures,omitempty"`
	Content    string           `json:"content,omitempty"`
	Error      string           `json:"error,omitempty"`
	DurationMS int64            `json:"duration_ms"`
	Metrics    agent.RunMetrics `json:"metrics,omitempty"`
}

type evalSummary struct {
	Suite      string           `json:"suite"`
	Passed     int              `json:"passed"`
	Failed     int              `json:"failed"`
	DurationMS int64            `json:"duration_ms"`
	Results    []evalCaseResult `json:"results"`
}

func runEvalSuite(ctx context.Context, path string, factory func() *agent.Agent) (evalSummary, error) {
	cases, err := loadEvalCases(path)
	if err != nil {
		return evalSummary{}, err
	}
	startedAt := time.Now()
	summary := evalSummary{Suite: path, Results: make([]evalCaseResult, 0, len(cases))}
	for idx, testCase := range cases {
		if strings.TrimSpace(testCase.Name) == "" {
			testCase.Name = fmt.Sprintf("case-%d", idx+1)
		}
		caseStartedAt := time.Now()
		timeout := 5 * time.Minute
		if testCase.TimeoutSeconds > 0 {
			timeout = time.Duration(testCase.TimeoutSeconds) * time.Second
		}
		caseCtx, cancel := context.WithTimeout(ctx, timeout)
		result, runErr := factory().RunWithTrace(caseCtx, testCase.Input)
		cancel()

		caseResult := evalCaseResult{
			Name:       testCase.Name,
			Content:    result.Content,
			DurationMS: time.Since(caseStartedAt).Milliseconds(),
			Metrics:    result.Metrics,
		}
		if runErr != nil {
			caseResult.Error = runErr.Error()
			caseResult.Failures = append(caseResult.Failures, "run returned an error")
		}
		caseResult.Failures = append(caseResult.Failures, evaluateCase(testCase, result)...)
		caseResult.Passed = len(caseResult.Failures) == 0
		if caseResult.Passed {
			summary.Passed++
		} else {
			summary.Failed++
		}
		summary.Results = append(summary.Results, caseResult)
	}
	summary.DurationMS = time.Since(startedAt).Milliseconds()
	return summary, nil
}

func loadEvalCases(path string) ([]evalCase, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	cases := make([]evalCase, 0)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var testCase evalCase
		if err := json.Unmarshal([]byte(line), &testCase); err != nil {
			return nil, fmt.Errorf("eval line %d: %w", lineNumber, err)
		}
		if strings.TrimSpace(testCase.Input) == "" {
			return nil, fmt.Errorf("eval line %d: input is required", lineNumber)
		}
		cases = append(cases, testCase)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("eval suite %s has no cases", path)
	}
	return cases, nil
}

func evaluateCase(testCase evalCase, result agent.AgentRunResult) []string {
	failures := make([]string, 0)
	for _, expected := range testCase.ExpectContains {
		if !strings.Contains(result.Content, expected) {
			failures = append(failures, fmt.Sprintf("content does not contain %q", expected))
		}
	}
	for _, forbidden := range testCase.ExpectNotContains {
		if strings.Contains(result.Content, forbidden) {
			failures = append(failures, fmt.Sprintf("content contains forbidden text %q", forbidden))
		}
	}
	usedTools := make(map[string]struct{})
	for _, trace := range result.Trace {
		if trace.Type != "tool" {
			continue
		}
		name := strings.TrimPrefix(trace.Title, "Tool use: ")
		usedTools[name] = struct{}{}
	}
	for _, required := range testCase.RequireTools {
		if _, ok := usedTools[required]; !ok {
			failures = append(failures, fmt.Sprintf("required tool %q was not used", required))
		}
	}
	if testCase.MaxModelCalls > 0 && result.Metrics.ModelCalls > testCase.MaxModelCalls {
		failures = append(failures, fmt.Sprintf("model calls %d exceed %d", result.Metrics.ModelCalls, testCase.MaxModelCalls))
	}
	if testCase.MaxToolCalls > 0 && result.Metrics.ToolCalls > testCase.MaxToolCalls {
		failures = append(failures, fmt.Sprintf("tool calls %d exceed %d", result.Metrics.ToolCalls, testCase.MaxToolCalls))
	}
	return failures
}
