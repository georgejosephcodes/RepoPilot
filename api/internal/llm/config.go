package llm

import (
	"fmt"
	"strconv"
	"time"
)

// Config for the answer model. Defaults are the values verified by worker/scripts/probe_gemini_generate.py.
type Config struct {
	BaseURL         string
	APIKey          string
	Model           string
	Temperature     float64
	MaxOutputTokens int
	ThinkingLevel   string // "minimal", "low", ...; empty omits the field
	Timeout         time.Duration
}

func DefaultConfig() Config {
	return Config{
		BaseURL:         "https://generativelanguage.googleapis.com/v1beta",
		Model:           "gemini-3.5-flash-lite",
		Temperature:     0.1,
		MaxOutputTokens: 1024,
		ThinkingLevel:   "minimal",
		Timeout:         45 * time.Second,
	}
}

// ConfigFromEnv reads LLM_BASE_URL, LLM_API_KEY (falling back to GEMINI_API_KEY), LLM_MODEL, LLM_TEMPERATURE,
// LLM_MAX_OUTPUT_TOKENS, LLM_THINKING_LEVEL ("none" omits the field) and LLM_TIMEOUT_SEC.
// Empty values keep the default. The key is not required here.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	cfg := DefaultConfig()
	if v := getenv("LLM_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	cfg.APIKey = getenv("LLM_API_KEY")
	if cfg.APIKey == "" {
		cfg.APIKey = getenv("GEMINI_API_KEY")
	}
	if v := getenv("LLM_MODEL"); v != "" {
		cfg.Model = v
	}
	if v := getenv("LLM_TEMPERATURE"); v != "" {
		t, err := strconv.ParseFloat(v, 64)
		if err != nil || t < 0 || t > 2 {
			return Config{}, fmt.Errorf("LLM_TEMPERATURE must be a number between 0 and 2")
		}
		cfg.Temperature = t
	}
	if v := getenv("LLM_MAX_OUTPUT_TOKENS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("LLM_MAX_OUTPUT_TOKENS must be an integer")
		}
		if n < 1 || n > 65536 {
			return Config{}, fmt.Errorf("LLM_MAX_OUTPUT_TOKENS must be between 1 and 65536")
		}
		cfg.MaxOutputTokens = n
	}
	if v := getenv("LLM_THINKING_LEVEL"); v != "" {
		if v == "none" {
			cfg.ThinkingLevel = ""
		} else {
			cfg.ThinkingLevel = v
		}
	}
	if v := getenv("LLM_TIMEOUT_SEC"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("LLM_TIMEOUT_SEC must be an integer")
		}
		if n < 1 {
			return Config{}, fmt.Errorf("LLM_TIMEOUT_SEC must be at least 1")
		}
		cfg.Timeout = time.Duration(n) * time.Second
	}
	return cfg, nil
}
