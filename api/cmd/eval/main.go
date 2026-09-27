// eval measures retrieval against the labelled evaluation set (docs/phase2/eval.json) and writes a JSON run plus
// a Markdown report. Question vectors come from the persistent question cache, so only the first run of a new
// set spends embedding requests; -dry-run shows that cost without sending anything.
//
//	go run ./cmd/eval -dry-run
//	go run ./cmd/eval -split dev -out ../docs/phase2/runs/x.json -report ../docs/phase2/x.md -compare ../docs/phase2/runs/baseline.json
//	go run ./cmd/eval -split all -final -out ../docs/phase2/runs/baseline.json -report ../docs/phase2/baseline.md
//	go run ./cmd/eval -variant hybrid-rerank -keyword-weight 0.75 -test-penalty 0.5 -rerank-depth 10 -dry-run
//	go run ./cmd/eval -variant hybrid-rewrite -keyword-weight 0.75 -test-penalty 0.5 -dry-run
//
// hybrid-rerank calls the answer model (Gemini) once per question whose ranking is not in rerank_cache; the dry run
// counts those calls, -max-llm-requests caps them, and upstream calls are spaced for Gemini's per-minute limit.
// The -rewrite variants (PHASE2.md step 8) also call Gemini once per question whose rewrite is not in the
// -rewrite-cache file; those calls count toward the same cap and share the same spacing.
//
// Parameters are tuned on the dev split only; the test split (and "all", which contains it) needs -final.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"repopilot/api/internal/embed"
	"repopilot/api/internal/llm"
	"repopilot/api/internal/repos"
	"repopilot/api/internal/rerank"
	"repopilot/api/internal/retrieval"
	"repopilot/api/internal/rewrite"
)

var variants = map[string]bool{"vector": true, "keyword-tsrank": true, "keyword-bm25": true, "hybrid": true, "hybrid-rerank": true,
	"hybrid-rewrite": true, "hybrid-rerank-rewrite": true}

// rerankGap spaces upstream rerank calls: Gemini allows 15 requests per minute (PHASE2.md 6.3).
const rerankGap = 4200 * time.Millisecond

func usesHybrid(variant string) bool  { return strings.HasPrefix(variant, "hybrid") }
func usesRerank(variant string) bool  { return strings.HasPrefix(variant, "hybrid-rerank") }
func usesRewrite(variant string) bool { return strings.HasSuffix(variant, "-rewrite") }

// hybridParams are the tunable hybrid settings (PHASE2.md step 5).
type hybridParams struct {
	RRFK          int
	KeywordWeight float64
	TestPenalty   float64
	Pool          int
}

// retrieverFor builds the retriever a variant names.
func retrieverFor(variant string, pool *pgxpool.Pool, model string, dim int, hp hybridParams) retrieval.Retriever {
	switch variant {
	case "hybrid", "hybrid-rerank", "hybrid-rewrite", "hybrid-rerank-rewrite": // rewrite and rerank wrappers are added in main
		return retrieval.HybridRetriever{
			Vector:  retrieval.VectorRetriever{Searcher: retrieval.NewPgSearcher(pool, model, dim)},
			Keyword: retrieval.NewKeywordRetriever(pool, retrieval.ScoreBM25),
			RRFK:    hp.RRFK, KeywordWeight: hp.KeywordWeight, TestPenalty: hp.TestPenalty, Pool: hp.Pool,
		}
	case "keyword-tsrank":
		return retrieval.NewKeywordRetriever(pool, retrieval.ScoreTSRank)
	case "keyword-bm25":
		return retrieval.NewKeywordRetriever(pool, retrieval.ScoreBM25)
	default:
		return retrieval.VectorRetriever{Searcher: retrieval.NewPgSearcher(pool, model, dim)}
	}
}

