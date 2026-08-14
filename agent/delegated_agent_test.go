package agent

import (
	"os"
	"path/filepath"
	"testing"

	"ternura/tool"
)

func TestParseDelegatedAgentFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "researcher.md")
	content := `---
name: Researcher
description: Research a focused question.
tools:
  - read
  - web_search
  - web_search
---
Collect evidence and return a concise synthesis.
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write agent definition: %v", err)
	}

	definition, err := ParseDelegatedAgentFile(path)
	if err != nil {
		t.Fatalf("parse agent definition: %v", err)
	}
	if definition.Name != "researcher" || definition.Description != "Research a focused question." {
		t.Fatalf("definition = %+v", definition)
	}
	if definition.Instructions != "Collect evidence and return a concise synthesis." {
		t.Fatalf("instructions = %q", definition.Instructions)
	}
	if len(definition.Tools) != 2 || definition.Tools[0] != tool.AgentToolRead || definition.Tools[1] != tool.AgentToolWebSearch {
		t.Fatalf("tools = %+v", definition.Tools)
	}
}

func TestLoadDelegatedAgentsLaterRootOverridesName(t *testing.T) {
	projectRoot := t.TempDir()
	localRoot := t.TempDir()
	writeDelegatedAgentTestFile(t, projectRoot, "researcher.md", "researcher", "project")
	writeDelegatedAgentTestFile(t, localRoot, "researcher.md", "researcher", "local")
	writeDelegatedAgentTestFile(t, projectRoot, "reviewer.md", "reviewer", "review")

	definitions, err := LoadDelegatedAgents(projectRoot, localRoot)
	if err != nil {
		t.Fatalf("load delegated agents: %v", err)
	}
	if len(definitions) != 2 {
		t.Fatalf("definitions = %+v", definitions)
	}
	if definitions[0].Name != "researcher" || definitions[0].Instructions != "local" {
		t.Fatalf("researcher override = %+v", definitions[0])
	}
	if definitions[1].Name != "reviewer" {
		t.Fatalf("reviewer = %+v", definitions[1])
	}
}

func writeDelegatedAgentTestFile(t *testing.T, root string, filename string, name string, instructions string) {
	t.Helper()
	content := "---\nname: " + name + "\n---\n" + instructions + "\n"
	if err := os.WriteFile(filepath.Join(root, filename), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
}
