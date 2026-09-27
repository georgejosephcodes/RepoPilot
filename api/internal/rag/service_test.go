package rag

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"repopilot/api/internal/embed"
	"repopilot/api/internal/llm"
	"repopilot/api/internal/repos"
	"repopilot/api/internal/retrieval"
)

type fakeRepos struct {
	status string
	err    error
	gets   int
}

func (f *fakeRepos) CreateOrGet(context.Context, repos.RepoRef) (repos.CreateResult, error) {
	return repos.CreateResult{}, errors.New("not used")
}
func (f *fakeRepos) List(context.Context) ([]repos.Repository, error) { return nil, nil }
func (f *fakeRepos) Get(_ context.Context, id int64) (repos.RepoDetail, error) {
	f.gets++
	if f.err != nil {
		return repos.RepoDetail{}, f.err
	}
	return repos.RepoDetail{Repository: repos.Repository{ID: id, Status: f.status}}, nil
}

type fakeRetriever struct {
	chunks []retrieval.Chunk
	err    error
	calls  int
	k      int
	filter retrieval.Filter
	trace  retrieval.TraceData // recorded into the context's trace, like a real retriever
}

func (f *fakeRetriever) Retrieve(ctx context.Context, q retrieval.Query) ([]retrieval.Chunk, error) {
	f.calls++
	f.k, f.filter = q.K, q.Filter
	retrieval.TraceFrom(ctx).Record(func(d *retrieval.TraceData) { *d = f.trace })
	return f.chunks, f.err
}

type harness struct {
	svc      *Service
	repos    *fakeRepos
	embedder *embed.Fake
	search   *fakeRetriever
	llm      *llm.Fake
}

func newHarness(answer string, chunks ...retrieval.Chunk) *harness {
	h := &harness{
		repos:    &fakeRepos{status: "ready"},
		embedder: embed.NewFake(8),
		search:   &fakeRetriever{chunks: chunks},
		llm:      llm.NewFake(answer),
	}
	h.svc = &Service{Repos: h.repos, Embed: h.embedder, Retrieve: h.search, Mode: "hybrid_rerank", LLM: h.llm}
	return h
}

func threeChunks() []retrieval.Chunk {
	return []retrieval.Chunk{
		mk("outyet/main.go", 58, 63, "NewServer", "func NewServer() {}"),
		mk("outyet/main.go", 65, 75, "Server.poll", "func (s *Server) poll() {}"),
		mk("outyet/main.go", 83, 95, "isTagged", "func isTagged(url string) bool {}"),
	}
}

