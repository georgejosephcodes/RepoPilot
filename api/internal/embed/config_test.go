package embed

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestConfigOverrides(t *testing.T) {
	cfg, err := ConfigFromEnv(env(map[string]string{
		"EMBED_BASE_URL": "http://localhost:11434/v1", "EMBED_API_KEY": "abc", "EMBED_MODEL": "local",
		"EMBED_DIM": "768", "EMBED_SEND_DIMENSIONS": "true", "EMBED_INPUT_TYPES": "0", "EMBED_MAX_CHARS": "100",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "http://localhost:11434/v1" || cfg.APIKey != "abc" || cfg.Model != "local" || cfg.Dimension != 768 ||
		!cfg.SendDimensions || cfg.InputTypes || cfg.MaxChars != 100 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestConfigErrors(t *testing.T) {
	for _, tc := range []struct{ name, value, want string }{
		{"EMBED_DIM", "big", "must be an integer"},
		{"EMBED_DIM", "0", "at least 1"},
		{"EMBED_MAX_CHARS", "-5", "at least 1"},
		{"EMBED_SEND_DIMENSIONS", "maybe", "true or false"},
		{"EMBED_INPUT_TYPES", "2", "true or false"},
	} {
		_, err := ConfigFromEnv(env(map[string]string{tc.name: tc.value}))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s=%s: err = %v, want %q", tc.name, tc.value, err, tc.want)
		}
	}
}

func TestBoolValuesAreCaseInsensitive(t *testing.T) {
	for _, v := range []string{"TRUE", "Yes", "ON", "1"} {
		cfg, err := ConfigFromEnv(env(map[string]string{"EMBED_SEND_DIMENSIONS": v}))
		if err != nil || !cfg.SendDimensions {
			t.Errorf("%q: err=%v cfg=%v", v, err, cfg.SendDimensions)
		}
	}
}
