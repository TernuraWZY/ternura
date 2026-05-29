package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	skillFileName        = "SKILL.md"
	maxSkillFileBytes    = 128 * 1024
	maxSkillScanEntries  = 3000
	defaultSkillRootName = "skills"
)

type SkillRoot struct {
	Path   string
	Source string
}

type SkillSource struct {
	Name  string
	Roots []SkillRoot
}

type SkillLoadOptions struct {
	WorkspaceRoot string
	HomeDir       string
	ExtraDirs     []string
	Allowlist     []string
	Disabled      []string
}

type skillFrontmatter struct {
	Name            string         `yaml:"name"`
	Description     string         `yaml:"description"`
	Metadata        map[string]any `yaml:"metadata"`
	UserInvocable   bool           `yaml:"user-invocable"`
	Hidden          bool           `yaml:"hidden"`
	CommandDispatch string         `yaml:"command-dispatch"`
	CommandTool     string         `yaml:"command-tool"`
}

func DefaultSkillRoots(workspaceRoot string) []SkillRoot {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot = "."
	}
	homeDir, _ := os.UserHomeDir()
	return defaultSkillRootsWithHome(workspaceRoot, homeDir)
}

func defaultSkillRootsWithHome(workspaceRoot string, homeDir string) []SkillRoot {
	sources := defaultSkillSourcesWithHome(workspaceRoot, homeDir)
	roots := make([]SkillRoot, 0)
	for _, source := range sources {
		roots = append(roots, source.Roots...)
	}
	return roots
}

func defaultSkillSourcesWithHome(workspaceRoot string, homeDir string) []SkillSource {
	sources := []SkillSource{
		{
			Name: "project",
			Roots: []SkillRoot{
				{Path: filepath.Join(workspaceRoot, defaultSkillRootName), Source: "workspace"},
				{Path: filepath.Join(workspaceRoot, ".agents", defaultSkillRootName), Source: "project-agent"},
			},
		},
	}
	if strings.TrimSpace(homeDir) != "" {
		sources = append(sources,
			SkillSource{
				Name: "personal",
				Roots: []SkillRoot{
					{Path: filepath.Join(homeDir, ".agents", defaultSkillRootName), Source: "personal-agent"},
				},
			},
			SkillSource{
				Name:  "openclaw",
				Roots: openClawSkillRoots(homeDir),
			},
			SkillSource{
				Name: "skillhub",
				Roots: []SkillRoot{
					{Path: filepath.Join(homeDir, ".skillhub", defaultSkillRootName), Source: "skillhub"},
				},
			},
		)
	}
	return sources
}

func LoadSkills(options SkillLoadOptions) ([]Skill, error) {
	workspaceRoot := strings.TrimSpace(options.WorkspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot = "."
	}
	homeDir := strings.TrimSpace(options.HomeDir)
	if homeDir == "" {
		homeDir, _ = os.UserHomeDir()
	}

	roots := defaultSkillRootsWithHome(workspaceRoot, homeDir)
	for _, dir := range options.ExtraDirs {
		if strings.TrimSpace(dir) != "" {
			roots = append(roots, SkillRoot{Path: dir, Source: "extra"})
		}
	}
	roots = dedupeSkillRoots(roots)

	allowlist := normalizedNameSet(options.Allowlist)
	disabled := normalizedNameSet(options.Disabled)
	loaded := make([]Skill, 0)
	seen := make(map[string]struct{})
	for _, root := range roots {
		skills, err := loadSkillsFromRoot(root, allowlist, disabled)
		if err != nil {
			return nil, err
		}
		for _, skill := range skills {
			key := normalizeSkillName(skill)
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			loaded = append(loaded, skill)
		}
	}
	return loaded, nil
}

func openClawSkillRoots(homeDir string) []SkillRoot {
	openClawDir := filepath.Join(homeDir, ".openclaw")
	roots := make([]SkillRoot, 0, 2)
	for _, workspace := range openClawWorkspaces(openClawDir) {
		roots = append(roots, SkillRoot{
			Path:   filepath.Join(workspace, defaultSkillRootName),
			Source: "openclaw-workspace",
		})
	}
	roots = append(roots, SkillRoot{
		Path:   filepath.Join(openClawDir, defaultSkillRootName),
		Source: "openclaw-legacy",
	})
	return roots
}

