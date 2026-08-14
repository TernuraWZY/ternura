package agent

import (
	"context"
	"errors"
	"log"
	"os"
	"strconv"
	"strings"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"ternura/config"
)

func newConfiguredChatModel(modelConf config.ModelConfig) einomodel.ToolCallingChatModel {
	chatModel, err := einoopenai.NewChatModel(context.Background(), &einoopenai.ChatModelConfig{
		BaseURL: modelConf.BaseURL,
		APIKey:  modelConf.ApiKey,
		Model:   modelConf.Model,
		ExtraFields: map[string]any{
			"parallel_tool_calls": envBool("TERNURA_PARALLEL_TOOL_CALLS", true),
		},
	})
	if err != nil {
		log.Printf("create Eino OpenAI chat model %s: %v", modelConf.Model, err)
		return nil
	}
	return chatModel
}

func newFallbackChatModelFromEnv(primary config.ModelConfig) einomodel.ToolCallingChatModel {
	modelName := strings.TrimSpace(os.Getenv("TERNURA_FALLBACK_MODEL"))
	if modelName == "" {
		return nil
	}
	fallback := config.ModelConfig{
		BaseURL: strings.TrimSpace(os.Getenv("TERNURA_FALLBACK_BASE_URL")),
		ApiKey:  strings.TrimSpace(os.Getenv("TERNURA_FALLBACK_API_KEY")),
		Model:   modelName,
	}
	if fallback.BaseURL == "" {
		fallback.BaseURL = primary.BaseURL
	}
	if fallback.ApiKey == "" {
		fallback.ApiKey = primary.ApiKey
	}
	return newConfiguredChatModel(fallback)
}

func defaultModelFailoverConfig(fallbacks []einomodel.ToolCallingChatModel) *adk.ModelFailoverConfig[*schema.Message] {
	available := make([]einomodel.ToolCallingChatModel, 0, len(fallbacks))
	for _, fallback := range fallbacks {
		if fallback != nil {
			available = append(available, fallback)
		}
	}
	if len(available) == 0 {
		return nil
	}
	return &adk.ModelFailoverConfig[*schema.Message]{
		MaxRetries: uint(len(available)),
		ShouldFailover: func(ctx context.Context, _ *schema.Message, err error) bool {
			return shouldFailoverModel(ctx, err)
		},
		GetFailoverModel: func(_ context.Context, failoverCtx *adk.FailoverContext[*schema.Message]) (einomodel.BaseModel[*schema.Message], []*schema.Message, error) {
			if failoverCtx == nil || failoverCtx.FailoverAttempt == 0 || int(failoverCtx.FailoverAttempt) > len(available) {
				return nil, nil, errors.New("no fallback model available for this attempt")
			}
			return available[failoverCtx.FailoverAttempt-1], nil, nil
		},
	}
}

func shouldFailoverModel(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrRunBudgetExceeded) || isPromptTooLongError(err) {
		return false
	}
	lower := strings.ToLower(err.Error())
	for _, marker := range []string{
		"timeout", "temporarily unavailable", "rate limit", "too many requests",
		"connection reset", "connection refused", "unexpected eof", "service unavailable",
		"status code: 401", "status code: 403", "status code: 429", "status code: 500",
		"status code: 502", "status code: 503", "status code: 504", "insufficient_quota",
		"model_not_found",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
