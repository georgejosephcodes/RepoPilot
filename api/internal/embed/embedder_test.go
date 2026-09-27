package embed

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testKey = "sk-or-SECRETKEY123"

type recorder struct {
	mu     sync.Mutex
	waits  []time.Duration
	bodies []map[string]any
	auth   []string
	paths  []string
}

func (r *recorder) sleep(_ context.Context, d time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.waits = append(r.waits, d)
	return nil
}

func vectorJSON(dim int, scale float64) []float64 {
	v := make([]float64, dim)
	for i := range v {
		v[i] = scale * float64(i+1)
	}
	return v
}

type step struct {
	status  int
	body    any
	headers map[string]string
}

// serve answers each request with the next step (200 with a good vector once the script runs out).
func serve(t *testing.T, rec *recorder, dim int, script ...step) (*httptest.Server, Config) {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		rec.bodies = append(rec.bodies, body)
		rec.auth = append(rec.auth, r.Header.Get("Authorization"))
		rec.paths = append(rec.paths, r.Method+" "+r.URL.Path)
		var s step
		if len(script) > 0 {
			s, script = script[0], script[1:]
		} else {
			s = step{status: 200, body: map[string]any{"data": []any{map[string]any{"index": 0, "embedding": vectorJSON(dim, 0.01)}}}}
		}
		mu.Unlock()
		for k, v := range s.headers {
			w.Header().Set(k, v)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(s.status)
		_ = json.NewEncoder(w).Encode(s.body)
	}))
	t.Cleanup(srv.Close)
	cfg := DefaultConfig()
	cfg.BaseURL = srv.URL + "/api/v1/"
	cfg.APIKey = testKey
	cfg.Model = "test/model"
	cfg.Dimension = dim
	return srv, cfg
}

func newEmbedder(t *testing.T, cfg Config, rec *recorder) *OpenAICompat {
	t.Helper()
	e, err := New(cfg, WithSleep(rec.sleep))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func norm(v []float32) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}

func TestRequestShape(t *testing.T) {
	rec := &recorder{}
	_, cfg := serve(t, rec, 4)
	vec, err := newEmbedder(t, cfg, rec).EmbedQuery(context.Background(), "where is auth?")
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.bodies) != 1 {
		t.Fatalf("requests = %d, want 1", len(rec.bodies))
	}
	if rec.paths[0] != "POST /api/v1/embeddings" || rec.auth[0] != "Bearer "+testKey {
		t.Fatalf("path %q auth %q", rec.paths[0], rec.auth[0])
	}
	b := rec.bodies[0]
	input, _ := b["input"].([]any)
	if b["model"] != "test/model" || b["input_type"] != "search_query" || len(input) != 1 || input[0] != "where is auth?" {
		t.Fatalf("unexpected body: %v", b)
	}
	if _, has := b["dimensions"]; has {
		t.Fatal("dimensions must not be sent by default")
	}
	if len(vec) != 4 || math.Abs(norm(vec)-1) > 1e-6 {
		t.Fatalf("vector not normalised: len=%d norm=%v", len(vec), norm(vec))
	}
}

func TestOptionalFieldsFollowConfig(t *testing.T) {
	rec := &recorder{}
	_, cfg := serve(t, rec, 4)
	cfg.SendDimensions, cfg.InputTypes = true, false
	if _, err := newEmbedder(t, cfg, rec).EmbedQuery(context.Background(), "q"); err != nil {
		t.Fatal(err)
	}
	b := rec.bodies[0]
	if _, has := b["input_type"]; has {
		t.Fatal("input_type must be omitted when disabled")
	}
	if b["dimensions"] != float64(4) {
		t.Fatalf("dimensions = %v", b["dimensions"])
	}
}

