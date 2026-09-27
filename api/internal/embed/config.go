package embed

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Config mirrors the Python worker's embedding settings: same names, same defaults.
// testdata/embed_contract.json holds the defaults both sides are tested against.
type Config struct {
	BaseURL        string
	APIKey         string
	Model          string
	Dimension      int
	SendDimensions bool   // this model rejects any `dimensions` other than its native size
	InputTypes     bool   // send input_type with every request
	QueryType      string // input_type for questions: search_query (OpenRouter), query (NVIDIA NIM)
	MaxChars       int    // text is cut to this many characters before sending
	Timeout        time.Duration
}

func DefaultConfig() Config {
	return Config{
		BaseURL:    "https://openrouter.ai/api/v1",
		Model:      "nvidia/nemotron-3-embed-1b:free",
		Dimension:  2048,
		InputTypes: true,
		QueryType:  "search_query",
		MaxChars:   8000,
		Timeout:    15 * time.Second,
	}
}

// ConfigFromEnv reads EMBED_BASE_URL, EMBED_API_KEY, EMBED_MODEL, EMBED_DIM, EMBED_SEND_DIMENSIONS,
// EMBED_INPUT_TYPES, EMBED_QUERY_TYPE and EMBED_MAX_CHARS. Empty values keep the default. The key is not required here.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	cfg := DefaultConfig()
	if v := getenv("EMBED_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	cfg.APIKey = getenv("EMBED_API_KEY")
	if v := getenv("EMBED_MODEL"); v != "" {
		cfg.Model = v
	}
	if v := getenv("EMBED_QUERY_TYPE"); v != "" {
		cfg.QueryType = v
	}
	var err error
	if cfg.Dimension, err = intEnv(getenv, "EMBED_DIM", cfg.Dimension, 1); err != nil {
		return Config{}, err
	}
	if cfg.MaxChars, err = intEnv(getenv, "EMBED_MAX_CHARS", cfg.MaxChars, 1); err != nil {
		return Config{}, err
	}
	if cfg.SendDimensions, err = boolEnv(getenv, "EMBED_SEND_DIMENSIONS", cfg.SendDimensions); err != nil {
		return Config{}, err
	}
	if cfg.InputTypes, err = boolEnv(getenv, "EMBED_INPUT_TYPES", cfg.InputTypes); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func intEnv(getenv func(string) string, name string, def, min int) (int, error) {
	raw := getenv(name)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	if n < min {
		return 0, fmt.Errorf("%s must be at least %d", name, min)
	}
	return n, nil
}

func boolEnv(getenv func(string) string, name string, def bool) (bool, error) {
	raw := getenv(name)
	if raw == "" {
		return def, nil
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("%s must be true or false", name)
}
