package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanStorePersistsPendingPlan(t *testing.T) {
	store := newPlanStore(t.TempDir())
	plan := pendingPlan{
		SessionID:       "session-test",
		OriginalMessage: "plan 帮我重构 cron",
		Task:            "帮我重构 cron",
		Plan:            "1. Read cron code\n2. Refactor",
	}

	if err := store.Save(plan); err != nil {
		t.Fatalf("save pending plan: %v", err)
	}
	loaded, ok, err := store.Pending("session-test")
	if err != nil {
		t.Fatalf("load pending plan: %v", err)
	}
	if !ok {
		t.Fatal("pending plan missing")
	}
	if loaded.ID == "" || loaded.Task != plan.Task || loaded.Plan != plan.Plan {
		t.Fatalf("loaded plan = %+v", loaded)
	}
	if err := store.Clear("session-test"); err != nil {
		t.Fatalf("clear pending plan: %v", err)
	}
	if _, ok, err := store.Pending("session-test"); err != nil || ok {
		t.Fatalf("pending after clear ok=%v err=%v", ok, err)
	}
}

func TestPlanFlowStartsPendingPlan(t *testing.T) {
	server := testPlanServer(t,
		[]string{"## 目标\n重构 cron\n\n## 执行步骤\n1. 阅读代码\n2. 修改代码"},
		[]planRoutingDecision{{Action: planRouteStart, Task: "帮我重构 cron"}},
	)

	decision, err := server.handlePlanFlow(context.Background(), "session-test", "plan 帮我重构 cron")
	if err != nil {
		t.Fatalf("handle plan flow: %v", err)
	}

	if !decision.Handled || decision.Execute {
		t.Fatalf("decision = %+v, want handled planning response", decision)
	}
	if !strings.Contains(decision.Result.Content, "确认执行") || !strings.Contains(decision.Result.Content, "重构 cron") {
		t.Fatalf("plan response missing confirmation guidance:\n%s", decision.Result.Content)
	}
	if len(decision.Result.Trace) != 1 || decision.Result.Trace[0].Type != "plan" {
		t.Fatalf("plan trace = %+v", decision.Result.Trace)
	}
	if pending, ok, err := server.planStore.Pending("session-test"); err != nil || !ok || pending.Task != "帮我重构 cron" {
		t.Fatalf("pending plan ok=%v err=%v plan=%+v", ok, err, pending)
	}
}

func TestPlanFlowApprovesPendingPlanForExecution(t *testing.T) {
	server := testPlanServer(t,
		[]string{"## 目标\n检查代码\n\n## 执行步骤\n1. 读文件\n2. 总结"},
		[]planRoutingDecision{
			{Action: planRouteStart, Task: "帮我检查代码"},
			{Action: planRouteApprove},
		},
	)
	if _, err := server.handlePlanFlow(context.Background(), "session-test", "plan 帮我检查代码"); err != nil {
		t.Fatalf("start plan: %v", err)
	}

	decision, err := server.handlePlanFlow(context.Background(), "session-test", "确认执行")
	if err != nil {
		t.Fatalf("approve plan: %v", err)
	}

	if !decision.Handled || !decision.Execute {
		t.Fatalf("decision = %+v, want execute", decision)
	}
	if !strings.Contains(decision.RuntimeMessage, "[approved plan execution]") ||
		!strings.Contains(decision.RuntimeMessage, "帮我检查代码") ||
		!strings.Contains(decision.RuntimeMessage, "检查代码") {
		t.Fatalf("runtime message missing approved plan:\n%s", decision.RuntimeMessage)
	}
	if decision.ShortcutMessage != "帮我检查代码" {
		t.Fatalf("shortcut message = %q", decision.ShortcutMessage)
	}
	if _, ok, err := server.planStore.Pending("session-test"); err != nil || ok {
		t.Fatalf("pending after approve ok=%v err=%v", ok, err)
	}
}

func TestPlanFlowRevisesPendingPlan(t *testing.T) {
	server := testPlanServer(t,
		[]string{
			"## 目标\n初版\n\n## 执行步骤\n1. 读代码",
			"## 目标\n新版\n\n## 执行步骤\n1. 先跑测试\n2. 再改代码",
		},
		[]planRoutingDecision{
			{Action: planRouteStart, Task: "帮我改代码"},
			{Action: planRouteRevise, Feedback: "先跑测试"},
		},
	)
	if _, err := server.handlePlanFlow(context.Background(), "session-test", "plan 帮我改代码"); err != nil {
		t.Fatalf("start plan: %v", err)
	}

	decision, err := server.handlePlanFlow(context.Background(), "session-test", "先跑测试")
	if err != nil {
		t.Fatalf("revise plan: %v", err)
	}

	if !decision.Handled || decision.Execute {
		t.Fatalf("decision = %+v, want revised plan response", decision)
	}
	if !strings.Contains(decision.Result.Content, "已根据你的反馈更新计划") ||
		!strings.Contains(decision.Result.Content, "先跑测试") {
		t.Fatalf("revised response missing feedback:\n%s", decision.Result.Content)
	}
	pending, ok, err := server.planStore.Pending("session-test")
	if err != nil || !ok {
		t.Fatalf("pending after revise ok=%v err=%v", ok, err)
	}
	if !strings.Contains(pending.Plan, "先跑测试") {
		t.Fatalf("pending plan not revised:\n%s", pending.Plan)
	}
}

