package app

import (
	"path/filepath"
	"strings"
	"testing"

	"ternura/agent"
	"ternura/tool"
)

func TestAppSkillRegistryBuildsDefaultCapabilities(t *testing.T) {
	root := t.TempDir()
	server := &agentServer{
		store:  newSessionStore(filepath.Join(root, "session.json")),
		memory: newMemoryStore(root),
	}
	registry := server.newSkillRegistry("", tool.NewCronTool(nil, nil, nil))

	toolNames := make(map[tool.AgentTool]struct{})
	for _, item := range registry.Tools() {
		toolNames[item.ToolName()] = struct{}{}
	}
	for _, want := range []tool.AgentTool{
		tool.AgentToolRead,
		tool.AgentToolEdit,
		tool.AgentToolWrite,
		tool.AgentToolBash,
		tool.AgentToolUpdateTodos,
		tool.AgentToolRemember,
		tool.AgentToolForgetMemory,
		tool.AgentToolCron,
		tool.AgentToolWebFetch,
	} {
		if _, ok := toolNames[want]; !ok {
			t.Fatalf("skill registry missing tool %s", want)
		}
	}

	hooks := registry.Hooks()
	if len(hooks) < 6 {
		t.Fatalf("hooks = %d, want skill runtime + memory/schedule hooks", len(hooks))
	}
	if _, ok := hooks[0].(*agent.SkillRuntimeHook); !ok {
		t.Fatalf("first hook = %T, want *agent.SkillRuntimeHook", hooks[0])
	}

	instructions := registry.RuntimeInstructions()
	for _, want := range []string{`<skill name="workspace">`, `<skill name="memory">`, `<skill name="schedule">`, `<skill name="web">`} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("skill instructions missing %q:\n%s", want, instructions)
		}
	}
}
