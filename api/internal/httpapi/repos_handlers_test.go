package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"repopilot/api/internal/repos"
)

type fakeStore struct {
	create func(ref repos.RepoRef) (repos.CreateResult, error)
	list   func() ([]repos.Repository, error)
	get    func(id int64) (repos.RepoDetail, error)
	calls  int
}

func (f *fakeStore) CreateOrGet(_ context.Context, ref repos.RepoRef) (repos.CreateResult, error) {
	f.calls++
	return f.create(ref)
}
func (f *fakeStore) List(context.Context) ([]repos.Repository, error) { return f.list() }
func (f *fakeStore) Get(_ context.Context, id int64) (repos.RepoDetail, error) {
	return f.get(id)
}

func do(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not the error shape: %s", w.Body.String())
	}
	return body.Error.Code
}

func newRepo(id int64, status string) repos.Repository {
	return repos.Repository{ID: id, URL: "https://github.com/a/b", Owner: "a", Name: "b", Status: status, CreatedAt: time.Now()}
}

func TestCreate(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		result     repos.CreateResult
		storeErr   error
		wantStatus int
		wantCode   string
		wantCalls  int
	}{
		{"created", `{"url":"https://github.com/a/b"}`,
			repos.CreateResult{Repo: newRepo(1, "queued"), Created: true}, nil, http.StatusAccepted, "", 1},
		{"existing", `{"url":"https://github.com/a/b"}`,
			repos.CreateResult{Repo: newRepo(1, "ready")}, nil, http.StatusOK, "", 1},
		{"requeued failed", `{"url":"https://github.com/a/b"}`,
			repos.CreateResult{Repo: newRepo(1, "queued"), Requeued: true}, nil, http.StatusAccepted, "", 1},
		{"invalid host", `{"url":"https://evil.com/a/b"}`, repos.CreateResult{}, nil, http.StatusBadRequest, "invalid_url", 0},
		{"injection", `{"url":"--upload-pack=touch /tmp/x"}`, repos.CreateResult{}, nil, http.StatusBadRequest, "invalid_url", 0},
		{"missing url", `{}`, repos.CreateResult{}, nil, http.StatusBadRequest, "invalid_url", 0},
		{"not json", `hello`, repos.CreateResult{}, nil, http.StatusBadRequest, "invalid_request", 0},
		{"empty body", ``, repos.CreateResult{}, nil, http.StatusBadRequest, "invalid_request", 0},
		{"oversized body", `{"url":"` + strings.Repeat("a", 5000) + `"}`, repos.CreateResult{}, nil, http.StatusRequestEntityTooLarge, "invalid_request", 0},
		{"store error", `{"url":"https://github.com/a/b"}`, repos.CreateResult{}, errors.New("db down"), http.StatusInternalServerError, "internal", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := &fakeStore{create: func(repos.RepoRef) (repos.CreateResult, error) { return tc.result, tc.storeErr }}
			r := NewRouter(Deps{DB: fakePinger{}, Repos: fs})
			w := do(r, http.MethodPost, "/api/repositories", tc.body)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantCode != "" {
				if got := errCode(t, w); got != tc.wantCode {
					t.Fatalf("code = %q, want %q", got, tc.wantCode)
				}
			}
			if fs.calls != tc.wantCalls {
				t.Fatalf("store calls = %d, want %d", fs.calls, tc.wantCalls)
			}
		})
	}
}

func TestCreate_PassesCanonicalRef(t *testing.T) {
	var got repos.RepoRef
	fs := &fakeStore{create: func(ref repos.RepoRef) (repos.CreateResult, error) {
		got = ref
		return repos.CreateResult{Repo: newRepo(1, "queued"), Created: true}, nil
	}}
	r := NewRouter(Deps{DB: fakePinger{}, Repos: fs})
	do(r, http.MethodPost, "/api/repositories", `{"url":"HTTPS://GitHub.com/Foo/Bar.git/"}`)
	if got.URL != "https://github.com/Foo/Bar" || got.Owner != "Foo" || got.Name != "Bar" {
		t.Fatalf("got %+v", got)
	}
}

func TestCreate_ResponseShape(t *testing.T) {
	fs := &fakeStore{create: func(repos.RepoRef) (repos.CreateResult, error) {
		return repos.CreateResult{Repo: newRepo(7, "queued"), Created: true}, nil
	}}
	w := do(NewRouter(Deps{DB: fakePinger{}, Repos: fs}), http.MethodPost, "/api/repositories", `{"url":"https://github.com/a/b"}`)
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["id"] != float64(7) || body["status"] != "queued" || body["created"] != true || body["requeued"] != false {
		t.Fatalf("unexpected body: %v", body)
	}
}

