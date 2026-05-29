package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSkillFileLoadsAgentSkillsMarkdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte(`---
name: research
description: Research public sources.
metadata: { "openclaw": { "platforms": ["darwin", "linux"], "requires": { "env": ["TERNURA_TEST_SKILL_KEY"] } } }
---
Use sources under {baseDir}.
`), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
	t.Setenv("TERNURA_TEST_SKILL_KEY", "ok")

	skill, err := ParseSkillFile(path, "workspace")
	if err != nil {
		t.Fatalf("parse skill: %v", err)
	}
	if skill.Name() != "research" || skill.Description() != "Research public sources." {
		t.Fatalf("skill = %s / %s", skill.Name(), skill.Description())
	}
	if !strings.Contains(skill.Instructions(), dir) {
		t.Fatalf("instructions did not expand baseDir:\n%s", skill.Instructions())
	}
	if !skillEligible(skill) {
		t.Fatal("skill should be eligible with required env")
	}
}

func TestLoadSkillsAppliesOpenClawPrecedenceAndGates(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	openClawWorkspace := filepath.Join(home, "openclaw-workspace")
	if err := os.MkdirAll(filepath.Join(home, ".openclaw"), 0o755); err != nil {
		t.Fatalf("mkdir openclaw: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".openclaw", "openclaw.json"), []byte(`{"agents":{"defaults":{"workspace":`+quoteJSON(openClawWorkspace)+`}}}`), 0o644); err != nil {
		t.Fatalf("write openclaw config: %v", err)
	}
	writeSkillFile(t, filepath.Join(home, ".openclaw", "skills", "research", "SKILL.md"), `---
name: research
description: Legacy managed copy.
---
Legacy managed instructions.
`)
	writeSkillFile(t, filepath.Join(openClawWorkspace, "skills", "research", "SKILL.md"), `---
name: research
description: OpenClaw workspace copy.
---
OpenClaw workspace instructions.
`)
	writeSkillFile(t, filepath.Join(home, ".skillhub", "skills", "weather", "SKILL.md"), `---
name: weather
description: SkillHub weather.
metadata: { "clawdbot": { "requires": { "bins": ["go"] } } }
---
Weather instructions.
`)
	writeSkillFile(t, filepath.Join(root, "skills", "group", "research", "SKILL.md"), `---
name: research
description: Workspace copy.
---
Workspace instructions.
`)
	writeSkillFile(t, filepath.Join(root, ".agents", "skills", "disabled", "SKILL.md"), `---
name: disabled
description: Disabled skill.
---
Disabled instructions.
`)
	writeSkillFile(t, filepath.Join(root, "skills", "needs-env", "SKILL.md"), `---
name: needs-env
description: Needs env.
metadata: { "openclaw": { "requires": { "env": ["MISSING_TERNURA_SKILL_ENV"] } } }
---
Needs env instructions.
`)

	skills, err := LoadSkills(SkillLoadOptions{
		WorkspaceRoot: root,
		HomeDir:       home,
		Disabled:      []string{"disabled"},
	})
	if err != nil {
		t.Fatalf("load skills: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("skills = %d, want workspace research plus skillhub weather; %+v", len(skills), skills)
	}
	byName := map[string]*FileSkill{}
	for _, loaded := range skills {
		skill, ok := loaded.(*FileSkill)
		if !ok {
			t.Fatalf("skill type = %T, want *FileSkill", loaded)
		}
		byName[skill.Name()] = skill
	}
	research := byName["research"]
	if research == nil {
		t.Fatalf("research missing from loaded skills: %+v", skills)
	}
	if research.Description() != "Workspace copy." || research.Source() != "workspace" {
		t.Fatalf("research = %+v", research)
	}
	if !strings.Contains(research.SkillPath(), filepath.Join(root, "skills", "group", "research", "SKILL.md")) {
		t.Fatalf("research path = %s", research.SkillPath())
	}
	weather := byName["weather"]
	if weather == nil {
		t.Fatalf("weather missing from loaded skills: %+v", skills)
	}
	if weather.Description() != "SkillHub weather." || weather.Source() != "skillhub" {
		t.Fatalf("weather = %+v", weather)
	}
	if !strings.Contains(weather.SkillPath(), filepath.Join(home, ".skillhub", "skills", "weather", "SKILL.md")) {
		t.Fatalf("weather path = %s", weather.SkillPath())
	}
}

func TestLoadSkillsUsesOpenClawWorkspaceWhenNoProjectOverride(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	openClawWorkspace := filepath.Join(home, ".openclaw", "workspace")
	writeSkillFile(t, filepath.Join(openClawWorkspace, "skills", "baidu-search", "SKILL.md"), `---
name: baidu-search
description: Search with Baidu.
---
Baidu instructions.
`)

	skills, err := LoadSkills(SkillLoadOptions{
		WorkspaceRoot: root,
		HomeDir:       home,
	})
	if err != nil {
		t.Fatalf("load skills: %v", err)
	}
	if len(skills) != 1 {
		t.Fatalf("skills = %d, want openclaw workspace skill; %+v", len(skills), skills)
	}
	skill := skills[0].(*FileSkill)
	if skill.Name() != "baidu-search" || skill.Source() != "openclaw-workspace" {
		t.Fatalf("skill = %+v", skill)
	}
}

func TestSkillRuntimeHookListsFileSkillLocation(t *testing.T) {
	registry := NewSkillRegistry(NewFileSkill(FileSkillConfig{
		SkillConfig: SkillConfig{
			Name:         "research",
			Description:  "Research public sources.",
			Instructions: "Long instructions should stay in SKILL.md.",
		},
		SkillPath: "/workspace/skills/research/SKILL.md",
		BaseDir:   "/workspace/skills/research",
		Source:    "workspace",
		LazyLoad:  true,
	}))
	run := NewRunContext("research this", RunModeSync)
	if err := NewSkillRuntimeHook(registry).BeforeModelCall(nil, run); err != nil {
		t.Fatalf("before model call: %v", err)
	}
	rendered := run.RuntimeContextText()
	for _, want := range []string{
		"<skills>",
		`<skill name="research">`,
		"location: /workspace/skills/research/SKILL.md",
		"load: use the read tool",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("runtime context missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Long instructions should stay") {
		t.Fatalf("lazy file skill instructions should not be injected:\n%s", rendered)
	}
}

func writeSkillFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
}

func quoteJSON(value string) string {
	quoted, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(quoted)
}
