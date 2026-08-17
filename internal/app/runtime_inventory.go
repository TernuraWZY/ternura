package app

import (
	"context"
	"sort"
	"strings"

	"ternura/agent"
)

type runtimeSkillView struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Source      string   `json:"source"`
	Path        string   `json:"path,omitempty"`
	LazyLoad    bool     `json:"lazy_load,omitempty"`
	Tools       []string `json:"tools"`
	Hooks       []string `json:"hooks"`
}

type runtimeToolView struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Skills      []string `json:"skills"`
	MCP         bool     `json:"mcp"`
}

type runtimeInventorySnapshot struct {
	SkillCount int                `json:"skill_count"`
	ToolCount  int                `json:"tool_count"`
	Skills     []runtimeSkillView `json:"skills"`
	Tools      []runtimeToolView  `json:"tools"`
}

func (s *agentServer) captureRuntimeInventory(registry *agent.SkillRegistry) {
	if s == nil || registry == nil {
		return
	}
	snapshot := buildRuntimeInventory(registry)
	s.inventoryMu.Lock()
	s.inventory = snapshot
	s.inventoryMu.Unlock()
}

func (s *agentServer) runtimeInventory() (int, int) {
	if s == nil {
		return 0, 0
	}
	s.inventoryMu.RLock()
	defer s.inventoryMu.RUnlock()
	return s.inventory.SkillCount, s.inventory.ToolCount
}

func (s *agentServer) runtimeInventoryDetails() runtimeInventorySnapshot {
	if s == nil {
		return runtimeInventorySnapshot{}
	}
	s.inventoryMu.RLock()
	defer s.inventoryMu.RUnlock()
	return cloneRuntimeInventory(s.inventory)
}

func buildRuntimeInventory(registry *agent.SkillRegistry) runtimeInventorySnapshot {
	if registry == nil {
		return runtimeInventorySnapshot{}
	}
	skills := registry.Skills()
	toolIndex := make(map[string]*runtimeToolView)
	skillViews := make([]runtimeSkillView, 0, len(skills))
	for _, skill := range skills {
		if skill == nil {
			continue
		}
		skillName := strings.TrimSpace(skill.Name())
		view := runtimeSkillView{
			Name:        skillName,
			Description: compactInventoryText(skill.Description(), 600),
			Source:      "builtin",
			Tools:       make([]string, 0),
			Hooks:       make([]string, 0),
		}
		if located, ok := skill.(agent.LocatedSkill); ok {
			view.Source = strings.TrimSpace(located.Source())
			if view.Source == "" {
				view.Source = "external"
			}
			view.Path = strings.TrimSpace(located.SkillPath())
			view.LazyLoad = located.LazyLoad()
		}
		for _, currentTool := range skill.Tools() {
			if currentTool == nil {
				continue
			}
			name := strings.TrimSpace(string(currentTool.ToolName()))
			if name == "" {
				continue
			}
			view.Tools = appendUniqueString(view.Tools, name)
			toolView := toolIndex[name]
			if toolView == nil {
				toolView = &runtimeToolView{Name: name, Skills: make([]string, 0)}
				if info, err := currentTool.Info(context.Background()); err == nil && info != nil {
					toolView.Description = compactInventoryText(info.Desc, 800)
				}
				toolIndex[name] = toolView
			}
			toolView.Skills = appendUniqueString(toolView.Skills, skillName)
			if skillName == "mcp" || strings.HasPrefix(name, "mcp_") {
				toolView.MCP = true
			}
		}
		for _, hook := range skill.Hooks() {
			if named, ok := hook.(agent.NamedHook); ok {
				if name := strings.TrimSpace(named.HookName()); name != "" {
					view.Hooks = appendUniqueString(view.Hooks, name)
				}
			}
		}
		sort.Strings(view.Tools)
		sort.Strings(view.Hooks)
		skillViews = append(skillViews, view)
	}
	sort.SliceStable(skillViews, func(i, j int) bool { return skillViews[i].Name < skillViews[j].Name })

	toolViews := make([]runtimeToolView, 0, len(toolIndex))
	for _, view := range toolIndex {
		sort.Strings(view.Skills)
		toolViews = append(toolViews, *view)
	}
	sort.SliceStable(toolViews, func(i, j int) bool { return toolViews[i].Name < toolViews[j].Name })
	return runtimeInventorySnapshot{
		SkillCount: len(skillViews),
		ToolCount:  len(toolViews),
		Skills:     skillViews,
		Tools:      toolViews,
	}
}

func cloneRuntimeInventory(source runtimeInventorySnapshot) runtimeInventorySnapshot {
	cloned := runtimeInventorySnapshot{
		SkillCount: source.SkillCount,
		ToolCount:  source.ToolCount,
		Skills:     make([]runtimeSkillView, len(source.Skills)),
		Tools:      make([]runtimeToolView, len(source.Tools)),
	}
	copy(cloned.Skills, source.Skills)
	copy(cloned.Tools, source.Tools)
	for idx := range cloned.Skills {
		cloned.Skills[idx].Tools = append(make([]string, 0, len(source.Skills[idx].Tools)), source.Skills[idx].Tools...)
		cloned.Skills[idx].Hooks = append(make([]string, 0, len(source.Skills[idx].Hooks)), source.Skills[idx].Hooks...)
	}
	for idx := range cloned.Tools {
		cloned.Tools[idx].Skills = append(make([]string, 0, len(source.Tools[idx].Skills)), source.Tools[idx].Skills...)
	}
	return cloned
}

func appendUniqueString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func compactInventoryText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}
