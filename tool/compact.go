package tool

import "context"

type CompactTool struct {
	*agentTool
}

type CompactToolParam struct {
	Focus string `json:"focus,omitempty" jsonschema_description:"optional focus for the compaction summary"`
}

func NewCompactTool() *CompactTool {
	t := &CompactTool{}
	t.agentTool = newAgentTool(AgentToolCompact, "summarize earlier conversation to free context space", t.run)
	return t
}

func (t *CompactTool) run(ctx context.Context, p CompactToolParam) (string, error) {
	_ = ctx
	if p.Focus != "" {
		return "Compaction requested. Conversation history will be summarized before the next model call. Focus: " + p.Focus, nil
	}
	return "Compaction requested. Conversation history will be summarized before the next model call.", nil
}
