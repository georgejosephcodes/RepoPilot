package retrieval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLanguagesMatchSharedFixture(t *testing.T) {
	// The same fixture is checked against the worker's scan.LANGUAGES (worker/tests/test_languages_fixture.py).
	data, err := os.ReadFile("../../../testdata/languages.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Languages []string `json:"languages"`
	}
	if err := json.Unmarshal(data, &fx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fx.Languages, Languages) {
		t.Errorf("fixture %v, Go %v", fx.Languages, Languages)
	}
	if !slices.IsSorted(Languages) {
		t.Error("Languages must be sorted")
	}
}

func TestValidateFilter(t *testing.T) {
	ok := []Filter{
		{},
		{Languages: []string{"go"}},
		{Languages: []string{"python", "typescript"}, PathPrefix: "src/"},
		{PathPrefix: "api/internal/"},
		{PathPrefix: "README"},
		{PathPrefix: "a..b/c"}, // ".." only counts as a whole segment
	}
	for _, f := range ok {
		if err := ValidateFilter(f); err != nil {
			t.Errorf("%+v: %v", f, err)
		}
	}
	bad := map[string]Filter{
		"unknown language": {Languages: []string{"rust"}},
		"case":             {Languages: []string{"Go"}},
		"too many":         {Languages: []string{"go", "go", "go", "go", "go", "go", "go", "go", "go", "go", "go"}},
		"absolute":         {PathPrefix: "/etc"},
		"dotdot":           {PathPrefix: "../x"},
		"dotdot inside":    {PathPrefix: "a/../b"},
		"dotdot end":       {PathPrefix: "a/.."},
		"backslash":        {PathPrefix: `api\x`},
		"control":          {PathPrefix: "api\n"},
		"nul":              {PathPrefix: "api\x00"},
		"too long":         {PathPrefix: strings.Repeat("a", MaxPathPrefixLength+1)},
	}
	for name, f := range bad {
		if err := ValidateFilter(f); !errors.Is(err, ErrInvalidFilter) {
			t.Errorf("%s: err = %v, want ErrInvalidFilter", name, err)
		}
	}
	if err := ValidateFilter(Filter{Languages: []string{strings.Repeat("x", 500)}}); err == nil || len(err.Error()) > 200 {
		t.Errorf("a long unknown language must be cut in the message: %v", err)
	}
}

func TestFilterEmptyAndSQLLanguages(t *testing.T) {
	if !(Filter{}).Empty() || (Filter{PathPrefix: "a"}).Empty() || (Filter{Languages: []string{"go"}}).Empty() {
		t.Error("Empty is wrong")
	}
	if l := (Filter{}).sqlLanguages(); l == nil || len(l) != 0 {
		t.Errorf("sqlLanguages must be an empty non-nil slice, got %#v", l)
	}
}

func TestHybridPassesFilterAndPoolToBothLists(t *testing.T) {
	h, v, k := hybrid([]Chunk{ch(1, "a")}, []Chunk{ch(2, "b")})
	h.Pool = 7
	f := Filter{Languages: []string{"go"}, PathPrefix: "api/"}
	if _, err := h.Retrieve(context.Background(), Query{RepoID: 1, Text: "q", K: 3, Filter: f}); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]*fakeRetriever{"vector": v, "keyword": k} {
		if r.gotK != 7 || !reflect.DeepEqual(r.gotFilter, f) {
			t.Errorf("%s got k %d filter %+v", name, r.gotK, r.gotFilter)
		}
	}
}

type fakeSearcher struct{ chunks []Chunk }

func (f fakeSearcher) Search(_ context.Context, _ int64, _ []float32, k int, _ Filter) ([]Chunk, error) {
	return f.chunks[:min(k, len(f.chunks))], nil
}

func TestTraceIsFilledByVectorAndHybrid(t *testing.T) {
	vec := VectorRetriever{Searcher: fakeSearcher{chunks: []Chunk{ch(1, "a"), ch(2, "b")}}}
	ctx, tr := WithTrace(context.Background())
	if _, err := vec.Retrieve(ctx, Query{RepoID: 1, K: 5}); err != nil {
		t.Fatal(err)
	}
	if d := tr.Snapshot(); !reflect.DeepEqual(d.Vector, []int64{1, 2}) || d.Fused != nil || d.Keyword != nil {
		t.Errorf("vector trace %+v", d)
	}

	h, _, _ := hybrid([]Chunk{ch(1, "a"), ch(2, "b")}, []Chunk{ch(2, "b"), ch(3, "c")})
	h.Vector = vec
	ctx, tr = WithTrace(context.Background())
	got, _ := h.Retrieve(ctx, Query{RepoID: 1, Text: "q", K: 2})
	if d := tr.Snapshot(); !reflect.DeepEqual(d.Vector, []int64{1, 2}) || !reflect.DeepEqual(d.Fused, ChunkIDs(got)) || len(d.Fused) != 2 {
		t.Errorf("hybrid trace %+v", d)
	}

	// no trace in the context: nothing breaks
	if _, err := h.Retrieve(context.Background(), Query{RepoID: 1, Text: "q", K: 2}); err != nil {
		t.Fatal(err)
	}
	var nilTrace *Trace
	nilTrace.Record(func(d *TraceData) { d.VectorMS = 1 })
	if nilTrace.Snapshot().VectorMS != 0 {
		t.Error("nil trace recorded something")
	}
}

func TestTraceIsSafeForConcurrentUse(t *testing.T) {
	_, tr := WithTrace(context.Background())
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.Record(func(d *TraceData) { d.VectorMS += 1; d.Vector = append(d.Vector, int64(i)) })
			_ = tr.Snapshot()
		}()
	}
	wg.Wait()
	if d := tr.Snapshot(); d.VectorMS != 50 || len(d.Vector) != 50 {
		t.Errorf("lost updates: %+v", d)
	}
}

