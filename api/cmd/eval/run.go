package main

import (
	"context"
	"fmt"
	"sort"

	"repopilot/api/internal/evalmetrics"
	"repopilot/api/internal/rerank"
	"repopilot/api/internal/retrieval"
)

// Run is everything one evaluation produced; it is written as JSON so later runs can be compared with it.
type Run struct {
	Settings  Settings                       `json:"settings"`
	Overall   evalmetrics.Summary            `json:"overall"`
	BySplit   map[string]evalmetrics.Summary `json:"by_split"`
	ByRepo    map[string]evalmetrics.Summary `json:"by_repository"`
	ByKind    map[string]evalmetrics.Summary `json:"by_kind"`
	Questions []QuestionResult               `json:"questions"`
}

type Settings struct {
	Variant      string             `json:"variant"`
	Depth        int                `json:"depth"` // chunks retrieved per question
	Split        string             `json:"split"`
	Model        string             `json:"embed_model"`
	Dimension    int                `json:"embed_dimension"`
	LabelsSHA256 string             `json:"labels_sha256"`
	LabelsFrozen bool               `json:"labels_frozen"`
	CodeCommit   string             `json:"code_commit"`
	Repositories []RepoPin          `json:"repositories"`
	CreatedAt    string             `json:"created_at"`
	Params       map[string]float64 `json:"params,omitempty"` // variant settings, for example the hybrid weights
	Rerank       *RerankInfo        `json:"rerank,omitempty"` // hybrid-rerank only
}

// RerankInfo records what the reranker did in a run. Times are upstream calls, measured when each ranking was
// made (a ranking served from rerank_cache keeps its original time), so a re-run from the cache reports the same.
type RerankInfo struct {
	Name          string         `json:"name"` // prompt version / model
	Depth         int            `json:"depth"`
	UpstreamCalls int            `json:"upstream_calls"`
	CacheHits     int            `json:"cache_hits"`
	FallbackCount int            `json:"fallback_count"`
	Fallbacks     map[string]int `json:"fallbacks,omitempty"`
	MedianMS      int64          `json:"median_ms"`
	P90MS         int64          `json:"p90_ms"`
	MaxMS         int64          `json:"max_ms"`
}

func rerankInfo(name string, depth int, s rerank.Stats) *RerankInfo {
	return &RerankInfo{Name: name, Depth: depth, UpstreamCalls: s.Calls, CacheHits: s.CacheHits,
		FallbackCount: s.FallbackCount(), Fallbacks: s.Fallbacks, MedianMS: s.Percentile(50).Milliseconds(),
		P90MS: s.Percentile(90).Milliseconds(), MaxMS: s.Percentile(100).Milliseconds()}
}

type QuestionResult struct {
	ID          string             `json:"id"`
	Repo        string             `json:"repo"`
	Kind        string             `json:"kind"`
	Split       string             `json:"split"`
	Question    string             `json:"question"`
	Score       *evalmetrics.Score `json:"score,omitempty"` // nil for unanswerable questions
	TopDistance float64            `json:"top_distance"`    // data for a possible refusal threshold later
	Retrieved   []Retrieved        `json:"retrieved"`
	// RerankFallback is why this question's list is in base order instead of reranked ("timeout", ...), or empty.
	RerankFallback string `json:"rerank_fallback,omitempty"`
}

type Retrieved struct {
	Rank     int     `json:"rank"`
	File     string  `json:"file"`
	Start    int     `json:"start_line"`
	End      int     `json:"end_line"`
	Symbol   string  `json:"symbol,omitempty"`
	Kind     string  `json:"kind"`
	ChunkID  int64   `json:"chunk_id"`
	Distance float64 `json:"distance"`
	Score    float64 `json:"score,omitempty"` // keyword variants only
	Grade    int     `json:"grade"`
}

// Depth is how many chunks each question retrieves: enough to score reciprocal rank to MaxRank.
const Depth = evalmetrics.MaxRank

// retrieveAll runs one retrieval per question with its precomputed vector and scores it.
func retrieveAll(ctx context.Context, qs []Question, vecs [][]float32, ids map[string]int64, r retrieval.Retriever) ([]QuestionResult, error) {
	if len(vecs) != len(qs) {
		return nil, fmt.Errorf("got %d vectors for %d questions", len(vecs), len(qs))
	}
	out := make([]QuestionResult, 0, len(qs))
	for i, q := range qs {
		qctx, trace := retrieval.WithTrace(ctx)
		chunks, err := r.Retrieve(qctx, retrieval.Query{RepoID: ids[q.Repo], Text: q.Question, Vec: vecs[i], K: Depth})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", q.ID, err)
		}
		spans := make([]evalmetrics.Span, len(chunks))
		for j, c := range chunks {
			spans[j] = evalmetrics.Span{File: c.FilePath, Start: c.StartLine, End: c.EndLine}
		}
		hits := evalmetrics.Hits(spans, q.Relevant)
		r := QuestionResult{ID: q.ID, Repo: q.Repo, Kind: q.Kind, Split: q.Split, Question: q.Question, Retrieved: make([]Retrieved, len(chunks)),
			RerankFallback: trace.Snapshot().RerankFallback}
		for j, c := range chunks {
			r.Retrieved[j] = Retrieved{Rank: j + 1, File: c.FilePath, Start: c.StartLine, End: c.EndLine, Symbol: c.Symbol,
				Kind: c.Kind, ChunkID: c.ID, Distance: c.Distance, Score: c.Score, Grade: hits[j]}
		}
		if len(chunks) > 0 {
			r.TopDistance = chunks[0].Distance
		}
		if q.Answerable() {
			score := evalmetrics.ScoreQuestion(spans, q.Relevant)
			r.Score = &score
		}
		out = append(out, r)
	}
	return out, nil
}

// summarize fills the overall and grouped means. Unanswerable questions have no score and are left out.
func summarize(run *Run) {
	var all []evalmetrics.Score
	groups := map[string]map[string][]evalmetrics.Score{"split": {}, "repo": {}, "kind": {}}
	for _, q := range run.Questions {
		if q.Score == nil {
			continue
		}
		all = append(all, *q.Score)
		groups["split"][q.Split] = append(groups["split"][q.Split], *q.Score)
		groups["repo"][q.Repo] = append(groups["repo"][q.Repo], *q.Score)
		groups["kind"][q.Kind] = append(groups["kind"][q.Kind], *q.Score)
	}
	run.Overall = evalmetrics.Summarize(all)
	sum := func(g map[string][]evalmetrics.Score) map[string]evalmetrics.Summary {
		out := map[string]evalmetrics.Summary{}
		for k, v := range g {
			out[k] = evalmetrics.Summarize(v)
		}
		return out
	}
	run.BySplit, run.ByRepo, run.ByKind = sum(groups["split"]), sum(groups["repo"]), sum(groups["kind"])
}

func sortedKeys(m map[string]evalmetrics.Summary) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