func main() {
	questionsPath := flag.String("questions", "../docs/phase2/eval.json", "evaluation set")
	variant := flag.String("variant", "vector", "retrieval variant: vector, keyword-tsrank, keyword-bm25, hybrid, hybrid-rerank, hybrid-rewrite or hybrid-rerank-rewrite")
	var hp hybridParams
	flag.IntVar(&hp.RRFK, "rrf-k", 60, "hybrid: RRF constant")
	flag.Float64Var(&hp.KeywordWeight, "keyword-weight", 1, "hybrid: weight of the keyword list, in (0, 1]")
	flag.Float64Var(&hp.TestPenalty, "test-penalty", 1, "hybrid: multiplier for test files, in (0, 1]; 1 = none")
	flag.IntVar(&hp.Pool, "pool", 20, "hybrid: chunks taken from each list, 1..50")
	rerankDepth := flag.Int("rerank-depth", 20, "hybrid-rerank: candidates shown to the reranker, 1..50")
	maxLLM := flag.Int("max-llm-requests", 50, "rerank and rewrite variants: refuse to run if more Gemini calls than this are needed")
	rewriteCache := flag.String("rewrite-cache", "../docs/phase2/runs/rewrites-dev.json", "rewrite variants: the JSON file that caches rewrites")
	split := flag.String("split", "dev", "dev, test or all")
	final := flag.Bool("final", false, "allow the test split (and all); use only for the baseline and the final run")
	outPath := flag.String("out", "", "write the run as JSON here")
	reportPath := flag.String("report", "", "write the Markdown report here")
	comparePath := flag.String("compare", "", "an earlier run (JSON) to compare against")
	dryRun := flag.Bool("dry-run", false, "check the setup and count embedding requests; send nothing")
	maxRequests := flag.Int("max-requests", 3, "refuse to run if the questions need more embedding requests than this")
	flag.Parse()

	if err := checkFlags(*variant, *split, *final); err != nil {
		fatal(err.Error())
	}
	if err := checkHybrid(hp); usesHybrid(*variant) && err != nil {
		fatal(err.Error())
	}
	if usesRerank(*variant) && (*rerankDepth < 1 || *rerankDepth > retrieval.MaxK) {
		fatal(fmt.Sprintf("-rerank-depth must be 1..%d", retrieval.MaxK))
	}
	raw, err := os.ReadFile(*questionsPath)
	if err != nil {
		fatal(err.Error())
	}
	set, err := loadSet(raw)
	if err != nil {
		fatal(err.Error())
	}
	qs := selectSplit(set.Questions, *split)
	if len(qs) == 0 {
		fatal("no questions in split " + *split)
	}
	var base *Run
	if *comparePath != "" {
		b, err := readRun(*comparePath)
		if err != nil {
			fatal(err.Error())
		}
		base = &b
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fatal("DATABASE_URL is required")
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fatal("database: " + err.Error())
	}
	defer pool.Close()

	indexed, err := repos.NewPgStore(pool).List(ctx)
	if err != nil {
		fatal("list repositories: " + err.Error())
	}
	ids, problems := checkRepos(set.Repositories, indexed)
	if len(problems) > 0 {
		fatal("setup problems:\n  " + strings.Join(problems, "\n  "))
	}

	cfg, err := embed.ConfigFromEnv(os.Getenv)
	if err != nil {
		fatal(err.Error())
	}
	upstream, err := embed.New(cfg)
	if err != nil {
		fatal(err.Error())
	}
	if err := retrieval.CheckDimension(ctx, pool, upstream.Dimension()); err != nil {
		fatal(err.Error())
	}
	if err := retrieval.CheckSchema(ctx, pool); err != nil {
		fatal(err.Error())
	}
	embedder := embed.NewPersistent(upstream, embed.NewPgQueryStore(pool, upstream.Dimension()), embed.DefaultBatchSize)

	texts := make([]string, len(qs))
	for i, q := range qs {
		texts[i] = q.Question
	}
	misses, err := embedder.Misses(ctx, texts)
	if err != nil {
		fatal("question cache: " + err.Error())
	}
	requests := embedder.Requests(misses)
	fmt.Printf("%d questions (split %s), %d not cached: %d embedding request(s)\n", len(qs), *split, misses, requests)
	if requests > *maxRequests && !*dryRun {
		fatal(fmt.Sprintf("that is more than -max-requests %d; raise it deliberately if this is expected", *maxRequests))
	}

	retriever := retrieverFor(*variant, pool, upstream.ModelName(), upstream.Dimension(), hp)
	pace := pacer(rerankGap) // rewrite and rerank calls share Gemini's per-minute limit
	var lcfg llm.Config
	if usesRewrite(*variant) || usesRerank(*variant) {
		if lcfg, err = llm.ConfigFromEnv(os.Getenv); err != nil {
			fatal(err.Error())
		}
	}
	var rw *rewrite.Retriever
	rewriteMisses := 0
	if usesRewrite(*variant) {
		model, err := llm.NewGemini(rewrite.LLMConfig(lcfg))
		if err != nil {
			fatal(err.Error())
		}
		store, err := rewrite.OpenFileStore(*rewriteCache)
		if err != nil {
			fatal("rewrite cache: " + err.Error())
		}
		rw = &rewrite.Retriever{Base: retriever, Rewriter: rewrite.LLMRewriter{LLM: model}, Cache: store, Pace: pace}
		retriever = rw
		for _, q := range qs {
			if rw.Pending(q.Question) {
				rewriteMisses++
			}
		}
		fmt.Printf("rewrite (%s): %d of %d questions not cached in %s: %d Gemini request(s), about %s with pacing\n",
			rw.Rewriter.Name(), rewriteMisses, len(qs), *rewriteCache, rewriteMisses,
			(time.Duration(rewriteMisses) * rerankGap).Round(time.Second))
		if rewriteMisses > *maxLLM && !*dryRun {
			fatal(fmt.Sprintf("that is more than -max-llm-requests %d; raise it deliberately if this is expected", *maxLLM))
		}
	}
	var rr *rerank.Retriever
	if usesRerank(*variant) {
		if err := rerank.CheckSchema(ctx, pool); err != nil {
			fatal(err.Error())
		}
		model, err := llm.NewGemini(rerank.LLMConfig(lcfg))
		if err != nil {
			fatal(err.Error())
		}
		rr = &rerank.Retriever{Base: retriever, Reranker: rerank.LLMReranker{LLM: model}, Depth: *rerankDepth,
			Cache: rerank.NewPgStore(pool), Pace: pace, RecordDurations: true}
		retriever = rr
	}

	if *dryRun && (rr == nil || misses > 0 || rewriteMisses > 0) {
		if rr != nil {
			fmt.Println("rerank calls: unknown until every question is embedded and rewritten (the rankings depend on the candidates)")
		}
		fmt.Println("dry run: setup ok, nothing sent")
		return
	}

	// With every question cached, this sends nothing, so a dry run may use it to count rerank calls.
	vecs, err := embedder.EmbedQueries(ctx, texts)
	if err != nil {
		fatal("embed questions: " + err.Error())
	}
	if rw != nil && rr != nil && rewriteMisses > 0 {
		// Rewrite first, so the rerank count below sees final candidates and sends nothing itself.
		for _, q := range qs {
			if _, err := rw.Terms(ctx, q.Question); err != nil {
				fatal("rewrite: " + err.Error())
			}
		}
	}
	if rr != nil {
		pending, err := countPending(ctx, rr, qs, vecs, ids)
		if err != nil {
			fatal("rerank cache: " + err.Error())
		}
		fmt.Printf("rerank (%s, depth %d): %d ranking(s) not cached: %d Gemini request(s) (more only if one is retried), about %s with pacing\n",
			rr.Reranker.Name(), rr.Depth, pending, pending, (time.Duration(pending) * rerankGap).Round(time.Second))
		if *dryRun {
			fmt.Println("dry run: setup ok, nothing sent")
			return
		}
		if pending > *maxLLM-rewriteMisses {
			fatal(fmt.Sprintf("with %d rewrite call(s) that is more than -max-llm-requests %d; raise it deliberately if this is expected",
				rewriteMisses, *maxLLM))
		}
	}
	results, err := retrieveAll(ctx, qs, vecs, ids, retriever)
	if err != nil {
		fatal("retrieve: " + err.Error())
	}

	sum := sha256.Sum256(raw)
	run := Run{
		Settings: Settings{
			Variant: *variant, Depth: Depth, Split: *split, Model: upstream.ModelName(), Dimension: upstream.Dimension(),
			LabelsSHA256: hex.EncodeToString(sum[:]), LabelsFrozen: set.LabelsFrozen, CodeCommit: codeCommit(),
			Repositories: set.Repositories, CreatedAt: time.Now().UTC().Format(time.RFC3339),
			Params: paramsFor(*variant, hp, *rerankDepth),
		},
		Questions: results,
	}
	if rr != nil {
		run.Settings.Rerank = rerankInfo(rr.Reranker.Name(), rr.Depth, rr.Stats())
	}
	if rw != nil {
		run.Settings.Rewrite = rewriteInfo(rw.Rewriter.Name(), rw.Stats())
	}
	summarize(&run)
	if base != nil && base.Settings.LabelsSHA256 != run.Settings.LabelsSHA256 {
		fmt.Println("warning: the compared run used a different label file; the numbers are not comparable")
	}

	o := run.Overall
	fmt.Printf("%s: n=%d  R@1 %.3f  R@3 %.3f  R@5 %.3f  R@8 %.3f  MRR@20 %.3f  cov@8 %.3f\n",
		*variant, o.N, o.RecallAt1, o.RecallAt3, o.RecallAt5, o.RecallAt8, o.MRR, o.CoverageAt8)
	if ri := run.Settings.Rerank; ri != nil {
		fmt.Printf("rerank: %d upstream call(s), %d from cache, %d fallback(s) %v, median %d ms, p90 %d ms, max %d ms\n",
			ri.UpstreamCalls, ri.CacheHits, ri.FallbackCount, ri.Fallbacks, ri.MedianMS, ri.P90MS, ri.MaxMS)
	}
	if wi := run.Settings.Rewrite; wi != nil {
		fmt.Printf("rewrite: %d upstream call(s), %d from cache, %d fallback(s) %v, median %d ms, p90 %d ms, max %d ms\n",
			wi.UpstreamCalls, wi.CacheHits, wi.FallbackCount, wi.Fallbacks, wi.MedianMS, wi.P90MS, wi.MaxMS)
	}
	if *outPath != "" {
		data, _ := json.MarshalIndent(run, "", " ")
		writeFile(*outPath, append(data, '\n'))
	}
	if *reportPath != "" {
		writeFile(*reportPath, []byte(renderReport(run, base)))
	}
}

