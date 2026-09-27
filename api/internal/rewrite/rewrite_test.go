package rewrite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"repopilot/api/internal/llm"
	"repopilot/api/internal/retrieval"
)

func TestParseTerms(t *testing.T) {
	long := strings.Repeat("x", 100)
	cases := []struct {
		name  string
		reply string
		want  []string
		err   bool
	}{
		{"plain", `{"terms": ["CliRunner", "isolation"]}`, []string{"CliRunner", "isolation"}, false},
		{"fenced", "```json\n{\"terms\": [\"a\"]}\n```", []string{"a"}, false},
		{"text around", `Sure: {"terms": ["a","b"]} hope it helps`, []string{"a", "b"}, false},
		{"non strings dropped", `{"terms": ["a", 3, null, {"x":1}, "b"]}`, []string{"a", "b"}, false},
		{"repeats and blanks dropped", `{"terms": ["a", " a ", "", "  ", "b"]}`, []string{"a", "b"}, false},
		{"inner spaces collapsed", `{"terms": ["styled   output"]}`, []string{"styled output"}, false},
		{"too many cut", `{"terms": ["1","2","3","4","5","6","7","8","9","10","11","12","13","14"]}`,
			[]string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11", "12"}, false},
		{"too long cut", `{"terms": ["` + long + `"]}`, []string{long[:MaxTermLen]}, false},
		{"empty list", `{"terms": []}`, []string{}, false},
		{"second object", `{"x": 1} {"terms": ["a"]}`, []string{"a"}, false},
		{"no terms key", `{"ranking": [1]}`, nil, true},
		{"terms not a list", `{"terms": "a b"}`, nil, true},
		{"broken", `{"terms": ["a"`, nil, true},
		{"no json", `CliRunner, isolation`, nil, true},
	}
	for _, c := range cases {
		got, err := ParseTerms(c.reply)
		if c.err {
			if !errors.Is(err, ErrUnparseable) {
				t.Errorf("%s: err = %v, want ErrUnparseable", c.name, err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

func TestPromptAndName(t *testing.T) {
	if got := BuildPrompt("  where is x?  "); got != "Question:\nwhere is x?" {
		t.Errorf("prompt %q", got)
	}
	if n := len([]rune(BuildPrompt(strings.Repeat("é", 2000)))); n != len("Question:\n")+1000 {
		t.Errorf("long question not cut to 1000 characters: %d", n)
	}
	f := llm.NewFake(`{"terms": ["a"]}`)
	r := LLMRewriter{LLM: f}
	if r.Name() != "rewrite-v1/fake-llm" {
		t.Errorf("name %q", r.Name())
	}
	terms, err := r.Rewrite(context.Background(), "q")
	if err != nil || !reflect.DeepEqual(terms, []string{"a"}) || !strings.Contains(f.System, `{"terms"`) || f.User != "Question:\nq" {
		t.Errorf("terms %v err %v system %q user %q", terms, err, f.System, f.User)
	}
	f.Err = llm.ErrUnavailable
	if _, err := r.Rewrite(context.Background(), "q"); !errors.Is(err, llm.ErrUnavailable) {
		t.Errorf("err = %v", err)
	}
	c := LLMConfig(llm.DefaultConfig())
	if c.Temperature != 0 || c.MaxOutputTokens != 128 {
		t.Errorf("config %+v", c)
	}
}

func TestKeywordText(t *testing.T) {
	if KeywordText("q", nil) != "" {
		t.Error("no terms must leave KeywordText empty, so keyword search uses the question")
	}
	if got := KeywordText(" q ", []string{"a_b", "C"}); got != "q\na_b C" {
		t.Errorf("got %q", got)
	}
}

// fakes

type scripted struct {
	mu    sync.Mutex
	terms []string
	err   error
	block bool
	calls int
}

func (s *scripted) Name() string { return "rewrite-test/fake" }
func (s *scripted) Rewrite(ctx context.Context, _ string) ([]string, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if s.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.terms, s.err
}

type captureBase struct{ got retrieval.Query }

func (c *captureBase) Retrieve(_ context.Context, q retrieval.Query) ([]retrieval.Chunk, error) {
	c.got = q
	return []retrieval.Chunk{{ID: 1}}, nil
}

type memStore struct {
	mu sync.Mutex
	m  map[string]Entry
}

func (s *memStore) Get(k string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[k]
	return e, ok
}
func (s *memStore) Put(k string, e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = e
	return nil
}

func TestRetrieverSetsKeywordTextOnly(t *testing.T) {
	base := &captureBase{}
	r := &Retriever{Base: base, Rewriter: &scripted{terms: []string{"CliRunner", "isolation"}}}
	q := retrieval.Query{RepoID: 3, Text: "where is output captured?", Vec: []float32{1}, K: 8}
	if _, err := r.Retrieve(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	want := q
	want.KeywordText = "where is output captured?\nCliRunner isolation"
	if !reflect.DeepEqual(base.got, want) {
		t.Errorf("base got %+v, want %+v", base.got, want)
	}
}

func TestFailuresFallBackToThePlainQuestion(t *testing.T) {
	for reason, s := range map[string]*scripted{
		"error":       {err: errors.New("503")},
		"unparseable": {err: ErrUnparseable},
		"timeout":     {block: true},
	} {
		base := &captureBase{}
		cache := &memStore{m: map[string]Entry{}}
		r := &Retriever{Base: base, Rewriter: s, Cache: cache, Timeout: 20 * time.Millisecond}
		if _, err := r.Retrieve(context.Background(), retrieval.Query{RepoID: 1, Text: "q", K: 3}); err != nil {
			t.Fatalf("%s: %v", reason, err)
		}
		st := r.Stats()
		if base.got.KeywordText != "" || st.Fallbacks[reason] != 1 || st.FallbackCount() != 1 || st.Calls != 1 || len(cache.m) != 0 {
			t.Errorf("%s: keyword text %q, stats %+v, cached %d", reason, base.got.KeywordText, st, len(cache.m))
		}
	}
}

func TestCancelledContextIsAnError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Retriever{Base: &captureBase{}, Rewriter: &scripted{block: true}, Timeout: time.Minute}
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	if _, err := r.Retrieve(ctx, retrieval.Query{RepoID: 1, Text: "q", K: 3}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v", err)
	}
	if r.Stats().FallbackCount() != 0 {
		t.Error("cancellation counted as a fallback")
	}
}

func TestCacheReuseAndPendingAndPacing(t *testing.T) {
	s := &scripted{terms: []string{"a"}}
	cache := &memStore{m: map[string]Entry{}}
	paced := 0
	r := &Retriever{Base: &captureBase{}, Rewriter: s, Cache: cache, Pace: func(context.Context) error { paced++; return nil }}
	if !r.Pending("q") {
		t.Fatal("an uncached question must be pending")
	}
	r.Retrieve(context.Background(), retrieval.Query{RepoID: 1, Text: "q", K: 3})
	s.terms = []string{"different"} // the cache must win
	base := &captureBase{}
	r.Base = base
	r.Retrieve(context.Background(), retrieval.Query{RepoID: 1, Text: " q ", K: 3})
	if s.calls != 1 || paced != 1 || base.got.KeywordText != "q\na" || r.Pending("q") {
		t.Errorf("calls %d paced %d keyword text %q pending %v", s.calls, paced, base.got.KeywordText, r.Pending("q"))
	}
	st := r.Stats()
	if st.Calls != 1 || st.CacheHits != 1 || len(st.Durations) != 1 || len(st.CachedDurations) != 1 {
		t.Errorf("stats %+v", st)
	}
	e := cache.m[Key(s.Name(), "q")]
	if e.Question != "q" || e.Model != "rewrite-test/fake" || !reflect.DeepEqual(e.Terms, []string{"a"}) {
		t.Errorf("entry %+v", e)
	}
}

func TestKeyStability(t *testing.T) {
	k := Key("rewrite-v1/m", "where is x?")
	if k != Key("rewrite-v1/m", "  where is x?\n") || len(k) != 64 {
		t.Error("key must ignore surrounding space and be a sha256 hex")
	}
	for _, other := range []string{Key("rewrite-v2/m", "where is x?"), Key("rewrite-v1/n", "where is x?"), Key("rewrite-v1/m", "where is y?")} {
		if other == k {
			t.Error("prompt version, model and question must all change the key")
		}
	}
}

func TestFileStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "rewrites.json")
	s, err := OpenFileStore(path)
	if err != nil || s.Len() != 0 {
		t.Fatalf("new store: %v, %d", err, s.Len())
	}
	e := Entry{Question: "q", Terms: []string{"a", "b"}, Model: "m", TookMS: 1234}
	if err := s.Put("k1", e); err != nil {
		t.Fatal(err)
	}
	again, err := OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := again.Get("k1"); !ok || !reflect.DeepEqual(got, e) || again.Len() != 1 {
		t.Errorf("reloaded %+v, %v", got, ok)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("the temporary file was left behind")
	}
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenFileStore(path); err == nil {
		t.Error("a broken file must be an error, not an empty cache")
	}
}

func TestPercentile(t *testing.T) {
	s := Stats{Durations: []time.Duration{3 * time.Second, 1 * time.Second}, CachedDurations: []time.Duration{2 * time.Second}}
	if s.Percentile(50) != 2*time.Second || s.Percentile(100) != 3*time.Second || (Stats{}).Percentile(50) != 0 {
		t.Errorf("median %v max %v", s.Percentile(50), s.Percentile(100))
	}
}
