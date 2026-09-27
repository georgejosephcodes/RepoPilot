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
	ChunksRetrieved int   `json:"chunks_retrieved"`
	ChunksInPrompt  int   `json:"chunks_in_prompt"`
	EmbedMS         int64 `json:"embed_ms"`
	SearchMS        int64 `json:"search_ms"`
	LLMMS           int64 `json:"llm_ms"`
	InputTokens     int   `json:"input_tokens"`
	OutputTokens    int   `json:"output_tokens"`
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
	Search           retrieval.Searcher
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

// Ask answers `question` about repository `repoID`. Errors are typed; use Classify to get an HTTP status.
func (s *Service) Ask(ctx context.Context, repoID int64, question string) (Response, error) {
	question = strings.TrimSpace(question)
	if question == "" || utf8.RuneCountInString(question) > s.maxQuestionChars() {
		return Response{}, ErrQuestionInvalid
	}
	detail, err := s.Repos.Get(ctx, repoID)
	if err != nil {
		return Response{}, err
	}
	if detail.Status != "ready" {
		return Response{}, ErrNotReady
	}

	var stats Stats
	t0 := time.Now()
	vec, err := s.Embed.EmbedQuery(ctx, question)
	if err != nil {
		return Response{}, err
	}
	stats.EmbedMS = time.Since(t0).Milliseconds()

	t0 = time.Now()
	chunks, err := s.Search.Search(ctx, repoID, vec, s.topK())
	if err != nil {
		return Response{}, err
	}
	stats.SearchMS = time.Since(t0).Milliseconds()
	stats.ChunksRetrieved = len(chunks)

	if len(chunks) == 0 {
		// Nothing to answer from: refuse without spending an answer-model request.
		slog.Info("question refused: no chunks", "repo_id", repoID, "question_chars", utf8.RuneCountInString(question))
		return Response{
			Answer:    RefusalSentence + " The repository has no indexed content to search.",
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

	slog.Info("question answered",
		"repo_id", repoID,
		"question_chars", utf8.RuneCountInString(question), // the question text itself is not logged
		"chunks_retrieved", len(chunks), "chunks_in_prompt", len(used), "budget_hit", budgetHit,
		"nearest", chunks[0].Distance, "farthest", chunks[len(chunks)-1].Distance,
		"citations", len(v.Citations), "invalid_citations", v.Invalid,
		"grounded", v.Grounded, "refused", v.Refused, "truncated", out.Truncated,
		"input_tokens", stats.InputTokens, "output_tokens", stats.OutputTokens,
		"embed_ms", stats.EmbedMS, "search_ms", stats.SearchMS, "llm_ms", stats.LLMMS,
	)
	return out, nil
}
