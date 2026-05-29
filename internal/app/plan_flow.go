package app

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"ternura/agent"
	"ternura/config"
)

const planSystemPrompt = `You are Ternura's planning gate.

Create a concise execution plan for the user's task. Do not execute the task.
The plan will be shown to the user for approval before any real action is taken.

Output Chinese Markdown only, with:
- 目标
- 执行步骤
- 需要确认的风险或假设, if any

Keep it specific and actionable. Do not claim that any step has already been done.`

const planRouteSystemPrompt = `You are Ternura's plan-mode router.

Decide whether the user's message should be handled by Plan Mode.
Plan Mode means: first create or revise a plan, wait for user approval, and only execute after approval.

Return JSON only, with this schema:
{
  "action": "ignore" | "start" | "approve" | "cancel" | "revise",
  "task": "task to plan when action=start",
  "feedback": "revision request when action=revise",
  "reason": "brief internal reason"
}

Rules:
- If there is no pending plan, use "start" only when the user clearly wants a plan/approval gate before execution.
- If there is no pending plan and the user is asking a normal question or task, use "ignore".
- If there is a pending plan, use "approve" only when the user clearly authorizes execution.
- If there is a pending plan, use "cancel" only when the user wants to abandon the pending plan.
- If there is a pending plan and the user asks to adjust the plan, use "revise".
- If there is a pending plan but the message is unrelated to that pending plan, use "ignore".
- Do not execute the task yourself. Only classify the user's intent.
- The "task" should remove plan-mode trigger words and keep the actual task.
- The "feedback" should preserve the user's requested change.`

type planController interface {
	planGenerator
	planDecider
}

type planGenerator interface {
	GeneratePlan(ctx context.Context, input planGenerationInput) (string, error)
}

type planDecider interface {
	DecidePlanRoute(ctx context.Context, input planRoutingInput) (planRoutingDecision, error)
}

type planGenerationInput struct {
	Task         string
	ExistingPlan string
	Feedback     string
}

type planRouteAction string

const (
	planRouteIgnore  planRouteAction = "ignore"
	planRouteStart   planRouteAction = "start"
	planRouteApprove planRouteAction = "approve"
	planRouteCancel  planRouteAction = "cancel"
	planRouteRevise  planRouteAction = "revise"
)

type planRoutingInput struct {
	Message string
	Pending *pendingPlan
}

type planRoutingDecision struct {
	Action   planRouteAction `json:"action"`
	Task     string          `json:"task,omitempty"`
	Feedback string          `json:"feedback,omitempty"`
	Reason   string          `json:"reason,omitempty"`
}

type einoPlanController struct {
	model einomodel.BaseChatModel
}

type planFlowDecision struct {
	Handled          bool
	Execute          bool
	RuntimeMessage   string
	ShortcutMessage  string
	Result           agent.AgentRunResult
	ApprovedPlanID   string
	OriginalPlanTask string
}

func newEinoPlanController(modelConf config.ModelConfig) planController {
	model, err := einoopenai.NewChatModel(context.Background(), &einoopenai.ChatModelConfig{
		BaseURL: modelConf.BaseURL,
		APIKey:  modelConf.ApiKey,
		Model:   modelConf.Model,
	})
	if err != nil {
		log.Printf("create plan controller model: %v", err)
		return fallbackPlanController{}
	}
	return &einoPlanController{model: model}
}

func (g *einoPlanController) GeneratePlan(ctx context.Context, input planGenerationInput) (string, error) {
	if g == nil || g.model == nil {
		return "", errors.New("plan controller model is not initialized")
	}
	message, err := g.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(planSystemPrompt),
		schema.UserMessage(renderPlanGenerationPrompt(input)),
	})
	if err != nil {
		return "", err
	}
	if message == nil {
		return "", errors.New("plan generator returned nil message")
	}
	return strings.TrimSpace(message.Content), nil
}

func (g *einoPlanController) DecidePlanRoute(ctx context.Context, input planRoutingInput) (planRoutingDecision, error) {
	if g == nil || g.model == nil {
		return planRoutingDecision{}, errors.New("plan controller model is not initialized")
	}
	message, err := g.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(planRouteSystemPrompt),
		schema.UserMessage(renderPlanRoutingPrompt(input)),
	})
	if err != nil {
		return planRoutingDecision{}, err
	}
	if message == nil {
		return planRoutingDecision{}, errors.New("plan router returned nil message")
	}
	return parsePlanRoutingDecision(message.Content)
}

type fallbackPlanController struct{}

