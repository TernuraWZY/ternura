package app

import "testing"

func TestParseRelativeScheduleShortcut(t *testing.T) {
	params, ok := parseRelativeScheduleShortcut("2分钟后提醒我喝水")
	if !ok {
		t.Fatal("expected shortcut parse")
	}
	if params.DelaySeconds != 120 || params.Message != "提醒用户：喝水" {
		t.Fatalf("params = %+v", params)
	}
}
