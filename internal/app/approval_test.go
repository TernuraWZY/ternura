package app

import "testing"

func TestParseApprovalCommand(t *testing.T) {
	command, ok := parseApprovalCommand("approve run-20260815T000000-0001")
	if !ok || !command.Approved || command.CheckpointID != "run-20260815T000000-0001" {
		t.Fatalf("approve command = %+v ok=%v", command, ok)
	}

	command, ok = parseApprovalCommand("拒绝 run-20260815T000000-0001 不允许删除")
	if !ok || command.Approved || command.Reason != "不允许删除" {
		t.Fatalf("reject command = %+v ok=%v", command, ok)
	}
}
