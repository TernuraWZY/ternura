package agent

import (
	"context"
	"errors"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

type adkTraceAgent struct {
	agent        *Agent
	checkpointID string
	complete     func(AgentRunResult, error)
}

// NewADKTraceAgent exposes Ternura's traced Agent run as an Eino ADK Agent.
// AgentRunOptions supplied by ADK, including TurnLoop cancellation, are passed
// through to the underlying ChatModelAgent rather than being reimplemented.
func NewADKTraceAgent(agentInstance *Agent, checkpointID string, complete func(AgentRunResult, error)) adk.Agent {
	return &adkTraceAgent{
		agent:        agentInstance,
		checkpointID: checkpointID,
		complete:     complete,
	}
}

func (a *adkTraceAgent) Name(context.Context) string {
	return "ternura-turn"
}

func (a *adkTraceAgent) Description(context.Context) string {
	return "Ternura traced ReAct turn"
}

func (a *adkTraceAgent) Run(ctx context.Context, input *adk.AgentInput, options ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	iterator, generator := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	go func() {
		defer generator.Close()
		if a == nil || a.agent == nil {
			err := errors.New("Ternura agent is not initialized")
			a.finish(AgentRunResult{}, err)
			generator.Send(&adk.AgentEvent{AgentName: "ternura-turn", Err: err})
			return
		}
		message := lastADKUserMessage(input)
		if message == nil {
			err := errors.New("Eino TurnLoop input does not contain a user message")
			a.finish(AgentRunResult{}, err)
			generator.Send(&adk.AgentEvent{AgentName: "ternura-turn", Err: err})
			return
		}

		result, err := a.agent.runMessageWithTraceOptions(ctx, message, a.checkpointID, nil, options)
		a.finish(result, err)
		if err != nil {
			generator.Send(&adk.AgentEvent{AgentName: "ternura-turn", Err: err})
			return
		}
		generator.Send(&adk.AgentEvent{
			AgentName: "ternura-turn",
			Output: &adk.AgentOutput{MessageOutput: &adk.MessageVariant{
				Message: schema.AssistantMessage(result.Content, nil),
			}},
		})
	}()
	return iterator
}

func (a *adkTraceAgent) finish(result AgentRunResult, err error) {
	if a.complete != nil {
		a.complete(result, err)
	}
}

func lastADKUserMessage(input *adk.AgentInput) *schema.Message {
	if input == nil {
		return nil
	}
	for idx := len(input.Messages) - 1; idx >= 0; idx-- {
		message := input.Messages[idx]
		if message != nil && message.Role == schema.User {
			return cloneMessages([]*schema.Message{message})[0]
		}
	}
	return nil
}
