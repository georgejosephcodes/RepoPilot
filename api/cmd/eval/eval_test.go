package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"repopilot/api/internal/evalmetrics"
	"repopilot/api/internal/repos"
	"repopilot/api/internal/rerank"
	"repopilot/api/internal/retrieval"
	"repopilot/api/internal/rewrite"
)

var update = flag.Bool("update", false, "rewrite the golden report")

const sha = "7f05d217867b2af52b0a28c6d1c91df97e1b5b39"

func label(file string, a, b, g int) evalmetrics.Label {
	return evalmetrics.Label{Span: evalmetrics.Span{File: file, Start: a, End: b}, Grade: g}
}

func testSet() EvalSet {
	return EvalSet{
		Version:      1,
		Repositories: []RepoPin{{Name: "o/r", Commit: sha}},
		Questions: []Question{
			{ID: "q1", Repo: "o/r", Kind: "location", Split: "dev", Question: "where is a", Relevant: []evalmetrics.Label{label("a.go", 10, 20, 2)}},
			{ID: "q2", Repo: "o/r", Kind: "flow", Split: "test", Question: "how does b work", Relevant: []evalmetrics.Label{label("b.go", 1, 5, 2), label("c.go", 1, 5, 2)}},
			{ID: "q3", Repo: "o/r", Kind: "unanswerable", Split: "dev", Question: "how does billing work"},
		},
	}
}

// ---------------------------------------------------------------- the real file

func TestTheRealEvalSetLoads(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "phase2", "eval.json"))
	if err != nil {
		t.Skip("docs/phase2/eval.json not found")
	}
	s, err := loadSet(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Questions) < 70 || len(s.Repositories) != 4 {
		t.Fatalf("%d questions, %d repositories", len(s.Questions), len(s.Repositories))
	}
	if n := len(selectSplit(s.Questions, "dev")) + len(selectSplit(s.Questions, "test")); n != len(s.Questions) {
		t.Fatal("every question must be in dev or test")
	}
}

// ---------------------------------------------------------------- validation and selection

func TestValidateSetFindsProblems(t *testing.T) {
	if p := validateSet(testSet()); len(p) != 0 {
		t.Fatalf("valid set has problems: %v", p)
	}
	cases := map[string]func(*EvalSet){
		"duplicate id":           func(s *EvalSet) { s.Questions[1].ID = "q1" },
		"is not pinned":          func(s *EvalSet) { s.Questions[0].Repo = "x/y" },
		"unknown kind":           func(s *EvalSet) { s.Questions[0].Kind = "vibes" },
		"split must be":          func(s *EvalSet) { s.Questions[0].Split = "train" },
		"empty question":         func(s *EvalSet) { s.Questions[0].Question = " " },
		"without a grade-2 span": func(s *EvalSet) { s.Questions[0].Relevant[0].Grade = 1 },
		"unanswerable question with spans": func(s *EvalSet) {
			s.Questions[2].Relevant = []evalmetrics.Label{label("a.go", 1, 1, 2)}
		},
		"bad span":          func(s *EvalSet) { s.Questions[0].Relevant[0].End = 3 },
		"commit must be 40": func(s *EvalSet) { s.Repositories[0].Commit = "7f05d21" },
	}
	for want, mutate := range cases {
		s := testSet()
		mutate(&s)
		found := false
		for _, p := range validateSet(s) {
			found = found || strings.Contains(p, want)
		}
		if !found {
			t.Errorf("expected a problem containing %q, got %v", want, validateSet(s))
		}
	}
}

func TestLoadSetRejectsBadJSON(t *testing.T) {
	if _, err := loadSet([]byte("{")); err == nil {
		t.Fatal("bad JSON must fail")
	}
}

func TestSelectSplit(t *testing.T) {
	qs := testSet().Questions
	if len(selectSplit(qs, "dev")) != 2 || len(selectSplit(qs, "test")) != 1 || len(selectSplit(qs, "all")) != 3 {
		t.Fatal("split selection wrong")
	}
}

func TestCheckFlags(t *testing.T) {
	if checkFlags("vector", "dev", false) != nil {
		t.Fatal("dev must run without -final")
	}
	for _, s := range []string{"test", "all"} {
		if checkFlags("vector", s, false) == nil {
			t.Fatalf("%s must require -final", s)
		}
		if checkFlags("vector", s, true) != nil {
			t.Fatalf("%s with -final must run", s)
		}
	}
	if checkFlags("keyword-tsrank", "dev", false) != nil || checkFlags("keyword-bm25", "dev", false) != nil || checkFlags("hybrid", "dev", false) != nil {
		t.Fatal("keyword variants must be accepted")
	}
	if checkFlags("hybrid-rerank", "dev", false) != nil {
		t.Fatal("hybrid-rerank must be accepted")
	}
	if checkFlags("reranked", "dev", false) == nil || checkFlags("vector", "train", true) == nil {
		t.Fatal("unknown variant or split must fail")
	}
}

