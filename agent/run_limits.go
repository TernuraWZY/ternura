package agent

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"ternura/tool"
)

const (
	defaultMaxReactSteps    = 24
	defaultMaxModelCalls    = 16
	defaultMaxToolCalls     = 12
	defaultMaxWebFetchCalls = 5
)

var ErrRunBudgetExceeded = errors.New("run budget exceeded")

type RunLimits struct {
	MaxReactSteps      int
	MaxModelCalls      int
	MaxToolCalls       int
	MaxToolCallsByName map[tool.AgentTool]int
}

type RunBudgetError struct {
	Kind  string
	Tool  tool.AgentTool
	Limit int
}

func (e RunBudgetError) Error() string {
	switch e.Kind {
	case "model":
		return fmt.Sprintf("model call limit reached (%d)", e.Limit)
	case "tool":
		if e.Tool != "" {
			return fmt.Sprintf("%s call limit reached (%d)", e.Tool, e.Limit)
		}
		return fmt.Sprintf("total tool call limit reached (%d)", e.Limit)
	default:
		return "run budget exceeded"
	}
}

func (e RunBudgetError) Unwrap() error {
	return ErrRunBudgetExceeded
}

func DefaultRunLimits() RunLimits {
	return RunLimits{
		MaxReactSteps: defaultMaxReactSteps,
		MaxModelCalls: defaultMaxModelCalls,
		MaxToolCalls:  defaultMaxToolCalls,
		MaxToolCallsByName: map[tool.AgentTool]int{
			tool.AgentToolWebFetch: defaultMaxWebFetchCalls,
		},
	}
}

func RunLimitsFromEnv() RunLimits {
	limits := DefaultRunLimits()
	limits.MaxReactSteps = envInt("TERNURA_MAX_REACT_STEPS", limits.MaxReactSteps)
	limits.MaxModelCalls = envInt("TERNURA_MAX_MODEL_CALLS", limits.MaxModelCalls)
	limits.MaxToolCalls = envInt("TERNURA_MAX_TOOL_CALLS", limits.MaxToolCalls)

	webFetchLimit := envInt("TERNURA_MAX_WEB_FETCH_CALLS", defaultMaxWebFetchCalls)
	if limits.MaxToolCallsByName == nil {
		limits.MaxToolCallsByName = make(map[tool.AgentTool]int)
	}
	limits.MaxToolCallsByName[tool.AgentToolWebFetch] = webFetchLimit
	return normalizeRunLimits(limits)
}

func normalizeRunLimits(limits RunLimits) RunLimits {
	if limits.MaxReactSteps <= 0 {
		limits.MaxReactSteps = defaultMaxReactSteps
	}
	if limits.MaxToolCallsByName == nil {
		limits.MaxToolCallsByName = make(map[tool.AgentTool]int)
	}
	return limits
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