func TestPlanFlowTreatsGoTestAsFeedbackNotApproval(t *testing.T) {
	server := testPlanServer(t,
		[]string{
			"## 目标\n初版\n\n## 执行步骤\n1. 改代码",
			"## 目标\n新版\n\n## 执行步骤\n1. 先跑 go test\n2. 再改代码",
		},
		[]planRoutingDecision{
			{Action: planRouteStart, Task: "帮我改代码"},
			{Action: planRouteRevise, Feedback: "先跑 go test"},
		},
	)
	if _, err := server.handlePlanFlow(context.Background(), "session-test", "plan 帮我改代码"); err != nil {
		t.Fatalf("start plan: %v", err)
	}

	decision, err := server.handlePlanFlow(context.Background(), "session-test", "先跑 go test")
	if err != nil {
		t.Fatalf("revise plan: %v", err)
	}

	if decision.Execute {
		t.Fatalf("go test feedback should not approve execution: %+v", decision)
	}
	pending, ok, err := server.planStore.Pending("session-test")
	if err != nil || !ok {
		t.Fatalf("pending after go test feedback ok=%v err=%v", ok, err)
	}
	if !strings.Contains(pending.Plan, "go test") {
		t.Fatalf("pending plan not revised with go test:\n%s", pending.Plan)
	}
}

func TestPlanFlowCancelsPendingPlan(t *testing.T) {
	server := testPlanServer(t,
		[]string{"## 目标\n删除文件\n\n## 执行步骤\n1. 检查\n2. 删除"},
		[]planRoutingDecision{
			{Action: planRouteStart, Task: "删除文件"},
			{Action: planRouteCancel},
		},
	)
	if _, err := server.handlePlanFlow(context.Background(), "session-test", "plan 删除文件"); err != nil {
		t.Fatalf("start plan: %v", err)
	}

	decision, err := server.handlePlanFlow(context.Background(), "session-test", "取消")
	if err != nil {
		t.Fatalf("cancel plan: %v", err)
	}

	if !decision.Handled || decision.Execute || !strings.Contains(decision.Result.Content, "已取消") {
		t.Fatalf("decision = %+v, want cancel response", decision)
	}
	if _, ok, err := server.planStore.Pending("session-test"); err != nil || ok {
		t.Fatalf("pending after cancel ok=%v err=%v", ok, err)
	}
}

func TestPlanFlowIgnoresWhenRouterChoosesIgnore(t *testing.T) {
	server := testPlanServer(t, nil, []planRoutingDecision{{Action: planRouteIgnore}})

	decision, err := server.handlePlanFlow(context.Background(), "session-test", "帮我改代码")
	if err != nil {
		t.Fatalf("handle plan flow: %v", err)
	}

	if decision.Handled || decision.Execute {
		t.Fatalf("decision = %+v, want unhandled", decision)
	}
}

func TestParsePlanRoutingDecisionExtractsJSON(t *testing.T) {
	decision, err := parsePlanRoutingDecision("```json\n{\"action\":\"start\",\"task\":\"帮我改代码\",\"reason\":\"asked for plan\"}\n```")
	if err != nil {
		t.Fatalf("parse decision: %v", err)
	}
	if decision.Action != planRouteStart || decision.Task != "帮我改代码" {
		t.Fatalf("decision = %+v", decision)
	}
}

func testPlanServer(t *testing.T, plans []string, decisions []planRoutingDecision) *agentServer {
	t.Helper()
	root := t.TempDir()
	return &agentServer{
		store:       newSessionStore(filepath.Join(root, "session.json")),
		planStore:   newPlanStore(root),
		planner:     &fakePlanGenerator{plans: plans},
		planDecider: &fakePlanDecider{decisions: decisions},
	}
}

type fakePlanGenerator struct {
	plans []string
	calls int
}

func (f *fakePlanGenerator) GeneratePlan(ctx context.Context, input planGenerationInput) (string, error) {
	if len(f.plans) == 0 {
		return "## 目标\n" + input.Task + "\n\n## 执行步骤\n1. 执行", nil
	}
	idx := f.calls
	if idx >= len(f.plans) {
		idx = len(f.plans) - 1
	}
	f.calls++
	return f.plans[idx], nil
}

type fakePlanDecider struct {
	decisions []planRoutingDecision
	calls     int
}

func (f *fakePlanDecider) DecidePlanRoute(ctx context.Context, input planRoutingInput) (planRoutingDecision, error) {
	if len(f.decisions) == 0 {
		return planRoutingDecision{Action: planRouteIgnore}, nil
	}
	idx := f.calls
	if idx >= len(f.decisions) {
		idx = len(f.decisions) - 1
	}
	f.calls++
	return f.decisions[idx], nil
}
