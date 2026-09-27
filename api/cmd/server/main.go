package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"repopilot/api/internal/embed"
	"repopilot/api/internal/httpapi"
	"repopilot/api/internal/llm"
	"repopilot/api/internal/rag"
	"repopilot/api/internal/repos"
	"repopilot/api/internal/retrieval"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fatal("DATABASE_URL is required")
	}
	addr := os.Getenv("API_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	rateLimit := intEnv("RATE_LIMIT_PER_MIN", 30, 0)
	queryRateLimit := intEnv("QUERY_RATE_LIMIT_PER_MIN", 10, 0)

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fatal("db pool: " + err.Error())
	}
	defer pool.Close()
	store := repos.NewPgStore(pool)

	srv := &http.Server{
		Addr: addr,
		Handler: httpapi.NewRouter(httpapi.Deps{
			DB:                   pool,
			Repos:                store,
			RateLimitPerMin:      rateLimit,
			Query:                buildQueryService(ctx, pool, store),
			QueryRateLimitPerMin: queryRateLimit,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		// A question takes an embedding request plus a generation request, each with retries; the handler
		// gives up after 90 seconds, so the server must not cut the connection first.
		WriteTimeout: 120 * time.Second,
	}
	slog.Info("api listening", "addr", addr)
	if err := srv.ListenAndServe(); err != nil {
		fatal(err.Error())
	}
}

// buildQueryService wires embedder, searcher and answer model. When a key is missing it returns nil, and the
// query route answers 503 not_configured while the rest of the API keeps working. A wrong configuration
// (dimension mismatch, unreadable settings) is fatal: better to fail at startup than on the first question.
func buildQueryService(ctx context.Context, pool *pgxpool.Pool, store repos.Store) httpapi.Asker {
	embedCfg, err := embed.ConfigFromEnv(os.Getenv)
	if err != nil {
		fatal(err.Error())
	}
	llmCfg, err := llm.ConfigFromEnv(os.Getenv)
	if err != nil {
		fatal(err.Error())
	}
	if embedCfg.APIKey == "" || llmCfg.APIKey == "" {
		slog.Warn("question answering is disabled: set EMBED_API_KEY and LLM_API_KEY (or GEMINI_API_KEY)",
			"embed_key_set", embedCfg.APIKey != "", "llm_key_set", llmCfg.APIKey != "")
		return nil
	}

	if err := retrieval.CheckDimension(ctx, pool, embedCfg.Dimension); err != nil {
		fatal(err.Error())
	}
	if err := retrieval.CheckSchema(ctx, pool); err != nil {
		fatal(err.Error())
	}
	if version, err := retrieval.CheckPgvector(ctx, pool); err != nil {
		slog.Warn("pgvector check", "version", version, "error", err.Error())
	}

	embedder, err := embed.New(embedCfg)
	if err != nil {
		fatal(err.Error())
	}
	answerer, err := llm.NewGemini(llmCfg)
	if err != nil {
		fatal(err.Error())
	}
	slog.Info("question answering enabled",
		"embed_model", embedder.ModelName(), "embed_dimension", embedder.Dimension(), "llm_model", answerer.ModelName())

	return &rag.Service{
		Repos:            store,
		Embed:            embed.NewCached(embed.NewPersistent(embedder, embed.NewPgQueryStore(pool, embedder.Dimension()), embed.DefaultBatchSize), 256),
		Search:           retrieval.NewPgSearcher(pool, embedder.ModelName(), embedder.Dimension()),
		LLM:              answerer,
		TopK:             intEnv("RETRIEVAL_TOP_K", rag.DefaultTopK, 1),
		BudgetChars:      intEnv("CONTEXT_BUDGET_CHARS", rag.DefaultBudgetChars, 1),
		MaxQuestionChars: intEnv("MAX_QUESTION_CHARS", rag.DefaultMaxQuestionChars, 1),
	}
}

func intEnv(name string, def, min int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		fatal(fmt.Sprintf("%s must be an integer", name))
	}
	if n < min {
		fatal(fmt.Sprintf("%s must be at least %d", name, min))
	}
	return n
}

func fatal(msg string) {
	slog.Error(msg)
	os.Exit(1)
}