func (fallbackPlanController) GeneratePlan(_ context.Context, input planGenerationInput) (string, error) {
	task := strings.TrimSpace(input.Task)
	if task == "" {
		task = "用户请求的任务"
	}
	return strings.TrimSpace("## 目标\n" + task + "\n\n## 执行步骤\n1. 明确任务目标和约束。\n2. 检查相关上下文或项目文件。\n3. 按计划执行必要操作。\n4. 验证结果并向用户说明。\n\n## 需要确认的风险或假设\n- 需要你确认后才会真正执行。"), nil
}

func (fallbackPlanController) DecidePlanRoute(_ context.Context, input planRoutingInput) (planRoutingDecision, error) {
	if input.Pending != nil {
		return planRoutingDecision{
			Action:   planRouteRevise,
			Feedback: strings.TrimSpace(input.Message),
			Reason:   "fallback treats pending-plan replies as revision feedback",
		}, nil
	}
	return planRoutingDecision{Action: planRouteIgnore, Reason: "fallback cannot infer plan intent"}, nil
}

func renderPlanGenerationPrompt(input planGenerationInput) string {
	sections := []string{"Task:", strings.TrimSpace(input.Task)}
	if strings.TrimSpace(input.ExistingPlan) != "" {
		sections = append(sections, "", "Existing plan:", input.ExistingPlan)
	}
	if strings.TrimSpace(input.Feedback) != "" {
		sections = append(sections, "", "User feedback for revision:", input.Feedback)
	}
	return strings.Join(sections, "\n")
}

func renderPlanRoutingPrompt(input planRoutingInput) string {
	sections := []string{"User message:", strings.TrimSpace(input.Message)}
	if input.Pending == nil {
		sections = append(sections, "", "Pending plan: none")
		return strings.Join(sections, "\n")
	}
	sections = append(sections,
		"",
		"Pending plan:",
		"ID: "+input.Pending.ID,
		"Task: "+input.Pending.Task,
		"Plan:",
		input.Pending.Plan,
	)
	return strings.Join(sections, "\n")
}

func (s *agentServer) handlePlanFlow(ctx context.Context, sessionID string, message string) (planFlowDecision, error) {
	if s == nil || s.planStore == nil {
		return planFlowDecision{}, nil
	}

	pending, hasPending, err := s.planStore.Pending(sessionID)
	if err != nil {
		return planFlowDecision{}, err
	}
	var pendingPtr *pendingPlan
	if hasPending {
		pendingPtr = &pending
	}

	route, err := s.decidePlanRoute(ctx, planRoutingInput{Message: message, Pending: pendingPtr})
	if err != nil {
		return planFlowDecision{}, err
	}
	switch route.Action {
	case planRouteIgnore:
		return planFlowDecision{}, nil
	case planRouteStart:
		return s.startPlan(ctx, sessionID, message, route.Task)
	case planRouteApprove:
		if !hasPending {
			return planFlowDecision{}, nil
		}
		return s.approvePendingPlan(pending)
	case planRouteCancel:
		if !hasPending {
			return planFlowDecision{}, nil
		}
		return s.cancelPendingPlan(pending)
	case planRouteRevise:
		if !hasPending {
			return planFlowDecision{}, nil
		}
		feedback := strings.TrimSpace(route.Feedback)
		if feedback == "" {
			feedback = message
		}
		return s.revisePendingPlan(ctx, pending, feedback)
	default:
		return planFlowDecision{}, nil
	}
}

func (s *agentServer) startPlan(ctx context.Context, sessionID string, message string, task string) (planFlowDecision, error) {
	if strings.TrimSpace(task) == "" {
		result := agent.AgentRunResult{Content: "可以，我会先做计划再等你确认。请补充你希望我规划的具体任务。"}
		return planFlowDecision{Handled: true, Result: result}, nil
	}
	task = strings.TrimSpace(task)

	planText, err := s.generatePlan(ctx, planGenerationInput{Task: task})
	if err != nil {
		return planFlowDecision{}, err
	}
	pending := pendingPlan{
		Version:         pendingPlanStoreVersion,
		ID:              newPendingPlanID(time.Now()),
		SessionID:       sessionID,
		OriginalMessage: message,
		Task:            task,
		Plan:            planText,
	}
	if err := s.planStore.Save(pending); err != nil {
		return planFlowDecision{}, err
	}

	return planFlowDecision{
		Handled: true,
		Result: agent.AgentRunResult{
			Content: formatPlanConfirmationReply(planText),
			Trace: []agent.AgentTraceItem{{
				Type:    "plan",
				Title:   "Plan pending confirmation",
				Content: planText,
			}},
		},
	}, nil
}

