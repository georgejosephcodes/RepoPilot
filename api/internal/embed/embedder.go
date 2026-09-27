// Package embed turns a question into a vector through an OpenAI-compatible embeddings API.
//
// It must agree with the Python worker that embedded the documents: same model, same dimension,
// the configured input_type for questions (EMBED_QUERY_TYPE), unit-length vectors, same character cut. The shared
// fixture testdata/embed_contract.json is what keeps the two honest.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Embedder is what the rest of the API depends on. Nothing outside this package knows the provider.
type Embedder interface {
	ModelName() string
	Dimension() int
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
}

// Typed errors so callers can map them to API responses without matching strings.
// Their messages are safe to show: no key, no URL, no upstream body.
var (
	ErrUnauthorized    = errors.New("embedding API key rejected or expired")
	ErrNotFound        = errors.New("embedding model not found")
	ErrInputRejected   = errors.New("embedding input rejected")
	ErrRateLimited     = errors.New("embedding rate limit reached, try again later")
	ErrUnavailable     = errors.New("embedding service unavailable, try again later")
	ErrInvalidResponse = errors.New("embedding service returned an unexpected response")
	ErrUpstream        = errors.New("embedding service error")
)

const (
	maxAttempts     = 3
	maxRetryAfter   = 5 * time.Second
	maxResponseSize = 4 << 20
)

var backoff = [...]time.Duration{500 * time.Millisecond, time.Second}

type OpenAICompat struct {
	cfg    Config
	client *http.Client
	sleep  func(ctx context.Context, d time.Duration) error
	url    string
}

type Option func(*OpenAICompat)

func WithHTTPClient(c *http.Client) Option { return func(e *OpenAICompat) { e.client = c } }

func WithSleep(fn func(ctx context.Context, d time.Duration) error) Option {
	return func(e *OpenAICompat) { e.sleep = fn }
}

func New(cfg Config, opts ...Option) (*OpenAICompat, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("embedding is not configured: set EMBED_API_KEY")
	}
	if cfg.Dimension < 1 || cfg.MaxChars < 1 || cfg.Model == "" || cfg.BaseURL == "" {
		return nil, errors.New("embedding configuration is incomplete")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	e := &OpenAICompat{
		cfg:    cfg,
		client: &http.Client{},
		sleep:  sleepContext,
		url:    strings.TrimRight(cfg.BaseURL, "/") + "/embeddings",
	}
	for _, o := range opts {
		o(e)
	}
	return e, nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *OpenAICompat) ModelName() string { return e.cfg.Model }
func (e *OpenAICompat) Dimension() int    { return e.cfg.Dimension }

// Prepare cuts text to MaxChars characters (code points, like Python's str slicing).
func (e *OpenAICompat) Prepare(text string) string {
	if len(text) <= e.cfg.MaxChars { // a string has at most len(text) characters
		return text
	}
	runes := []rune(text)
	if len(runes) <= e.cfg.MaxChars {
		return text
	}
	return string(runes[:e.cfg.MaxChars])
}

// retryable wraps an error that is worth another attempt, with the wait the server asked for.
type retryable struct {
	err  error
	wait time.Duration
}

func (r *retryable) Error() string { return r.err.Error() }
func (r *retryable) Unwrap() error { return r.err }

func (e *OpenAICompat) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	vecs, err := e.EmbedQueries(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// EmbedQueries embeds several questions in one request. Vectors come back in input order, matched to the
// inputs by the response's index field. The caller keeps a batch within the provider's limit (64 here).
func (e *OpenAICompat) EmbedQueries(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	prepared := make([]string, len(texts))
	for i, t := range texts {
		prepared[i] = e.Prepare(t)
		if strings.TrimSpace(prepared[i]) == "" {
			return nil, ErrInputRejected
		}
	}
	body, err := json.Marshal(e.requestBody(prepared))
	if err != nil {
		return nil, ErrUpstream
	}

	var last error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		vecs, err := e.once(ctx, body, len(texts))
		if err == nil {
			return vecs, nil
		}
		var r *retryable
		if !errors.As(err, &r) {
			return nil, err
		}
		last = r.err
		if attempt == maxAttempts-1 {
			break
		}
		wait := backoff[min(attempt, len(backoff)-1)]
		if r.wait > 0 {
			wait = min(r.wait, maxRetryAfter)
		}
		if err := e.sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
	return nil, last
}

func (e *OpenAICompat) requestBody(texts []string) map[string]any {
	body := map[string]any{"model": e.cfg.Model, "input": texts}
	if e.cfg.InputTypes {
		body["input_type"] = e.cfg.QueryType
	}
	if e.cfg.SendDimensions {
		body["dimensions"] = e.cfg.Dimension
	}
	return body
}

func (e *OpenAICompat) once(ctx context.Context, body []byte, n int) ([][]float32, error) {
	reqCtx, cancel := context.WithTimeout(ctx, e.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return nil, ErrUpstream
	}
	req.Header.Set("Authorization", "Bearer "+e.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err() // the caller gave up; do not retry
		}
		slog.Warn("embedding request failed", "error_type", fmt.Sprintf("%T", err)) // the error text would contain the URL
		return nil, &retryable{err: ErrUnavailable}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &retryable{err: ErrUnavailable}
	}

	if resp.StatusCode == http.StatusOK {
		return e.parse(raw, n)
	}
	slog.Warn("embedding request got an error status", "status", resp.StatusCode, "body", snippet(raw))
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return nil, &retryable{err: ErrRateLimited, wait: retryAfter(resp.Header.Get("Retry-After"))}
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return nil, &retryable{err: ErrUnavailable, wait: retryAfter(resp.Header.Get("Retry-After"))}
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrUnauthorized
	case http.StatusNotFound:
		return nil, ErrNotFound
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		return nil, ErrInputRejected
	}
	return nil, ErrUpstream
}

func retryAfter(header string) time.Duration {
	secs, err := strconv.ParseFloat(strings.TrimSpace(header), 64)
	if err != nil || secs < 0 || math.IsNaN(secs) || math.IsInf(secs, 0) {
		return 0
	}
	return time.Duration(secs * float64(time.Second))
}

func snippet(raw []byte) string {
	if len(raw) > 300 {
		raw = raw[:300]
	}
	return string(raw)
}

// parse expects exactly n vectors. With one input a missing index means 0; with several, every item must
// carry a distinct index in [0, n), because position alone is not a promise the API makes.
func (e *OpenAICompat) parse(raw []byte, n int) ([][]float32, error) {
	var payload struct {
		Data []struct {
			Index     *int      `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || len(payload.Data) != n {
		return nil, ErrInvalidResponse
	}
	out := make([][]float32, n)
	for _, item := range payload.Data {
		i := 0
		if item.Index != nil {
			i = *item.Index
		} else if n > 1 {
			return nil, ErrInvalidResponse
		}
		if i < 0 || i >= n || out[i] != nil || len(item.Embedding) != e.cfg.Dimension {
			return nil, ErrInvalidResponse
		}
		vec, err := Normalize(item.Embedding)
		if err != nil {
			return nil, err
		}
		out[i] = vec
	}
	return out, nil
}

// Normalize returns the vector scaled to unit length, or ErrInvalidResponse for a zero or non-finite vector.
func Normalize(v []float64) ([]float32, error) {
	var sum float64
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, ErrInvalidResponse
		}
		sum += x * x
	}
	norm := math.Sqrt(sum)
	if norm == 0 {
		return nil, ErrInvalidResponse
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(x / norm)
	}
	return out, nil
}