func TestPrepareCutsByCharacters(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIKey = "k"
	cfg.MaxChars = 3
	e, _ := New(cfg)
	for in, want := range map[string]string{"abcdef": "abc", "héllo": "hél", "世界世界": "世界世", "ab": "ab", "": ""} {
		if got := e.Prepare(in); got != want {
			t.Errorf("Prepare(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLongQuestionIsCutInTheRequest(t *testing.T) {
	rec := &recorder{}
	_, cfg := serve(t, rec, 4)
	cfg.MaxChars = 10
	if _, err := newEmbedder(t, cfg, rec).EmbedQuery(context.Background(), strings.Repeat("x", 500)); err != nil {
		t.Fatal(err)
	}
	if got := rec.bodies[0]["input"].([]any)[0].(string); got != strings.Repeat("x", 10) {
		t.Fatalf("sent %q", got)
	}
}

func TestBlankQuestionIsRejectedWithoutARequest(t *testing.T) {
	rec := &recorder{}
	_, cfg := serve(t, rec, 4)
	for _, q := range []string{"", "   ", "\n\t"} {
		if _, err := newEmbedder(t, cfg, rec).EmbedQuery(context.Background(), q); !errors.Is(err, ErrInputRejected) {
			t.Errorf("%q: err = %v", q, err)
		}
	}
	if len(rec.bodies) != 0 {
		t.Fatal("no request expected")
	}
}

func TestBadResponses(t *testing.T) {
	good := func(v any) step {
		return step{200, map[string]any{"data": []any{map[string]any{"index": 0, "embedding": v}}}, nil}
	}
	tests := []struct {
		name string
		s    step
	}{
		{"wrong dimension", good([]float64{1, 2, 3})},
		{"zero vector", good([]float64{0, 0, 0, 0})},
		{"no embedding", step{200, map[string]any{"data": []any{map[string]any{"index": 0}}}, nil}},
		{"empty data", step{200, map[string]any{"data": []any{}}, nil}},
		{"two items", step{200, map[string]any{"data": []any{
			map[string]any{"index": 0, "embedding": []float64{1, 2, 3, 4}},
			map[string]any{"index": 1, "embedding": []float64{1, 2, 3, 4}}}}, nil}},
		{"wrong index", step{200, map[string]any{"data": []any{map[string]any{"index": 3, "embedding": []float64{1, 2, 3, 4}}}}, nil}},
		{"no data key", step{200, map[string]any{"result": 1}, nil}},
		{"not an object", step{200, "text", nil}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			_, cfg := serve(t, rec, 4, tc.s)
			if _, err := newEmbedder(t, cfg, rec).EmbedQuery(context.Background(), "q"); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("err = %v, want ErrInvalidResponse", err)
			}
			if len(rec.bodies) != 1 {
				t.Fatalf("an invalid response must not be retried, got %d requests", len(rec.bodies))
			}
		})
	}
}

func TestPermanentErrorsFailAtOnce(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{401, ErrUnauthorized}, {403, ErrUnauthorized}, {404, ErrNotFound},
		{400, ErrInputRejected}, {413, ErrInputRejected}, {422, ErrInputRejected}, {418, ErrUpstream},
	}
	for _, tc := range tests {
		rec := &recorder{}
		_, cfg := serve(t, rec, 4, step{tc.status, map[string]any{"error": map[string]any{"message": "detail " + testKey}}, nil})
		_, err := newEmbedder(t, cfg, rec).EmbedQuery(context.Background(), "q")
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: err = %v, want %v", tc.status, err, tc.want)
		}
		if len(rec.bodies) != 1 {
			t.Errorf("status %d: %d requests, want 1", tc.status, len(rec.bodies))
		}
	}
}

