package agent

import (
	"context"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"

	"ternura/tool"
)

func TestPrepareToolSearchAutoUsesFullListBelowThreshold(t *testing.T) {
	t.Parallel()

	a := &Agent{toolSearch: DynamicToolSearchConfig{Mode: DynamicToolSearchAuto, Threshold: 10}}
	tools := []tool.Tool{tool.NewReadTool(), tool.NewWebFetchTool()}
	base, middleware, err := a.prepareToolSearch(context.Background(), nil, []einotool.BaseTool{tools[0], tools[1]})
	if err != nil {
		t.Fatal(err)
	}
	if middleware != nil {
		t.Fatal("expected tool search to remain disabled below threshold")
	}
	if len(base) != len(tools) {
		t.Fatalf("expected %d base tools, got %d", len(tools), len(base))
	}
}

func TestPrepareToolSearchKeepsCoreToolsVisible(t *testing.T) {
	t.Parallel()

	a := &Agent{toolSearch: DynamicToolSearchConfig{
		Mode:      DynamicToolSearchEnabled,
		Threshold: 1,
		CoreTools: []tool.AgentTool{tool.AgentToolRead},
	}}
	tools := []einotool.BaseTool{tool.NewReadTool(), tool.NewWebFetchTool()}
	base, middleware, err := a.prepareToolSearch(context.Background(), nil, tools)
	if err != nil {
		t.Fatal(err)
	}
	if middleware == nil {
		t.Fatal("expected dynamic tool search middleware")
	}
	if len(base) != 1 {
		t.Fatalf("expected one core tool, got %d", len(base))
	}
	info, err := base[0].Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != string(tool.AgentToolRead) {
		t.Fatalf("expected read core tool, got %s", info.Name)
	}
}

func TestPrepareToolSearchDoesNotHideSpecificallyRequiredTool(t *testing.T) {
	t.Parallel()

	a := &Agent{toolSearch: DynamicToolSearchConfig{
		Mode:      DynamicToolSearchEnabled,
		Threshold: 1,
		CoreTools: []tool.AgentTool{tool.AgentToolRead},
	}}
	runCtx := NewRunContext("fetch", RunModeSync)
	runCtx.SetToolPolicy(RequireTool(tool.AgentToolWebFetch))
	tools := []einotool.BaseTool{tool.NewWebFetchTool()}
	base, middleware, err := a.prepareToolSearch(context.Background(), runCtx, tools)
	if err != nil {
		t.Fatal(err)
	}
	if middleware != nil || len(base) != 1 {
		t.Fatalf("required tool should remain directly visible: base=%d middleware=%T", len(base), middleware)
	}
}
