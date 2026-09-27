// eval measures retrieval against the labelled evaluation set (docs/phase2/eval.json) and writes a JSON run plus
// a Markdown report. Question vectors come from the persistent question cache, so only the first run of a new
// set spends embedding requests; -dry-run shows that cost without sending anything.
//
//	go run ./cmd/eval -dry-run
//	go run ./cmd/eval -split dev -out ../docs/phase2/runs/x.json -report ../docs/phase2/x.md -compare ../docs/phase2/runs/baseline.json
//	go run ./cmd/eval -split all -final -out ../docs/phase2/runs/baseline.json -report ../docs/phase2/baseline.md
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
	"repopilot/api/internal/repos"
	"repopilot/api/internal/retrieval"
)

var variants = map[string]bool{"vector": true}

func main() {
	questionsPath := flag.String("questions", "../docs/phase2/eval.json", "evaluation set")
	variant := flag.String("variant", "vector", "retrieval variant: vector")
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
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
	if *dryRun {
		fmt.Println("dry run: setup ok, nothing sent")
		return
	}
	if requests > *maxRequests {
		fatal(fmt.Sprintf("that is more than -max-requests %d; raise it deliberately if this is expected", *maxRequests))
	}

	vecs, err := embedder.EmbedQueries(ctx, texts)
	if err != nil {
		fatal("embed questions: " + err.Error())
	}
	searcher := retrieval.NewPgSearcher(pool, upstream.ModelName(), upstream.Dimension())
	results, err := retrieveAll(ctx, qs, vecs, ids, searcher)
	if err != nil {
		fatal("retrieve: " + err.Error())
	}

	sum := sha256.Sum256(raw)
	run := Run{
		Settings: Settings{
			Variant: *variant, Depth: Depth, Split: *split, Model: upstream.ModelName(), Dimension: upstream.Dimension(),
			LabelsSHA256: hex.EncodeToString(sum[:]), LabelsFrozen: set.LabelsFrozen, CodeCommit: codeCommit(),
			Repositories: set.Repositories, CreatedAt: time.Now().UTC().Format(time.RFC3339),
		},
		Questions: results,
	}
	summarize(&run)
	if base != nil && base.Settings.LabelsSHA256 != run.Settings.LabelsSHA256 {
		fmt.Println("warning: the compared run used a different label file; the numbers are not comparable")
	}

	o := run.Overall
	fmt.Printf("%s: n=%d  R@1 %.3f  R@3 %.3f  R@5 %.3f  R@8 %.3f  MRR@20 %.3f  cov@8 %.3f\n",
		*variant, o.N, o.RecallAt1, o.RecallAt3, o.RecallAt5, o.RecallAt8, o.MRR, o.CoverageAt8)
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