func checkFlags(variant, split string, final bool) error {
	if !variants[variant] {
		return fmt.Errorf("unknown variant %q", variant)
	}
	switch split {
	case "dev":
	case "test", "all":
		if !final {
			return fmt.Errorf("split %s includes the held-out test questions; pass -final (baseline and final run only)", split)
		}
	default:
		return fmt.Errorf("split must be dev, test or all")
	}
	return nil
}

func checkHybrid(hp hybridParams) error {
	if hp.RRFK < 1 || hp.KeywordWeight <= 0 || hp.KeywordWeight > 1 || hp.TestPenalty <= 0 || hp.TestPenalty > 1 ||
		hp.Pool < 1 || hp.Pool > retrieval.MaxK {
		return fmt.Errorf("hybrid settings out of range: -rrf-k >= 1, -keyword-weight and -test-penalty in (0, 1], -pool 1..%d", retrieval.MaxK)
	}
	return nil
}

// paramsFor records the settings a variant ran with, so every run file names its configuration.
func paramsFor(variant string, hp hybridParams, rerankDepth int) map[string]float64 {
	if !usesHybrid(variant) {
		return nil
	}
	p := map[string]float64{"rrf_k": float64(hp.RRFK), "keyword_weight": hp.KeywordWeight,
		"test_penalty": hp.TestPenalty, "pool": float64(hp.Pool)}
	if usesRerank(variant) {
		p["rerank_depth"] = float64(rerankDepth)
	}
	return p
}