func TestRetries(t *testing.T) {
	t.Run("429 twice then success uses backoff", func(t *testing.T) {
		rec := &recorder{}
		_, cfg := serve(t, rec, 4, step{status: 429, body: map[string]any{}}, step{status: 429, body: map[string]any{}})
		if _, err := newEmbedder(t, cfg, rec).EmbedQuery(context.Background(), "q"); err != nil {
			t.Fatal(err)
		}
		if len(rec.bodies) != 3 || len(rec.waits) != 2 || rec.waits[0] != 500*time.Millisecond || rec.waits[1] != time.Second {
			t.Fatalf("requests=%d waits=%v", len(rec.bodies), rec.waits)
		}
	})
	t.Run("Retry-After is honoured and capped", func(t *testing.T) {
		rec := &recorder{}
		_, cfg := serve(t, rec, 4, step{status: 429, body: map[string]any{}, headers: map[string]string{"Retry-After": "3"}},
			step{status: 503, body: map[string]any{}, headers: map[string]string{"Retry-After": "99"}})
		if _, err := newEmbedder(t, cfg, rec).EmbedQuery(context.Background(), "q"); err != nil {
			t.Fatal(err)
		}
		if len(rec.waits) != 2 || rec.waits[0] != 3*time.Second || rec.waits[1] != 5*time.Second {
			t.Fatalf("waits=%v", rec.waits)
		}
	})
	t.Run("three 503s give ErrUnavailable", func(t *testing.T) {
		rec := &recorder{}
		s := step{status: 503, body: map[string]any{}}
		_, cfg := serve(t, rec, 4, s, s, s)
		_, err := newEmbedder(t, cfg, rec).EmbedQuery(context.Background(), "q")
		if !errors.Is(err, ErrUnavailable) || len(rec.bodies) != 3 {
			t.Fatalf("err=%v requests=%d", err, len(rec.bodies))
		}
	})
	t.Run("three 429s give ErrRateLimited", func(t *testing.T) {
		rec := &recorder{}
		s := step{status: 429, body: map[string]any{}}
		_, cfg := serve(t, rec, 4, s, s, s)
		if _, err := newEmbedder(t, cfg, rec).EmbedQuery(context.Background(), "q"); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("network errors are retried then reported as unavailable", func(t *testing.T) {
		rec := &recorder{}
		cfg := DefaultConfig()
		cfg.APIKey, cfg.BaseURL, cfg.Dimension = testKey, "http://127.0.0.1:1", 4
		_, err := newEmbedder(t, cfg, rec).EmbedQuery(context.Background(), "q")
		if !errors.Is(err, ErrUnavailable) || len(rec.waits) != 2 {
			t.Fatalf("err=%v waits=%v", err, rec.waits)
		}
	})
}

func TestContextCancellationStopsImmediately(t *testing.T) {
	rec := &recorder{}
	s := step{status: 503, body: map[string]any{}}
	_, cfg := serve(t, rec, 4, s, s, s)
	ctx, cancel := context.WithCancel(context.Background())
	e, _ := New(cfg, WithSleep(func(ctx context.Context, d time.Duration) error {
		cancel()
		return ctx.Err()
	}))
	_, err := e.EmbedQuery(ctx, "q")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if len(rec.bodies) != 1 {
		t.Fatalf("requests = %d, want 1", len(rec.bodies))
	}
}

func TestTimeoutIsRetriedAndThenUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()
	rec := &recorder{}
	cfg := DefaultConfig()
	cfg.APIKey, cfg.BaseURL, cfg.Dimension, cfg.Timeout = testKey, srv.URL, 4, 50*time.Millisecond
	_, err := newEmbedder(t, cfg, rec).EmbedQuery(context.Background(), "q")
	if !errors.Is(err, ErrUnavailable) || len(rec.waits) != 2 {
		t.Fatalf("err=%v waits=%v", err, rec.waits)
	}
}

func TestErrorsNeverContainKeyOrHost(t *testing.T) {
	scripts := [][]step{
		{{status: 503, body: map[string]any{"d": testKey}}, {status: 503, body: map[string]any{"d": testKey}}, {status: 503, body: map[string]any{"d": testKey}}},
		{{status: 401, body: map[string]any{"d": testKey}}},
		{{status: 200, body: map[string]any{"data": []any{}}}},
		{{status: 422, body: map[string]any{"d": testKey}}},
	}
	for i, script := range scripts {
		rec := &recorder{}
		srv, cfg := serve(t, rec, 4, script...)
		_, err := newEmbedder(t, cfg, rec).EmbedQuery(context.Background(), "q")
		if err == nil {
			t.Fatalf("script %d: expected an error", i)
		}
		host := strings.TrimPrefix(srv.URL, "http://")
		if strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), host) {
			t.Fatalf("script %d: error leaks: %v", i, err)
		}
	}
	cfg := DefaultConfig()
	cfg.APIKey, cfg.BaseURL, cfg.Dimension = testKey, "http://127.0.0.1:1", 4
	_, err := newEmbedder(t, cfg, &recorder{}).EmbedQuery(context.Background(), "q")
	if strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("network error leaks: %v", err)
	}
}

func TestNewRequiresAKeyAndSaneConfig(t *testing.T) {
	cfg := DefaultConfig()
	if _, err := New(cfg); err == nil || !strings.Contains(err.Error(), "EMBED_API_KEY") {
		t.Fatalf("err = %v", err)
	}
	cfg.APIKey = "k"
	cfg.Dimension = 0
	if _, err := New(cfg); err == nil {
		t.Fatal("expected an error for a zero dimension")
	}
}

func TestNormalize(t *testing.T) {
	v, err := Normalize([]float64{3, 4})
	if err != nil || math.Abs(float64(v[0])-0.6) > 1e-6 || math.Abs(float64(v[1])-0.8) > 1e-6 {
		t.Fatalf("v=%v err=%v", v, err)
	}
	for _, bad := range [][]float64{{0, 0}, {math.NaN(), 1}, {math.Inf(1), 1}} {
		if _, err := Normalize(bad); !errors.Is(err, ErrInvalidResponse) {
			t.Errorf("Normalize(%v) err = %v", bad, err)
		}
	}
}

