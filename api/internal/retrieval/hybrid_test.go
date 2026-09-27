package retrieval

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestIsTestPath(t *testing.T) {
	yes := []string{"tests/test_basic.py", "test/test.ts", "src/__tests__/a.js", "pkg/testdata/x.go", "spec/models/user.rb",
		"a/b/test_x.py", "a/x_test.py", "a/x_test.go", "conftest.py", "tests/conftest.py", "src/a.test.ts", "src/a.spec.jsx",
		"src/A.Test.TSX", "Tests/Thing.py"}
	no := []string{"latest.py", "contest/main.go", "attest.go", "src/testing.py", "src/click/testing.py", "src/test_helpers.go",
		"docs/testing.md", "protest/x.py", "a/tester.ts", "src/spec.ts"}
	for _, p := range yes {
		if !IsTestPath(p) {
			t.Errorf("IsTestPath(%q) = false, want true", p)
		}
	}
	for _, p := range no {
		if IsTestPath(p) {
			t.Errorf("IsTestPath(%q) = true, want false", p)
		}
	}
}

type fakeRetriever struct {
	chunks    []Chunk
	err       error
	gotK      int
	gotQ      string
	gotFilter Filter
	ctxKey    any
}

type ctxKeyT struct{}

func (f *fakeRetriever) Retrieve(ctx context.Context, q Query) ([]Chunk, error) {
	k := q.K
	f.gotK, f.gotQ, f.gotFilter, f.ctxKey = k, q.Text, q.Filter, ctx.Value(ctxKeyT{})
	if f.err != nil {
		return nil, f.err
	}
	if len(f.chunks) > k {
		return f.chunks[:k], nil
	}
	return f.chunks, nil
}

func ch(id int64, file string) Chunk { return Chunk{ID: id, FilePath: file} }

func hybrid(vec, kw []Chunk) (HybridRetriever, *fakeRetriever, *fakeRetriever) {
	v, k := &fakeRetriever{chunks: vec}, &fakeRetriever{chunks: kw}
	return HybridRetriever{Vector: v, Keyword: k, RRFK: 60, KeywordWeight: 1, TestPenalty: 1, Pool: 20}, v, k
}

func ids(cs []Chunk) string {
	var b strings.Builder
	for i, c := range cs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(string(rune('0' + c.ID)))
	}
	return b.String()
}

func TestHybridFusesByRank(t *testing.T) {
	h, _, _ := hybrid([]Chunk{ch(1, "a"), ch(2, "b"), ch(3, "c")}, []Chunk{ch(3, "c"), ch(4, "d")})
	got, err := h.Retrieve(context.Background(), Query{RepoID: 1, Text: "q", K: 10})
	if err != nil {
		t.Fatal(err)
	}
	// 3: 1/63 + 1/61 beats 1 (1/61); 2 and 4 both have 1/62, so the lower id comes first.
	if ids(got) != "3,1,2,4" {
		t.Fatalf("order = %s, want 3,1,2,4", ids(got))
	}
	if want := 1.0/63 + 1.0/61; math.Abs(got[0].Score-want) > 1e-12 {
		t.Fatalf("score = %v, want %v", got[0].Score, want)
	}
}

func TestHybridKeywordWeight(t *testing.T) {
	h, _, _ := hybrid([]Chunk{ch(1, "a")}, []Chunk{ch(2, "b")})
	h.KeywordWeight = 0.5
	got, _ := h.Retrieve(context.Background(), Query{RepoID: 1, Text: "q", K: 10})
	if ids(got) != "1,2" || math.Abs(got[1].Score-0.5/61) > 1e-12 {
		t.Fatalf("got %s scores %v %v", ids(got), got[0].Score, got[1].Score)
	}
}