func TestHappyPath(t *testing.T) {
	h := newHarness("It polls [2] and checks with a HEAD request [3].", threeChunks()...)
	resp, err := h.svc.Ask(context.Background(), 1, "  How does it check?  ", retrieval.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Answer != "It polls [2] and checks with a HEAD request [3]." || !resp.Grounded || resp.Refused || resp.Truncated {
		t.Fatalf("resp = %+v", resp)
	}
	if len(resp.Citations) != 2 || resp.Citations[0].N != 2 || resp.Citations[0].File != "outyet/main.go" ||
		resp.Citations[0].StartLine != 65 || resp.Citations[0].Snippet != "func (s *Server) poll() {}" ||
		resp.Citations[1].N != 3 || resp.Citations[1].Symbol != "isTagged" {
		t.Fatalf("citations = %+v", resp.Citations)
	}
	if resp.Stats.ChunksRetrieved != 3 || resp.Stats.ChunksInPrompt != 3 || resp.Stats.InputTokens != 100 || resp.Stats.OutputTokens != 20 {
		t.Fatalf("stats = %+v", resp.Stats)
	}
	if h.embedder.Calls() != 1 || h.search.calls != 1 || h.llm.Calls() != 1 || h.search.k != DefaultTopK {
		t.Fatalf("calls: embed=%d search=%d llm=%d k=%d", h.embedder.Calls(), h.search.calls, h.llm.Calls(), h.search.k)
	}
	if h.llm.System != SystemPrompt || !strings.HasSuffix(h.llm.User, "Question: How does it check?") {
		t.Fatalf("the model saw the wrong prompt:\n%s", h.llm.User)
	}
	if !strings.Contains(h.llm.User, "[3] outyet/main.go:83-95 (isTagged, function, go)") {
		t.Fatalf("prompt = %s", h.llm.User)
	}
}

func TestInvalidCitationsAreRemovedAndValidOnesStay(t *testing.T) {
	h := newHarness("Real [1]. Made up [7]. Another made up [3][9].", threeChunks()[:2]...) // only 2 chunks in the prompt
	resp, err := h.svc.Ask(context.Background(), 1, "q", retrieval.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Answer != "Real [1]. Made up. Another made up." || len(resp.Citations) != 1 || resp.Citations[0].N != 1 {
		t.Fatalf("answer=%q citations=%+v", resp.Answer, resp.Citations)
	}
	for _, c := range resp.Citations {
		if c.N < 1 || c.N > resp.Stats.ChunksInPrompt {
			t.Fatalf("citation %d outside the prompt", c.N)
		}
	}
}

func TestBudgetLimitsWhatCanBeCited(t *testing.T) {
	big := strings.Repeat("x", 500)
	chunks := []retrieval.Chunk{mk("a.go", 1, 2, "a", big), mk("b.go", 1, 2, "b", big), mk("c.go", 1, 2, "c", big)}
	h := newHarness("From the first [1] and a chunk that was dropped [3].", chunks...)
	h.svc.BudgetChars = 700 // room for one block only
	resp, _ := h.svc.Ask(context.Background(), 1, "q", retrieval.Filter{})
	if resp.Stats.ChunksRetrieved != 3 || resp.Stats.ChunksInPrompt != 1 {
		t.Fatalf("stats = %+v", resp.Stats)
	}
	if strings.Contains(resp.Answer, "[3]") || len(resp.Citations) != 1 || resp.Citations[0].File != "a.go" {
		t.Fatalf("answer=%q citations=%+v", resp.Answer, resp.Citations)
	}
}

func TestRefusalIsGrounded(t *testing.T) {
	h := newHarness(RefusalSentence+" The context has no billing code.", threeChunks()...)
	resp, err := h.svc.Ask(context.Background(), 1, "How does billing work?", retrieval.Filter{})
	if err != nil || !resp.Refused || !resp.Grounded || len(resp.Citations) != 0 {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if resp.Citations == nil {
		t.Fatal("citations must be an empty list, not null")
	}
}

func TestNoChunksMeansNoAnswerModelCall(t *testing.T) {
	h := newHarness("must not be used")
	resp, err := h.svc.Ask(context.Background(), 1, "anything", retrieval.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if h.llm.Calls() != 0 {
		t.Fatalf("the answer model was called %d times", h.llm.Calls())
	}
	if !resp.Refused || !resp.Grounded || !strings.HasPrefix(resp.Answer, RefusalSentence) || resp.Citations == nil || len(resp.Citations) != 0 {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestTruncatedIsReported(t *testing.T) {
	h := newHarness("Half an answ", threeChunks()...)
	h.llm.Result.FinishReason = "MAX_TOKENS"
	resp, _ := h.svc.Ask(context.Background(), 1, "q", retrieval.Filter{})
	if !resp.Truncated || resp.Grounded {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestUngroundedAnswerIsFlagged(t *testing.T) {
	h := newHarness("An answer with no citations at all.", threeChunks()...)
	resp, _ := h.svc.Ask(context.Background(), 1, "q", retrieval.Filter{})
	if resp.Grounded || resp.Refused {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestRepositoryProblemsCauseNoProviderCalls(t *testing.T) {
	h := newHarness("x", threeChunks()...)
	h.repos.status = "indexing"
	if _, err := h.svc.Ask(context.Background(), 1, "q", retrieval.Filter{}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("err = %v", err)
	}
	h.repos.status, h.repos.err = "ready", repos.ErrNotFound
	if _, err := h.svc.Ask(context.Background(), 1, "q", retrieval.Filter{}); !errors.Is(err, repos.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if h.embedder.Calls() != 0 || h.search.calls != 0 || h.llm.Calls() != 0 {
		t.Fatalf("calls: embed=%d search=%d llm=%d", h.embedder.Calls(), h.search.calls, h.llm.Calls())
	}
}

func TestBadQuestionsCauseNoCalls(t *testing.T) {
	h := newHarness("x", threeChunks()...)
	h.svc.MaxQuestionChars = 10
	for _, q := range []string{"", "   ", "\n\t", strings.Repeat("a", 11), strings.Repeat("\u4e16", 11)} {
		if _, err := h.svc.Ask(context.Background(), 1, q, retrieval.Filter{}); !errors.Is(err, ErrQuestionInvalid) {
			t.Errorf("%q: err = %v", q, err)
		}
	}
	if _, err := h.svc.Ask(context.Background(), 1, strings.Repeat("\u4e16", 10), retrieval.Filter{}); err != nil { // 10 characters, 30 bytes
		t.Fatalf("the limit counts characters, not bytes: %v", err)
	}
	if h.repos.gets != 1 {
		t.Fatalf("repository lookups = %d, want 1", h.repos.gets)
	}
}

func TestProviderErrorsPassThroughTyped(t *testing.T) {
	h := newHarness("x", threeChunks()...)
	h.embedder.Err = embed.ErrUnauthorized
	if _, err := h.svc.Ask(context.Background(), 1, "q", retrieval.Filter{}); !errors.Is(err, embed.ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	h.embedder.Err = nil
	h.search.err = retrieval.ErrModelMismatch
	if _, err := h.svc.Ask(context.Background(), 1, "q", retrieval.Filter{}); !errors.Is(err, retrieval.ErrModelMismatch) {
		t.Fatalf("err = %v", err)
	}
	h.search.err = nil
	h.llm.Err = llm.ErrBlocked
	if _, err := h.svc.Ask(context.Background(), 1, "q", retrieval.Filter{}); !errors.Is(err, llm.ErrBlocked) {
		t.Fatalf("err = %v", err)
	}
}

func TestTopKFromConfigIsClamped(t *testing.T) {
	h := newHarness("x [1]", threeChunks()...)
	h.svc.TopK = 5
	_, _ = h.svc.Ask(context.Background(), 1, "q", retrieval.Filter{})
	if h.search.k != 5 {
		t.Fatalf("k = %d", h.search.k)
	}
	h.svc.TopK = 1000
	_, _ = h.svc.Ask(context.Background(), 1, "q", retrieval.Filter{})
	if h.search.k != retrieval.MaxK {
		t.Fatalf("k = %d, want the cap %d", h.search.k, retrieval.MaxK)
	}
}

func TestQuestionTextIsNotLogged(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)

	secret := "where is the SECRETQUESTIONTEXT handled"
	h := newHarness("Answer [1].", threeChunks()...)
	if _, err := h.svc.Ask(context.Background(), 1, secret, retrieval.Filter{}); err != nil {
		t.Fatal(err)
	}
	newHarness("x").svc.Ask(context.Background(), 1, secret, retrieval.Filter{}) // the no-chunks path logs too
	logs := buf.String()
	if strings.Contains(logs, "SECRETQUESTIONTEXT") {
		t.Fatalf("the question text was logged:\n%s", logs)
	}
	if !strings.Contains(logs, "question answered") || !strings.Contains(logs, "question_chars=") {
		t.Fatalf("expected a summary line, got:\n%s", logs)
	}
}

func TestStatsReportModeAndTrace(t *testing.T) {
	h := newHarness("Answer [1].", threeChunks()...)
	h.search.trace = retrieval.TraceData{VectorMS: 3, KeywordMS: 7, RerankMS: 1690, RerankSource: "cache", Fused: []int64{1}}
	resp, err := h.svc.Ask(context.Background(), 1, "q", retrieval.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	st := resp.Stats
	if st.RetrievalMode != "hybrid_rerank" || st.KeywordMS != 7 || st.RerankMS != 1690 || !st.RerankCached || st.RerankFallback != "" {
		t.Fatalf("stats = %+v", st)
	}
	h.search.trace = retrieval.TraceData{RerankMS: 10000, RerankSource: "upstream", RerankFallback: "timeout"}
	resp, _ = h.svc.Ask(context.Background(), 1, "q", retrieval.Filter{})
	if resp.Stats.RerankCached || resp.Stats.RerankFallback != "timeout" || resp.Stats.RerankMS != 10000 {
		t.Fatalf("stats = %+v", resp.Stats)
	}
}

func TestFilterIsPassedToRetrieval(t *testing.T) {
	h := newHarness("Answer [1].", threeChunks()...)
	f := retrieval.Filter{Languages: []string{"go"}, PathPrefix: "outyet/"}
	if _, err := h.svc.Ask(context.Background(), 1, "q", f); err != nil {
		t.Fatal(err)
	}
	if h.search.filter.PathPrefix != "outyet/" || len(h.search.filter.Languages) != 1 {
		t.Fatalf("filter = %+v", h.search.filter)
	}
}

func TestInvalidFilterCausesNoCalls(t *testing.T) {
	h := newHarness("x", threeChunks()...)
	for _, f := range []retrieval.Filter{{Languages: []string{"rust"}}, {PathPrefix: "../etc"}, {PathPrefix: "/abs"}} {
		_, err := h.svc.Ask(context.Background(), 1, "q", f)
		if !errors.Is(err, retrieval.ErrInvalidFilter) || Classify(err).Status != 400 {
			t.Errorf("%+v: err = %v", f, err)
		}
	}
	if h.repos.gets != 0 || h.embedder.Calls() != 0 || h.search.calls != 0 || h.llm.Calls() != 0 {
		t.Fatalf("calls: repos=%d embed=%d search=%d llm=%d", h.repos.gets, h.embedder.Calls(), h.search.calls, h.llm.Calls())
	}
}

func TestFilterMatchingNothingIsRefusedWithoutTheModel(t *testing.T) {
	h := newHarness("must not be used")
	resp, err := h.svc.Ask(context.Background(), 1, "q", retrieval.Filter{Languages: []string{"markdown"}})
	if err != nil {
		t.Fatal(err)
	}
	if h.llm.Calls() != 0 || !resp.Refused || !strings.Contains(resp.Answer, "No indexed code matches the filters.") {
		t.Fatalf("calls=%d resp=%+v", h.llm.Calls(), resp)
	}
	resp, _ = h.svc.Ask(context.Background(), 1, "q", retrieval.Filter{})
	if strings.Contains(resp.Answer, "filters") {
		t.Fatalf("an unfiltered empty repository must not mention filters: %q", resp.Answer)
	}
}

func TestDistancesAreLoggedOnlyForAVectorList(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	h := newHarness("Answer [1].", threeChunks()...)
	h.search.trace = retrieval.TraceData{Vector: []int64{1}}
	h.svc.Ask(context.Background(), 1, "q", retrieval.Filter{})
	if !strings.Contains(buf.String(), "nearest=") {
		t.Fatalf("vector mode should log distances:\n%s", buf.String())
	}
	buf.Reset()
	h.search.trace = retrieval.TraceData{Vector: []int64{1}, Keyword: []int64{2}, Fused: []int64{1, 2}}
	h.svc.Ask(context.Background(), 1, "q", retrieval.Filter{})
	if strings.Contains(buf.String(), "nearest=") || !strings.Contains(buf.String(), "keyword_ms=") {
		t.Fatalf("hybrid mode must not log distances:\n%s", buf.String())
	}
}
