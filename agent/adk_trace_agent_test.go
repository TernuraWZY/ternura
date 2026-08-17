package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"ternura/tool"
)

func TestADKTraceAgentPassesTurnLoopSafePointCancellation(t *testing.T) {
	firstModelStarted := make(chan struct{})
	releaseFirstModel := make(chan struct{})
	fakeTool := &fakeAgentTool{name: "preempt_test_tool", result: "should not run"}
	chatModel := &scriptedChatModel{}
	chatModel.generate = func(call int, _ []*schema.Message, _ ...einomodel.Option) (*schema.Message, error) {
		switch call {
		case 1:
			close(firstModelStarted)
			<-releaseFirstModel
			return schema.AssistantMessage("", []schema.ToolCall{{
				ID: "preempt-call",
				Function: schema.FunctionCall{
					Name:      string(fakeTool.name),
					Arguments: `{}`,
				},
			}}), nil
		case 2:
			return schema.AssistantMessage("latest answer", nil), nil
		default:
			return nil, errors.New("unexpected model call")
		}
	}

	type completion struct {
		input  string
		result AgentRunResult
		err    error
	}
	completed := make(chan completion, 2)
	loop := adk.NewTurnLoop(adk.TurnLoopConfig[string, *schema.Message]{
		GenInput: func(_ context.Context, _ *adk.TurnLoop[string, *schema.Message], items []string) (*adk.GenInputResult[string, *schema.Message], error) {
			return &adk.GenInputResult[string, *schema.Message]{
				Input:     &adk.AgentInput{Messages: []*schema.Message{schema.UserMessage(items[0])}},
				Consumed:  items[:1],
				Remaining: items[1:],
			}, nil
		},
		PrepareAgent: func(_ context.Context, _ *adk.TurnLoop[string, *schema.Message], consumed []string) (adk.Agent, error) {
			input := consumed[0]
			turnAgent := NewAgent(testModelConfig(), "system", []tool.Tool{fakeTool})
			turnAgent.chatModel = chatModel
			return NewADKTraceAgent(turnAgent, "", func(result AgentRunResult, err error) {
				completed <- completion{input: input, result: result, err: err}
			}), nil
		},
		OnAgentEvents: func(_ context.Context, _ *adk.TurnContext[string, *schema.Message], events *adk.AsyncIterator[*adk.AgentEvent]) error {
			for {
				if _, ok := events.Next(); !ok {
					return nil
				}
			}
		},
	})
	loop.Run(context.Background())
	loop.Push("first")

	select {
	case <-firstModelStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first model call did not start")
	}
	loop.Push("latest", adk.WithPreemptTimeout[string, *schema.Message](adk.AnySafePoint, time.Second))
	close(releaseFirstModel)

	outcomes := make(map[string]completion)
	for len(outcomes) < 2 {
		select {
		case outcome := <-completed:
			outcomes[outcome.input] = outcome
		case <-time.After(3 * time.Second):
			t.Fatalf("timed out waiting for turn outcomes: %+v", outcomes)
		}
	}
	loop.Stop()
	exit := loop.Wait()
	if exit.ExitReason != nil {
		t.Fatalf("turn loop exit: %v", exit.ExitReason)
	}

	var cancelErr *adk.CancelError
	if !errors.As(outcomes["first"].err, &cancelErr) {
		t.Fatalf("first turn error = %v, want Eino CancelError", outcomes["first"].err)
	}
	if outcomes["latest"].err != nil || outcomes["latest"].result.Content != "latest answer" {
		t.Fatalf("latest turn = %+v", outcomes["latest"])
	}
	if len(fakeTool.calls) != 0 {
		t.Fatalf("tool executed after safe-point preemption: %+v", fakeTool.calls)
	}
}
