// search-debug embeds a question and prints the nearest chunks of one repository, so relevance can be
// judged by eye. Each run makes ONE embedding request against the provider's (small) daily budget.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"repopilot/api/internal/embed"
	"repopilot/api/internal/retrieval"
)

func main() {
	repo := flag.Int64("repo", 0, "repository id")
	question := flag.String("q", "", "question to search for")
	k := flag.Int("k", 8, "number of chunks to return")
	flag.Parse()
	if *repo < 1 || strings.TrimSpace(*question) == "" {
		fatal("usage: search-debug --repo <id> --q \"question\" [--k 8]")
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fatal("DATABASE_URL is required")
	}
	cfg, err := embed.ConfigFromEnv(os.Getenv)
	if err != nil {
		fatal(err.Error())
	}
	embedder, err := embed.New(cfg)
	if err != nil {
		fatal(err.Error())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fatal("database: " + err.Error())
	}
	defer pool.Close()
	if err := retrieval.CheckDimension(ctx, pool, embedder.Dimension()); err != nil {
		fatal(err.Error())
	}
	if version, err := retrieval.CheckPgvector(ctx, pool); err != nil {
		fatal(err.Error())
	} else {
		fmt.Printf("model %s, %d dimensions, pgvector %s\n", embedder.ModelName(), embedder.Dimension(), version)
	}

	start := time.Now()
	vec, err := embedder.EmbedQuery(ctx, *question)
	if err != nil {
		fatal(err.Error())
	}
	embedded := time.Since(start)

	start = time.Now()
	chunks, err := retrieval.NewPgSearcher(pool, embedder.ModelName(), embedder.Dimension()).Search(ctx, *repo, vec, *k)
	if err != nil {
		fatal(err.Error())
	}
	fmt.Printf("question: %q\nembedding %v, search %v, %d results\n\n", *question, embedded.Round(time.Millisecond), time.Since(start).Round(time.Millisecond), len(chunks))
	for i, c := range chunks {
		symbol := c.Symbol
		if symbol == "" {
			symbol = "-"
		}
		fmt.Printf("%2d  d=%.4f  %s:%d-%d  %s (%s)\n", i+1, c.Distance, c.FilePath, c.StartLine, c.EndLine, symbol, c.Kind)
		fmt.Printf("      %s\n", firstLine(c.Content, 110))
	}
	fmt.Println("\nthis run used 1 embedding request of your free daily budget")
}

func firstLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > max {
		s = s[:max] + "..."
	}
	return s
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "error: "+msg)
	os.Exit(1)
}
