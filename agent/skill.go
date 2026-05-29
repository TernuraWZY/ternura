package agent

import (
	"context"
	"fmt"
	"strings"

	"ternura/tool"
)

const defaultSkillRuntimeBudgetRunes = 4000

// Skill groups a focused capability: model-facing instructions, tools, and hooks.
type Skill interface {
	Name() string
	Description() string
	Instructions() string
	Tools() []tool.Tool
	Hooks() []Hook
}

type LocatedSkill interface {
	Skill
	SkillPath() string
	BaseDir() string
	Source() string
	LazyLoad() bool
}

type SkillConfig struct {
	Name         string
	Description  string
	Instructions string
	Tools        []tool.Tool
	Hooks        []Hook
}

type StaticSkill struct {
	name         string
	description  string
	instructions string
	tools        []tool.Tool
	hooks        []Hook
}

type FileSkill struct {
	*StaticSkill
	skillPath string
	baseDir   string
	source    string
	lazyLoad  bool
}

func NewStaticSkill(config SkillConfig) *StaticSkill {
	return &StaticSkill{
		name:         strings.TrimSpace(config.Name),
		description:  strings.TrimSpace(config.Description),
		instructions: strings.TrimSpace(config.Instructions),
		tools:        cloneSkillTools(config.Tools),
		hooks:        cloneSkillHooks(config.Hooks),
	}
}

func (s *StaticSkill) Name() string {
	if s == nil {
		return ""
	}
	return s.name
}

func (s *StaticSkill) Description() string {
	if s == nil {
		return ""
	}
	return s.description
}

func (s *StaticSkill) Instructions() string {
	if s == nil {
		return ""
	}
	return s.instructions
}

func (s *StaticSkill) Tools() []tool.Tool {
	if s == nil {
		return nil
	}
	return cloneSkillTools(s.tools)
}

func (s *StaticSkill) Hooks() []Hook {
	if s == nil {
		return nil
	}
	return cloneSkillHooks(s.hooks)
}

type FileSkillConfig struct {
	SkillConfig
	SkillPath string
	BaseDir   string
	Source    string
	LazyLoad  bool
}

func NewFileSkill(config FileSkillConfig) *FileSkill {
	return &FileSkill{
		StaticSkill: NewStaticSkill(config.SkillConfig),
		skillPath:   strings.TrimSpace(config.SkillPath),
		baseDir:     strings.TrimSpace(config.BaseDir),
		source:      strings.TrimSpace(config.Source),
		lazyLoad:    config.LazyLoad,
	}
}

func (s *FileSkill) SkillPath() string {
	if s == nil {
		return ""
	}
	return s.skillPath
}

func (s *FileSkill) BaseDir() string {
	if s == nil {
		return ""
	}
	return s.baseDir
}

func (s *FileSkill) Source() string {
	if s == nil {
		return ""
	}
	return s.source
}

func (s *FileSkill) LazyLoad() bool {
	if s == nil {
		return false
	}
	return s.lazyLoad
}

type SkillRegistry struct {
	skills []Skill
}

func NewSkillRegistry(skills ...Skill) *SkillRegistry {
	registry := &SkillRegistry{}
	registry.Register(skills...)
	return registry
}

func (r *SkillRegistry) Register(skills ...Skill) {
	if r == nil {
		return
	}
	indexByName := make(map[string]int, len(r.skills))
	for idx, skill := range r.skills {
		if name := normalizeSkillName(skill); name != "" {
			indexByName[name] = idx
		}
	}
	for _, skill := range skills {
		name := normalizeSkillName(skill)
		if name == "" {
			continue
		}
		if idx, ok := indexByName[name]; ok {
			r.skills[idx] = mergeSkills(r.skills[idx], skill)
			continue
		}
		indexByName[name] = len(r.skills)
		r.skills = append(r.skills, skill)
	}
}

func (r *SkillRegistry) Skills() []Skill {
	if r == nil {
		return nil
	}
	skills := make([]Skill, len(r.skills))
	copy(skills, r.skills)
	return skills
}

func (r *SkillRegistry) Tools() []tool.Tool {
	if r == nil {
		return nil
	}
	tools := make([]tool.Tool, 0)
	seen := make(map[tool.AgentTool]struct{})
	for _, skill := range r.skills {
		for _, t := range skill.Tools() {
			if t == nil {
				continue
			}
			name := t.ToolName()
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			tools = append(tools, t)
		}
	}
	return tools
}

func (r *SkillRegistry) Hooks() []Hook {
	if r == nil {
		return nil
	}
	hooks := make([]Hook, 0)
	if strings.TrimSpace(r.RuntimeInstructions()) != "" {
		hooks = append(hooks, NewSkillRuntimeHook(r))
	}
	for _, skill := range r.skills {
		hooks = append(hooks, skill.Hooks()...)
	}
	return hooks
}

