package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testKey = "AIzaSECRETKEY123"

type recorder struct {
	mu      sync.Mutex
	waits   []time.Duration
	bodies  []map[string]any
	keys    []string
	paths   []string
	methods []string
}

func (r *recorder) sleep(_ context.Context, d time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.waits = append(r.waits, d)
	return nil
}

type step struct {
	status  int
	body    any
	headers map[string]string
}

func okBody(text string) map[string]any {
	return map[string]any{
		"candidates": []any{map[string]any{
			"content":      map[string]any{"parts": []any{map[string]any{"text": text}}},
			"finishReason": "STOP",
		}},
		"usageMetadata": map[string]any{"promptTokenCount": 538, "candidatesTokenCount": 52, "totalTokenCount": 590},
	}
}

func serve(t *testing.T, rec *recorder, script ...step) (*httptest.Server, Config) {
	t.Helper()
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		rec.bodies = append(rec.bodies, body)
		rec.keys = append(rec.keys, r.Header.Get("x-goog-api-key"))
		rec.paths = append(rec.paths, r.URL.Path)
		rec.methods = append(rec.methods, r.Method)
		var s step
		if len(script) > 0 {
			s, script = script[0], script[1:]
		} else {
			s = step{status: 200, body: okBody("The answer [1].")}
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
	cfg.BaseURL = srv.URL + "/v1beta/"
	cfg.APIKey = testKey
	return srv, cfg
}

func newLLM(t *testing.T, cfg Config, rec *recorder) *Gemini {
	t.Helper()
	g, err := NewGemini(cfg, WithSleep(rec.sleep))
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestRequestShape(t *testing.T) {
	rec := &recorder{}
	_, cfg := serve(t, rec)
	res, err := newLLM(t, cfg, rec).Generate(context.Background(), "SYSTEM TEXT", "USER TEXT")
	if err != nil {
		t.Fatal(err)
	}
	if rec.methods[0] != http.MethodPost || rec.paths[0] != "/v1beta/models/gemini-3.5-flash-lite:generateContent" {
		t.Fatalf("%s %s", rec.methods[0], rec.paths[0])
	}
	if rec.keys[0] != testKey {
		t.Fatalf("key header = %q", rec.keys[0])
	}
	b := rec.bodies[0]
	sys := b["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"]
	contents := b["contents"].([]any)[0].(map[string]any)
	user := contents["parts"].([]any)[0].(map[string]any)["text"]
	if sys != "SYSTEM TEXT" || user != "USER TEXT" || contents["role"] != "user" {
		t.Fatalf("unexpected body: %v", b)
	}
	gen := b["generationConfig"].(map[string]any)
	if gen["temperature"] != 0.1 || gen["maxOutputTokens"] != float64(1024) {
		t.Fatalf("generationConfig = %v", gen)
	}
	if gen["thinkingConfig"].(map[string]any)["thinkingLevel"] != "minimal" {
		t.Fatalf("thinkingConfig = %v", gen["thinkingConfig"])
	}
	if res.Text != "The answer [1]." || res.FinishReason != "STOP" || res.InputTokens != 538 || res.OutputTokens != 52 {
		t.Fatalf("result = %+v", res)
	}
}

func TestThinkingConfigIsOmittedWhenEmpty(t *testing.T) {
	rec := &recorder{}
	_, cfg := serve(t, rec)
	cfg.ThinkingLevel = ""
	if _, err := newLLM(t, cfg, rec).Generate(context.Background(), "s", "u"); err != nil {
		t.Fatal(err)
	}
	if _, has := rec.bodies[0]["generationConfig"].(map[string]any)["thinkingConfig"]; has {
		t.Fatal("thinkingConfig must be omitted")
	}
}

func TestKeyIsNeverInTheURL(t *testing.T) {
	rec := &recorder{}
	srv, cfg := serve(t, rec)
	g := newLLM(t, cfg, rec)
	if strings.Contains(g.url, testKey) || !strings.HasPrefix(g.url, srv.URL) {
		t.Fatalf("url = %q", g.url)
	}
}

func TestTextIsJoinedFromPartsAndThoughtsAreSkipped(t *testing.T) {
	rec := &recorder{}
	body := map[string]any{"candidates": []any{map[string]any{
		"content": map[string]any{"parts": []any{
			map[string]any{"text": "hidden reasoning", "thought": true},
			map[string]any{"text": "First part. "},
			map[string]any{"text": "Second part [2]."},
		}},
		"finishReason": "STOP",
	}}}
	_, cfg := serve(t, rec, step{status: 200, body: body})
	res, err := newLLM(t, cfg, rec).Generate(context.Background(), "s", "u")
	if err != nil || res.Text != "First part. Second part [2]." {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestMaxTokensIsNotAnError(t *testing.T) {
	rec := &recorder{}
	body := okBody("The")
	body["candidates"].([]any)[0].(map[string]any)["finishReason"] = "MAX_TOKENS"
	_, cfg := serve(t, rec, step{status: 200, body: body})
	res, err := newLLM(t, cfg, rec).Generate(context.Background(), "s", "u")
	if err != nil || res.FinishReason != "MAX_TOKENS" || res.Text != "The" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestBlockedAndEmptyResponses(t *testing.T) {
	withReason := func(reason string) map[string]any {
		b := okBody("partial text")
		b["candidates"].([]any)[0].(map[string]any)["finishReason"] = reason
		return b
	}
	tests := []struct {
		name string
		body any
		want error
	}{
		{"finishReason SAFETY", withReason("SAFETY"), ErrBlocked},
		{"finishReason RECITATION", withReason("RECITATION"), ErrBlocked},
		{"finishReason PROHIBITED_CONTENT", withReason("PROHIBITED_CONTENT"), ErrBlocked},
		{"prompt blocked", map[string]any{"promptFeedback": map[string]any{"blockReason": "SAFETY"}}, ErrBlocked},
		{"no candidates", map[string]any{"candidates": []any{}}, ErrEmpty},
		{"no candidates key", map[string]any{"usageMetadata": map[string]any{}}, ErrEmpty},
		{"empty text", okBody("   \n"), ErrEmpty},
		{"no parts", map[string]any{"candidates": []any{map[string]any{"content": map[string]any{}, "finishReason": "STOP"}}}, ErrEmpty},
		{"not an object", "just text", ErrUpstream},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			_, cfg := serve(t, rec, step{status: 200, body: tc.body})
			_, err := newLLM(t, cfg, rec).Generate(context.Background(), "s", "u")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if len(rec.bodies) != 1 {
				t.Fatalf("must not retry, got %d requests", len(rec.bodies))
			}
		})
	}
}

func TestPermanentStatusesFailAtOnce(t *testing.T) {
	tests := []struct {
		status int
		want   error
	}{
		{401, ErrUnauthorized}, {403, ErrUnauthorized}, {404, ErrNotFound},
		{400, ErrInputRejected}, {413, ErrInputRejected}, {418, ErrUpstream},
	}
	for _, tc := range tests {
		rec := &recorder{}
		_, cfg := serve(t, rec, step{status: tc.status, body: map[string]any{"error": map[string]any{"message": "detail " + testKey}}})
		_, err := newLLM(t, cfg, rec).Generate(context.Background(), "s", "u")
		if !errors.Is(err, tc.want) || len(rec.bodies) != 1 {
			t.Errorf("status %d: err=%v requests=%d", tc.status, err, len(rec.bodies))
		}
	}
}

func TestRetries(t *testing.T) {
	t.Run("503 then success uses backoff", func(t *testing.T) {
		rec := &recorder{}
		_, cfg := serve(t, rec, step{status: 503, body: map[string]any{"error": map[string]any{"status": "UNAVAILABLE"}}})
		if _, err := newLLM(t, cfg, rec).Generate(context.Background(), "s", "u"); err != nil {
			t.Fatal(err)
		}
		if len(rec.bodies) != 2 || len(rec.waits) != 1 || rec.waits[0] != time.Second {
			t.Fatalf("requests=%d waits=%v", len(rec.bodies), rec.waits)
		}
	})
	t.Run("429 twice then success", func(t *testing.T) {
		rec := &recorder{}
		s := step{status: 429, body: map[string]any{}}
		_, cfg := serve(t, rec, s, s)
		if _, err := newLLM(t, cfg, rec).Generate(context.Background(), "s", "u"); err != nil {
			t.Fatal(err)
		}
		if len(rec.waits) != 2 || rec.waits[0] != time.Second || rec.waits[1] != 2*time.Second {
			t.Fatalf("waits=%v", rec.waits)
		}
	})
	t.Run("Retry-After honoured and capped", func(t *testing.T) {
		rec := &recorder{}
		_, cfg := serve(t, rec,
			step{status: 429, body: map[string]any{}, headers: map[string]string{"Retry-After": "4"}},
			step{status: 503, body: map[string]any{}, headers: map[string]string{"Retry-After": "999"}})
		if _, err := newLLM(t, cfg, rec).Generate(context.Background(), "s", "u"); err != nil {
			t.Fatal(err)
		}
		if len(rec.waits) != 2 || rec.waits[0] != 4*time.Second || rec.waits[1] != 8*time.Second {
			t.Fatalf("waits=%v", rec.waits)
		}
	})
	t.Run("three 503s give ErrUnavailable", func(t *testing.T) {
		rec := &recorder{}
		s := step{status: 503, body: map[string]any{}}
		_, cfg := serve(t, rec, s, s, s)
		_, err := newLLM(t, cfg, rec).Generate(context.Background(), "s", "u")
		if !errors.Is(err, ErrUnavailable) || len(rec.bodies) != 3 {
			t.Fatalf("err=%v requests=%d", err, len(rec.bodies))
		}
	})
	t.Run("three 429s give ErrRateLimited", func(t *testing.T) {
		rec := &recorder{}
		s := step{status: 429, body: map[string]any{}}
		_, cfg := serve(t, rec, s, s, s)
		if _, err := newLLM(t, cfg, rec).Generate(context.Background(), "s", "u"); !errors.Is(err, ErrRateLimited) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("network errors are retried", func(t *testing.T) {
		rec := &recorder{}
		cfg := DefaultConfig()
		cfg.APIKey, cfg.BaseURL = testKey, "http://127.0.0.1:1"
		_, err := newLLM(t, cfg, rec).Generate(context.Background(), "s", "u")
		if !errors.Is(err, ErrUnavailable) || len(rec.waits) != 2 {
			t.Fatalf("err=%v waits=%v", err, rec.waits)
		}
	})
}

func TestContextCancellationStopsImmediately(t *testing.T) {
	rec := &recorder{}
	s := step{status: 503, body: map[string]any{}}
	_, cfg := serve(t, rec, s, s, s)
	ctx, cancel := context.WithCancel(context.Background())
	g, _ := NewGemini(cfg, WithSleep(func(ctx context.Context, d time.Duration) error {
		cancel()
		return ctx.Err()
	}))
	if _, err := g.Generate(ctx, "s", "u"); !errors.Is(err, context.Canceled) || len(rec.bodies) != 1 {
		t.Fatalf("err=%v requests=%d", err, len(rec.bodies))
	}
}

func TestAttemptTimeoutIsRetriedThenUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer srv.Close()
	rec := &recorder{}
	cfg := DefaultConfig()
	cfg.APIKey, cfg.BaseURL, cfg.Timeout = testKey, srv.URL, 50*time.Millisecond
	_, err := newLLM(t, cfg, rec).Generate(context.Background(), "s", "u")
	if !errors.Is(err, ErrUnavailable) || len(rec.waits) != 2 {
		t.Fatalf("err=%v waits=%v", err, rec.waits)
	}
}

func TestErrorsNeverContainKeyOrHost(t *testing.T) {
	scripts := [][]step{
		{{status: 503, body: map[string]any{"d": testKey}}, {status: 503, body: map[string]any{"d": testKey}}, {status: 503, body: map[string]any{"d": testKey}}},
		{{status: 401, body: map[string]any{"d": testKey}}},
		{{status: 400, body: map[string]any{"d": testKey}}},
		{{status: 200, body: map[string]any{"promptFeedback": map[string]any{"blockReason": "SAFETY"}}}},
		{{status: 200, body: map[string]any{"candidates": []any{}}}},
	}
	for i, script := range scripts {
		rec := &recorder{}
		srv, cfg := serve(t, rec, script...)
		_, err := newLLM(t, cfg, rec).Generate(context.Background(), "s", "u")
		if err == nil {
			t.Fatalf("script %d: expected an error", i)
		}
		host := strings.TrimPrefix(srv.URL, "http://")
		if strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), host) {
			t.Fatalf("script %d: error leaks: %v", i, err)
		}
	}
	cfg := DefaultConfig()
	cfg.APIKey, cfg.BaseURL = testKey, "http://127.0.0.1:1"
	_, err := newLLM(t, cfg, &recorder{}).Generate(context.Background(), "s", "u")
	if strings.Contains(err.Error(), testKey) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("network error leaks: %v", err)
	}
}

func TestNewRequiresKeyAndSaneConfig(t *testing.T) {
	cfg := DefaultConfig()
	if _, err := NewGemini(cfg); err == nil || !strings.Contains(err.Error(), "LLM_API_KEY") {
		t.Fatalf("err = %v", err)
	}
	cfg.APIKey = "k"
	cfg.MaxOutputTokens = 0
	if _, err := NewGemini(cfg); err == nil {
		t.Fatal("expected an error for zero output tokens")
	}
}

func TestFakeRecordsCalls(t *testing.T) {
	f := NewFake("hello")
	res, err := f.Generate(context.Background(), "sys", "usr")
	if err != nil || res.Text != "hello" || f.Calls() != 1 || f.System != "sys" || f.User != "usr" {
		t.Fatalf("res=%+v err=%v calls=%d", res, err, f.Calls())
	}
	f.Err = ErrBlocked
	if _, err := f.Generate(context.Background(), "a", "b"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v", err)
	}
}