func TestHybridTestPenalty(t *testing.T) {
	h, _, _ := hybrid([]Chunk{ch(1, "tests/test_a.py"), ch(2, "src/a.py")}, nil)
	got, _ := h.Retrieve(context.Background(), Query{RepoID: 1, Text: "q", K: 10})
	if ids(got) != "1,2" {
		t.Fatalf("without penalty: %s", ids(got))
	}
	h.TestPenalty = 0.5
	got, _ = h.Retrieve(context.Background(), Query{RepoID: 1, Text: "q", K: 10})
	if ids(got) != "2,1" {
		t.Fatalf("with penalty the test file must drop: %s", ids(got))
	}
}

func TestHybridPoolKAndOneEmptyList(t *testing.T) {
	h, v, k := hybrid([]Chunk{ch(1, "a"), ch(2, "b"), ch(3, "c")}, nil)
	h.Pool = 2
	got, err := h.Retrieve(context.Background(), Query{RepoID: 1, Text: "question", K: 1})
	if err != nil {
		t.Fatal(err)
	}
	if v.gotK != 2 || k.gotK != 2 || k.gotQ != "question" {
		t.Fatalf("pool not passed on: vector k=%d keyword k=%d q=%q", v.gotK, k.gotK, k.gotQ)
	}
	if ids(got) != "1" {
		t.Fatalf("k=1 with an empty keyword list: %s", ids(got))
	}
}

func TestHybridKeepsVectorDistance(t *testing.T) {
	a := ch(1, "a")
	a.Distance = 0.3
	b := ch(1, "a")
	b.Score = 99 // a keyword score must not leak into the fused score
	h, _, _ := hybrid([]Chunk{a}, []Chunk{b})
	got, _ := h.Retrieve(context.Background(), Query{RepoID: 1, Text: "q", K: 10})
	if got[0].Distance != 0.3 || math.Abs(got[0].Score-2.0/61) > 1e-12 {
		t.Fatalf("distance %v score %v", got[0].Distance, got[0].Score)
	}
}

func TestHybridErrorsAndContext(t *testing.T) {
	boom := errors.New("boom")
	for _, which := range []string{"vector", "keyword"} {
		h, v, k := hybrid(nil, nil)
		if which == "vector" {
			v.err = boom
		} else {
			k.err = boom
		}
		if _, err := h.Retrieve(context.Background(), Query{RepoID: 1, Text: "q", K: 5}); !errors.Is(err, boom) {
			t.Fatalf("%s error not returned: %v", which, err)
		}
	}
	h, v, k := hybrid(nil, nil)
	ctx := context.WithValue(context.Background(), ctxKeyT{}, "marker")
	h.Retrieve(ctx, Query{RepoID: 1, Text: "q", K: 5})
	if v.ctxKey != "marker" || k.ctxKey != "marker" {
		t.Fatal("context must reach both retrievers")
	}
}

func TestHybridValidatesSettings(t *testing.T) {
	bad := []func(*HybridRetriever){
		func(h *HybridRetriever) { h.RRFK = 0 },
		func(h *HybridRetriever) { h.Pool = 0 },
		func(h *HybridRetriever) { h.Pool = MaxK + 1 },
		func(h *HybridRetriever) { h.KeywordWeight = 0 },
		func(h *HybridRetriever) { h.KeywordWeight = 1.5 },
		func(h *HybridRetriever) { h.TestPenalty = 0 },
		func(h *HybridRetriever) { h.TestPenalty = 2 },
		func(h *HybridRetriever) { h.Keyword = nil },
	}
	for i, mutate := range bad {
		h, _, _ := hybrid(nil, nil)
		mutate(&h)
		if _, err := h.Retrieve(context.Background(), Query{RepoID: 1, Text: "q", K: 5}); !errors.Is(err, ErrBadHybridConfig) {
			t.Errorf("case %d: err = %v, want ErrBadHybridConfig", i, err)
		}
	}
	h, _, _ := hybrid(nil, nil)
	if _, err := h.Retrieve(context.Background(), Query{RepoID: 1, Text: "q", K: 0}); !errors.Is(err, ErrInvalidK) {
		t.Fatal("k=0 must fail")
	}
}
