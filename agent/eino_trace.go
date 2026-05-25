package agent

import (
	"context"
	"errors"
	"io"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	einoagent "github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/schema"
)

func (r *einoAgentRun) traceCallbackOption() einoagent.AgentOption {
	return einoagent.WithComposeOptions(compose.WithCallbacks(&traceCallbackHandler{run: r}))
}

type traceCallbackHandler struct {
	run *einoAgentRun
}

func (h *traceCallbackHandler) Needed(_ context.Context, info *callbacks.RunInfo, timing callbacks.CallbackTiming) bool {
	if h == nil || h.run == nil || info == nil {
		return false
	}
	switch info.Component {
	case components.ComponentOfChatModel:
		return timing == callbacks.TimingOnEnd || timing == callbacks.TimingOnEndWithStreamOutput
	case compose.ComponentOfToolsNode:
		return timing == callbacks.TimingOnEnd || timing == callbacks.TimingOnEndWithStreamOutput
	default:
		return false
	}
}

func (h *traceCallbackHandler) OnStart(ctx context.Context, _ *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context {
	return ctx
}

func (h *traceCallbackHandler) OnEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	if h == nil || h.run == nil || info == nil {
		return ctx
	}
	var err error
	switch info.Component {
	case components.ComponentOfChatModel:
		err = h.recordModelOutput(ctx, output)
	case compose.ComponentOfToolsNode:
		err = h.recordToolsNodeOutput(ctx, output)
	}
	if err != nil {
		h.run.setCallbackError(err)
	}
	return ctx
}

func (h *traceCallbackHandler) OnError(ctx context.Context, _ *callbacks.RunInfo, _ error) context.Context {
	return ctx
}

func (h *traceCallbackHandler) OnStartWithStreamInput(ctx context.Context, _ *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	if input != nil {
		input.Close()
	}
	return ctx
}

func (h *traceCallbackHandler) OnEndWithStreamOutput(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	if h == nil || h.run == nil || info == nil || output == nil {
		if output != nil {
			output.Close()
		}
		return ctx
	}
	switch info.Component {
	case components.ComponentOfChatModel:
		h.run.observeTraceAsync(func() error {
			stream := schema.StreamReaderWithConvert(output, func(item callbacks.CallbackOutput) (*schema.Message, error) {
				modelOutput := einomodel.ConvCallbackOutput(item)
				if modelOutput == nil {
					return nil, nil
				}
				return modelOutput.Message, nil
			})
			return h.run.recordEinoMessageStream(ctx, stream)
		})
	case compose.ComponentOfToolsNode:
		h.run.observeTraceAsync(func() error {
			defer output.Close()
			for {
				item, err := output.Recv()
				if errors.Is(err, io.EOF) {
					return nil
				}
				if err != nil {
					return err
				}
				if err := h.recordToolsNodeOutput(ctx, item); err != nil {
					return err
				}
			}
		})
	default:
		output.Close()
	}
	return ctx
}

func (h *traceCallbackHandler) recordModelOutput(ctx context.Context, output callbacks.CallbackOutput) error {
	modelOutput := einomodel.ConvCallbackOutput(output)
	if modelOutput == nil {
		return nil
	}
	return h.run.recordEinoMessage(ctx, modelOutput.Message)
}

func (h *traceCallbackHandler) recordToolsNodeOutput(ctx context.Context, output callbacks.CallbackOutput) error {
	messages, ok := output.([]*schema.Message)
	if !ok {
		return nil
	}
	for _, message := range messages {
		if err := h.run.recordEinoMessage(ctx, message); err != nil {
			return err
		}
	}
	return nil
}

func (r *einoAgentRun) observeTraceAsync(fn func() error) {
	r.callbackWG.Add(1)
	go func() {
		defer r.callbackWG.Done()
		if err := fn(); err != nil {
			r.setCallbackError(err)
		}
	}()
}

func (r *einoAgentRun) setCallbackError(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.callbackErr == nil {
		r.callbackErr = err
	}
}

func (r *einoAgentRun) waitTraceCallbacks() error {
	r.callbackWG.Wait()
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.callbackErr
}

func (r *einoAgentRun) recordEinoMessageStream(ctx context.Context, stream *schema.StreamReader[*schema.Message]) error {
	if stream == nil {
		return nil
	}
	defer stream.Close()

	var contentRouter *streamingContentRouter
	if r.emit != nil {
		contentRouter = r.newContentRouter()
	}

	chunks := make([]*schema.Message, 0)
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if chunk == nil {
			continue
		}
		chunks = append(chunks, chunk)
		if chunk.Role != schema.Tool && chunk.Content != "" {
			r.appendRawContent(chunk.Content)
			if contentRouter != nil {
				if err := contentRouter.Write(chunk.Content); err != nil {
					return err
				}
			}
		}
	}
	if contentRouter != nil {
		if err := contentRouter.Flush(); err != nil {
			return err
		}
	}
	if len(chunks) == 0 {
		return nil
	}

	message, err := schema.ConcatMessages(chunks)
	if err != nil {
		return err
	}
	return r.recordEinoMessage(ctx, message)
}
