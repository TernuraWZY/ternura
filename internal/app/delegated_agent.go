package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"ternura/agent"
	"ternura/tool"
)

func (s *agentServer) newDelegationSkill(baseRegistry *agent.SkillRegistry) agent.Skill {
	definitions := loadDelegatedAgentDefinitions()
	if len(definitions) == 0 || baseRegistry == nil {
		return nil
	}

	availableTools := indexToolsByName(baseRegistry.Tools())
	definitionByName := make(map[string]agent.DelegatedAgentDefinition, len(definitions))
	lines := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		definitionByName[definition.Name] = definition
		line := "- " + definition.Name
		if definition.Description != "" {
			line += ": " + definition.Description
		}
		lines = append(lines, line)
	}

	description := "delegate a focused task to one of these specialist agents: " + strings.Join(lines, "; ")
	delegateTool := tool.NewDelegateAgentTool(description, func(ctx context.Context, params tool.DelegateAgentParams) (string, error) {
		definition, ok := definitionByName[strings.ToLower(strings.TrimSpace(params.Agent))]
		if !ok {
			return "", fmt.Errorf("delegated agent %q not found; available agents: %s", params.Agent, delegatedAgentNames(definitions))
		}
		selectedTools, err := selectDelegatedAgentTools(definition, availableTools)
		if err != nil {
			return "", err
		}
		subAgent := agent.NewAgent(
			s.modelConf,
			delegatedAgentSystemPrompt(definition),
			selectedTools,
			agent.WithHooks(newCurrentTimeHook(), newToolGroundingGuardHook()),
			agent.WithRunLimits(delegatedAgentRunLimits()),
		)
		result, err := subAgent.RunWithTrace(ctx, params.Task)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(result.Content), nil
	})

	return agent.NewStaticSkill(agent.SkillConfig{
		Name:        "delegation",
		Description: "Delegate bounded, self-contained work to specialist agents with isolated context and explicit tool access.",
		Instructions: strings.Join([]string{
			"Use delegate_agent only when a listed specialist can handle a substantial self-contained subtask.",
			"Give the delegated agent a complete task with the necessary constraints and expected output.",
			"Treat the delegated result as supporting work; the parent agent remains responsible for the final answer.",
			"Available delegated agents:",
			strings.Join(lines, "\n"),
		}, "\n"),
		Tools: []tool.Tool{delegateTool},
	})
}

func loadDelegatedAgentDefinitions() []agent.DelegatedAgentDefinition {
	workspaceRoot, err := os.Getwd()
	if err != nil {
		log.Printf("resolve workspace for delegated agents: %v", err)
		workspaceRoot = "."
	}
	roots := []string{
		filepath.Join(workspaceRoot, "agents"),
		filepath.Join(workspaceRoot, ".ternura", "agents"),
	}
	if configured := strings.TrimSpace(os.Getenv("TERNURA_AGENT_DIRS")); configured != "" {
		for _, root := range filepath.SplitList(configured) {
			if root = strings.TrimSpace(root); root != "" {
				roots = append(roots, root)
			}
		}
	}
	definitions, err := agent.LoadDelegatedAgents(roots...)
	if err != nil {
		log.Printf("load delegated agents: %v", err)
		return nil
	}
	if len(definitions) > 0 {
		log.Printf("loaded %d delegated agents", len(definitions))
	}
	return definitions
}

func indexToolsByName(tools []tool.Tool) map[tool.AgentTool]tool.Tool {
	indexed := make(map[tool.AgentTool]tool.Tool, len(tools))
	for _, current := range tools {
		if current != nil {
			indexed[current.ToolName()] = current
		}
	}
	return indexed
}

func selectDelegatedAgentTools(definition agent.DelegatedAgentDefinition, available map[tool.AgentTool]tool.Tool) ([]tool.Tool, error) {
	selected := make([]tool.Tool, 0, len(definition.Tools))
	for _, name := range definition.Tools {
		current, ok := available[name]
		if !ok {
			return nil, fmt.Errorf("delegated agent %q requires unavailable tool %q", definition.Name, name)
		}
		selected = append(selected, current)
	}
	return selected, nil
}

func delegatedAgentNames(definitions []agent.DelegatedAgentDefinition) string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func delegatedAgentSystemPrompt(definition agent.DelegatedAgentDefinition) string {
	description := strings.TrimSpace(definition.Description)
	if description == "" {
		description = "Complete the delegated task precisely."
	}
	return strings.Join([]string{
		"You are a delegated specialist agent running inside Ternura.",
		"Your context is isolated from the parent conversation. Work only on the task you receive.",
		"Role: " + description,
		"",
		"Specialist instructions:",
		strings.TrimSpace(definition.Instructions),
		"",
		"Return a self-contained final result to the parent agent. Do not ask the end user questions and do not claim side effects without tool evidence.",
	}, "\n")
}

func delegatedAgentRunLimits() agent.RunLimits {
	limits := agent.DefaultRunLimits()
	limits.MaxReactSteps = 12
	limits.MaxModelCalls = 8
	limits.MaxToolCalls = 8
	return limits
}
