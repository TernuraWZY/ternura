package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"ternura/tool"
)

const maxDelegatedAgentFileBytes = 256 * 1024

type DelegatedAgentDefinition struct {
	Name         string
	Description  string
	Instructions string
	Tools        []tool.AgentTool
	Path         string
}

type delegatedAgentFrontmatter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Tools       []string `yaml:"tools"`
}

func LoadDelegatedAgents(roots ...string) ([]DelegatedAgentDefinition, error) {
	byName := make(map[string]DelegatedAgentDefinition)
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
				continue
			}
			definition, err := ParseDelegatedAgentFile(filepath.Join(root, entry.Name()))
			if err != nil {
				return nil, err
			}
			byName[normalizeDelegatedAgentName(definition.Name)] = definition
		}
	}

	definitions := make([]DelegatedAgentDefinition, 0, len(byName))
	for _, definition := range byName {
		definitions = append(definitions, definition)
	}
	sort.SliceStable(definitions, func(i, j int) bool {
		return definitions[i].Name < definitions[j].Name
	})
	return definitions, nil
}

func ParseDelegatedAgentFile(path string) (DelegatedAgentDefinition, error) {
	info, err := os.Stat(path)
	if err != nil {
		return DelegatedAgentDefinition{}, err
	}
	if info.Size() > maxDelegatedAgentFileBytes {
		return DelegatedAgentDefinition{}, fmt.Errorf("delegated agent file too large: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return DelegatedAgentDefinition{}, err
	}
	meta, body, err := parseDelegatedAgentMarkdown(content)
	if err != nil {
		return DelegatedAgentDefinition{}, fmt.Errorf("parse %s: %w", path, err)
	}
	name := normalizeDelegatedAgentName(meta.Name)
	if name == "" {
		name = normalizeDelegatedAgentName(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}
	if name == "" {
		return DelegatedAgentDefinition{}, fmt.Errorf("delegated agent name is required: %s", path)
	}
	instructions := strings.TrimSpace(body)
	if instructions == "" {
		return DelegatedAgentDefinition{}, fmt.Errorf("delegated agent instructions are required: %s", path)
	}

	tools := make([]tool.AgentTool, 0, len(meta.Tools))
	seen := make(map[tool.AgentTool]struct{}, len(meta.Tools))
	for _, name := range meta.Tools {
		toolName := tool.AgentTool(strings.TrimSpace(name))
		if toolName == "" {
			continue
		}
		if _, ok := seen[toolName]; ok {
			continue
		}
		seen[toolName] = struct{}{}
		tools = append(tools, toolName)
	}

	return DelegatedAgentDefinition{
		Name:         name,
		Description:  strings.TrimSpace(meta.Description),
		Instructions: instructions,
		Tools:        tools,
		Path:         path,
	}, nil
}

func parseDelegatedAgentMarkdown(content []byte) (delegatedAgentFrontmatter, string, error) {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return delegatedAgentFrontmatter{}, strings.TrimSpace(text), nil
	}
	end := -1
	for idx := 1; idx < len(lines); idx++ {
		if strings.TrimSpace(lines[idx]) == "---" {
			end = idx
			break
		}
	}
	if end < 0 {
		return delegatedAgentFrontmatter{}, "", errors.New("unterminated frontmatter")
	}
	var meta delegatedAgentFrontmatter
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &meta); err != nil {
		return delegatedAgentFrontmatter{}, "", err
	}
	return meta, strings.Join(lines[end+1:], "\n"), nil
}

func normalizeDelegatedAgentName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var normalized strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			normalized.WriteRune(r)
		case r == ' ':
			normalized.WriteRune('-')
		}
	}
	return strings.Trim(normalized.String(), "-_")
}
