package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"ternura/tool"
)

type ToolApprovalRequest struct {
	CheckpointID string         `json:"checkpoint_id"`
	InterruptID  string         `json:"interrupt_id"`
	ToolCallID   string         `json:"tool_call_id,omitempty"`
	Tool         tool.AgentTool `json:"tool"`
	Arguments    string         `json:"arguments"`
	Reason       string         `json:"reason"`
}

func (r *ToolApprovalRequest) String() string {
	if r == nil {
		return "tool approval required"
	}
	return fmt.Sprintf("tool %q requires approval: %s", r.Tool, r.Reason)
}

type ToolApprovalDecision struct {
	Approved bool   `json:"approved"`
	Reason   string `json:"reason,omitempty"`
}

type ToolApprovalPolicy struct {
	Mode          string
	WorkspaceRoot string
}

const (
	ToolApprovalNone        = "none"
	ToolApprovalDangerous   = "dangerous"
	ToolApprovalSideEffects = "side_effects"
	ToolApprovalAll         = "all"
)

func init() {
	schema.Register[*ToolApprovalRequest]()
	schema.Register[*ToolApprovalDecision]()
}

func ToolApprovalPolicyFromEnv() ToolApprovalPolicy {
	root, err := os.Getwd()
	if err != nil {
		root = "."
	}
	if configured := strings.TrimSpace(os.Getenv("TERNURA_WORKSPACE_ROOT")); configured != "" {
		root = configured
	}
	root, _ = filepath.Abs(root)
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("TERNURA_TOOL_APPROVAL_MODE")))
	switch mode {
	case ToolApprovalNone, ToolApprovalDangerous, ToolApprovalSideEffects, ToolApprovalAll:
	default:
		mode = ToolApprovalDangerous
	}
	return ToolApprovalPolicy{Mode: mode, WorkspaceRoot: root}
}

func (p ToolApprovalPolicy) RequiresApproval(name tool.AgentTool, arguments string) (bool, string) {
	switch p.Mode {
	case ToolApprovalNone:
		return false, ""
	case ToolApprovalAll:
		return true, "all tool calls require approval by policy"
	case ToolApprovalSideEffects:
		if isSideEffectTool(name) {
			return true, "this tool can change external or persisted state"
		}
		return false, ""
	default:
		return p.requiresDangerousApproval(name, arguments)
	}
}

func (p ToolApprovalPolicy) requiresDangerousApproval(name tool.AgentTool, arguments string) (bool, string) {
	switch name {
	case tool.AgentToolBash:
		var input struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(arguments), &input) != nil {
			return true, "the shell command could not be inspected safely"
		}
		if looksDestructiveShellCommand(input.Command) {
			return true, "the shell command may delete data, change privileges, or alter system state"
		}
	case tool.AgentToolWrite, tool.AgentToolEdit:
		var input struct {
			Path string `json:"path"`
		}
		if json.Unmarshal([]byte(arguments), &input) != nil {
			return true, "the file path could not be inspected safely"
		}
		if !pathWithinRoot(p.WorkspaceRoot, input.Path) {
			return true, "the file change targets a path outside the configured workspace"
		}
	}
	return false, ""
}

func isSideEffectTool(name tool.AgentTool) bool {
	switch name {
	case tool.AgentToolWrite,
		tool.AgentToolEdit,
		tool.AgentToolBash,
		tool.AgentToolRemember,
		tool.AgentToolForgetMemory,
		tool.AgentToolCron:
		return true
	default:
		return false
	}
}

func looksDestructiveShellCommand(command string) bool {
	lower := strings.ToLower(strings.TrimSpace(command))
	for _, marker := range []string{
		"rm -rf", "rm -fr", "sudo ", "shutdown", "reboot", "killall ",
		"git reset --hard", "git clean -f", "mkfs", "diskutil erase",
		"chmod -r", "chown -r", "curl | sh", "wget | sh", "> /dev/",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func pathWithinRoot(root string, path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	absolute := path
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(root, absolute)
	}
	absolute, err := filepath.Abs(absolute)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, absolute)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func (r *einoAgentRun) gateToolCall(ctx context.Context, input *compose.ToolInput) (string, *compose.ToolOutput, bool, error) {
	if r == nil || r.agent == nil || input == nil {
		return "", nil, false, nil
	}
	name := tool.AgentTool(input.Name)
	wasInterrupted, _, storedArguments := einotool.GetInterruptState[string](ctx)
	if !wasInterrupted {
		required, reason := r.agent.toolApprovalRequirement(name, input.Arguments)
		if !required {
			return input.Arguments, nil, false, nil
		}
		request := &ToolApprovalRequest{
			ToolCallID: input.CallID,
			Tool:       name,
			Arguments:  input.Arguments,
			Reason:     reason,
		}
		return "", nil, true, einotool.StatefulInterrupt(ctx, request, input.Arguments)
	}

	isResumeTarget, hasData, decision := einotool.GetResumeContext[*ToolApprovalDecision](ctx)
	if !isResumeTarget {
		request := &ToolApprovalRequest{
			ToolCallID: input.CallID,
			Tool:       name,
			Arguments:  storedArguments,
			Reason:     "this tool call is still waiting for approval",
		}
		return "", nil, true, einotool.StatefulInterrupt(ctx, request, storedArguments)
	}
	if !hasData || decision == nil {
		return "", nil, true, fmt.Errorf("tool %q resumed without an approval decision", name)
	}
	if !decision.Approved {
		reason := strings.TrimSpace(decision.Reason)
		if reason == "" {
			reason = "rejected by user"
		}
		return "", &compose.ToolOutput{Result: fmt.Sprintf("Tool call %s was rejected: %s", name, reason)}, true, nil
	}
	return storedArguments, nil, false, nil
}

func (a *Agent) toolApprovalRequirement(name tool.AgentTool, arguments string) (bool, string) {
	if a == nil {
		return false, ""
	}
	required, reason := a.approvalPolicy.RequiresApproval(name, arguments)
	if required || a.approvalPolicy.Mode == ToolApprovalNone {
		return required, reason
	}
	if candidate, ok := a.tools[name]; ok {
		if aware, ok := candidate.(tool.ApprovalAwareTool); ok {
			return aware.ApprovalRequirement()
		}
	}
	return false, ""
}
