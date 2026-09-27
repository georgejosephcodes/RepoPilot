package llm

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaultsMatchTheProbedValues(t *testing.T) {
	cfg, err := ConfigFromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "gemini-3.5-flash-lite" || cfg.Temperature != 0.1 || cfg.MaxOutputTokens != 1024 ||
		cfg.ThinkingLevel != "minimal" || cfg.Timeout != 45*time.Second ||
		cfg.BaseURL != "https://generativelanguage.googleapis.com/v1beta" || cfg.APIKey != "" {
		t.Fatalf("defaults: %+v", cfg)
	}
}

func TestKeyFallsBackToGeminiAPIKey(t *testing.T) {
	cfg, _ := ConfigFromEnv(env(map[string]string{"GEMINI_API_KEY": "from-gemini"}))
	if cfg.APIKey != "from-gemini" {
		t.Fatalf("key = %q", cfg.APIKey)
	}
	cfg, _ = ConfigFromEnv(env(map[string]string{"GEMINI_API_KEY": "from-gemini", "LLM_API_KEY": "explicit"}))
	if cfg.APIKey != "explicit" {
		t.Fatalf("LLM_API_KEY must win, got %q", cfg.APIKey)
	}
}

func TestOverrides(t *testing.T) {
	cfg, err := ConfigFromEnv(env(map[string]string{
		"LLM_MODEL": "other", "LLM_TEMPERATURE": "0.7", "LLM_MAX_OUTPUT_TOKENS": "256",
		"LLM_THINKING_LEVEL": "low", "LLM_TIMEOUT_SEC": "10", "LLM_BASE_URL": "http://localhost:9",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "other" || cfg.Temperature != 0.7 || cfg.MaxOutputTokens != 256 || cfg.ThinkingLevel != "low" ||
		cfg.Timeout != 10*time.Second || cfg.BaseURL != "http://localhost:9" {
		t.Fatalf("overrides: %+v", cfg)
	}
	cfg, _ = ConfigFromEnv(env(map[string]string{"LLM_THINKING_LEVEL": "none"}))
	if cfg.ThinkingLevel != "" {
		t.Fatalf("none must omit the field, got %q", cfg.ThinkingLevel)
	}
}

func TestInvalidValues(t *testing.T) {
	for _, tc := range []struct{ name, value, want string }{
		{"LLM_TEMPERATURE", "hot", "between 0 and 2"},
		{"LLM_TEMPERATURE", "3", "between 0 and 2"},
		{"LLM_TEMPERATURE", "-1", "between 0 and 2"},
		{"LLM_MAX_OUTPUT_TOKENS", "abc", "must be an integer"},
		{"LLM_MAX_OUTPUT_TOKENS", "0", "between 1 and 65536"},
		{"LLM_MAX_OUTPUT_TOKENS", "70000", "between 1 and 65536"},
		{"LLM_TIMEOUT_SEC", "x", "must be an integer"},
		{"LLM_TIMEOUT_SEC", "0", "at least 1"},
	} {
		_, err := ConfigFromEnv(env(map[string]string{tc.name: tc.value}))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s=%s: err = %v, want %q", tc.name, tc.value, err, tc.want)
		}
	}
}
