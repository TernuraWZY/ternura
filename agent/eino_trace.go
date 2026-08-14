package agent

import (
	"context"
	"errors"
	"io"

	"github.com/cloudwego/eino/schema"
)

func (r *einoAgentRun) recordEinoMessageStream(ctx context.Context, stream *schema.StreamReader[*schema.Message]) (*schema.Message, error) {
	if stream == nil {
		return nil, nil
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
			return nil, err
		}
		if chunk == nil {
			continue
		}
		chunks = append(chunks, chunk)
		if chunk.Role != schema.Tool && chunk.Content != "" {
			r.appendRawContent(chunk.Content)
			if contentRouter != nil {
				if err := contentRouter.Write(chunk.Content); err != nil {
					return nil, err
				}
			}
		}
	}
	if contentRouter != nil {
		if err := contentRouter.Flush(); err != nil {
			return nil, err
		}
	}
	if len(chunks) == 0 {
		return nil, nil
	}

	message, err := schema.ConcatMessages(chunks)
	if err != nil {
		return nil, err
	}
	if err := r.recordEinoMessage(ctx, message); err != nil {
		return nil, err
	}
	return message, nil
}
