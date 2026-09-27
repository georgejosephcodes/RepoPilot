package rag

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"repopilot/api/internal/embed"
	"repopilot/api/internal/llm"
	"repopilot/api/internal/repos"
	"repopilot/api/internal/retrieval"
)

var (
	ErrQuestionInvalid = errors.New("question is empty or too long")
	ErrNotReady        = errors.New("repository is not indexed yet")
)

const (
	DefaultTopK             = 8
	DefaultMaxQuestionChars = 1000
)

type Stats struct {
	RetrievalMode   string `json:"retrieval_mode"`
	ChunksRetrieved int    `json:"chunks_retrieved"`
	ChunksInPrompt  int    `json:"chunks_in_prompt"`
	EmbedMS         int64  `json:"embed_ms"`
	SearchMS        int64  `json:"search_ms"`  // the whole retrieval, including keyword search and reranking
	KeywordMS       int64  `json:"keyword_ms"` // part of search_ms (runs next to the vector search)
	RerankMS        int64  `json:"rerank_ms"`  // part of search_ms; for a cached ranking, the time it took when made
	RerankCached    bool   `json:"rerank_cached"`
	RerankFallback  string `json:"rerank_fallback,omitempty"` // why the hybrid order was used: timeout, unparseable, error
	LLMMS           int64  `json:"llm_ms"`
	InputTokens     int    `json:"input_tokens"`
	OutputTokens    int    `json:"output_tokens"`
}

type CitationJSON struct {
	N         int    `json:"n"`
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Symbol    string `json:"symbol"`
	Kind      string `json:"kind"`
	Language  string `json:"language"`
	Snippet   string `json:"snippet"`
}

type Response struct {
	Answer    string         `json:"answer"`
	Grounded  bool           `json:"grounded"`
	Refused   bool           `json:"refused"`
	Truncated bool           `json:"truncated"` // the model hit its output limit
	Citations []CitationJSON `json:"citations"`
	Stats     Stats          `json:"stats"`
}

// Service answers questions about an indexed repository.
type Service struct {
	Repos            repos.Store
	Embed            embed.Embedder
	Retrieve         retrieval.Retriever
	Mode             string // retrieval mode, reported in Stats (pipeline.ModeVector, ...)
	LLM              llm.LLM
	TopK             int // chunks retrieved per question (default 8)
	BudgetChars      int // characters of code allowed in the prompt (default 24,000)
	MaxQuestionChars int // question length limit in characters (default 1,000)
}

func (s *Service) topK() int {
	if s.TopK < 1 {
		return DefaultTopK
	}
	return min(s.TopK, retrieval.MaxK)
}

func (s *Service) maxQuestionChars() int {
	if s.MaxQuestionChars < 1 {
		return DefaultMaxQuestionChars
	}
	return s.MaxQuestionChars
}

// Ask answers `question` about repository `repoID`, retrieving only chunks that match filter. Errors are typed;
// use Classify to get an HTTP status.
func (s *Service) Ask(ctx context.Context, repoID int64, question string, filter retrieval.Filter) (Response, error) {
	question = strings.TrimSpace(question)
	if question == "" || utf8.RuneCountInString(question) > s.maxQuestionChars() {
		return Response{}, ErrQuestionInvalid
	}
	if err := retrieval.ValidateFilter(filter); err != nil {
		return Response{}, err
	}
	detail, err := s.Repos.Get(ctx, repoID)
	if err != nil {
		return Response{}, err
	}
	if detail.Status != "ready" {
		return Response{}, ErrNotReady
	}

	stats := Stats{RetrievalMode: s.Mode}
	t0 := time.Now()
	vec, err := s.Embed.EmbedQuery(ctx, question)
	if err != nil {
		return Response{}, err
	}
	stats.EmbedMS = time.Since(t0).Milliseconds()

	t0 = time.Now()
	rctx, trace := retrieval.WithTrace(ctx)
	chunks, err := s.Retrieve.Retrieve(rctx, retrieval.Query{RepoID: repoID, Text: question, Vec: vec, K: s.topK(), Filter: filter})
	if err != nil {
		return Response{}, err
	}
	stats.SearchMS = time.Since(t0).Milliseconds()
	stats.ChunksRetrieved = len(chunks)
	td := trace.Snapshot()
	stats.KeywordMS, stats.RerankMS = td.KeywordMS, td.RerankMS
	stats.RerankCached, stats.RerankFallback = td.RerankSource == "cache", td.RerankFallback

	if len(chunks) == 0 {
		// Nothing to answer from: refuse without spending an answer-model request.
		reason := " The repository has no indexed content to search."
		if !filter.Empty() {
			reason = " No indexed code matches the filters."
		}
		slog.Info("question refused: no chunks", "repo_id", repoID, "question_chars", utf8.RuneCountInString(question),
			"filtered", !filter.Empty())
		return Response{
			Answer:    RefusalSentence + reason,
			Grounded:  true,
			Refused:   true,
			Citations: []CitationJSON{},
			Stats:     stats,
		}, nil
	}

	user, used, budgetHit := BuildPrompt(question, chunks, s.BudgetChars)
	stats.ChunksInPrompt = len(used)

	t0 = time.Now()
	res, err := s.LLM.Generate(ctx, SystemPrompt, user)
	if err != nil {
		return Response{}, err
	}
	stats.LLMMS = time.Since(t0).Milliseconds()
	stats.InputTokens, stats.OutputTokens = res.InputTokens, res.OutputTokens

	v := Validate(res.Text, used)
	out := Response{
		Answer:    v.Text,
		Grounded:  v.Grounded,
		Refused:   v.Refused,
		Truncated: res.FinishReason == "MAX_TOKENS",
		Citations: make([]CitationJSON, 0, len(v.Citations)),
		Stats:     stats,
	}
	for _, c := range v.Citations {
		out.Citations = append(out.Citations, CitationJSON{
			N: c.N, File: c.File, StartLine: c.StartLine, EndLine: c.EndLine,
			Symbol: c.Symbol, Kind: c.Kind, Language: c.Language, Snippet: c.Snippet,
		})
	}

	attrs := []any{
		"repo_id", repoID,
		"question_chars", utf8.RuneCountInString(question), // the question text itself is not logged
		"retrieval_mode", stats.RetrievalMode, "filtered", !filter.Empty(),
		"chunks_retrieved", len(chunks), "chunks_in_prompt", len(used), "budget_hit", budgetHit,
		"citations", len(v.Citations), "invalid_citations", v.Invalid,
		"grounded", v.Grounded, "refused", v.Refused, "truncated", out.Truncated,
		"input_tokens", stats.InputTokens, "output_tokens", stats.OutputTokens,
		"embed_ms", stats.EmbedMS, "search_ms", stats.SearchMS, "keyword_ms", stats.KeywordMS,
		"rerank_ms", stats.RerankMS, "rerank_source", td.RerankSource, "rerank_fallback", stats.RerankFallback,
		"llm_ms", stats.LLMMS,
	}
	if td.Keyword == nil && td.Fused == nil {
		// Distances mean something only for a pure vector list; a keyword-only chunk in a fused list has none.
		attrs = append(attrs, "nearest", chunks[0].Distance, "farthest", chunks[len(chunks)-1].Distance)
	}
	slog.Info("question answered", attrs...)
	return out, nil
}
