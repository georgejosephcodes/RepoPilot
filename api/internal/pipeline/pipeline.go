// Package pipeline builds the retriever the API uses from settings (PHASE2.md step 7.3). The defaults are the
// values chosen on the dev split in steps 5 and 6; RETRIEVAL_MODE=vector gives Phase 1 retrieval back.
package pipeline

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"repopilot/api/internal/llm"
	"repopilot/api/internal/rerank"
	"repopilot/api/internal/retrieval"
)

const (
	ModeVector       = "vector"
	ModeHybrid       = "hybrid"
	ModeHybridRerank = "hybrid_rerank"
)

type Config struct {
	Mode          string
	RRFK          int
	KeywordWeight float64
	TestPenalty   float64
	Pool          int
	RerankDepth   int
	RerankTimeout time.Duration
}

// DefaultConfig is the dev-chosen configuration: hybrid w0.75 p0.5 pool 20 (step 5), reranked at depth 20 (step 6).
func DefaultConfig() Config {
	return Config{
		Mode:          ModeHybridRerank,
		RRFK:          60,
		KeywordWeight: 0.75,
		TestPenalty:   0.5,
		Pool:          20,
		RerankDepth:   20,
		RerankTimeout: rerank.DefaultTimeout,
	}
}

var ErrBadConfig = errors.New("invalid retrieval settings")

// ConfigFromEnv reads RETRIEVAL_MODE, HYBRID_RRF_K, HYBRID_KEYWORD_WEIGHT, HYBRID_TEST_PENALTY, HYBRID_POOL,
// RERANK_DEPTH and RERANK_TIMEOUT_SEC. Empty values keep the default.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	cfg := DefaultConfig()
	if v := getenv("RETRIEVAL_MODE"); v != "" {
		cfg.Mode = v
	}
	var err error
	if cfg.RRFK, err = intEnv(getenv, "HYBRID_RRF_K", cfg.RRFK); err != nil {
		return Config{}, err
	}
	if cfg.KeywordWeight, err = floatEnv(getenv, "HYBRID_KEYWORD_WEIGHT", cfg.KeywordWeight); err != nil {
		return Config{}, err
	}
	if cfg.TestPenalty, err = floatEnv(getenv, "HYBRID_TEST_PENALTY", cfg.TestPenalty); err != nil {
		return Config{}, err
	}
	if cfg.Pool, err = intEnv(getenv, "HYBRID_POOL", cfg.Pool); err != nil {
		return Config{}, err
	}
	if cfg.RerankDepth, err = intEnv(getenv, "RERANK_DEPTH", cfg.RerankDepth); err != nil {
		return Config{}, err
	}
	sec, err := intEnv(getenv, "RERANK_TIMEOUT_SEC", int(cfg.RerankTimeout/time.Second))
	if err != nil {
		return Config{}, err
	}
	cfg.RerankTimeout = time.Duration(sec) * time.Second
	return cfg, cfg.Validate()
}

// Validate checks the ranges cmd/eval enforces for the same settings.
func (c Config) Validate() error {
	switch c.Mode {
	case ModeVector, ModeHybrid, ModeHybridRerank:
	default:
		return fmt.Errorf("%w: RETRIEVAL_MODE must be %s, %s or %s", ErrBadConfig, ModeVector, ModeHybrid, ModeHybridRerank)
	}
	switch {
	case c.RRFK < 1:
		return fmt.Errorf("%w: HYBRID_RRF_K must be at least 1", ErrBadConfig)
	case c.KeywordWeight <= 0 || c.KeywordWeight > 1:
		return fmt.Errorf("%w: HYBRID_KEYWORD_WEIGHT must be in (0, 1]", ErrBadConfig)
	case c.TestPenalty <= 0 || c.TestPenalty > 1:
		return fmt.Errorf("%w: HYBRID_TEST_PENALTY must be in (0, 1]", ErrBadConfig)
	case c.Pool < 1 || c.Pool > retrieval.MaxK:
		return fmt.Errorf("%w: HYBRID_POOL must be 1..%d", ErrBadConfig, retrieval.MaxK)
	case c.RerankDepth < 1 || c.RerankDepth > retrieval.MaxK:
		return fmt.Errorf("%w: RERANK_DEPTH must be 1..%d", ErrBadConfig, retrieval.MaxK)
	case c.RerankTimeout < time.Second:
		return fmt.Errorf("%w: RERANK_TIMEOUT_SEC must be at least 1", ErrBadConfig)
	}
	return nil
}

// Params lists the settings that apply to the mode, for the startup log.
func (c Config) Params() []any {
	out := []any{"retrieval_mode", c.Mode}
	if c.Mode == ModeVector {
		return out
	}
	out = append(out, "rrf_k", c.RRFK, "keyword_weight", c.KeywordWeight, "test_penalty", c.TestPenalty, "pool", c.Pool)
	if c.Mode == ModeHybridRerank {
		out = append(out, "rerank_depth", c.RerankDepth, "rerank_timeout_sec", int(c.RerankTimeout/time.Second))
	}
	return out
}

// Build returns the retriever for the mode. rerankLLM is used only in hybrid_rerank mode, where it is required;
// build it with rerank.LLMConfig. The caller checks the schema (rerank.CheckSchema) before using hybrid_rerank.
func Build(c Config, pool *pgxpool.Pool, embedModel string, dim int, rerankLLM llm.LLM) (retrieval.Retriever, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	vector := retrieval.VectorRetriever{Searcher: retrieval.NewPgSearcher(pool, embedModel, dim)}
	if c.Mode == ModeVector {
		return vector, nil
	}
	hybrid := retrieval.HybridRetriever{
		Vector:  vector,
		Keyword: retrieval.NewKeywordRetriever(pool, retrieval.ScoreBM25),
		RRFK:    c.RRFK, KeywordWeight: c.KeywordWeight, TestPenalty: c.TestPenalty, Pool: c.Pool,
	}
	if c.Mode == ModeHybrid {
		return hybrid, nil
	}
	if rerankLLM == nil {
		return nil, fmt.Errorf("%w: hybrid_rerank needs a rerank model", ErrBadConfig)
	}
	return &rerank.Retriever{
		Base:     hybrid,
		Reranker: rerank.LLMReranker{LLM: rerankLLM},
		Depth:    c.RerankDepth,
		Cache:    rerank.NewPgStore(pool),
		Timeout:  c.RerankTimeout,
	}, nil
}

func intEnv(getenv func(string) string, name string, def int) (int, error) {
	raw := getenv(name)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be an integer", ErrBadConfig, name)
	}
	return n, nil
}

func floatEnv(getenv func(string) string, name string, def float64) (float64, error) {
	raw := getenv(name)
	if raw == "" {
		return def, nil
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be a number", ErrBadConfig, name)
	}
	return f, nil
}
