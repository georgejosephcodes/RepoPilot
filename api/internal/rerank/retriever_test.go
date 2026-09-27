package rerank

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"repopilot/api/internal/retrieval"
)

// baseList is a fake Base retriever returning a fixed list, cut to k.
type baseList struct {
	chunks []retrieval.Chunk
	err    error
	lastK  int
}

func (b *baseList) Retrieve(_ context.Context, _ int64, _ string, _ []float32, k int) ([]retrieval.Chunk, error) {
	b.lastK = k
	if b.err != nil {
		return nil, b.err
	}
	return b.chunks[:min(k, len(b.chunks))], nil
}

// scripted is a fake Reranker.
type scripted struct {
	mu      sync.Mutex
	ranking []int
	err     error
	block   bool // wait for ctx to end
	calls   int
	seen    int // candidates shown on the last call
}

func (s *scripted) Name() string { return "test-v1/fake" }

func (s *scripted) Rerank(ctx context.Context, _ string, cands []retrieval.Chunk) ([]int, error) {
	s.mu.Lock()
	s.calls++
	s.seen = len(cands)
	s.mu.Unlock()
	if s.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.ranking, s.err
}

// memStore is an in-memory Store.
type memStore struct {
	mu     sync.Mutex
	m      map[string]Entry
	getErr error
	putErr error
	puts   int
}

func newMem() *memStore { return &memStore{m: map[string]Entry{}} }

func (s *memStore) Get(_ context.Context, key string) (Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.getErr != nil {
		return Entry{}, false, s.getErr
	}
	e, ok := s.m[key]
	return e, ok, nil
}

func (s *memStore) Put(_ context.Context, key, _ string, e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puts++
	if s.putErr != nil {
		return s.putErr
	}
	s.m[key] = Entry{Ranking: append([]int(nil), e.Ranking...), Took: e.Took}
	return nil
}

func chunks(n int) []retrieval.Chunk {
	out := make([]retrieval.Chunk, n)
	for i := range out {
		out[i] = retrieval.Chunk{ID: int64(100 + i), FilePath: "f.go", Content: string(rune('a' + i))}
	}
	return out
}

