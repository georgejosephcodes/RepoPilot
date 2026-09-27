package pipeline

import (
	"errors"
	"testing"
	"time"

	"repopilot/api/internal/llm"
	"repopilot/api/internal/rerank"
	"repopilot/api/internal/retrieval"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaultsAreTheDevChosenConfiguration(t *testing.T) {
	cfg, err := ConfigFromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := Config{Mode: ModeHybridRerank, RRFK: 60, KeywordWeight: 0.75, TestPenalty: 0.5, Pool: 20, RerankDepth: 20,
		RerankTimeout: 10 * time.Second}
	if cfg != want {
		t.Errorf("got %+v, want %+v", cfg, want)
	}
}

func TestConfigFromEnvReadsEverySetting(t *testing.T) {
	cfg, err := ConfigFromEnv(env(map[string]string{
		"RETRIEVAL_MODE": "hybrid", "HYBRID_RRF_K": "30", "HYBRID_KEYWORD_WEIGHT": "1", "HYBRID_TEST_PENALTY": "0.25",
		"HYBRID_POOL": "50", "RERANK_DEPTH": "10", "RERANK_TIMEOUT_SEC": "5",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := Config{Mode: ModeHybrid, RRFK: 30, KeywordWeight: 1, TestPenalty: 0.25, Pool: 50, RerankDepth: 10, RerankTimeout: 5 * time.Second}
	if cfg != want {
		t.Errorf("got %+v, want %+v", cfg, want)
	}
}

func TestConfigFromEnvRejectsBadValues(t *testing.T) {
	bad := []map[string]string{
		{"RETRIEVAL_MODE": "hybrid-rerank"}, // eval's spelling is not a mode
		{"RETRIEVAL_MODE": "Vector"},
		{"HYBRID_RRF_K": "0"},
		{"HYBRID_RRF_K": "x"},
		{"HYBRID_KEYWORD_WEIGHT": "0"},
		{"HYBRID_KEYWORD_WEIGHT": "1.5"},
		{"HYBRID_KEYWORD_WEIGHT": "abc"},
		{"HYBRID_TEST_PENALTY": "-1"},
		{"HYBRID_POOL": "51"},
		{"HYBRID_POOL": "0"},
		{"RERANK_DEPTH": "0"},
		{"RERANK_DEPTH": "51"},
		{"RERANK_TIMEOUT_SEC": "0"},
	}
	for _, m := range bad {
		if _, err := ConfigFromEnv(env(m)); !errors.Is(err, ErrBadConfig) {
			t.Errorf("%v: err = %v, want ErrBadConfig", m, err)
		}
	}
}

func TestBuildPicksTheRetrieverForTheMode(t *testing.T) {
	cfg := DefaultConfig()

	cfg.Mode = ModeVector
	r, err := Build(cfg, nil, "m", 8, nil)
	if _, ok := r.(retrieval.VectorRetriever); err != nil || !ok {
		t.Errorf("vector: %T, %v", r, err)
	}

	cfg.Mode = ModeHybrid
	r, err = Build(cfg, nil, "m", 8, nil)
	h, ok := r.(retrieval.HybridRetriever)
	if err != nil || !ok || h.KeywordWeight != 0.75 || h.TestPenalty != 0.5 || h.Pool != 20 || h.RRFK != 60 {
		t.Errorf("hybrid: %T %+v, %v", r, h, err)
	}

	cfg.Mode = ModeHybridRerank
	if _, err := Build(cfg, nil, "m", 8, nil); !errors.Is(err, ErrBadConfig) {
		t.Errorf("hybrid_rerank without a model: err = %v", err)
	}
	r, err = Build(cfg, nil, "m", 8, llm.NewFake("{}"))
	rr, ok := r.(*rerank.Retriever)
	if err != nil || !ok || rr.Depth != 20 || rr.Timeout != 10*time.Second || rr.Cache == nil || rr.RecordDurations {
		t.Fatalf("hybrid_rerank: %T %+v, %v", r, rr, err)
	}
	if _, ok := rr.Base.(retrieval.HybridRetriever); !ok {
		t.Errorf("rerank base is %T, want HybridRetriever", rr.Base)
	}
}

func TestParamsNameOnlyWhatTheModeUses(t *testing.T) {
	c := DefaultConfig()
	if n := len(c.Params()); n != 2+8+4 {
		t.Errorf("hybrid_rerank params: %v", c.Params())
	}
	c.Mode = ModeVector
	if n := len(c.Params()); n != 2 {
		t.Errorf("vector params: %v", c.Params())
	}
}
