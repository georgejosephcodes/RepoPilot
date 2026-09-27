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

type fakeSearcher struct {
	chunks []retrieval.Chunk
	err    error
	calls  int
	k      int
}

func (f *fakeSearcher) Search(_ context.Context, _ int64, _ []float32, k int) ([]retrieval.Chunk, error) {
	f.calls++
	f.k = k
	return f.chunks, f.err
}

type harness struct {
	svc      *Service
	repos    *fakeRepos
	embedder *embed.Fake
	search   *fakeSearcher
	llm      *llm.Fake
}

func newHarness(answer string, chunks ...retrieval.Chunk) *harness {
	h := &harness{
		repos:    &fakeRepos{status: "ready"},
		embedder: embed.NewFake(8),
		search:   &fakeSearcher{chunks: chunks},
		llm:      llm.NewFake(answer),
	}
	h.svc = &Service{Repos: h.repos, Embed: h.embedder, Search: h.search, LLM: h.llm}
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
	resp, err := h.svc.Ask(context.Background(), 1, "  How does it check?  ")
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
	resp, err := h.svc.Ask(context.Background(), 1, "q")
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
	resp, _ := h.svc.Ask(context.Background(), 1, "q")
	if resp.Stats.ChunksRetrieved != 3 || resp.Stats.ChunksInPrompt != 1 {
		t.Fatalf("stats = %+v", resp.Stats)
	}
	if strings.Contains(resp.Answer, "[3]") || len(resp.Citations) != 1 || resp.Citations[0].File != "a.go" {
		t.Fatalf("answer=%q citations=%+v", resp.Answer, resp.Citations)
	}
}

func TestRefusalIsGrounded(t *testing.T) {
	h := newHarness(RefusalSentence+" The context has no billing code.", threeChunks()...)
	resp, err := h.svc.Ask(context.Background(), 1, "How does billing work?")
	if err != nil || !resp.Refused || !resp.Grounded || len(resp.Citations) != 0 {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if resp.Citations == nil {
		t.Fatal("citations must be an empty list, not null")
	}
}

func TestNoChunksMeansNoAnswerModelCall(t *testing.T) {
	h := newHarness("must not be used")
	resp, err := h.svc.Ask(context.Background(), 1, "anything")
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
	resp, _ := h.svc.Ask(context.Background(), 1, "q")
	if !resp.Truncated || resp.Grounded {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestUngroundedAnswerIsFlagged(t *testing.T) {
	h := newHarness("An answer with no citations at all.", threeChunks()...)
	resp, _ := h.svc.Ask(context.Background(), 1, "q")
	if resp.Grounded || resp.Refused {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestRepositoryProblemsCauseNoProviderCalls(t *testing.T) {
	h := newHarness("x", threeChunks()...)
	h.repos.status = "indexing"
	if _, err := h.svc.Ask(context.Background(), 1, "q"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("err = %v", err)
	}
	h.repos.status, h.repos.err = "ready", repos.ErrNotFound
	if _, err := h.svc.Ask(context.Background(), 1, "q"); !errors.Is(err, repos.ErrNotFound) {
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
		if _, err := h.svc.Ask(context.Background(), 1, q); !errors.Is(err, ErrQuestionInvalid) {
			t.Errorf("%q: err = %v", q, err)
		}
	}
	if _, err := h.svc.Ask(context.Background(), 1, strings.Repeat("\u4e16", 10)); err != nil { // 10 characters, 30 bytes
		t.Fatalf("the limit counts characters, not bytes: %v", err)
	}
	if h.repos.gets != 1 {
		t.Fatalf("repository lookups = %d, want 1", h.repos.gets)
	}
}

func TestProviderErrorsPassThroughTyped(t *testing.T) {
	h := newHarness("x", threeChunks()...)
	h.embedder.Err = embed.ErrUnauthorized
	if _, err := h.svc.Ask(context.Background(), 1, "q"); !errors.Is(err, embed.ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	h.embedder.Err = nil
	h.search.err = retrieval.ErrModelMismatch
	if _, err := h.svc.Ask(context.Background(), 1, "q"); !errors.Is(err, retrieval.ErrModelMismatch) {
		t.Fatalf("err = %v", err)
	}
	h.search.err = nil
	h.llm.Err = llm.ErrBlocked
	if _, err := h.svc.Ask(context.Background(), 1, "q"); !errors.Is(err, llm.ErrBlocked) {
		t.Fatalf("err = %v", err)
	}
}

func TestTopKFromConfigIsClamped(t *testing.T) {
	h := newHarness("x [1]", threeChunks()...)
	h.svc.TopK = 5
	_, _ = h.svc.Ask(context.Background(), 1, "q")
	if h.search.k != 5 {
		t.Fatalf("k = %d", h.search.k)
	}
	h.svc.TopK = 1000
	_, _ = h.svc.Ask(context.Background(), 1, "q")
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
	if _, err := h.svc.Ask(context.Background(), 1, secret); err != nil {
		t.Fatal(err)
	}
	newHarness("x").svc.Ask(context.Background(), 1, secret) // the no-chunks path logs too
	logs := buf.String()
	if strings.Contains(logs, "SECRETQUESTIONTEXT") {
		t.Fatalf("the question text was logged:\n%s", logs)
	}
	if !strings.Contains(logs, "question answered") || !strings.Contains(logs, "question_chars=") {
		t.Fatalf("expected a summary line, got:\n%s", logs)
	}
}
