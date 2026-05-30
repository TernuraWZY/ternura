package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"ternura/agent"
	"ternura/config"
	"ternura/tool"
)

const toolGroundingVerifierSystemPrompt = `You are Ternura's evidence verifier.

Given a user's message, the final answer, and ONLY the current run's tool evidence, identify claims in the final answer that require current-run tool evidence.
Return ONLY a JSON object:
{
  "claims": [
    {
      "text": "short claim",
      "needs_current_tool_evidence": true,
      "evidence_refs": ["call_id"],
      "reason": "short reason",
      "suggested_tools": ["web_fetch"]
    }
  ]
}

Rules:
- Current-run tool evidence means the provided tool calls in this verifier input only. Conversation history and memory are not current-run evidence.
- Mark needs_current_tool_evidence=true for claims about external/current facts, fetched/search results, stock prices, weather, news, exchange rates, local environment state, command output, installation status, file mutations, memory mutations, cron/job state, or any real side effect.
- Mark needs_current_tool_evidence=false for advice, general knowledge, opinions, summaries of the conversation, or explicit uncertainty/disclosure that no tool was run.
- If a claim needs current-run evidence, evidence_refs must list the supporting current successful tool call ids. Do not invent ids.
- If no provided successful tool call supports the claim, leave evidence_refs empty and suggest appropriate available tools by name when useful.
- Suggested tools may include read, write, edit, bash, update_todos, remember, forget_memory, cron, web_fetch.
- Keep claims few and high-signal.`

const toolGroundingRepairSystemPrompt = `You are Ternura's final-answer repairer.

Rewrite the final answer so it only keeps claims supported by the current run's tool evidence or claims that do not need current-run evidence.
Return ONLY the repaired answer text.

Rules:
- Remove unsupported claims listed in the input.
- Do not add new facts, names, numbers, URLs, dates, file states, command results, or side effects.
- Keep supported content, useful caveats, and the user's original answer style.
- If a removed detail is important, replace it with a brief uncertainty disclosure such as "本轮工具结果没有覆盖这个字段".
- Do not mention this verifier or internal guard unless the original answer already did.`

type toolGroundingVerifier interface {
	VerifyToolGrounding(ctx context.Context, input toolGroundingVerificationInput) (toolGroundingVerification, error)
}

type toolGroundingRepairer interface {
	RepairToolGrounding(ctx context.Context, input toolGroundingRepairInput) (string, error)
}

type toolGroundingVerificationInput struct {
	UserMessage  string
	FinalAnswer  string
	ToolEvidence []toolGroundingToolEvidence
}

type toolGroundingRepairInput struct {
	UserMessage       string
	FinalAnswer       string
	ToolEvidence      []toolGroundingToolEvidence
	UnsupportedClaims []toolGroundingUnsupportedClaim
}

type toolGroundingToolEvidence struct {
	CallID  string `json:"call_id"`
	Tool    string `json:"tool"`
	Success bool   `json:"success"`
	Summary string `json:"summary"`
}

type toolGroundingVerification struct {
	Claims []toolGroundingVerifiedClaim `json:"claims"`
}

type toolGroundingVerifiedClaim struct {
	Text                     string   `json:"text"`
	NeedsCurrentToolEvidence bool     `json:"needs_current_tool_evidence"`
	EvidenceRefs             []string `json:"evidence_refs"`
	Reason                   string   `json:"reason"`
	SuggestedTools           []string `json:"suggested_tools"`
}

type toolGroundingUnsupportedClaim struct {
	Text         string
	Reason       string
	EvidenceRefs []string
}

type einoToolGroundingVerifier struct {
	model einomodel.BaseChatModel
}

func newEinoToolGroundingVerifier(modelConf config.ModelConfig) toolGroundingVerifier {
	model, err := einoopenai.NewChatModel(context.Background(), &einoopenai.ChatModelConfig{
		BaseURL: modelConf.BaseURL,
		APIKey:  modelConf.ApiKey,
		Model:   modelConf.Model,
	})
	if err != nil {
		log.Printf("create tool grounding verifier model: %v", err)
		return nil
	}
	return &einoToolGroundingVerifier{model: model}
}

