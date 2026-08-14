package app

import (
	"strings"
	"testing"

	"ternura/agent"
	"ternura/tool"
)

func TestSelectDelegatedAgentToolsUsesExplicitAllowlist(t *testing.T) {
	readTool := tool.NewReadTool()
	webFetchTool := tool.NewWebFetchTool()
	available := indexToolsByName([]tool.Tool{readTool, webFetchTool})
	definition := agent.DelegatedAgentDefinition{
		Name:  "researcher",
		Tools: []tool.AgentTool{tool.AgentToolRead, tool.AgentToolWebFetch},
	}

	selected, err := selectDelegatedAgentTools(definition, available)
	if err != nil {
		t.Fatalf("select tools: %v", err)
	}
	if len(selected) != 2 || selected[0].ToolName() != tool.AgentToolRead || selected[1].ToolName() != tool.AgentToolWebFetch {
		t.Fatalf("selected tools = %+v", selected)
	}
}

func TestSelectDelegatedAgentToolsRejectsUnavailableTool(t *testing.T) {
	definition := agent.DelegatedAgentDefinition{
		Name:  "researcher",
		Tools: []tool.AgentTool{tool.AgentToolBash},
	}

	_, err := selectDelegatedAgentTools(definition, indexToolsByName([]tool.Tool{tool.NewReadTool()}))
	if err == nil || !strings.Contains(err.Error(), "unavailable tool") {
		t.Fatalf("error = %v, want unavailable tool", err)
	}
}

func TestDelegatedAgentSystemPromptKeepsTaskIsolated(t *testing.T) {
	prompt := delegatedAgentSystemPrompt(agent.DelegatedAgentDefinition{
		Name:         "reviewer",
		Description:  "Review code changes.",
		Instructions: "Prioritize correctness.",
	})
	for _, expected := range []string{"isolated from the parent conversation", "Review code changes.", "Prioritize correctness."} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt missing %q:\n%s", expected, prompt)
		}
	}
}