func (s *agentServer) approvePendingPlan(pending pendingPlan) (planFlowDecision, error) {
	if err := s.planStore.Clear(pending.SessionID); err != nil {
		return planFlowDecision{}, err
	}
	return planFlowDecision{
		Handled:          true,
		Execute:          true,
		RuntimeMessage:   buildApprovedPlanRuntimeMessage(pending),
		ShortcutMessage:  pending.Task,
		ApprovedPlanID:   pending.ID,
		OriginalPlanTask: pending.Task,
	}, nil
}

func (s *agentServer) cancelPendingPlan(pending pendingPlan) (planFlowDecision, error) {
	if err := s.planStore.Clear(pending.SessionID); err != nil {
		return planFlowDecision{}, err
	}
	return planFlowDecision{
		Handled: true,
		Result: agent.AgentRunResult{
			Content: "已取消这次待确认计划，不会执行任何操作。",
			Trace: []agent.AgentTraceItem{{
				Type:    "plan",
				Title:   "Plan cancelled",
				Content: pending.Plan,
			}},
		},
	}, nil
}

func (s *agentServer) revisePendingPlan(ctx context.Context, pending pendingPlan, feedback string) (planFlowDecision, error) {
	revisedPlan, err := s.generatePlan(ctx, planGenerationInput{
		Task:         pending.Task,
		ExistingPlan: pending.Plan,
		Feedback:     feedback,
	})
	if err != nil {
		return planFlowDecision{}, err
	}
	pending.Plan = revisedPlan
	if err := s.planStore.Save(pending); err != nil {
		return planFlowDecision{}, err
	}
	return planFlowDecision{
		Handled: true,
		Result: agent.AgentRunResult{
			Content: "已根据你的反馈更新计划：\n\n" + formatPlanConfirmationReply(revisedPlan),
			Trace: []agent.AgentTraceItem{{
				Type:    "plan",
				Title:   "Plan revised",
				Content: revisedPlan,
			}},
		},
	}, nil
}

func (s *agentServer) decidePlanRoute(ctx context.Context, input planRoutingInput) (planRoutingDecision, error) {
	decider := planDecider(fallbackPlanController{})
	if s != nil && s.planDecider != nil {
		decider = s.planDecider
	}
	decision, err := decider.DecidePlanRoute(ctx, input)
	if err != nil {
		log.Printf("decide plan route: %v", err)
		return fallbackPlanController{}.DecidePlanRoute(ctx, input)
	}
	return normalizePlanRoutingDecision(decision), nil
}

func (s *agentServer) generatePlan(ctx context.Context, input planGenerationInput) (string, error) {
	if s == nil || s.planner == nil {
		return fallbackPlanController{}.GeneratePlan(ctx, input)
	}
	planText, err := s.planner.GeneratePlan(ctx, input)
	if err != nil {
		log.Printf("generate plan: %v", err)
		return fallbackPlanController{}.GeneratePlan(ctx, input)
	}
	if strings.TrimSpace(planText) == "" {
		return fallbackPlanController{}.GeneratePlan(ctx, input)
	}
	return strings.TrimSpace(planText), nil
}

func parsePlanRoutingDecision(content string) (planRoutingDecision, error) {
	var decision planRoutingDecision
	if err := json.Unmarshal([]byte(extractPlanDecisionJSON(content)), &decision); err != nil {
		return planRoutingDecision{}, err
	}
	return normalizePlanRoutingDecision(decision), nil
}

func normalizePlanRoutingDecision(decision planRoutingDecision) planRoutingDecision {
	switch decision.Action {
	case planRouteStart, planRouteApprove, planRouteCancel, planRouteRevise:
	default:
		decision.Action = planRouteIgnore
	}
	decision.Task = strings.Join(strings.Fields(decision.Task), " ")
	decision.Feedback = strings.TrimSpace(decision.Feedback)
	decision.Reason = strings.TrimSpace(decision.Reason)
	return decision
}

func extractPlanDecisionJSON(content string) string {
	content = strings.TrimSpace(content)
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end >= start {
		return content[start : end+1]
	}
	return content
}

func formatPlanConfirmationReply(planText string) string {
	return strings.TrimSpace(planText) + "\n\n---\n\n请回复 `确认执行` 开始执行；回复 `取消` 放弃；也可以直接发修改意见，我会先更新计划。"
}

func buildApprovedPlanRuntimeMessage(plan pendingPlan) string {
	return strings.TrimSpace(strings.Join([]string{
		"[approved plan execution]",
		"User has approved the following plan. Execute the original task now.",
		"",
		"Original task:",
		plan.Task,
		"",
		"Approved plan:",
		plan.Plan,
	}, "\n"))
}