func TestCheckHybrid(t *testing.T) {
	good := hybridParams{RRFK: 60, KeywordWeight: 0.75, TestPenalty: 0.5, Pool: 20}
	if checkHybrid(good) != nil {
		t.Fatal("good settings rejected")
	}
	for _, bad := range []hybridParams{
		{RRFK: 0, KeywordWeight: 1, TestPenalty: 1, Pool: 20},
		{RRFK: 60, KeywordWeight: 0, TestPenalty: 1, Pool: 20},
		{RRFK: 60, KeywordWeight: 1.1, TestPenalty: 1, Pool: 20},
		{RRFK: 60, KeywordWeight: 1, TestPenalty: 0, Pool: 20},
		{RRFK: 60, KeywordWeight: 1, TestPenalty: 1, Pool: 0},
		{RRFK: 60, KeywordWeight: 1, TestPenalty: 1, Pool: 51},
	} {
		if checkHybrid(bad) == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if paramsFor("vector", good, 10) != nil || paramsFor("hybrid", good, 10)["test_penalty"] != 0.5 {
		t.Fatal("params recorded wrongly")
	}
	if _, ok := paramsFor("hybrid", good, 10)["rerank_depth"]; ok {
		t.Fatal("hybrid must not record a rerank depth")
	}
	if p := paramsFor("hybrid-rerank", good, 10); p["rerank_depth"] != 10 || p["keyword_weight"] != 0.75 {
		t.Fatalf("hybrid-rerank params: %v", p)
	}
}

func TestCheckRepos(t *testing.T) {
	commit := sha
	other := "0000000000000000000000000000000000000000"
	pins := []RepoPin{{Name: "o/r", Commit: sha}}
	ids, p := checkRepos(pins, []repos.Repository{{ID: 5, Owner: "O", Name: "R", Status: "ready", CommitSHA: &commit}})
	if len(p) != 0 || ids["o/r"] != 5 {
		t.Fatalf("matching repository rejected: %v %v", p, ids)
	}
	_, p = checkRepos(pins, nil)
	if len(p) != 1 || !strings.Contains(p[0], "not indexed") {
		t.Fatalf("missing repository: %v", p)
	}
	_, p = checkRepos(pins, []repos.Repository{{ID: 5, Owner: "o", Name: "r", Status: "indexing", CommitSHA: &other}})
	if len(p) != 2 || !strings.Contains(p[0], "not ready") || !strings.Contains(p[1], "indexed at 0000000") {
		t.Fatalf("not ready and wrong commit: %v", p)
	}
	_, p = checkRepos(pins, []repos.Repository{{ID: 5, Owner: "o", Name: "r", Status: "ready"}})
	if len(p) != 1 || !strings.Contains(p[0], "indexed at none") {
		t.Fatalf("no commit: %v", p)
	}
}

// ---------------------------------------------------------------- retrieval and scoring

type fakeSearcher struct {
	results map[int64][]retrieval.Chunk
	calls   []int
}

func (f *fakeSearcher) Retrieve(_ context.Context, q retrieval.Query) ([]retrieval.Chunk, error) {
	f.calls = append(f.calls, q.K)
	return f.results[q.RepoID], nil
}

func chunk(id int64, file string, a, b int, dist float64) retrieval.Chunk {
	return retrieval.Chunk{ID: id, FilePath: file, StartLine: a, EndLine: b, Kind: "function", Symbol: "f", Distance: dist}
}

func fixtureRun(t *testing.T) Run {
	t.Helper()
	s := testSet()
	f := &fakeSearcher{results: map[int64][]retrieval.Chunk{7: {
		chunk(1, "x.go", 1, 9, 0.30), chunk(2, "a.go", 12, 18, 0.35), chunk(3, "b.go", 4, 8, 0.40), chunk(4, "z.go", 1, 2, 0.50),
	}}}
	vecs := make([][]float32, len(s.Questions))
	results, err := retrieveAll(context.Background(), s.Questions, vecs, map[string]int64{"o/r": 7}, f)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range f.calls {
		if k != Depth {
			t.Fatalf("searched with k=%d, want %d", k, Depth)
		}
	}
	run := Run{
		Settings: Settings{Variant: "vector", Depth: Depth, Split: "all", Model: "m", Dimension: 4, LabelsSHA256: strings.Repeat("ab", 32),
			CodeCommit: "abc1234", Repositories: s.Repositories, CreatedAt: "2026-09-27T00:00:00Z"},
		Questions: results,
	}
	summarize(&run)
	return run
}

func TestRetrieveAllScoresAndGrades(t *testing.T) {
	run := fixtureRun(t)
	q1, q2, q3 := run.Questions[0], run.Questions[1], run.Questions[2]
	if q1.Score.FirstHitRank != 2 || q1.Retrieved[1].Grade != 2 || q1.Retrieved[0].Grade != 0 {
		t.Fatalf("q1: %+v", q1.Score)
	}
	if q2.Score.FirstHitRank != 3 || q2.Score.CoverageAt8 != 0.5 {
		t.Fatalf("q2: %+v", q2.Score)
	}
	if q3.Score != nil || q3.TopDistance != 0.30 {
		t.Fatalf("q3 must be unscored with its top distance: %+v", q3)
	}
	if run.Overall.N != 2 || run.ByKind["unanswerable"].N != 0 || run.BySplit["dev"].N != 1 {
		t.Fatalf("summaries must leave unanswerable questions out: %+v %+v", run.Overall, run.BySplit)
	}
	if _, err := retrieveAll(context.Background(), testSet().Questions, nil, nil, &fakeSearcher{}); err == nil {
		t.Fatal("a vector count mismatch must fail")
	}
}

func TestReportMatchesGolden(t *testing.T) {
	run := fixtureRun(t)
	base := fixtureRun(t)
	base.Settings.Variant = "baseline"
	base.Questions[0].Score.FirstHitRank = 4 // q1 got better: 4 -> 2
	base.Questions[0].Score.RR = 0.25
	base.Questions[0].Score.RecallAt3 = false
	base.Questions[1].Score.FirstHitRank = 1 // q2 got worse: 1 -> 3
	base.Questions[1].Score.RR = 1
	base.Questions[1].Score.RecallAt1 = true
	summarize(&base)
	got := renderReport(run, nil) + "\n---\n\n" + renderReport(run, &base)
	path := filepath.Join("testdata", "report.golden.md")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run: go test ./cmd/eval -update)", err)
	}
	if got != string(want) {
		t.Fatalf("report differs from %s; if the change is intended, rerun with -update and review the diff", path)
	}
	for _, s := range []string{"1 better, 1 worse, 0 unchanged", "q1 (4 → 2)", "q2 (1 → 3)", "| q3 | o/r | dev | 0.3000 |"} {
		if !strings.Contains(got, s) {
			t.Errorf("report is missing %q", s)
		}
	}
}

