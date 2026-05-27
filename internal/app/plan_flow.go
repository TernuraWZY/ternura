package app

import (
	"context"
	"errors"
	"log"
	"regexp"
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

var planKeywordPattern = regexp.MustCompile(`(?i)(^|[\s[:punct:]])plan([\s[:punct:]]|$)`)

type planGenerator interface {
	GeneratePlan(ctx context.Context, input planGenerationInput) (string, error)
}

type planGenerationInput struct {
	Task         string
	ExistingPlan string
	Feedback     string
}

type einoPlanGenerator struct {
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

func newEinoPlanGenerator(modelConf config.ModelConfig) planGenerator {
	model, err := einoopenai.NewChatModel(context.Background(), &einoopenai.ChatModelConfig{
		BaseURL: modelConf.BaseURL,
		APIKey:  modelConf.ApiKey,
		Model:   modelConf.Model,
	})
	if err != nil {
		log.Printf("create plan generator model: %v", err)
		return fallbackPlanGenerator{}
	}
	return &einoPlanGenerator{model: model}
}

func (g *einoPlanGenerator) GeneratePlan(ctx context.Context, input planGenerationInput) (string, error) {
	if g == nil || g.model == nil {
		return "", errors.New("plan generator model is not initialized")
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

type fallbackPlanGenerator struct{}

func (fallbackPlanGenerator) GeneratePlan(_ context.Context, input planGenerationInput) (string, error) {
	task := strings.TrimSpace(input.Task)
	if task == "" {
		task = "用户请求的任务"
	}
	return strings.TrimSpace("## 目标\n" + task + "\n\n## 执行步骤\n1. 明确任务目标和约束。\n2. 检查相关上下文或项目文件。\n3. 按计划执行必要操作。\n4. 验证结果并向用户说明。\n\n## 需要确认的风险或假设\n- 需要你确认后才会真正执行。"), nil
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

func (s *agentServer) handlePlanFlow(ctx context.Context, sessionID string, message string) (planFlowDecision, error) {
	if s == nil || s.planStore == nil {
		return planFlowDecision{}, nil
	}

	pending, hasPending, err := s.planStore.Pending(sessionID)
	if err != nil {
		return planFlowDecision{}, err
	}
	if hasPending {
		return s.handlePendingPlan(ctx, pending, message)
	}
	if !messageRequestsPlan(message) {
		return planFlowDecision{}, nil
	}
	return s.startPlan(ctx, sessionID, message)
}

func (s *agentServer) startPlan(ctx context.Context, sessionID string, message string) (planFlowDecision, error) {
	task := stripPlanKeyword(message)
	if strings.TrimSpace(task) == "" {
		result := agent.AgentRunResult{Content: "可以。请把 `plan` 和你要我执行的任务放在同一条消息里，例如：`plan 帮我重构 cron 模块`。"}
		return planFlowDecision{Handled: true, Result: result}, nil
	}

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

func (s *agentServer) handlePendingPlan(ctx context.Context, pending pendingPlan, message string) (planFlowDecision, error) {
	switch classifyPlanResponse(message) {
	case planResponseApprove:
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
	case planResponseCancel:
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
	default:
		revisedPlan, err := s.generatePlan(ctx, planGenerationInput{
			Task:         pending.Task,
			ExistingPlan: pending.Plan,
			Feedback:     message,
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
}

func (s *agentServer) generatePlan(ctx context.Context, input planGenerationInput) (string, error) {
	if s == nil || s.planner == nil {
		return fallbackPlanGenerator{}.GeneratePlan(ctx, input)
	}
	planText, err := s.planner.GeneratePlan(ctx, input)
	if err != nil {
		log.Printf("generate plan: %v", err)
		return fallbackPlanGenerator{}.GeneratePlan(ctx, input)
	}
	if strings.TrimSpace(planText) == "" {
		return fallbackPlanGenerator{}.GeneratePlan(ctx, input)
	}
	return strings.TrimSpace(planText), nil
}

type planResponseKind int

const (
	planResponseFeedback planResponseKind = iota
	planResponseApprove
	planResponseCancel
)

func classifyPlanResponse(message string) planResponseKind {
	normalized := strings.ToLower(strings.TrimSpace(message))
	normalized = strings.Trim(normalized, " \t\r\n。.!！")
	if normalized == "" {
		return planResponseFeedback
	}

	for _, marker := range []string{"取消", "不要", "不用", "不可以", "算了", "停止", "放弃"} {
		if normalized == marker || strings.Contains(normalized, marker) {
			return planResponseCancel
		}
	}
	if normalized == "否" {
		return planResponseCancel
	}
	for _, marker := range []string{"确认", "确定", "同意", "执行", "开始", "继续", "批准", "可以", "按这个来"} {
		if normalized == marker || strings.Contains(normalized, marker) {
			return planResponseApprove
		}
	}
	tokens := asciiWordTokens(normalized)
	if hasAnyToken(tokens, "cancel", "no", "reject", "stop", "n") {
		return planResponseCancel
	}
	if hasAnyToken(tokens, "yes", "y", "ok", "approve", "confirm") || normalized == "go" {
		return planResponseApprove
	}
	return planResponseFeedback
}

func messageRequestsPlan(message string) bool {
	message = strings.TrimSpace(message)
	if message == "" {
		return false
	}
	lower := strings.ToLower(message)
	return hasPlanPrefix(lower) || planKeywordPattern.MatchString(lower) || strings.Contains(message, "计划")
}

func stripPlanKeyword(message string) string {
	cleaned := strings.TrimSpace(message)
	if hasPlanPrefix(strings.ToLower(cleaned)) {
		cleaned = strings.TrimSpace(cleaned[len("plan"):])
	}
	cleaned = planKeywordPattern.ReplaceAllString(cleaned, " ")
	for _, marker := range []string{"先做计划", "做个计划", "制定计划", "计划一下", "先计划", "计划"} {
		cleaned = strings.ReplaceAll(cleaned, marker, " ")
	}
	cleaned = strings.Trim(cleaned, " \t\r\n:：,，.。-—")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	return cleaned
}

func hasPlanPrefix(message string) bool {
	message = strings.TrimSpace(message)
	if message == "plan" {
		return true
	}
	if !strings.HasPrefix(message, "plan") {
		return false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(message, "plan"))
	if rest == "" {
		return true
	}
	return !isASCIIWordRune(firstRune(rest))
}

func firstRune(value string) rune {
	for _, r := range value {
		return r
	}
	return 0
}

func isASCIIWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

func asciiWordTokens(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return !isASCIIWordRune(r)
	})
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.ToLower(strings.TrimSpace(field))
		if field != "" {
			tokens = append(tokens, field)
		}
	}
	return tokens
}

func hasAnyToken(tokens []string, candidates ...string) bool {
	if len(tokens) == 0 {
		return false
	}
	allowed := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		allowed[candidate] = struct{}{}
	}
	for _, token := range tokens {
		if _, ok := allowed[token]; ok {
			return true
		}
	}
	return false
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
