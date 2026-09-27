package embed

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// memStore is an in-memory QueryStore that records what it was asked and can be told to fail.
type memStore struct {
	mu      sync.Mutex
	vecs    map[string][]float32 // key: model + "\x00" + hash
	getErr  error
	putErr  error
	gets    int
	puts    int
	lastPut map[string][]float32
}

func newMemStore() *memStore { return &memStore{vecs: map[string][]float32{}} }

func (m *memStore) Get(_ context.Context, model string, hashes []string) (map[string][]float32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gets++
	if m.getErr != nil {
		return nil, m.getErr
	}
	out := map[string][]float32{}
	for _, h := range hashes {
		if v, ok := m.vecs[model+"\x00"+h]; ok {
			out[h] = copyVec(v)
		}
	}
	return out, nil
}

func (m *memStore) Put(_ context.Context, model string, vecs map[string][]float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.puts++
	m.lastPut = vecs
	if m.putErr != nil {
		return m.putErr
	}
	for h, v := range vecs {
		m.vecs[model+"\x00"+h] = copyVec(v)
	}
	return nil
}

// batchRecorder wraps Fake and records the size of every upstream batch and the texts sent.
type batchRecorder struct {
	*Fake
	maxChars int
	sizes    []int
	sent     []string
}

func (b *batchRecorder) Prepare(s string) string {
	if b.maxChars > 0 && len([]rune(s)) > b.maxChars {
		return string([]rune(s)[:b.maxChars])
	}
	return s
}

func (b *batchRecorder) EmbedQueries(ctx context.Context, texts []string) ([][]float32, error) {
	b.sizes = append(b.sizes, len(texts))
	b.sent = append(b.sent, texts...)
	return b.Fake.EmbedQueries(ctx, texts)
}

