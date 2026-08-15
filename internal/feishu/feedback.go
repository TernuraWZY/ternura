package feishu

import (
	"context"
	"log"
	"strings"
	"time"
)

func (s *Service) processingReactionEnabled() bool {
	return s.cfg.ProcessingReaction &&
		s.cfg.ProcessingDelay >= 0 &&
		strings.TrimSpace(s.cfg.ProcessingReactionType) != ""
}

func (s *Service) processingFeedbackTimer() *time.Timer {
	if s.cfg.ProcessingDelay < 0 || (!s.processingReactionEnabled() && !s.cfg.ProgressCard) {
		return nil
	}
	return time.NewTimer(s.cfg.ProcessingDelay)
}

func mergeProgressUpdate(current ProgressUpdate, next ProgressUpdate) ProgressUpdate {
	if strings.TrimSpace(next.RunID) == "" {
		next.RunID = current.RunID
	}
	if next.Stage == "" {
		next.Stage = current.Stage
	}
	if strings.TrimSpace(next.Detail) == "" {
		next.Detail = current.Detail
	}
	if next.ToolCalls == 0 {
		next.ToolCalls = current.ToolCalls
	}
	return next
}

func (s *Service) startProcessingFeedback(inbound InboundMessage, progress ProgressUpdate) (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ReplyTimeout)
	defer cancel()

	reactionID := ""
	if s.processingReactionEnabled() {
		var err error
		reactionID, err = s.addReaction(ctx, inbound.MessageID, s.cfg.ProcessingReactionType)
		if err != nil {
			log.Printf("feishu add processing reaction failed for %s: %v", inbound.MessageID, err)
		}
	}
	if !s.cfg.ProgressCard {
		return reactionID, ""
	}
	messageID, err := s.send(ctx, OutboundMessage{
		ReceiveIDType: inbound.ReceiveIDType,
		ReceiveID:     inbound.ReceiveID,
		MessageID:     inbound.MessageID,
		ThreadID:      inbound.ThreadID,
		Card:          processingCard(progress),
		Reply:         s.shouldReplyToInbound(inbound),
	})
	if err != nil {
		log.Printf("feishu send progress card failed for %s: %v", inbound.MessageID, err)
		return reactionID, ""
	}
	return reactionID, messageID
}

func (s *Service) patchProgressCard(messageID string, progress ProgressUpdate) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ReplyTimeout)
	defer cancel()
	if err := s.PatchCard(ctx, messageID, processingCard(progress)); err != nil {
		log.Printf("feishu update progress card failed for %s: %v", messageID, err)
	}
}

func (s *Service) finishProcessingFeedback(inbound InboundMessage, reactionID string, progressMessageID string, result handlerResult) {
	if result.err != nil {
		log.Printf("feishu agent turn failed for %s: %v", inbound.MessageID, result.err)
		result.reply = Reply{Content: s.failureReplyContent(result.err)}
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ReplyTimeout)
	defer cancel()
	if reactionID != "" {
		if err := s.DeleteReaction(ctx, inbound.MessageID, reactionID); err != nil {
			log.Printf("feishu remove processing reaction failed for %s: %v", inbound.MessageID, err)
		}
	}
	if result.reply.Empty() {
		return
	}
	if progressMessageID != "" {
		card := result.reply.Card
		if card == nil {
			card = basicReplyCard(result.reply.Content)
		}
		if err := s.PatchCard(ctx, progressMessageID, card); err == nil {
			return
		} else {
			log.Printf("feishu replace progress card failed for %s: %v", progressMessageID, err)
		}
	}
	if err := s.Send(ctx, OutboundMessage{
		ReceiveIDType: inbound.ReceiveIDType,
		ReceiveID:     inbound.ReceiveID,
		MessageID:     inbound.MessageID,
		ThreadID:      inbound.ThreadID,
		Content:       result.reply.Content,
		Card:          result.reply.Card,
		Reply:         s.shouldReplyToInbound(inbound),
	}); err != nil {
		log.Printf("feishu send reply failed for %s: %v", inbound.MessageID, err)
	}
}