func TestRetryAfterParsing(t *testing.T) {
	for in, want := range map[string]time.Duration{"7": 7 * time.Second, "0.5": 500 * time.Millisecond, "": 0, "abc": 0, "-3": 0, "NaN": 0, "Inf": 0} {
		if got := retryAfter(in); got != want {
			t.Errorf("retryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

func item(index any, scale float64) map[string]any {
	m := map[string]any{"embedding": vectorJSON(4, scale)}
	if index != nil {
		m["index"] = index
	}
	return m
}

func TestEmbedQueriesSendsOneRequestAndMatchesByIndex(t *testing.T) {
	rec := &recorder{}
	// out of order on purpose: index 1 first
	_, cfg := serve(t, rec, 4, step{200, map[string]any{"data": []any{item(1, -0.01), item(0, 0.01)}}, nil})
	vecs, err := newEmbedder(t, cfg, rec).EmbedQueries(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.bodies) != 1 {
		t.Fatalf("requests = %d, want 1", len(rec.bodies))
	}
	input, _ := rec.bodies[0]["input"].([]any)
	if len(input) != 2 || input[0] != "first" || input[1] != "second" || rec.bodies[0]["input_type"] != "search_query" {
		t.Fatalf("unexpected body: %v", rec.bodies[0])
	}
	if len(vecs) != 2 || vecs[0][0] <= 0 || vecs[1][0] >= 0 {
		t.Fatalf("vectors not matched by index: %v", vecs)
	}
}

func TestEmbedQueriesBadBatchResponses(t *testing.T) {
	cases := []struct {
		name string
		data []any
	}{
		{"too few", []any{item(0, 0.01)}},
		{"too many", []any{item(0, 0.01), item(1, 0.01), item(2, 0.01)}},
		{"duplicate index", []any{item(0, 0.01), item(0, 0.02)}},
		{"index out of range", []any{item(0, 0.01), item(2, 0.01)}},
		{"missing index in a batch", []any{item(nil, 0.01), item(1, 0.01)}},
		{"wrong dimension", []any{item(0, 0.01), map[string]any{"index": 1, "embedding": []float64{1, 2}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := &recorder{}
			_, cfg := serve(t, rec, 4, step{200, map[string]any{"data": c.data}, nil})
			_, err := newEmbedder(t, cfg, rec).EmbedQueries(context.Background(), []string{"a", "b"})
			if !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("err = %v, want ErrInvalidResponse", err)
			}
		})
	}
}

func TestEmbedQueriesSingleInputMayOmitIndex(t *testing.T) {
	rec := &recorder{}
	_, cfg := serve(t, rec, 4, step{200, map[string]any{"data": []any{item(nil, 0.01)}}, nil})
	if _, err := newEmbedder(t, cfg, rec).EmbedQuery(context.Background(), "q"); err != nil {
		t.Fatal(err)
	}
}

func TestEmbedQueriesRejectsABlankTextWithoutARequest(t *testing.T) {
	rec := &recorder{}
	_, cfg := serve(t, rec, 4)
	_, err := newEmbedder(t, cfg, rec).EmbedQueries(context.Background(), []string{"ok", "   "})
	if !errors.Is(err, ErrInputRejected) || len(rec.bodies) != 0 {
		t.Fatalf("err = %v, requests = %d; want ErrInputRejected and none", err, len(rec.bodies))
	}
	vecs, err := newEmbedder(t, cfg, rec).EmbedQueries(context.Background(), nil)
	if err != nil || len(vecs) != 0 || len(rec.bodies) != 0 {
		t.Fatal("an empty batch must return nothing and send nothing")
	}
}

func TestEmbedQueriesRetriesLikeASingleQuery(t *testing.T) {
	rec := &recorder{}
	ok := step{200, map[string]any{"data": []any{item(0, 0.01), item(1, 0.02)}}, nil}
	_, cfg := serve(t, rec, 4, step{status: 429, body: map[string]any{}}, step{status: 503, body: map[string]any{}}, ok)
	if _, err := newEmbedder(t, cfg, rec).EmbedQueries(context.Background(), []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if len(rec.bodies) != 3 {
		t.Fatalf("requests = %d, want 3", len(rec.bodies))
	}
}
