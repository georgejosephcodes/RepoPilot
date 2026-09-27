package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"repopilot/api/internal/embed"
	"repopilot/api/internal/llm"
	"repopilot/api/internal/rag"
	"repopilot/api/internal/repos"
	"repopilot/api/internal/retrieval"
)

type fakeAsker struct {
	resp      rag.Response
	err       error
	calls     int
	gotID     int64
	gotQuery  string
	gotFilter retrieval.Filter
}

func (f *fakeAsker) Ask(_ context.Context, id int64, q string, filter retrieval.Filter) (rag.Response, error) {
	f.calls++
	f.gotID, f.gotQuery, f.gotFilter = id, q, filter
	return f.resp, f.err
}

func queryRouter(a Asker, rate int) http.Handler {
	fs := &fakeStore{}
	return NewRouter(Deps{DB: fakePinger{}, Repos: fs, Query: a, QueryRateLimitPerMin: rate})
}

func TestQuerySuccessShape(t *testing.T) {
	a := &fakeAsker{resp: rag.Response{
		Answer: "It polls [2].", Grounded: true,
		Citations: []rag.CitationJSON{{N: 2, File: "outyet/main.go", StartLine: 65, EndLine: 75, Symbol: "Server.poll", Kind: "method", Language: "go", Snippet: "func (s *Server) poll() {}"}},
		Stats:     rag.Stats{ChunksRetrieved: 8, ChunksInPrompt: 8, InputTokens: 538, OutputTokens: 52},
	}}
	w := do(queryRouter(a, 0), http.MethodPost, "/api/repositories/7/query", `{"question":"How does it work?"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if a.gotID != 7 || a.gotQuery != "How does it work?" || a.calls != 1 {
		t.Fatalf("asker got id=%d q=%q calls=%d", a.gotID, a.gotQuery, a.calls)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"answer", "grounded", "refused", "truncated", "citations", "stats"} {
		if _, ok := body[key]; !ok {
			t.Errorf("response lacks %q: %v", key, body)
		}
	}
	cite := body["citations"].([]any)[0].(map[string]any)
	for _, key := range []string{"n", "file", "start_line", "end_line", "symbol", "kind", "language", "snippet"} {
		if _, ok := cite[key]; !ok {
			t.Errorf("citation lacks %q: %v", key, cite)
		}
	}
	stats := body["stats"].(map[string]any)
	for _, key := range []string{"chunks_retrieved", "chunks_in_prompt", "embed_ms", "search_ms", "llm_ms", "input_tokens", "output_tokens"} {
		if _, ok := stats[key]; !ok {
			t.Errorf("stats lack %q", key)
		}
	}
}

func TestQueryErrorMapping(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{rag.ErrQuestionInvalid, 400, "invalid_request"},
		{repos.ErrNotFound, 404, "not_found"},
		{rag.ErrNotReady, 409, "repo_not_ready"},
		{retrieval.ErrModelMismatch, 422, "model_mismatch"},
		{llm.ErrBlocked, 422, "answer_blocked"},
		{embed.ErrRateLimited, 429, "rate_limited"},
		{llm.ErrRateLimited, 429, "rate_limited"},
		{embed.ErrUnauthorized, 502, "upstream_error"},
		{llm.ErrUnavailable, 502, "upstream_error"},
		{llm.ErrEmpty, 502, "upstream_error"},
		{context.DeadlineExceeded, 504, "timeout"},
		{errors.New("database exploded: password=hunter2"), 500, "internal"},
	}
	for _, tc := range tests {
		t.Run(tc.code+"/"+tc.err.Error(), func(t *testing.T) {
			w := do(queryRouter(&fakeAsker{err: tc.err}, 0), http.MethodPost, "/api/repositories/1/query", `{"question":"q"}`)
			if w.Code != tc.status || errCode(t, w) != tc.code {
				t.Fatalf("status=%d code=%q body=%s", w.Code, errCode(t, w), w.Body.String())
			}
			if strings.Contains(w.Body.String(), "hunter2") {
				t.Fatal("an internal error text reached the client")
			}
		})
	}
}

func TestQueryRejectsBadRequests(t *testing.T) {
	a := &fakeAsker{}
	r := queryRouter(a, 0)
	tests := []struct {
		name, path, body string
		status           int
	}{
		{"not json", "/api/repositories/1/query", "hello", 400},
		{"empty body", "/api/repositories/1/query", "", 400},
		{"bad id", "/api/repositories/abc/query", `{"question":"q"}`, 400},
		{"zero id", "/api/repositories/0/query", `{"question":"q"}`, 400},
		{"oversized body", "/api/repositories/1/query", `{"question":"` + strings.Repeat("a", 9000) + `"}`, 413},
		{"language not a list", "/api/repositories/1/query", `{"question":"q","filters":{"language":"go"}}`, 400},
		{"prefix not a string", "/api/repositories/1/query", `{"question":"q","filters":{"path_prefix":3}}`, 400},
		{"filters not an object", "/api/repositories/1/query", `{"question":"q","filters":[]}`, 400},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := do(r, http.MethodPost, tc.path, tc.body)
			if w.Code != tc.status || errCode(t, w) != "invalid_request" {
				t.Fatalf("status=%d code=%q", w.Code, errCode(t, w))
			}
		})
	}
	if a.calls != 0 {
		t.Fatalf("the asker was called %d times for bad requests", a.calls)
	}
}

func TestQueryAcceptsALongQuestionThatCreateWouldRefuse(t *testing.T) {
	// the create route caps bodies at 4 KB, the query route at 8 KB
	a := &fakeAsker{}
	body := `{"question":"` + strings.Repeat("a", 5000) + `"}`
	if w := do(queryRouter(a, 0), http.MethodPost, "/api/repositories/1/query", body); w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	fs := &fakeStore{create: func(repos.RepoRef) (repos.CreateResult, error) { return repos.CreateResult{}, nil }}
	w := do(NewRouter(Deps{DB: fakePinger{}, Repos: fs}), http.MethodPost, "/api/repositories", `{"url":"`+strings.Repeat("a", 5000)+`"}`)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("create route status = %d, want 413", w.Code)
	}
}

func TestQueryNotConfigured(t *testing.T) {
	r := NewRouter(Deps{DB: fakePinger{}, Repos: &fakeStore{}}) // no Query
	w := do(r, http.MethodPost, "/api/repositories/1/query", `{"question":"q"}`)
	if w.Code != http.StatusServiceUnavailable || errCode(t, w) != "not_configured" {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "EMBED_API_KEY") || !strings.Contains(w.Body.String(), "LLM_API_KEY") {
		t.Fatalf("message should name the missing keys: %s", w.Body.String())
	}
}

func TestQueryRateLimit(t *testing.T) {
	a := &fakeAsker{}
	r := queryRouter(a, 2)
	for i := 0; i < 2; i++ {
		if w := do(r, http.MethodPost, "/api/repositories/1/query", `{"question":"q"}`); w.Code != http.StatusOK {
			t.Fatalf("request %d: %d", i+1, w.Code)
		}
	}
	w := do(r, http.MethodPost, "/api/repositories/1/query", `{"question":"q"}`)
	if w.Code != http.StatusTooManyRequests || errCode(t, w) != "rate_limited" {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if a.calls != 2 {
		t.Fatalf("asker calls = %d, want 2", a.calls)
	}
}

func TestQueryLimitIsSeparateFromTheCreateLimit(t *testing.T) {
	fs := &fakeStore{create: func(repos.RepoRef) (repos.CreateResult, error) {
		return repos.CreateResult{Repo: newRepo(1, "queued"), Created: true}, nil
	}}
	r := NewRouter(Deps{DB: fakePinger{}, Repos: fs, RateLimitPerMin: 1, Query: &fakeAsker{}, QueryRateLimitPerMin: 1})
	if w := do(r, http.MethodPost, "/api/repositories", `{"url":"https://github.com/a/b"}`); w.Code != http.StatusAccepted {
		t.Fatalf("create: %d", w.Code)
	}
	if w := do(r, http.MethodPost, "/api/repositories/1/query", `{"question":"q"}`); w.Code != http.StatusOK {
		t.Fatalf("query after a create must have its own bucket: %d", w.Code)
	}
}

func TestQueryPassesFilters(t *testing.T) {
	a := &fakeAsker{}
	r := queryRouter(a, 0)
	w := do(r, http.MethodPost, "/api/repositories/1/query", `{"question":"q","filters":{"language":["go","python"],"path_prefix":"api/"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if !reflect.DeepEqual(a.gotFilter, retrieval.Filter{Languages: []string{"go", "python"}, PathPrefix: "api/"}) {
		t.Fatalf("filter = %+v", a.gotFilter)
	}
	for _, body := range []string{`{"question":"q"}`, `{"question":"q","filters":null}`, `{"question":"q","filters":{}}`} {
		do(r, http.MethodPost, "/api/repositories/1/query", body)
		if !a.gotFilter.Empty() {
			t.Errorf("%s: filter = %+v, want empty", body, a.gotFilter)
		}
	}
}

func TestQueryInvalidFilterIs400WithTheReason(t *testing.T) {
	err := retrieval.ValidateFilter(retrieval.Filter{Languages: []string{"rust"}})
	w := do(queryRouter(&fakeAsker{err: err}, 0), http.MethodPost, "/api/repositories/1/query", `{"question":"q","filters":{"language":["rust"]}}`)
	if w.Code != 400 || errCode(t, w) != "invalid_request" || !strings.Contains(w.Body.String(), "unknown language") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