func equalVec(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPersistentMissThenHit(t *testing.T) {
	inner := &batchRecorder{Fake: NewFake(8)}
	store := newMemStore()
	p := NewPersistent(inner, store, 0)
	ctx := context.Background()

	first, err := p.EmbedQuery(ctx, "where is auth?")
	if err != nil {
		t.Fatal(err)
	}
	if inner.Calls() != 1 || store.puts != 1 {
		t.Fatalf("first call: upstream %d, puts %d; want 1 and 1", inner.Calls(), store.puts)
	}
	second, err := p.EmbedQuery(ctx, "where is auth?")
	if err != nil {
		t.Fatal(err)
	}
	if inner.Calls() != 1 {
		t.Fatalf("a hit must not call upstream, calls = %d", inner.Calls())
	}
	if !equalVec(first, second) {
		t.Fatal("hit returned a different vector")
	}
}

func TestPersistentSurvivesARestart(t *testing.T) {
	store := newMemStore()
	ctx := context.Background()
	a := &batchRecorder{Fake: NewFake(8)}
	if _, err := NewPersistent(a, store, 0).EmbedQuery(ctx, "q"); err != nil {
		t.Fatal(err)
	}
	b := &batchRecorder{Fake: NewFake(8)} // a new process with the same store
	if _, err := NewPersistent(b, store, 0).EmbedQuery(ctx, "q"); err != nil {
		t.Fatal(err)
	}
	if b.Calls() != 0 {
		t.Fatalf("second process called upstream %d times, want 0", b.Calls())
	}
}

func TestPersistentTrimsAndCutsBeforeHashing(t *testing.T) {
	inner := &batchRecorder{Fake: NewFake(8), maxChars: 5}
	store := newMemStore()
	p := NewPersistent(inner, store, 0)
	ctx := context.Background()
	if _, err := p.EmbedQuery(ctx, "  abcdefgh  "); err != nil {
		t.Fatal(err)
	}
	if inner.sent[0] != "abcde" {
		t.Fatalf("sent %q, want the trimmed and cut text", inner.sent[0])
	}
	if _, ok := store.lastPut[TextHash("abcde")]; !ok {
		t.Fatal("the hash must be taken over the prepared text")
	}
	if _, err := p.EmbedQuery(ctx, "abcdeXYZ"); err != nil { // same prepared text
		t.Fatal(err)
	}
	if inner.Calls() != 1 {
		t.Fatalf("texts equal after preparation must share one vector, calls = %d", inner.Calls())
	}
}

func TestPersistentBatchKeepsOrderAndSplits(t *testing.T) {
	inner := &batchRecorder{Fake: NewFake(8)}
	store := newMemStore()
	p := NewPersistent(inner, store, 3)
	ctx := context.Background()
	if _, err := p.EmbedQuery(ctx, "q2"); err != nil { // pre-cache one
		t.Fatal(err)
	}
	inner.sizes = nil

	texts := []string{"q0", "q1", "q2", "q3", "q1", "q4", "q5", "q6"}
	got, err := p.EmbedQueries(ctx, texts)
	if err != nil {
		t.Fatal(err)
	}
	// 7 distinct texts, 1 cached: 6 misses in batches of 3.
	if len(inner.sizes) != 2 || inner.sizes[0] != 3 || inner.sizes[1] != 3 {
		t.Fatalf("batch sizes = %v, want [3 3]", inner.sizes)
	}
	for i, text := range texts {
		want, _ := NewFake(8).EmbedQuery(ctx, text)
		if !equalVec(got[i], want) {
			t.Fatalf("vector %d (%q) is not the vector of that text", i, text)
		}
	}
	got[0][0] = 42 // callers must not be able to change cached vectors
	again, _ := p.EmbedQuery(ctx, "q0")
	if again[0] == 42 {
		t.Fatal("returned vector aliases the cache")
	}
}

func TestPersistentReadFailureStillAnswers(t *testing.T) {
	inner := &batchRecorder{Fake: NewFake(8)}
	store := newMemStore()
	store.getErr = errors.New("connection refused")
	vec, err := NewPersistent(inner, store, 0).EmbedQuery(context.Background(), "q")
	if err != nil || len(vec) != 8 {
		t.Fatalf("got %v, %v; want an answer despite the cache", vec, err)
	}
	if inner.Calls() != 1 {
		t.Fatalf("calls = %d, want 1", inner.Calls())
	}
}

func TestPersistentWriteFailureStillAnswers(t *testing.T) {
	store := newMemStore()
	store.putErr = errors.New("disk full")
	vec, err := NewPersistent(&batchRecorder{Fake: NewFake(8)}, store, 0).EmbedQuery(context.Background(), "q")
	if err != nil || len(vec) != 8 {
		t.Fatalf("got %v, %v; want an answer despite the cache", vec, err)
	}
}

func TestPersistentUpstreamErrorIsReturnedAndNotStored(t *testing.T) {
	fake := NewFake(8)
	fake.Err = ErrRateLimited
	store := newMemStore()
	_, err := NewPersistent(&batchRecorder{Fake: fake}, store, 0).EmbedQuery(context.Background(), "q")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	if store.puts != 0 {
		t.Fatal("an error must not be stored")
	}
}

func TestPersistentCancelledReadIsNotHidden(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := newMemStore()
	store.getErr = ctx.Err()
	inner := &batchRecorder{Fake: NewFake(8)}
	if _, err := NewPersistent(inner, store, 0).EmbedQuery(ctx, "q"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if inner.Calls() != 0 {
		t.Fatal("a cancelled request must not go upstream")
	}
}

func TestPersistentKeysByModel(t *testing.T) {
	store := newMemStore()
	ctx := context.Background()
	a := &batchRecorder{Fake: NewFake(8)}
	a.Model = "model-a"
	if _, err := NewPersistent(a, store, 0).EmbedQuery(ctx, "q"); err != nil {
		t.Fatal(err)
	}
	b := &batchRecorder{Fake: NewFake(8)}
	b.Model = "model-b"
	if _, err := NewPersistent(b, store, 0).EmbedQuery(ctx, "q"); err != nil {
		t.Fatal(err)
	}
	if b.Calls() != 1 {
		t.Fatal("a different model must never read another model's vector")
	}
}

func TestTextHashIsSHA256Hex(t *testing.T) {
	h := TextHash("abc")
	if h != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("hash = %s", h)
	}
	if strings.ToLower(h) != h || len(h) != 64 {
		t.Fatal("hash must be 64 lowercase hex characters")
	}
}

func TestVectorLiteralRoundTrip(t *testing.T) {
	v := []float32{0.5, -0.25, 1e-3, 0}
	s, err := FormatVectorLiteral(v, 4)
	if err != nil {
		t.Fatal(err)
	}
	if s != "[0.5,-0.25,0.001,0]" {
		t.Fatalf("literal = %s", s)
	}
	back, err := ParseVectorLiteral(" "+s+" ", 4)
	if err != nil || !equalVec(back, v) {
		t.Fatalf("round trip = %v, %v", back, err)
	}
}

func TestVectorLiteralRejectsBadInput(t *testing.T) {
	for _, s := range []string{"", "[]", "0.1,0.2", "[0.1,0.2", "[0.1,x]", "[0.1,NaN]", "[0.1,0.2,0.3]"} {
		if _, err := ParseVectorLiteral(s, 2); !errors.Is(err, ErrBadVectorLiteral) {
			t.Errorf("ParseVectorLiteral(%q) err = %v, want ErrBadVectorLiteral", s, err)
		}
	}
	if _, err := FormatVectorLiteral([]float32{1}, 2); !errors.Is(err, ErrBadVectorLiteral) {
		t.Error("wrong length must fail")
	}
	nan := float32(0)
	nan = nan / nan
	if _, err := FormatVectorLiteral([]float32{nan, 1}, 2); !errors.Is(err, ErrBadVectorLiteral) {
		t.Error("NaN must fail")
	}
}
