package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"ternura/config"
)

const activeMemoryKeywordSystemPrompt = `You are Ternura's active-memory query planner.

Given the latest user message and a small recent conversation window, decide the compact search query for memory recall.
Return ONLY a JSON object:
{
  "should_recall": true,
  "query_mode": "latest",
  "keywords": ["keyword or phrase"],
  "search_query": "compact keywords joined for retrieval"
}

Rules:
- Use "latest" when the latest message is a standalone new topic.
- Use "recent" when the latest message refers to earlier context, for example: 现在呢, 继续, 刚刚那个, this, that, it.
- Keywords should be literal nouns, file names, tool names, entities, errors, project terms, user preferences, or durable facts.
- Do not include filler words, greetings, or instructions.
- Use at most 8 keywords.
- Keep search_query short and concrete.
- Set should_recall=false only for pure greetings, thanks, or messages with no useful memory-recall target.`

type activeMemoryKeywordExtractor interface {
	ExtractActiveMemoryKeywords(ctx context.Context, input activeMemoryKeywordInput) (activeMemoryKeywordResult, error)
}

type activeMemoryKeywordInput struct {
	LatestQuery   string
	RecallQuery   string
	RecentTurns   []shortTermTurn
	MaxQueryRunes int
}

type activeMemoryKeywordResult struct {
	ShouldRecall bool     `json:"should_recall"`
	QueryMode    string   `json:"query_mode"`
	Keywords     []string `json:"keywords"`
	SearchQuery  string   `json:"search_query"`
}

type einoActiveMemoryKeywordExtractor struct {
	model einomodel.BaseChatModel
}

func newEinoActiveMemoryKeywordExtractor(modelConf config.ModelConfig) activeMemoryKeywordExtractor {
	model, err := einoopenai.NewChatModel(context.Background(), &einoopenai.ChatModelConfig{
		BaseURL: modelConf.BaseURL,
		APIKey:  modelConf.ApiKey,
		Model:   modelConf.Model,
	})
	if err != nil {
		log.Printf("create active memory keyword model: %v", err)
		return nil
	}
	return &einoActiveMemoryKeywordExtractor{model: model}
}

func (e *einoActiveMemoryKeywordExtractor) ExtractActiveMemoryKeywords(ctx context.Context, input activeMemoryKeywordInput) (activeMemoryKeywordResult, error) {
	if e == nil || e.model == nil {
		return activeMemoryKeywordResult{}, errors.New("active memory keyword model is not initialized")
	}
	message, err := e.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(activeMemoryKeywordSystemPrompt),
		schema.UserMessage(renderActiveMemoryKeywordPrompt(input)),
	})
	if err != nil {
		return activeMemoryKeywordResult{}, err
	}
	if message == nil {
		return activeMemoryKeywordResult{}, errors.New("active memory keyword model returned nil message")
	}
	result, err := parseActiveMemoryKeywordResult(message.Content)
	if err != nil {
		return activeMemoryKeywordResult{}, err
	}
	result.Keywords = normalizeKeywordList(result.Keywords, 8)
	result.SearchQuery = truncateRunes(strings.Join(strings.Fields(result.SearchQuery), " "), input.MaxQueryRunes)
	if result.SearchQuery == "" && len(result.Keywords) > 0 {
		result.SearchQuery = truncateRunes(strings.Join(result.Keywords, " "), input.MaxQueryRunes)
	}
	result.QueryMode = normalizeActiveMemoryQueryMode(result.QueryMode)
	return result, nil
}

func renderActiveMemoryKeywordPrompt(input activeMemoryKeywordInput) string {
	lines := []string{
		"Latest user message:",
		strings.TrimSpace(input.LatestQuery),
	}
	if len(input.RecentTurns) > 0 {
		lines = append(lines, "", "Recent conversation:")
		for _, turn := range input.RecentTurns {
			if turn.User != "" {
				lines = append(lines, "- User: "+turn.User)
			}
			if turn.Assistant != "" {
				lines = append(lines, "- Assistant: "+turn.Assistant)
			}
		}
	}
	if strings.TrimSpace(input.RecallQuery) != "" {
		lines = append(lines, "", "Full recall query candidate:", input.RecallQuery)
	}
	return strings.Join(lines, "\n")
}

func parseActiveMemoryKeywordResult(content string) (activeMemoryKeywordResult, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return activeMemoryKeywordResult{}, errors.New("empty active memory keyword response")
	}
	content = extractJSONObject(content)
	var result activeMemoryKeywordResult
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return activeMemoryKeywordResult{}, fmt.Errorf("parse active memory keyword response: %w", err)
	}
	return result, nil
}

func extractJSONObject(content string) string {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end >= start {
		return content[start : end+1]
	}
	return content
}

func normalizeActiveMemoryQueryMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "latest":
		return "latest"
	case "recent":
		return "recent"
	default:
		return "latest"
	}
}
