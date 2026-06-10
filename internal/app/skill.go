package app

import (
	"log"
	"os"
	"strings"

	"ternura/agent"
	"ternura/config"
	"ternura/internal/cron"
	"ternura/tool"
)

func newAgentFromSkillRegistry(modelConf config.ModelConfig, registry *agent.SkillRegistry) *agent.Agent {
	if registry == nil {
		registry = agent.NewSkillRegistry()
	}
	return agent.NewAgent(
		modelConf,
		agent.TernuraAgentSystemPrompt,
		registry.Tools(),
		agent.WithHooks(registry.Hooks()...),
	)
}

func newCLISkillRegistry(cronTool *tool.CronTool) *agent.SkillRegistry {
	registry := agent.NewSkillRegistry(loadOpenClawCompatibleSkills()...)
	registry.Register(
		newWorkspaceSkill(nil),
		newCLIMemorySkill(),
		newScheduleSkill(cronTool, nil),
		newWebSkill(),
		newGroundingSkill(),
	)
	return registry
}

func (s *agentServer) newSkillRegistry(sessionID string, cronTool *tool.CronTool) *agent.SkillRegistry {
	sessionIDFunc := s.store.CurrentSessionID
	updateTodos := s.updateTodos
	if strings.TrimSpace(sessionID) != "" {
		sessionIDFunc = func() string { return sessionID }
		updateTodos = s.updateTodosForSession(sessionID)
	}

	registry := agent.NewSkillRegistry(loadOpenClawCompatibleSkills()...)
	registry.Register(
		newWorkspaceSkill(updateTodos),
		s.newMemorySkill(sessionIDFunc),
		newScheduleSkill(cronTool, s.cron),
		newWebSkill(),
		newGroundingSkill(),
	)
	return registry
}

func loadOpenClawCompatibleSkills() []agent.Skill {
	workspaceRoot, err := os.Getwd()
	if err != nil {
		log.Printf("resolve workspace for skills: %v", err)
		workspaceRoot = "."
	}
	skills, err := agent.LoadSkills(agent.SkillLoadOptionsFromEnv(workspaceRoot))
	if err != nil {
		log.Printf("load skills: %v", err)
		return nil
	}
	if len(skills) > 0 {
		log.Printf("loaded %d OpenClaw-compatible skills", len(skills))
	}
	return skills
}

func newWorkspaceSkill(updateTodos tool.UpdateTodosFunc) agent.Skill {
	return agent.NewStaticSkill(agent.SkillConfig{
		Name:        "workspace",
		Description: "Work with the local workspace: inspect files, edit files, run shell commands, and maintain visible task steps.",
		Instructions: strings.Join([]string{
			"- Treat filesystem writes, shell commands, and edits as real side effects.",
			"- Read relevant context before changing files.",
			"- Use update_todos for multi-step work and keep the complete list current.",
		}, "\n"),
		Tools: []tool.Tool{
			tool.NewReadTool(),
			tool.NewEditTool(),
			tool.NewWriteTool(),
			tool.NewBashTool(),
			tool.NewUpdateTodosTool(updateTodos),
			tool.NewCompactTool(),
		},
	})
}

func (s *agentServer) newMemorySkill(sessionID func() string) agent.Skill {
	return agent.NewStaticSkill(agent.SkillConfig{
		Name:        "memory",
		Description: "Recall relevant long-term memory, short-term session memory, and summarized tool results; store or forget durable memories when appropriate.",
		Instructions: strings.Join([]string{
			"- Use recalled memory as context, not as guaranteed current truth when it may be stale.",
			"- Use remember only for stable, reusable user preferences, project facts, or standing instructions.",
			"- Use forget_memory when the user asks to forget something or a recalled memory is clearly stale or wrong.",
		}, "\n"),
		Tools: []tool.Tool{
			tool.NewRememberTool(s.rememberMemory),
			tool.NewForgetMemoryTool(s.forgetMemory),
		},
		Hooks: []agent.Hook{
			newSessionSummaryHook(s.memory, sessionID),
			newMemoryHook(
				s.memory,
				sessionID,
				withActiveMemoryKeywordExtractor(s.activeMemoryKeywords),
				withActiveMemorySummarizer(s.activeMemorySummary),
			),
			newToolMemoryHook(s.memory, sessionID),
		},
	})
}

func newCLIMemorySkill() agent.Skill {
	return agent.NewStaticSkill(agent.SkillConfig{
		Name:        "memory",
		Description: "Store or forget durable memories when the current runtime provides a backing memory store.",
		Instructions: strings.Join([]string{
			"- Use remember only for stable, reusable user preferences, project facts, or standing instructions.",
			"- Use forget_memory when the user asks to forget something.",
		}, "\n"),
		Tools: []tool.Tool{
			tool.NewRememberTool(nil),
			tool.NewForgetMemoryTool(nil),
		},
	})
}

func newScheduleSkill(cronTool *tool.CronTool, cronService *cron.Service) agent.Skill {
	return agent.NewStaticSkill(agent.SkillConfig{
		Name:        "schedule",
		Description: "Create, list, remove, and execute reminders or recurring jobs through the cron tool and background runner.",
		Instructions: strings.Join([]string{
			"- Use cron for reminders, timers, delayed continuations, recurring tasks, and cancellation of scheduled jobs.",
			"- Do not claim a reminder or job exists until cron returns success.",
			"- Ask a brief clarification when the requested time is vague.",
		}, "\n"),
		Tools: []tool.Tool{cronTool},
		Hooks: []agent.Hook{
			newCurrentTimeHook(),
			newScheduleGuidanceHook(),
			newStateGuardHook(cronService),
		},
	})
}

func newWebSkill() agent.Skill {
	return agent.NewStaticSkill(agent.SkillConfig{
		Name:        "web",
		Description: "Fetch a specific public HTTP or HTTPS URL with this machine's network and expose readable page text to the model.",
		Instructions: strings.Join([]string{
			"- Use web_fetch when the user provides a concrete URL or asks to inspect a specific public page.",
			"- web_fetch is not a search engine and does not automatically carry browser login state.",
			"- Cite the fetched URL when relying on fetched page content.",
		}, "\n"),
		Tools: []tool.Tool{tool.NewWebFetchTool()},
	})
}

func newGroundingSkill() agent.Skill {
	return agent.NewStaticSkill(agent.SkillConfig{
		Name:        "grounding",
		Description: "Prevent final answers from presenting fake tool calls or unverified side effects as facts.",
		Instructions: strings.Join([]string{
			"- Treat current-run tool results as evidence for side effects such as command execution, file writes, memory changes, and scheduled jobs.",
			"- Do not print raw tool-call markup as if it executed.",
			"- If a real side effect was not performed in this run, say so plainly instead of presenting it as done.",
		}, "\n"),
		Hooks: []agent.Hook{newToolGroundingGuardHook()},
	})
}
