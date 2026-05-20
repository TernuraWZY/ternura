package main

import "testing"

func TestParseScheduleShortcutChineseReminder(t *testing.T) {
	shortcut, ok := parseScheduleShortcut("2分钟后吃饭提醒")
	if !ok {
		t.Fatalf("expected shortcut")
	}
	if shortcut.DelaySeconds != 120 {
		t.Fatalf("delay = %d, want 120", shortcut.DelaySeconds)
	}
	if shortcut.Title != "吃饭提醒" {
		t.Fatalf("title = %q", shortcut.Title)
	}
	if shortcut.Prompt != "提醒用户：吃饭" {
		t.Fatalf("prompt = %q", shortcut.Prompt)
	}
}

func TestParseScheduleShortcutShowerReminder(t *testing.T) {
	shortcut, ok := parseScheduleShortcut("设置2分钟提醒我得洗澡")
	if !ok {
		t.Fatalf("expected shortcut")
	}
	if shortcut.DelaySeconds != 120 {
		t.Fatalf("delay = %d, want 120", shortcut.DelaySeconds)
	}
	if shortcut.Title != "洗澡提醒" {
		t.Fatalf("title = %q", shortcut.Title)
	}
}

func TestParseScheduleShortcutIgnoresOrdinaryQuestion(t *testing.T) {
	if _, ok := parseScheduleShortcut("2分钟能解释一下定时任务吗"); ok {
		t.Fatalf("ordinary question should not become a schedule")
	}
}
