package agent

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/dynamictool/toolsearch"
	einotool "github.com/cloudwego/eino/components/tool"

	"ternura/tool"
)

type DynamicToolSearchMode string

const (
	DynamicToolSearchAuto     DynamicToolSearchMode = "auto"
	DynamicToolSearchEnabled  DynamicToolSearchMode = "enabled"
	DynamicToolSearchDisabled DynamicToolSearchMode = "disabled"
)

type DynamicToolSearchConfig struct {
	Mode      DynamicToolSearchMode
	Threshold int
	CoreTools []tool.AgentTool
}

func DynamicToolSearchConfigFromEnv() DynamicToolSearchConfig {
	mode := DynamicToolSearchMode(strings.ToLower(strings.TrimSpace(os.Getenv("TERNURA_DYNAMIC_TOOL_SEARCH"))))
	switch mode {
	case "true", "on", "enabled":
		mode = DynamicToolSearchEnabled
	case "false", "off", "disabled":
		mode = DynamicToolSearchDisabled
	default:
		mode = DynamicToolSearchAuto
	}
	threshold := 16
	if value := strings.TrimSpace(os.Getenv("TERNURA_DYNAMIC_TOOL_SEARCH_THRESHOLD")); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			threshold = parsed
		}
	}
	return normalizeDynamicToolSearchConfig(DynamicToolSearchConfig{
		Mode:      mode,
		Threshold: threshold,
		CoreTools: []tool.AgentTool{tool.AgentToolRead, tool.AgentToolUpdateTodos, tool.AgentToolCompact},
	})
}

func normalizeDynamicToolSearchConfig(config DynamicToolSearchConfig) DynamicToolSearchConfig {
	switch config.Mode {
	case DynamicToolSearchEnabled, DynamicToolSearchDisabled, DynamicToolSearchAuto:
	default:
		config.Mode = DynamicToolSearchAuto
	}
	if config.Threshold <= 0 {
		config.Threshold = 16
	}
	if len(config.CoreTools) == 0 {
		config.CoreTools = []tool.AgentTool{tool.AgentToolRead, tool.AgentToolUpdateTodos, tool.AgentToolCompact}
	}
	return config
}

func (a *Agent) prepareToolSearch(ctx context.Context, runCtx *RunContext, tools []einotool.BaseTool) ([]einotool.BaseTool, adk.ChatModelAgentMiddleware, error) {
	config := normalizeDynamicToolSearchConfig(a.toolSearch)
	if runCtx != nil && len(runCtx.RequestedToolPolicy().AllowedTools) > 0 {
		return tools, nil, nil
	}
	if config.Mode == DynamicToolSearchDisabled || len(tools) == 0 {
		return tools, nil, nil
	}
	if config.Mode == DynamicToolSearchAuto && len(tools) < config.Threshold {
		return tools, nil, nil
	}

	coreNames := make(map[string]struct{}, len(config.CoreTools))
	for _, name := range config.CoreTools {
		coreNames[string(name)] = struct{}{}
	}
	core := make([]einotool.BaseTool, 0, len(coreNames))
	dynamic := make([]einotool.BaseTool, 0, len(tools))
	for _, candidate := range tools {
		info, err := candidate.Info(ctx)
		if err != nil {
			return nil, nil, err
		}
		if _, ok := coreNames[info.Name]; ok {
			core = append(core, candidate)
			continue
		}
		dynamic = append(dynamic, candidate)
	}
	if len(dynamic) == 0 {
		return tools, nil, nil
	}
	middleware, err := toolsearch.New(ctx, &toolsearch.Config{DynamicTools: dynamic})
	if err != nil {
		return nil, nil, err
	}
	return core, middleware, nil
}