func (r *SkillRegistry) RuntimeInstructions() string {
	if r == nil {
		return ""
	}
	sections := make([]string, 0, len(r.skills))
	lazySkills := 0
	for _, skill := range r.skills {
		name := strings.TrimSpace(skill.Name())
		description := strings.TrimSpace(skill.Description())
		instructions := strings.TrimSpace(skill.Instructions())
		location := ""
		source := ""
		lazyLoad := false
		if located, ok := skill.(LocatedSkill); ok {
			location = strings.TrimSpace(located.SkillPath())
			source = strings.TrimSpace(located.Source())
			lazyLoad = located.LazyLoad()
		}
		if name == "" || (description == "" && instructions == "") {
			continue
		}
		lines := []string{fmt.Sprintf(`<skill name="%s">`, xmlEscape(name))}
		if description != "" {
			lines = append(lines, fmt.Sprintf("description: %s", description))
		}
		if location != "" {
			lines = append(lines, fmt.Sprintf("location: %s", location))
		}
		if source != "" {
			lines = append(lines, fmt.Sprintf("source: %s", source))
		}
		if lazyLoad && location != "" {
			lazySkills++
			lines = append(lines, "load: use the read tool on the location before applying detailed instructions")
		} else if instructions != "" {
			lines = append(lines, "", instructions)
		}
		lines = append(lines, "</skill>")
		sections = append(sections, strings.Join(lines, "\n"))
	}
	if len(sections) == 0 {
		return ""
	}
	header := "Available skills are listed below. Use them when they match the user's task."
	if lazySkills > 0 {
		header += " For skills with a location, keep the base prompt compact by reading the listed SKILL.md only when the task needs that skill."
	}
	return header + "\n\n<skills>\n" + strings.Join(sections, "\n\n") + "\n</skills>"
}

type SkillRuntimeHook struct {
	registry    *SkillRegistry
	budgetRunes int
}

func NewSkillRuntimeHook(registry *SkillRegistry) *SkillRuntimeHook {
	return &SkillRuntimeHook{
		registry:    registry,
		budgetRunes: defaultSkillRuntimeBudgetRunes,
	}
}

func (h *SkillRuntimeHook) HookName() string {
	return "skills"
}

func (h *SkillRuntimeHook) BeforeModelCall(_ context.Context, run *RunContext) error {
	if h == nil || run == nil {
		return nil
	}
	content := h.registry.RuntimeInstructions()
	if strings.TrimSpace(content) == "" {
		run.SetContextBlock("skills", "Enabled Skills", "")
		return nil
	}
	run.SetContextBlockWithPriority("skills", "Enabled Skills", content, RuntimeContextPriorityHigh, h.budgetRunes)
	return nil
}

func normalizeSkillName(skill Skill) string {
	if skill == nil {
		return ""
	}
	return strings.TrimSpace(strings.ToLower(skill.Name()))
}

func cloneSkillTools(tools []tool.Tool) []tool.Tool {
	if len(tools) == 0 {
		return nil
	}
	cloned := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		if t != nil {
			cloned = append(cloned, t)
		}
	}
	return cloned
}

func cloneSkillHooks(hooks []Hook) []Hook {
	if len(hooks) == 0 {
		return nil
	}
	cloned := make([]Hook, 0, len(hooks))
	for _, hook := range hooks {
		if hook != nil {
			cloned = append(cloned, hook)
		}
	}
	return cloned
}

func mergeSkills(primary Skill, secondary Skill) Skill {
	if primary == nil {
		return secondary
	}
	if secondary == nil {
		return primary
	}
	name := strings.TrimSpace(primary.Name())
	if name == "" {
		name = strings.TrimSpace(secondary.Name())
	}
	description := strings.TrimSpace(primary.Description())
	if description == "" {
		description = strings.TrimSpace(secondary.Description())
	}
	instructions := strings.TrimSpace(primary.Instructions())
	if instructions == "" {
		instructions = strings.TrimSpace(secondary.Instructions())
	}
	config := SkillConfig{
		Name:         name,
		Description:  description,
		Instructions: instructions,
		Tools:        mergeSkillTools(primary.Tools(), secondary.Tools()),
		Hooks:        mergeSkillHooks(primary.Hooks(), secondary.Hooks()),
	}
	if located, ok := primary.(LocatedSkill); ok {
		return NewFileSkill(FileSkillConfig{
			SkillConfig: config,
			SkillPath:   located.SkillPath(),
			BaseDir:     located.BaseDir(),
			Source:      located.Source(),
			LazyLoad:    located.LazyLoad(),
		})
	}
	return NewStaticSkill(config)
}

func mergeSkillTools(first []tool.Tool, second []tool.Tool) []tool.Tool {
	merged := make([]tool.Tool, 0, len(first)+len(second))
	seen := make(map[tool.AgentTool]struct{}, len(first)+len(second))
	for _, tools := range [][]tool.Tool{first, second} {
		for _, t := range tools {
			if t == nil {
				continue
			}
			name := t.ToolName()
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
			merged = append(merged, t)
		}
	}
	return merged
}

func mergeSkillHooks(first []Hook, second []Hook) []Hook {
	merged := make([]Hook, 0, len(first)+len(second))
	seen := make(map[string]struct{}, len(first)+len(second))
	for _, hooks := range [][]Hook{first, second} {
		for _, hook := range hooks {
			if hook == nil {
				continue
			}
			key := hookName(hook)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, hook)
		}
	}
	return merged
}

func xmlEscape(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}