func (e *einoToolGroundingVerifier) VerifyToolGrounding(ctx context.Context, input toolGroundingVerificationInput) (toolGroundingVerification, error) {
	if e == nil || e.model == nil {
		return toolGroundingVerification{}, errors.New("tool grounding verifier model is not initialized")
	}
	message, err := e.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(toolGroundingVerifierSystemPrompt),
		schema.UserMessage(renderToolGroundingVerificationPrompt(input)),
	})
	if err != nil {
		return toolGroundingVerification{}, err
	}
	if message == nil {
		return toolGroundingVerification{}, errors.New("tool grounding verifier model returned nil message")
	}
	return parseToolGroundingVerification(message.Content)
}

func (e *einoToolGroundingVerifier) RepairToolGrounding(ctx context.Context, input toolGroundingRepairInput) (string, error) {
	if e == nil || e.model == nil {
		return "", errors.New("tool grounding repair model is not initialized")
	}
	message, err := e.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(toolGroundingRepairSystemPrompt),
		schema.UserMessage(renderToolGroundingRepairPrompt(input)),
	})
	if err != nil {
		return "", err
	}
	if message == nil {
		return "", errors.New("tool grounding repair model returned nil message")
	}
	repaired := stripToolGroundingRepairReasoning(message.Content)
	if repaired == "" {
		return "", errors.New("tool grounding repair model returned empty message")
	}
	return repaired, nil
}

func stripToolGroundingRepairReasoning(content string) string {
	var builder strings.Builder
	remaining := strings.TrimSpace(content)
	for {
		start := strings.Index(remaining, "<think>")
		if start == -1 {
			builder.WriteString(remaining)
			break
		}
		builder.WriteString(remaining[:start])
		afterStart := remaining[start+len("<think>"):]
		end := strings.Index(afterStart, "</think>")
		if end == -1 {
			break
		}
		remaining = afterStart[end+len("</think>"):]
	}
	return strings.TrimSpace(builder.String())
}

func renderToolGroundingVerificationPrompt(input toolGroundingVerificationInput) string {
	payload := struct {
		UserMessage  string                      `json:"user_message"`
		FinalAnswer  string                      `json:"final_answer"`
		ToolEvidence []toolGroundingToolEvidence `json:"current_run_tool_evidence"`
	}{
		UserMessage:  strings.TrimSpace(input.UserMessage),
		FinalAnswer:  strings.TrimSpace(input.FinalAnswer),
		ToolEvidence: input.ToolEvidence,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Sprintf("User message:\n%s\n\nFinal answer:\n%s", input.UserMessage, input.FinalAnswer)
	}
	return string(data)
}

func renderToolGroundingRepairPrompt(input toolGroundingRepairInput) string {
	payload := struct {
		UserMessage       string                          `json:"user_message"`
		FinalAnswer       string                          `json:"final_answer"`
		ToolEvidence      []toolGroundingToolEvidence     `json:"current_run_tool_evidence"`
		UnsupportedClaims []toolGroundingUnsupportedClaim `json:"unsupported_claims"`
	}{
		UserMessage:       strings.TrimSpace(input.UserMessage),
		FinalAnswer:       strings.TrimSpace(input.FinalAnswer),
		ToolEvidence:      input.ToolEvidence,
		UnsupportedClaims: input.UnsupportedClaims,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Sprintf("User message:\n%s\n\nFinal answer:\n%s", input.UserMessage, input.FinalAnswer)
	}
	return string(data)
}

func parseToolGroundingVerification(content string) (toolGroundingVerification, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return toolGroundingVerification{}, errors.New("empty tool grounding verifier response")
	}
	content = extractJSONObject(content)
	var result toolGroundingVerification
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return toolGroundingVerification{}, fmt.Errorf("parse tool grounding verifier response: %w", err)
	}
	for idx := range result.Claims {
		result.Claims[idx].Text = strings.TrimSpace(result.Claims[idx].Text)
		result.Claims[idx].Reason = strings.TrimSpace(result.Claims[idx].Reason)
		result.Claims[idx].EvidenceRefs = cleanGroundingStringList(result.Claims[idx].EvidenceRefs)
		result.Claims[idx].SuggestedTools = normalizeSuggestedToolNames(result.Claims[idx].SuggestedTools)
	}
	return result, nil
}