func ids(cs []retrieval.Chunk) []int64 {
	out := make([]int64, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

func TestApplyPutsRankedFirstThenBaseOrder(t *testing.T) {
	base := chunks(5)
	got := ids(Apply(base, []int{3, 1, 9, -1, 3}))
	if want := []int64{103, 101, 100, 102, 104}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
	if got := ids(Apply(base, nil)); !reflect.DeepEqual(got, ids(base)) {
		t.Errorf("nil ranking should keep base order: %v", got)
	}
	if base[0].ID != 100 || base[3].ID != 103 {
		t.Error("Apply modified base")
	}
}

func TestRetrieveReranksOnlyTheHeadAndReturnsK(t *testing.T) {
	b := &baseList{chunks: chunks(20)}
	s := &scripted{ranking: []int{4, 2}}
	r := &Retriever{Base: b, Reranker: s, Depth: 5}
	got, err := r.Retrieve(context.Background(), 1, "q", nil, 8)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{104, 102, 100, 101, 103, 105, 106, 107}; !reflect.DeepEqual(ids(got), want) {
		t.Errorf("got %v want %v", ids(got), want)
	}
	if b.lastK != 8 || s.seen != 5 {
		t.Errorf("base k %d (want 8), reranker saw %d (want 5)", b.lastK, s.seen)
	}

	// Depth above k: fetch Depth, rerank all of them, return k.
	r = &Retriever{Base: b, Reranker: &scripted{ranking: []int{9}}, Depth: 10}
	got, _ = r.Retrieve(context.Background(), 1, "q", nil, 3)
	if b.lastK != 10 || !reflect.DeepEqual(ids(got), []int64{109, 100, 101}) {
		t.Errorf("base k %d, got %v", b.lastK, ids(got))
	}
}

func TestEveryFailureFallsBackToBaseOrder(t *testing.T) {
	cases := []struct {
		name   string
		s      *scripted
		reason string
	}{
		{"error", &scripted{err: errors.New("503")}, "error"},
		{"timeout", &scripted{block: true}, "timeout"},
		{"unparseable", &scripted{err: ErrUnparseable}, "unparseable"},
	}
	for _, c := range cases {
		cache := newMem()
		r := &Retriever{Base: &baseList{chunks: chunks(6)}, Reranker: c.s, Depth: 6, Cache: cache, Timeout: 20 * time.Millisecond}
		got, err := r.Retrieve(context.Background(), 1, "q", nil, 6)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !reflect.DeepEqual(ids(got), ids(chunks(6))) {
			t.Errorf("%s: got %v, want base order", c.name, ids(got))
		}
		st := r.Stats()
		if st.Fallbacks[c.reason] != 1 || st.FallbackCount() != 1 || st.Calls != 1 || len(st.Durations) != 1 {
			t.Errorf("%s: stats %+v", c.name, st)
		}
		if cache.puts != 0 {
			t.Errorf("%s: a failed ranking was cached", c.name)
		}
	}
}

func TestCancelledContextIsAnErrorNotAFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &scripted{block: true}
	r := &Retriever{Base: &baseList{chunks: chunks(3)}, Reranker: s, Depth: 3, Timeout: time.Minute}
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	if _, err := r.Retrieve(ctx, 1, "q", nil, 3); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if r.Stats().FallbackCount() != 0 {
		t.Error("cancellation counted as a fallback")
	}
}

func TestCacheReusesRankingAndSkipsUpstream(t *testing.T) {
	cache := newMem()
	s := &scripted{ranking: []int{2}}
	r := &Retriever{Base: &baseList{chunks: chunks(4)}, Reranker: s, Depth: 4, Cache: cache}
	first, _ := r.Retrieve(context.Background(), 1, "q", nil, 4)
	s.ranking = []int{1} // a different answer now; the cache must win
	second, _ := r.Retrieve(context.Background(), 1, " q ", nil, 4)
	if s.calls != 1 || !reflect.DeepEqual(ids(first), ids(second)) || ids(first)[0] != 102 {
		t.Errorf("calls %d, first %v, second %v", s.calls, ids(first), ids(second))
	}
	st := r.Stats()
	if st.Calls != 1 || st.CacheHits != 1 || len(st.Durations) != 1 || len(st.CachedDurations) != 1 {
		t.Errorf("stats %+v", st)
	}
	if st.CachedDurations[0] != st.Durations[0] {
		t.Errorf("a cache hit should report the time measured when the ranking was made: %v vs %v", st.CachedDurations, st.Durations)
	}

	// A broken cache is ignored: reads fall through to the reranker, writes are dropped.
	broken := &memStore{m: map[string]Entry{}, getErr: errors.New("db down"), putErr: errors.New("db down")}
	r = &Retriever{Base: &baseList{chunks: chunks(4)}, Reranker: &scripted{ranking: []int{3}}, Depth: 4, Cache: broken}
	got, err := r.Retrieve(context.Background(), 1, "q", nil, 4)
	if err != nil || ids(got)[0] != 103 {
		t.Errorf("broken cache: %v %v", ids(got), err)
	}
}

func TestPendingCountsOnlyCacheMisses(t *testing.T) {
	cache := newMem()
	s := &scripted{ranking: []int{0}}
	r := &Retriever{Base: &baseList{chunks: chunks(4)}, Reranker: s, Depth: 4, Cache: cache}
	ctx := context.Background()
	if p, err := r.Pending(ctx, 1, "q", nil, 4); err != nil || !p {
		t.Fatalf("before: pending %v %v", p, err)
	}
	r.Retrieve(ctx, 1, "q", nil, 4)
	if p, err := r.Pending(ctx, 1, "q", nil, 4); err != nil || p {
		t.Errorf("after: pending %v %v", p, err)
	}
	if s.calls != 1 {
		t.Errorf("Pending called the reranker: %d calls", s.calls)
	}
	empty := &Retriever{Base: &baseList{}, Reranker: s, Depth: 4, Cache: cache}
	if p, _ := empty.Pending(ctx, 1, "q", nil, 4); p {
		t.Error("no candidates needs no call")
	}
}

func TestPaceRunsBeforeUpstreamCallsOnly(t *testing.T) {
	paced := 0
	cache := newMem()
	r := &Retriever{Base: &baseList{chunks: chunks(3)}, Reranker: &scripted{ranking: []int{1}}, Depth: 3, Cache: cache,
		Pace: func(context.Context) error { paced++; return nil }}
	r.Retrieve(context.Background(), 1, "q", nil, 3)
	r.Retrieve(context.Background(), 1, "q", nil, 3) // cache hit
	if paced != 1 {
		t.Errorf("paced %d times, want 1", paced)
	}
	r.Pace = func(context.Context) error { return context.Canceled }
	if _, err := r.Retrieve(context.Background(), 1, "other", nil, 3); !errors.Is(err, context.Canceled) {
		t.Errorf("pace error: %v", err)
	}
}

func TestRetrieveValidates(t *testing.T) {
	b := &baseList{chunks: chunks(3)}
	for _, r := range []*Retriever{
		{Base: nil, Reranker: &scripted{}, Depth: 3},
		{Base: b, Reranker: nil, Depth: 3},
		{Base: b, Reranker: &scripted{}, Depth: 0},
		{Base: b, Reranker: &scripted{}, Depth: retrieval.MaxK + 1},
	} {
		if _, err := r.Retrieve(context.Background(), 1, "q", nil, 3); !errors.Is(err, ErrBadConfig) {
			t.Errorf("%+v: err %v", r, err)
		}
	}
	r := &Retriever{Base: b, Reranker: &scripted{}, Depth: 3}
	for _, k := range []int{0, retrieval.MaxK + 1} {
		if _, err := r.Retrieve(context.Background(), 1, "q", nil, k); !errors.Is(err, retrieval.ErrInvalidK) {
			t.Errorf("k %d: err %v", k, err)
		}
	}
	b.err = errors.New("db")
	if _, err := r.Retrieve(context.Background(), 1, "q", nil, 3); err == nil {
		t.Error("base error swallowed")
	}
}

func TestKeyIsStableAndSensitive(t *testing.T) {
	c := chunks(3)
	k := Key("v1/m", "q", c)
	if k != Key("v1/m", "  q\n", c) || len(k) != 64 {
		t.Errorf("key not stable across whitespace: %s", k)
	}
	changed := func(name string, other string) {
		if other == k {
			t.Errorf("key did not change with %s", name)
		}
	}
	changed("question", Key("v1/m", "q2", c))
	changed("model or version", Key("v2/m", "q", c))
	changed("order", Key("v1/m", "q", []retrieval.Chunk{c[1], c[0], c[2]}))
	changed("candidate set", Key("v1/m", "q", c[:2]))
	edited := chunks(3)
	edited[2].Content = "edited"
	changed("content", Key("v1/m", "q", edited))
	moved := chunks(3)
	moved[0].ID = 999
	changed("id", Key("v1/m", "q", moved))
	// Field boundaries are length-prefixed, so shifting text between fields changes the key.
	if Key("ab", "c", nil) == Key("a", "bc", nil) {
		t.Error("key is ambiguous across fields")
	}
}

func TestStatsPercentile(t *testing.T) {
	var s Stats
	if s.Percentile(50) != 0 {
		t.Error("empty")
	}
	for _, ms := range []int{5, 1, 3} {
		s.Durations = append(s.Durations, time.Duration(ms)*time.Millisecond)
	}
	for _, ms := range []int{2, 4} { // cached rankings count with their original times
		s.CachedDurations = append(s.CachedDurations, time.Duration(ms)*time.Millisecond)
	}
	if s.Percentile(50) != 3*time.Millisecond || s.Percentile(90) != 5*time.Millisecond || s.Percentile(100) != 5*time.Millisecond {
		t.Errorf("p50 %v p90 %v max %v", s.Percentile(50), s.Percentile(90), s.Percentile(100))
	}
}
