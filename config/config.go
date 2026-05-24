package config

import (
	"encoding/json"
	"os"
	"strings"
)

type AppConfig struct {
	LLMProviders struct {
		FrontModel ModelConfig `json:"front_model"`
		BackModel  ModelConfig `json:"back_model"`
	} `json:"llm_providers"`
}

type ModelConfig struct {
	BaseURL string `json:"base_url"`
	ApiKey  string `json:"api_key"`
	Model   string `json:"model"`

	ContextWindow int `json:"context_window"`
}

func NewModelConfig() ModelConfig {
	if strings.EqualFold(getEnvDefault("LLM_PROVIDER", "openai"), "minimax") {
		return NewMiniMaxModelConfig()
	}
	return NewOpenAIModelConfig()
}

func NewOpenAIModelConfig() ModelConfig {
	return ModelConfig{
		BaseURL:       getEnvDefault("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		ApiKey:        getEnvDefault("OPENAI_API_KEY", ""),
		Model:         getEnvDefault("OPENAI_MODEL", "gpt-5.2"),
		ContextWindow: 200000,
	}
}

func NewMiniMaxModelConfig() ModelConfig {
	return ModelConfig{
		BaseURL:       getEnvDefault("MINIMAX_BASE_URL", "https://api.minimaxi.com/v1"),
		ApiKey:        getEnvDefault("MINIMAX_API_KEY", ""),
		Model:         getEnvDefault("MINIMAX_MODEL", "MiniMax-M2.7"),
		ContextWindow: 204800,
	}
}

func getEnvDefault(key, defaultValue string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return defaultValue
}

func LoadAppConfig(path string) (AppConfig, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return AppConfig{}, err
	}
	var config AppConfig
	err = json.Unmarshal(content, &config)
	if err != nil {
		return AppConfig{}, err
	}
	return config, nil
}
