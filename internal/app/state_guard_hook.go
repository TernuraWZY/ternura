package app

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"ternura/agent"
	"ternura/internal/cron"
	"ternura/tool"
)

var cronJobIDPattern = regexp.MustCompile(`(?:(?:cron|schedule)-\d{8}T\d{6}(?:\.\d{1,9})?)`)

type stateGuardHook struct {
	cron *cron.Service
}

func newStateGuardHook(cronService *cron.Service) *stateGuardHook {
	return &stateGuardHook{cron: cronService}
}

func (h *stateGuardHook) HookName() string {
	return "state_guard"
}

func (h *stateGuardHook) FinalizeRun(_ context.Context, run *agent.RunContext, result *agent.AgentRunResult) error {
	if result == nil || run == nil || strings.TrimSpace(result.Content) == "" {
		return nil
	}

	decision := h.checkScheduleGrounding(run, result)
	if !decision.Block {
		return nil
	}

	original := result.Content
	if result.RawContent == "" {
		result.RawContent = original
	}
	result.Trace = append(result.Trace, agent.AgentTraceItem{
		Type:    "guard",
		Title:   "Harness guard",
		Content: decision.TraceContent(),
	})
	result.Content = decision.UserMessage()
	return nil
}

type scheduleGroundingDecision struct {
	Block           bool
	ClaimedIDs      []string
	UngroundedIDs   []string
	SuccessfulTools []string
}

func (h *stateGuardHook) checkScheduleGrounding(run *agent.RunContext, result *agent.AgentRunResult) scheduleGroundingDecision {
	content := strings.TrimSpace(result.Content)
	claimedIDs := uniqueStrings(cronJobIDPattern.FindAllString(content, -1))
	successfulTools := h.successfulCronToolIDs(run, result)
	successfulSet := stringSet(successfulTools)
	knownSet := h.knownCronJobIDSet()
	for id := range successfulSet {
		knownSet[id] = struct{}{}
	}

	ungrounded := make([]string, 0)
	for _, id := range claimedIDs {
		if _, ok := knownSet[id]; !ok {
			ungrounded = append(ungrounded, id)
		}
	}

	claimsSuccess := looksLikeScheduleSuccess(content)
	if len(ungrounded) > 0 && (claimsSuccess || presentsScheduleJobID(content)) {
		return scheduleGroundingDecision{
			Block:           true,
			ClaimedIDs:      claimedIDs,
			UngroundedIDs:   ungrounded,
			SuccessfulTools: successfulTools,
		}
	}

	if claimsSuccess && len(successfulTools) == 0 && len(claimedIDs) == 0 && looksLikeConcreteScheduleIntent(run.Query) {
		return scheduleGroundingDecision{
			Block:           true,
			ClaimedIDs:      claimedIDs,
			UngroundedIDs:   nil,
			SuccessfulTools: successfulTools,
		}
	}

	return scheduleGroundingDecision{
		ClaimedIDs:      claimedIDs,
		UngroundedIDs:   ungrounded,
		SuccessfulTools: successfulTools,
	}
}

func (h *stateGuardHook) successfulCronToolIDs(run *agent.RunContext, result *agent.AgentRunResult) []string {
	ids := make([]string, 0)
	if run != nil {
		for _, item := range run.ToolResults() {
			if item.Call.Function.Name != string(tool.AgentToolCron) || item.Error != "" {
				continue
			}
			ids = append(ids, cronJobIDPattern.FindAllString(item.Content, -1)...)
		}
	}
	if result != nil {
		for _, item := range result.Trace {
			if item.Title != "Tool use: "+string(tool.AgentToolCron) {
				continue
			}
			if strings.Contains(item.Content, "**Error**") {
				continue
			}
			ids = append(ids, cronJobIDPattern.FindAllString(item.Content, -1)...)
		}
	}
	return uniqueStrings(ids)
}

func (h *stateGuardHook) knownCronJobIDSet() map[string]struct{} {
	ids := make(map[string]struct{})
	if h == nil || h.cron == nil {
		return ids
	}
	for _, job := range h.cron.List(true) {
		if job.ID != "" {
			ids[job.ID] = struct{}{}
		}
	}
	return ids
}

func looksLikeScheduleIntent(query string) bool {
	lower := strings.ToLower(query)
	if looksLikeReminderRequest(lower) {
		return true
	}
	keywords := []string{
		"定时", "稍后", "以后", "之后", "明天", "后天", "每天", "每周", "到点", "等一会",
		"schedule", "scheduled", "later", "tomorrow", "timer", "alarm",
	}
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

func looksLikeScheduleSuccess(content string) bool {
	lower := strings.ToLower(content)
	phrases := []string{
		"已设置", "已经设置", "设置好了", "已创建", "创建成功", "后台任务已", "到时候会提醒",
		"has been scheduled", "is scheduled", "i've scheduled", "schedule created", "created the scheduled task", "reminder is set",
		"created job",
	}
	for _, phrase := range phrases {
		if strings.Contains(lower, strings.ToLower(phrase)) {
			return true
		}
	}
	return false
}

func presentsScheduleJobID(content string) bool {
	lower := strings.ToLower(content)
	return strings.Contains(lower, "任务 id") || strings.Contains(lower, "job id")
}

func (d scheduleGroundingDecision) UserMessage() string {
	if len(d.UngroundedIDs) > 0 {
		return fmt.Sprintf("我没有找到真实创建成功的定时任务记录，所以不会把 `%s` 当成有效任务 ID。\n\n请重新发送提醒请求，我会通过 `cron` 工具创建并返回后端确认的 ID。", strings.Join(d.UngroundedIDs, "`, `"))
	}
	return "我没有检测到真实的 `cron` 工具执行结果，所以不会把这次回复当成已设置成功。\n\n请重新发送提醒请求，我会通过 cron 工具创建并返回确认 ID。"
}

func (d scheduleGroundingDecision) TraceContent() string {
	sections := []string{
		"**Reason**",
		"",
		"Final answer claimed a scheduled task was created, but the claim was not grounded in a successful `cron` result.",
		"",
	}
	if len(d.ClaimedIDs) > 0 {
		sections = append(sections, "**Claimed job IDs**", "", "`"+strings.Join(d.ClaimedIDs, "`, `")+"`", "")
	}
	if len(d.UngroundedIDs) > 0 {
		sections = append(sections, "**Ungrounded IDs**", "", "`"+strings.Join(d.UngroundedIDs, "`, `")+"`", "")
	}
	if len(d.SuccessfulTools) > 0 {
		sections = append(sections, "**Grounded tool IDs**", "", "`"+strings.Join(d.SuccessfulTools, "`, `")+"`", "")
	} else {
		sections = append(sections, "**Grounded tool IDs**", "", "None", "")
	}
	return strings.TrimSpace(strings.Join(sections, "\n"))
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	sort.Strings(unique)
	return unique
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return set
}