func currentToolEvidence(run *agent.RunContext) []toolGroundingToolEvidence {
	if run == nil {
		return nil
	}
	results := run.ToolResults()
	evidence := make([]toolGroundingToolEvidence, 0, len(results))
	for _, result := range results {
		callID := strings.TrimSpace(result.Call.ID)
		name := strings.TrimSpace(result.Call.Function.Name)
		if callID == "" || name == "" {
			continue
		}
		summary := summarizeToolEvidenceResult(result.Content, result.Error)
		evidence = append(evidence, toolGroundingToolEvidence{
			CallID:  callID,
			Tool:    name,
			Success: result.Error == "",
			Summary: summary,
		})
	}
	return evidence
}

func summarizeToolEvidenceResult(content string, errText string) string {
	summary := content
	if errText != "" {
		summary = errText
	}
	summary = strings.Join(strings.Fields(summary), " ")
	if len([]rune(summary)) <= 1400 {
		return summary
	}
	return truncateRunes(summary, 700) + " ... " + tailRunes(summary, 700)
}

func tailRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[len(runes)-limit:])
}

func decisionFromToolGroundingVerification(verification toolGroundingVerification, run *agent.RunContext) toolGroundingDecision {
	successfulRefs := successfulToolCallRefs(run)
	unsupported := make([]toolGroundingUnsupportedClaim, 0)
	requiredTools := make([]string, 0)
	for _, claim := range verification.Claims {
		if !claim.NeedsCurrentToolEvidence {
			continue
		}
		if hasValidEvidenceRef(claim.EvidenceRefs, successfulRefs) {
			continue
		}
		unsupported = append(unsupported, toolGroundingUnsupportedClaim{
			Text:         firstNonEmpty(claim.Text, "claim requiring current tool evidence"),
			Reason:       claim.Reason,
			EvidenceRefs: claim.EvidenceRefs,
		})
		requiredTools = append(requiredTools, claim.SuggestedTools...)
	}
	if len(unsupported) == 0 {
		return toolGroundingDecision{
			Verified:       true,
			VerifierClaims: verification.Claims,
		}
	}
	return toolGroundingDecision{
		Block:          true,
		Reason:         "unsupported evidence claim",
		RequiredTools:  uniqueStrings(requiredTools),
		GroundedTools:  collectToolGroundingEvidence(run, nil).names(),
		RequireSuccess: true,
		Unsupported:    unsupported,
		VerifierClaims: verification.Claims,
	}
}

func successfulToolCallRefs(run *agent.RunContext) map[string]struct{} {
	refs := make(map[string]struct{})
	if run == nil {
		return refs
	}
	for _, result := range run.ToolResults() {
		if result.Error != "" {
			continue
		}
		id := strings.TrimSpace(result.Call.ID)
		if id == "" {
			continue
		}
		refs[id] = struct{}{}
		refs["tool_result:"+id] = struct{}{}
		refs["tool:"+id] = struct{}{}
	}
	return refs
}

func hasValidEvidenceRef(refs []string, valid map[string]struct{}) bool {
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if _, ok := valid[ref]; ok {
			return true
		}
	}
	return false
}

func normalizeSuggestedToolNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		switch tool.AgentTool(name) {
		case tool.AgentToolRead, tool.AgentToolWrite, tool.AgentToolEdit, tool.AgentToolBash,
			tool.AgentToolUpdateTodos, tool.AgentToolRemember, tool.AgentToolForgetMemory,
			tool.AgentToolCron, tool.AgentToolWebFetch:
			out = append(out, name)
		}
	}
	return uniqueStrings(out)
}

func cleanGroundingStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