func TestPacerSpacesCalls(t *testing.T) {
	pace := pacer(30 * time.Millisecond)
	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := pace(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if took := time.Since(start); took < 60*time.Millisecond {
		t.Errorf("3 paced calls took %v, want at least 60ms", took)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := pace(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled wait must return the context error, got %v", err)
	}
}

func TestReportShowsRerankLine(t *testing.T) {
	run := fixtureRun(t)
	run.Settings.Variant = "hybrid-rerank"
	run.Settings.Rerank = rerankInfo("rerank-v1/m", 10, rerank.Stats{Calls: 2, CacheHits: 1,
		Fallbacks:       map[string]int{"timeout": 1},
		Durations:       []time.Duration{1500 * time.Millisecond, 10 * time.Second},
		CachedDurations: []time.Duration{900 * time.Millisecond}})
	got := renderReport(run, nil)
	want := "- Rerank: `rerank-v1/m`, depth 10; 2 upstream call(s) this run, 1 from cache; 1 fallback(s) (timeout 1); " +
		"rerank time (measured when each ranking was made) median 1500 ms, p90 10000 ms, max 10000 ms\n"
	if !strings.Contains(got, want) {
		t.Errorf("report lacks the rerank line; got:\n%s", got)
	}
}

func TestRewriteVariantsAreKnownAndNamed(t *testing.T) {
	for _, v := range []string{"hybrid-rewrite", "hybrid-rerank-rewrite"} {
		if checkFlags(v, "dev", false) != nil || !usesHybrid(v) || !usesRewrite(v) {
			t.Errorf("%s: not wired", v)
		}
	}
	if usesRerank("hybrid-rewrite") || !usesRerank("hybrid-rerank-rewrite") || usesRewrite("hybrid-rerank") || usesRewrite("hybrid") {
		t.Error("rerank and rewrite detection is wrong")
	}
	good := hybridParams{RRFK: 60, KeywordWeight: 0.75, TestPenalty: 0.5, Pool: 20}
	if p := paramsFor("hybrid-rerank-rewrite", good, 20); p["rerank_depth"] != 20 {
		t.Errorf("params %v", p)
	}
	if _, ok := paramsFor("hybrid-rewrite", good, 20)["rerank_depth"]; ok {
		t.Error("hybrid-rewrite has no rerank depth")
	}
}

func TestReportShowsRewriteLine(t *testing.T) {
	run := fixtureRun(t)
	run.Settings.Variant = "hybrid-rewrite"
	run.Settings.Rewrite = rewriteInfo("rewrite-v1/m", rewrite.Stats{Calls: 1, CacheHits: 2,
		Fallbacks:       map[string]int{"unparseable": 1},
		Durations:       []time.Duration{800 * time.Millisecond},
		CachedDurations: []time.Duration{1200 * time.Millisecond, 900 * time.Millisecond}})
	got := renderReport(run, nil)
	want := "- Rewrite: `rewrite-v1/m`; 1 upstream call(s) this run, 2 from cache; 1 fallback(s) (unparseable 1); " +
		"rewrite time (measured when each rewrite was made) median 900 ms, p90 1200 ms, max 1200 ms\n"
	if !strings.Contains(got, want) {
		t.Errorf("report lacks the rewrite line; got:\n%s", got)
	}
}