// countPending counts the questions whose ranking is not cached, which is the number of Gemini requests a run makes.
func countPending(ctx context.Context, rr *rerank.Retriever, qs []Question, vecs [][]float32, ids map[string]int64) (int, error) {
	n := 0
	for i, q := range qs {
		p, err := rr.Pending(ctx, retrieval.Query{RepoID: ids[q.Repo], Text: q.Question, Vec: vecs[i], K: Depth})
		if err != nil {
			return 0, fmt.Errorf("%s: %w", q.ID, err)
		}
		if p {
			n++
		}
	}
	return n, nil
}

// pacer returns a Pace function that spaces calls at least gap apart.
func pacer(gap time.Duration) func(ctx context.Context) error {
	var last time.Time
	return func(ctx context.Context) error {
		if wait := gap - time.Since(last); !last.IsZero() && wait > 0 {
			t := time.NewTimer(wait)
			defer t.Stop()
			select {
			case <-t.C:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		last = time.Now()
		return nil
	}
}

func readRun(path string) (Run, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Run{}, err
	}
	var r Run
	if err := json.Unmarshal(data, &r); err != nil {
		return Run{}, fmt.Errorf("read %s: %w", path, err)
	}
	return r, nil
}

// codeCommit names the code that produced a run: the short commit, plus "-dirty" with uncommitted changes.
func codeCommit() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	c := strings.TrimSpace(string(out))
	if exec.Command("git", "diff", "--quiet", "HEAD", "--", ".").Run() != nil {
		c += "-dirty"
	}
	return c
}

func writeFile(path string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fatal(err.Error())
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fatal(err.Error())
	}
	fmt.Println("wrote", path)
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "eval:", msg)
	os.Exit(1)
}