func TestGet(t *testing.T) {
	phase, done, total := "embedding", 120, 310
	fs := &fakeStore{get: func(id int64) (repos.RepoDetail, error) {
		switch id {
		case 1:
			return repos.RepoDetail{
				Repository: newRepo(1, "indexing"),
				Progress:   repos.Progress{Phase: &phase, FilesDone: &done, FilesTotal: &total},
			}, nil
		case 2:
			return repos.RepoDetail{}, errors.New("db down")
		}
		return repos.RepoDetail{}, repos.ErrNotFound
	}}
	r := NewRouter(Deps{DB: fakePinger{}, Repos: fs})

	tests := []struct {
		path     string
		want     int
		wantCode string
	}{
		{"/api/repositories/1", http.StatusOK, ""},
		{"/api/repositories/999", http.StatusNotFound, "not_found"},
		{"/api/repositories/2", http.StatusInternalServerError, "internal"},
		{"/api/repositories/abc", http.StatusBadRequest, "invalid_request"},
		{"/api/repositories/0", http.StatusBadRequest, "invalid_request"},
		{"/api/repositories/-5", http.StatusBadRequest, "invalid_request"},
		{"/api/repositories/1.5", http.StatusBadRequest, "invalid_request"},
		{"/api/repositories/99999999999999999999", http.StatusBadRequest, "invalid_request"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			w := do(r, http.MethodGet, tc.path, "")
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
			if tc.wantCode != "" && errCode(t, w) != tc.wantCode {
				t.Fatalf("code = %q, want %q", errCode(t, w), tc.wantCode)
			}
		})
	}

	var body map[string]any
	json.Unmarshal(do(r, http.MethodGet, "/api/repositories/1", "").Body.Bytes(), &body)
	progress, _ := body["progress"].(map[string]any)
	if body["status"] != "indexing" || progress["phase"] != "embedding" || progress["files_done"] != float64(120) || progress["files_total"] != float64(310) {
		t.Fatalf("unexpected body: %v", body)
	}
	if _, ok := body["error"]; !ok {
		t.Fatalf("error key missing: %v", body)
	}
}

func TestList(t *testing.T) {
	t.Run("empty list is [] not null", func(t *testing.T) {
		fs := &fakeStore{list: func() ([]repos.Repository, error) { return []repos.Repository{}, nil }}
		w := do(NewRouter(Deps{DB: fakePinger{}, Repos: fs}), http.MethodGet, "/api/repositories", "")
		if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "[]" {
			t.Fatalf("got %d %q", w.Code, w.Body.String())
		}
	})
	t.Run("store error", func(t *testing.T) {
		fs := &fakeStore{list: func() ([]repos.Repository, error) { return nil, errors.New("boom") }}
		w := do(NewRouter(Deps{DB: fakePinger{}, Repos: fs}), http.MethodGet, "/api/repositories", "")
		if w.Code != http.StatusInternalServerError || errCode(t, w) != "internal" {
			t.Fatalf("got %d %s", w.Code, w.Body.String())
		}
	})
}

func TestCreate_RateLimited(t *testing.T) {
	fs := &fakeStore{create: func(repos.RepoRef) (repos.CreateResult, error) {
		return repos.CreateResult{Repo: newRepo(1, "queued"), Created: true}, nil
	}}
	r := NewRouter(Deps{DB: fakePinger{}, Repos: fs, RateLimitPerMin: 2})
	body := `{"url":"https://github.com/a/b"}`
	for i := 0; i < 2; i++ {
		if w := do(r, http.MethodPost, "/api/repositories", body); w.Code != http.StatusAccepted {
			t.Fatalf("request %d: status %d", i+1, w.Code)
		}
	}
	w := do(r, http.MethodPost, "/api/repositories", body)
	if w.Code != http.StatusTooManyRequests || errCode(t, w) != "rate_limited" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	// reads are not limited
	fs.list = func() ([]repos.Repository, error) { return []repos.Repository{}, nil }
	if w := do(r, http.MethodGet, "/api/repositories", ""); w.Code != http.StatusOK {
		t.Fatalf("GET limited: %d", w.Code)
	}
}
