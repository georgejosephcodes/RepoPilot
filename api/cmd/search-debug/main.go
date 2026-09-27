// search-debug retrieves the chunks of one repository for a question and prints, for each result, its rank in
// every list the pipeline built (vector, keyword, fused, reranked), so odd rankings can be explained by eye.
//
// The question vector comes from the persistent question cache when it is there (0 embedding requests); the
// ranking comes from the rerank cache when it is there (0 Gemini requests). The run says what it spent.
//
//	go run ./cmd/search-debug --repo 1 --q "where does the server start" [--k 8] [--mode hybrid_rerank] [--lang go,python] [--path api/]
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"repopilot/api/internal/embed"
	"repopilot/api/internal/llm"
	"repopilot/api/internal/pipeline"
	"repopilot/api/internal/rerank"
	"repopilot/api/internal/retrieval"
)

func main() {
	repo := flag.Int64("repo", 0, "repository id")
	question := flag.String("q", "", "question to search for")
	k := flag.Int("k", 8, "number of chunks to return")
	mode := flag.String("mode", "", "vector, hybrid or hybrid_rerank (default: RETRIEVAL_MODE, else hybrid_rerank)")
	langs := flag.String("lang", "", "only these languages, comma separated (e.g. go,python)")
	prefix := flag.String("path", "", "only files whose path starts with this (e.g. api/)")
	flag.Parse()
	if *repo < 1 || strings.TrimSpace(*question) == "" {
		fatal("usage: search-debug --repo <id> --q \"question\" [--k 8] [--mode hybrid_rerank] [--lang go,python] [--path api/]")
	}
	if *k < 1 || *k > retrieval.MaxK {
		fatal(fmt.Sprintf("--k must be 1..%d", retrieval.MaxK))
	}
	filter := retrieval.Filter{PathPrefix: *prefix}
	for _, l := range strings.Split(*langs, ",") {
		if l = strings.TrimSpace(l); l != "" {
			filter.Languages = append(filter.Languages, l)
		}
	}
	if err := retrieval.ValidateFilter(filter); err != nil {
		fatal(err.Error())
	}

	pcfg, err := pipeline.ConfigFromEnv(os.Getenv)
	if err != nil {
		fatal(err.Error())
	}
	if *mode != "" {
		pcfg.Mode = *mode
		if err := pcfg.Validate(); err != nil {
			fatal(err.Error())
		}
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fatal("DATABASE_URL is required")
	}
	cfg, err := embed.ConfigFromEnv(os.Getenv)
	if err != nil {
		fatal(err.Error())
	}
	upstream, err := embed.New(cfg)
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
	if err := retrieval.CheckDimension(ctx, pool, upstream.Dimension()); err != nil {
		fatal(err.Error())
	}
	if err := retrieval.CheckSchema(ctx, pool); err != nil {
		fatal(err.Error())
	}
	version, err := retrieval.CheckPgvector(ctx, pool)
	if err != nil {
		fatal(err.Error())
	}

	var rerankLLM llm.LLM
	if pcfg.Mode == pipeline.ModeHybridRerank {
		if err := rerank.CheckSchema(ctx, pool); err != nil {
			fatal(err.Error())
		}
		lcfg, err := llm.ConfigFromEnv(os.Getenv)
		if err != nil {
			fatal(err.Error())
		}
		if rerankLLM, err = llm.NewGemini(rerank.LLMConfig(lcfg)); err != nil {
			fatal(err.Error())
		}
	}
	retriever, err := pipeline.Build(pcfg, pool, upstream.ModelName(), upstream.Dimension(), rerankLLM)
	if err != nil {
		fatal(err.Error())
	}
	fmt.Printf("model %s, %d dimensions, pgvector %s\n", upstream.ModelName(), upstream.Dimension(), version)
	fmt.Println(formatParams(pcfg.Params()))

	embedder := embed.NewPersistent(upstream, embed.NewPgQueryStore(pool, upstream.Dimension()), embed.DefaultBatchSize)
	misses, err := embedder.Misses(ctx, []string{*question})
	if err != nil {
		fatal("question cache: " + err.Error())
	}
	start := time.Now()
	vec, err := embedder.EmbedQuery(ctx, *question)
	if err != nil {
		fatal(err.Error())
	}
	embedded := time.Since(start)

	rctx, trace := retrieval.WithTrace(ctx)
	start = time.Now()
	chunks, err := retriever.Retrieve(rctx, retrieval.Query{RepoID: *repo, Text: *question, Vec: vec, K: *k, Filter: filter})
	if err != nil {
		fatal(err.Error())
	}
	took := time.Since(start)
	d := trace.Snapshot()

	fmt.Printf("question: %q\n", *question)
	if !filter.Empty() {
		fmt.Printf("filter: languages %v, path prefix %q\n", filter.Languages, filter.PathPrefix)
	}
	fmt.Printf("embedding %v, retrieval %v (vector %d ms, keyword %d ms, rerank %s), %d results\n",
		embedded.Round(time.Millisecond), took.Round(time.Millisecond), d.VectorMS, d.KeywordMS, rerankNote(d), len(chunks))
	fmt.Println("ranks: v = vector list, k = keyword list, f = fused list, r = reranked list; - = not in that list")
	fmt.Println()
	for i, c := range chunks {
		symbol := c.Symbol
		if symbol == "" {
			symbol = "-"
		}
		fmt.Printf("#%-2d v %2s  k %2s  f %2s  r %2s  %s:%d-%d  %s (%s)\n", i+1,
			rank(d.Vector, c.ID), rank(d.Keyword, c.ID), rank(d.Fused, c.ID), rank(d.Reranked, c.ID),
			c.FilePath, c.StartLine, c.EndLine, symbol, c.Kind)
		fmt.Printf("      %s\n", firstLine(c.Content, 110))
	}

	gemini := 0
	if rr, ok := retriever.(*rerank.Retriever); ok {
		gemini = rr.Stats().Calls
	}
	fmt.Printf("\nthis run used %d embedding request(s) and %d Gemini request(s)\n", misses, gemini)
}

func rerankNote(d retrieval.TraceData) string {
	switch {
	case d.RerankSource == "":
		return "not used"
	case d.RerankFallback != "":
		return fmt.Sprintf("failed (%s) after %d ms, fused order used", d.RerankFallback, d.RerankMS)
	case d.RerankSource == "cache":
		return fmt.Sprintf("from cache (took %d ms when made)", d.RerankMS)
	}
	return fmt.Sprintf("%d ms", d.RerankMS)
}

// rank is the 1-based position of id in list, or "-".
func rank(list []int64, id int64) string {
	if i := slices.Index(list, id); i >= 0 {
		return strconv.Itoa(i + 1)
	}
	return "-"
}

func formatParams(kv []any) string {
	var b strings.Builder
	for i := 0; i+1 < len(kv); i += 2 {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%v %v", kv[i], kv[i+1])
	}
	return b.String()
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
