package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ternura/agent"
	"ternura/config"
	"ternura/tool"
)

func TestRuntimeAPIReportsActiveRunAndApproval(t *testing.T) {
	t.Setenv("TERNURA_API_TOKEN", "")
	store := newSessionStore(filepath.Join(t.TempDir(), "session.json"))
	pendingRun := runLifecycle{ID: "run-pending-admin", StartedAt: time.Now().Add(-time.Second)}
	if err := store.StartRun(pendingRun, "delete the generated file"); err != nil {
		t.Fatal(err)
	}
	pending := agent.AgentRunResult{
		Content:      "approval needed",
		CheckpointID: "checkpoint-admin",
		PendingApproval: &agent.ToolApprovalRequest{
			CheckpointID: "checkpoint-admin",
			InterruptID:  "interrupt-admin",
			Tool:         tool.AgentToolBash,
			Arguments:    `{"command":"rm output.txt"}`,
			Reason:       "command can remove a file",
		},
	}
	if err := store.FinishRun(pendingRun, "delete the generated file", pending, runStatusWaitingApproval, time.Now(), nil); err != nil {
		t.Fatal(err)
	}

	monitor := newRuntimeMonitor()
	activeRun := runLifecycle{ID: "run-active-admin", StartedAt: time.Now()}
	monitor.Start(activeRun, store.CurrentSessionID(), "api", "inspect status", runtimeStageThinking)
	server := &agentServer{
		modelConf: config.ModelConfig{Model: "test-model", ContextWindow: 8192},
		store:     store,
		runtime:   monitor,
		inventory: runtimeInventorySnapshot{SkillCount: 4, ToolCount: 9},
	}

	request := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	response := httptest.NewRecorder()
	server.handleRuntime(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload runtimeOverview
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Model != "test-model" || payload.Counts.ActiveRuns != 1 || payload.Counts.PendingApprovals != 1 {
		t.Fatalf("unexpected overview: %+v", payload)
	}
	if payload.Counts.Skills != 4 || payload.Counts.Tools != 9 {
		t.Fatalf("unexpected inventory: %+v", payload.Counts)
	}
	if len(payload.PendingApprovals) != 1 || payload.PendingApprovals[0].Tool != string(tool.AgentToolBash) {
		t.Fatalf("unexpected approvals: %+v", payload.PendingApprovals)
	}
	if !strings.Contains(payload.PendingApprovals[0].Arguments, "rm output.txt") {
		t.Fatalf("approval arguments missing: %+v", payload.PendingApprovals[0])
	}
	if strings.Contains(response.Body.String(), "short_term_summary") {
		t.Fatalf("runtime overview exposed memory content: %s", response.Body.String())
	}
}

func TestSystemDetailAPIListsRuntimeInventoryAndMemory(t *testing.T) {
	t.Setenv("TERNURA_API_TOKEN", "")
	root := t.TempDir()
	store := newSessionStore(filepath.Join(root, "session.json"))
	memory := newMemoryStore(root)
	if _, err := memory.Remember(context.Background(), tool.MemoryItem{
		Category: tool.MemoryCategoryProject,
		Content:  "Ternura uses Eino ADK.",
		Source:   "test",
	}); err != nil {
		t.Fatal(err)
	}
	registry := agent.NewSkillRegistry(agent.NewStaticSkill(agent.SkillConfig{
		Name:        "workspace",
		Description: "Inspect the workspace.",
		Tools:       []tool.Tool{tool.NewReadTool()},
	}))
	server := &agentServer{store: store, memory: memory}
	server.captureRuntimeInventory(registry)

	skillsRequest := httptest.NewRequest(http.MethodGet, "/api/system/skills", nil)
	skillsRequest.RemoteAddr = "127.0.0.1:12345"
	skillsResponse := httptest.NewRecorder()
	server.handleSystemDetail(skillsResponse, skillsRequest)
	if skillsResponse.Code != http.StatusOK {
		t.Fatalf("skills status = %d, body = %s", skillsResponse.Code, skillsResponse.Body.String())
	}
	var skillsPayload struct {
		Skills []runtimeSkillView `json:"skills"`
	}
	if err := json.Unmarshal(skillsResponse.Body.Bytes(), &skillsPayload); err != nil {
		t.Fatal(err)
	}
	if len(skillsPayload.Skills) != 1 || skillsPayload.Skills[0].Name != "workspace" || len(skillsPayload.Skills[0].Tools) != 1 {
		t.Fatalf("unexpected skills: %+v", skillsPayload.Skills)
	}

	memoryRequest := httptest.NewRequest(http.MethodGet, "/api/system/long-term-memory", nil)
	memoryRequest.RemoteAddr = "127.0.0.1:12345"
	memoryResponse := httptest.NewRecorder()
	server.handleSystemDetail(memoryResponse, memoryRequest)
	if memoryResponse.Code != http.StatusOK || !strings.Contains(memoryResponse.Body.String(), "Ternura uses Eino ADK") {
		t.Fatalf("memory status = %d, body = %s", memoryResponse.Code, memoryResponse.Body.String())
	}
}

func TestAdminRoutesServeEmbeddedConsole(t *testing.T) {
	t.Parallel()

	server := &agentServer{}
	mux := http.NewServeMux()
	server.registerAdminRoutes(mux)
	request := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if body := response.Body.String(); !strings.Contains(body, "Ternura Runtime") || !strings.Contains(body, "/admin/app.js") {
		t.Fatalf("unexpected admin page: %s", body)
	}
}
