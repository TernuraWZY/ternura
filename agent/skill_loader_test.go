package agent

import (
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
	writeSkillFile(t, filepath.Join(home, ".openclaw", "skills", "research", "SKILL.md"), `---
name: research
description: Managed copy.
---
Managed instructions.
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
	if len(skills) != 1 {
		t.Fatalf("skills = %d, want only workspace research; %+v", len(skills), skills)
	}
	skill, ok := skills[0].(*FileSkill)
	if !ok {
		t.Fatalf("skill type = %T, want *FileSkill", skills[0])
	}
	if skill.Name() != "research" || skill.Description() != "Workspace copy." || skill.Source() != "workspace" {
		t.Fatalf("skill = %+v", skill)
	}
	if !strings.Contains(skill.SkillPath(), filepath.Join(root, "skills", "group", "research", "SKILL.md")) {
		t.Fatalf("skill path = %s", skill.SkillPath())
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