// ---- database tests

func addFileChunk(t *testing.T, pool *pgxpool.Pool, repoID int64, file, language, content string, vec []float32) int64 {
	t.Helper()
	literal, err := VectorLiteral(vec, dim)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	err = pool.QueryRow(context.Background(),
		`INSERT INTO chunks (repo_id, commit_sha, file_path, language, symbol, kind, start_line, end_line, content, embedding, embed_model)
		 VALUES ($1, 'c', $2, $3, NULL, 'function', 1, 3, $4, $5::text::halfvec, $6) RETURNING id`,
		repoID, file, language, content, literal, model).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// seedFilterRepo: five chunks in two languages and three folders, all mentioning "parse config", at distinct
// distances from basis(0).
func seedFilterRepo(t *testing.T, pool *pgxpool.Pool) int64 {
	repo := newRepo(t, pool)
	addFileChunk(t, pool, repo, "api/config.go", "go", "func parseConfig() {} // parse config", mix(0, 1, 1, 0.1))
	addFileChunk(t, pool, repo, "api/server.go", "go", "func serve() { parseConfig() } // parse config", mix(0, 1, 1, 0.3))
	addFileChunk(t, pool, repo, "worker/config.py", "python", "def parse_config(): pass  # parse config", mix(0, 1, 1, 0.5))
	addFileChunk(t, pool, repo, "worker/main.py", "python", "parse_config()  # parse config config", mix(0, 1, 1, 0.7))
	addFileChunk(t, pool, repo, "apiary/x.go", "go", "// parse config for bees", mix(0, 1, 1, 0.9))
	return repo
}

func filterByFile(cs []Chunk, keep func(Chunk) bool) []string {
	var out []string
	for _, c := range cs {
		if keep(c) {
			out = append(out, c.FilePath)
		}
	}
	return out
}

func TestFiltersOverPostgres(t *testing.T) {
	pool := testPool(t)
	repo := seedFilterRepo(t, pool)
	ctx := context.Background()
	retrievers := map[string]Retriever{
		"vector": VectorRetriever{Searcher: searcher(pool)},
		"tsrank": NewKeywordRetriever(pool, ScoreTSRank),
		"bm25":   NewKeywordRetriever(pool, ScoreBM25),
	}
	filters := map[string]struct {
		f    Filter
		keep func(Chunk) bool
	}{
		"language":  {Filter{Languages: []string{"python"}}, func(c Chunk) bool { return c.Language == "python" }},
		"prefix":    {Filter{PathPrefix: "api/"}, func(c Chunk) bool { return strings.HasPrefix(c.FilePath, "api/") }},
		"both":      {Filter{Languages: []string{"go"}, PathPrefix: "api"}, func(c Chunk) bool { return c.Language == "go" && strings.HasPrefix(c.FilePath, "api") }},
		"two langs": {Filter{Languages: []string{"go", "python"}}, func(c Chunk) bool { return true }},
	}
	for rname, r := range retrievers {
		all, err := r.Retrieve(ctx, Query{RepoID: repo, Text: "parse config", Vec: basis(0), K: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 5 {
			t.Fatalf("%s unfiltered: got %v", rname, files(all))
		}
		for fname, fc := range filters {
			got, err := r.Retrieve(ctx, Query{RepoID: repo, Text: "parse config", Vec: basis(0), K: 10, Filter: fc.f})
			if err != nil {
				t.Fatalf("%s %s: %v", rname, fname, err)
			}
			// a filter only removes chunks: the survivors keep their unfiltered order and scores
			if want := filterByFile(all, fc.keep); !reflect.DeepEqual(files(got), want) {
				t.Errorf("%s %s: got %v, want %v", rname, fname, files(got), want)
			}
			for _, g := range got {
				for _, a := range all {
					if a.ID == g.ID && (a.Score != g.Score || a.Distance != g.Distance) {
						t.Errorf("%s %s: %s rescored: %v/%v -> %v/%v", rname, fname, g.FilePath, a.Score, a.Distance, g.Score, g.Distance)
					}
				}
			}
		}
		none, err := r.Retrieve(ctx, Query{RepoID: repo, Text: "parse config", Vec: basis(0), K: 10, Filter: Filter{Languages: []string{"markdown"}}})
		if err != nil || len(none) != 0 {
			t.Errorf("%s no match: got %v, %v", rname, files(none), err)
		}
	}
}

func TestPrefixIsLiteralNotAPattern(t *testing.T) {
	pool := testPool(t)
	repo := newRepo(t, pool)
	addFileChunk(t, pool, repo, "a_b/x.go", "go", "x", basis(0))
	addFileChunk(t, pool, repo, "axb/y.go", "go", "y", basis(0))
	addFileChunk(t, pool, repo, "a%b/z.go", "go", "z", basis(0))
	for prefix, want := range map[string][]string{"a_b/": {"a_b/x.go"}, "a%b/": {"a%b/z.go"}, "a": {"a_b/x.go", "axb/y.go", "a%b/z.go"}} {
		got, err := searcher(pool).Search(context.Background(), repo, basis(0), 10, Filter{PathPrefix: prefix})
		if err != nil {
			t.Fatal(err)
		}
		g := files(got)
		slices.Sort(g)
		w := slices.Clone(want)
		slices.Sort(w)
		if !reflect.DeepEqual(g, w) {
			t.Errorf("prefix %q: got %v, want %v", prefix, g, w)
		}
	}
}