func openClawWorkspaces(openClawDir string) []string {
	openClawDir = strings.TrimSpace(openClawDir)
	if openClawDir == "" {
		return nil
	}
	workspaces := make([]string, 0, 2)
	configPath := filepath.Join(openClawDir, "openclaw.json")
	content, err := os.ReadFile(configPath)
	if err == nil {
		if workspace := parseOpenClawWorkspace(content); workspace != "" {
			workspaces = append(workspaces, expandHome(workspace))
		}
	}
	workspaces = append(workspaces, filepath.Join(openClawDir, "workspace"))
	return dedupeStrings(workspaces)
}

func parseOpenClawWorkspace(content []byte) string {
	var config struct {
		Agents struct {
			Defaults struct {
				Workspace string `json:"workspace"`
			} `json:"defaults"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(content, &config); err != nil {
		return ""
	}
	return strings.TrimSpace(config.Agents.Defaults.Workspace)
}

func loadSkillsFromRoot(root SkillRoot, allowlist map[string]struct{}, disabled map[string]struct{}) ([]Skill, error) {
	rootPath := strings.TrimSpace(root.Path)
	if rootPath == "" {
		return nil, nil
	}
	rootPath = expandHome(rootPath)
	absRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}

	count := 0
	skills := make([]Skill, 0)
	err = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		count++
		if count > maxSkillScanEntries {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if path != absRoot && shouldSkipSkillDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != skillFileName {
			return nil
		}
		if !skillFileWithinRoot(absRoot, path) {
			return nil
		}
		skill, err := ParseSkillFile(path, root.Source)
		if err != nil {
			return err
		}
		key := normalizeSkillName(skill)
		if key == "" {
			return nil
		}
		if len(allowlist) > 0 {
			if _, ok := allowlist[key]; !ok {
				return nil
			}
		}
		if _, ok := disabled[key]; ok {
			return nil
		}
		if !skillEligible(skill) {
			return nil
		}
		skills = append(skills, skill)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(skills, func(i, j int) bool {
		return strings.TrimSpace(skills[i].Name()) < strings.TrimSpace(skills[j].Name())
	})
	return skills, nil
}

func ParseSkillFile(path string, source string) (*FileSkill, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxSkillFileBytes {
		return nil, fmt.Errorf("skill file too large: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	meta, body, err := parseSkillMarkdown(content)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	baseDir := filepath.Dir(path)
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		name = filepath.Base(baseDir)
	}
	description := strings.TrimSpace(meta.Description)
	instructions := strings.ReplaceAll(strings.TrimSpace(body), "{baseDir}", baseDir)
	return NewFileSkill(FileSkillConfig{
		SkillConfig: SkillConfig{
			Name:         name,
			Description:  description,
			Instructions: instructions,
		},
		SkillPath: path,
		BaseDir:   baseDir,
		Source:    strings.TrimSpace(source),
		LazyLoad:  true,
	}), nil
}

func parseSkillMarkdown(content []byte) (skillFrontmatter, string, error) {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return skillFrontmatter{}, strings.TrimSpace(text), nil
	}
	end := -1
	for idx := 1; idx < len(lines); idx++ {
		if strings.TrimSpace(lines[idx]) == "---" {
			end = idx
			break
		}
	}
	if end < 0 {
		return skillFrontmatter{}, "", errors.New("unterminated frontmatter")
	}
	var meta skillFrontmatter
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &meta); err != nil {
		return skillFrontmatter{}, "", err
	}
	return meta, strings.Join(lines[end+1:], "\n"), nil
}

func skillEligible(skill *FileSkill) bool {
	if skill == nil {
		return false
	}
	meta := skillMetadataOpenClaw(skill)
	if boolMeta(meta, "alwaysInclude") || boolMeta(meta, "always_include") {
		return true
	}
	if platforms := stringListMeta(meta, "platforms"); len(platforms) > 0 && !platformAllowed(platforms) {
		return false
	}
	requires := mapMeta(meta, "requires")
	for _, envName := range stringListMeta(requires, "env") {
		if strings.TrimSpace(os.Getenv(envName)) == "" {
			return false
		}
	}
	for _, binName := range stringListMeta(requires, "bins") {
		if _, err := exec.LookPath(binName); err != nil {
			return false
		}
	}
	anyBins := append(stringListMeta(requires, "anyBins"), stringListMeta(requires, "any_bins")...)
	if len(anyBins) > 0 {
		found := false
		for _, binName := range anyBins {
			if _, err := exec.LookPath(binName); err == nil {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(stringListMeta(requires, "config")) > 0 {
		return false
	}
	return true
}

func skillMetadataOpenClaw(skill *FileSkill) map[string]any {
	if skill == nil {
		return nil
	}
	path := skill.SkillPath()
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	meta, _, err := parseSkillMarkdown(content)
	if err != nil {
		return nil
	}
	if openclaw := mapMeta(meta.Metadata, "openclaw"); len(openclaw) > 0 {
		return openclaw
	}
	if legacy := mapMeta(meta.Metadata, "clawdbot"); len(legacy) > 0 {
		return legacy
	}
	return nil
}

func platformAllowed(platforms []string) bool {
	current := runtime.GOOS
	if current == "windows" {
		current = "win32"
	}
	for _, platform := range platforms {
		value := strings.ToLower(strings.TrimSpace(platform))
		if value == runtime.GOOS || value == current {
			return true
		}
		if runtime.GOOS == "windows" && value == "windows" {
			return true
		}
	}
	return false
}

func mapMeta(input map[string]any, key string) map[string]any {
	if len(input) == 0 {
		return nil
	}
	value, ok := input[key]
	if !ok {
		return nil
	}
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	if typed, ok := value.(map[any]any); ok {
		out := make(map[string]any, len(typed))
		for k, v := range typed {
			if key, ok := k.(string); ok {
				out[key] = v
			}
		}
		return out
	}
	return nil
}

func boolMeta(input map[string]any, key string) bool {
	value, ok := input[key]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func stringListMeta(input map[string]any, key string) []string {
	value, ok := input[key]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return cleanStringList(typed)
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				values = append(values, text)
			}
		}
		return cleanStringList(values)
	case string:
		return cleanStringList(strings.FieldsFunc(typed, func(r rune) bool {
			return r == ',' || r == ':'
		}))
	default:
		return nil
	}
}

func cleanStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func normalizedNameSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}

func dedupeSkillRoots(roots []SkillRoot) []SkillRoot {
	out := make([]SkillRoot, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		path := strings.TrimSpace(root.Path)
		if path == "" {
			continue
		}
		key := expandHome(path)
		if abs, err := filepath.Abs(key); err == nil {
			key = abs
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		root.Path = path
		root.Source = strings.TrimSpace(root.Source)
		out = append(out, root)
	}
	return out
}

func dedupeStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := expandHome(value)
		if abs, err := filepath.Abs(key); err == nil {
			key = abs
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

func shouldSkipSkillDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor", ".git":
		return true
	default:
		return false
	}
}

func skillFileWithinRoot(root string, path string) bool {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	pathReal, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootReal, pathReal)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

func SkillLoadOptionsFromEnv(workspaceRoot string) SkillLoadOptions {
	return SkillLoadOptions{
		WorkspaceRoot: workspaceRoot,
		ExtraDirs:     splitSkillEnvList(os.Getenv("TERNURA_SKILL_DIRS")),
		Allowlist:     splitSkillEnvList(os.Getenv("TERNURA_SKILLS")),
		Disabled:      splitSkillEnvList(os.Getenv("TERNURA_SKILLS_DISABLED")),
	}
}

func splitSkillEnvList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return cleanStringList(strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ':'
	}))
}

func (s *FileSkill) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
		SkillPath   string `json:"skill_path,omitempty"`
		BaseDir     string `json:"base_dir,omitempty"`
		Source      string `json:"source,omitempty"`
	}{
		Name:        s.Name(),
		Description: s.Description(),
		SkillPath:   s.SkillPath(),
		BaseDir:     s.BaseDir(),
		Source:      s.Source(),
	})
}
