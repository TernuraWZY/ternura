package agent

import (
	"context"
	"strings"
	"testing"

	"ternura/tool"
)

func TestSkillRegistryCollectsToolsAndHooks(t *testing.T) {
	registry := NewSkillRegistry(
		NewStaticSkill(SkillConfig{
			Name:        "workspace",
			Description: "Workspace operations.",
			Tools: []tool.Tool{
				tool.NewReadTool(),
				tool.NewReadTool(),
			},
		}),
		NewStaticSkill(SkillConfig{
			Name:         "web",
			Description:  "Fetch public webpages.",
			Instructions: "Use web_fetch only for concrete URLs.",
			Tools:        []tool.Tool{tool.NewWebFetchTool()},
			Hooks:        []Hook{namedSkillTestHook{}},
		}),
	)

	tools := registry.Tools()
	if len(tools) != 2 {
		t.Fatalf("tools = %d, want deduplicated read + web_fetch", len(tools))
	}
	if tools[0].ToolName() != tool.AgentToolRead || tools[1].ToolName() != tool.AgentToolWebFetch {
		t.Fatalf("tools = %v, want read then web_fetch", []tool.AgentTool{tools[0].ToolName(), tools[1].ToolName()})
	}

	hooks := registry.Hooks()
	if len(hooks) != 2 {
		t.Fatalf("hooks = %d, want runtime hook + skill hook", len(hooks))
	}
	if _, ok := hooks[0].(*SkillRuntimeHook); !ok {
		t.Fatalf("first hook = %T, want *SkillRuntimeHook", hooks[0])
	}
	if _, ok := hooks[1].(namedSkillTestHook); !ok {
		t.Fatalf("second hook = %T, want namedSkillTestHook", hooks[1])
	}
}

func TestSkillRuntimeHookInjectsEnabledSkills(t *testing.T) {
	registry := NewSkillRegistry(NewStaticSkill(SkillConfig{
		Name:         "web",
		Description:  "Fetch public webpages.",
		Instructions: "Use web_fetch only for concrete URLs.",
	}))
	run := NewRunContext("inspect https://example.com", RunModeSync)

	if err := NewSkillRuntimeHook(registry).BeforeModelCall(context.Background(), run); err != nil {
		t.Fatalf("before model call: %v", err)
	}

	rendered := run.RuntimeContextText()
	for _, want := range []string{"## Enabled Skills", "<skills>", `<skill name="web">`, "Fetch public webpages.", "Use web_fetch only for concrete URLs."} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("runtime context missing %q:\n%s", want, rendered)
		}
	}
}

func TestSkillRegistryMergesDuplicateSkills(t *testing.T) {
	registry := NewSkillRegistry(NewFileSkill(FileSkillConfig{
		SkillConfig: SkillConfig{
			Name:         "web",
			Description:  "Workspace web skill.",
			Instructions: "Read the workspace guidance.",
		},
		SkillPath: "/workspace/skills/web/SKILL.md",
		BaseDir:   "/workspace/skills/web",
		Source:    "workspace",
		LazyLoad:  true,
	}))
	registry.Register(NewStaticSkill(SkillConfig{
		Name:        "web",
		Description: "Builtin web skill.",
		Tools:       []tool.Tool{tool.NewWebFetchTool()},
		Hooks:       []Hook{namedSkillTestHook{}},
	}))

	skills := registry.Skills()
	if len(skills) != 1 {
		t.Fatalf("skills = %d, want merged singleton", len(skills))
	}
	located, ok := skills[0].(LocatedSkill)
	if !ok {
		t.Fatalf("merged skill = %T, want LocatedSkill", skills[0])
	}
	if skills[0].Description() != "Workspace web skill." || located.SkillPath() != "/workspace/skills/web/SKILL.md" {
		t.Fatalf("merged skill lost workspace precedence: desc=%q path=%q", skills[0].Description(), located.SkillPath())
	}
	if len(registry.Tools()) != 1 || registry.Tools()[0].ToolName() != tool.AgentToolWebFetch {
		t.Fatalf("merged tools = %+v", registry.Tools())
	}
	if len(skills[0].Hooks()) != 1 {
		t.Fatalf("merged hooks = %d", len(skills[0].Hooks()))
	}
}

type namedSkillTestHook struct{}

func (namedSkillTestHook) HookName() string {
	return "named_skill_test"
}
