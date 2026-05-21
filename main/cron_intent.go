package main

import (
	"regexp"
	"strconv"
	"strings"

	"ternura/main/cron"
)

var (
	vagueTimePattern = regexp.MustCompile(`(?i)(等下|等一会|等会儿?|稍后|一会儿?|待会儿?|回头|到时候|later|soon)`)
	concreteTimePattern = regexp.MustCompile(
		`(?i)(\d+\s*(秒钟|秒|seconds?|secs?|分钟|分|min(?:ute)?s?|小时|钟头|hours?|hrs?|天|days?))` +
			`|(\d{1,2}\s*[:点时]\s*\d{0,2})` +
			`|(今晚|今天晚上|明早|明天早上|明晚|明天晚上|明天|后天|大后天|今天下午|今天上午|下周|下个?月|周[一二三四五六日天])` +
			`|(tomorrow|tonight|next\s+(week|month|monday|tuesday|wednesday|thursday|friday|saturday|sunday))`,
	)
	relativeReminderPattern = regexp.MustCompile(
		`(?i)^(?:请|帮我|给我|麻烦)?\s*(?:(\d+)\s*(秒钟|秒|seconds?|secs?|分钟|分|min(?:ute)?s?|小时|钟头|hours?|hrs?|天|days?)\s*(?:后|之后|过后)?\s*)` +
			`(?:(?:再|然后|顺便)\s*)?(?:提醒|叫|让|通知|tell|remind|notify)\s*(?:我|一下|一声)?\s*(?:[:：,，]?\s*)?(.+)$`,
	)
)

func looksLikeReminderRequest(message string) bool {
	lower := strings.ToLower(message)
	keywords := []string{"提醒", "叫我", "告诉我", "闹钟", "remind", "reminder", "notify", "tell me"}
	for _, keyword := range keywords {
		if strings.Contains(lower, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func looksLikeVagueScheduleIntent(query string) bool {
	if !hasReminderVerb(query) {
		return false
	}
	return vagueTimePattern.MatchString(query) && !concreteTimePattern.MatchString(query)
}

func looksLikeConcreteScheduleIntent(query string) bool {
	return hasReminderVerb(query) && concreteTimePattern.MatchString(query)
}

func parseRelativeScheduleShortcut(message string) (cron.AddParams, bool) {
	match := relativeReminderPattern.FindStringSubmatch(strings.TrimSpace(message))
	if len(match) != 4 {
		return cron.AddParams{}, false
	}
	amount, err := strconv.Atoi(match[1])
	if err != nil || amount <= 0 {
		return cron.AddParams{}, false
	}
	delay := amount * timeUnitSeconds(match[2])
	prompt := strings.TrimSpace(match[3])
	if prompt == "" {
		return cron.AddParams{}, false
	}
	return cron.AddParams{
		Name:           defaultCronName(prompt),
		Message:        "提醒用户：" + prompt,
		DelaySeconds:   delay,
		DeleteAfterRun: true,
		Deliver:        true,
	}, true
}

func defaultCronName(prompt string) string {
	runes := []rune(strings.Join(strings.Fields(prompt), " "))
	if len(runes) <= 30 {
		return string(runes)
	}
	return string(runes[:30]) + "..."
}

func timeUnitSeconds(unit string) int {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "秒钟", "秒", "second", "seconds", "sec", "secs":
		return 1
	case "分钟", "分", "minute", "minutes", "min", "mins":
		return 60
	case "小时", "钟头", "hour", "hours", "hr", "hrs":
		return 3600
	case "天", "day", "days":
		return 86400
	default:
		return 60
	}
}
