// Package llm calls the answer model. The rest of the API sees only the LLM interface.
package llm

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

type Result struct {
	Text         string
	FinishReason string // "STOP", "MAX_TOKENS", ...
	InputTokens  int
	OutputTokens int
}

type LLM interface {
	ModelName() string
	Generate(ctx context.Context, system, user string) (Result, error)
}

// Typed errors with messages that are safe to show: no key, no URL, no upstream body.
var (
	ErrUnauthorized  = errors.New("answer model API key rejected or expired")
	ErrNotFound      = errors.New("answer model not found")
	ErrInputRejected = errors.New("answer model rejected the request")
	ErrRateLimited   = errors.New("answer model rate limit reached, try again later")
	ErrUnavailable   = errors.New("answer model unavailable, try again later")
	ErrBlocked       = errors.New("answer model declined this request")
	ErrEmpty         = errors.New("answer model returned no answer")
	ErrUpstream      = errors.New("answer model error")
)

const (
	maxAttempts     = 3
	maxRetryAfter   = 8 * time.Second
	maxResponseSize = 8 << 20
)

var backoff = [...]time.Duration{time.Second, 2 * time.Second}

// blockedFinishReasons are finish reasons meaning the model refused or was filtered.
var blockedFinishReasons = map[string]bool{
	"SAFETY": true, "RECITATION": true, "BLOCKLIST": true, "PROHIBITED_CONTENT": true, "SPII": true,
}

type Gemini struct {
	cfg    Config
	client *http.Client
	sleep  func(ctx context.Context, d time.Duration) error
	url    string
}

type Option func(*Gemini)

func WithHTTPClient(c *http.Client) Option { return func(g *Gemini) { g.client = c } }

func WithSleep(fn func(ctx context.Context, d time.Duration) error) Option {
	return func(g *Gemini) { g.sleep = fn }
}

func NewGemini(cfg Config, opts ...Option) (*Gemini, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("answer model is not configured: set LLM_API_KEY")
	}
	if cfg.Model == "" || cfg.BaseURL == "" || cfg.MaxOutputTokens < 1 {
		return nil, errors.New("answer model configuration is incomplete")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 45 * time.Second
	}
	g := &Gemini{
		cfg:    cfg,
		client: &http.Client{},
		sleep:  sleepContext,
		url:    strings.TrimRight(cfg.BaseURL, "/") + "/models/" + cfg.Model + ":generateContent",
	}
	for _, o := range opts {
		o(g)
	}
	return g, nil
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

func (g *Gemini) ModelName() string { return g.cfg.Model }

type retryable struct {
	err  error
	wait time.Duration
}

func (r *retryable) Error() string { return r.err.Error() }
func (r *retryable) Unwrap() error { return r.err }

func (g *Gemini) requestBody(system, user string) ([]byte, error) {
	gen := map[string]any{
		"temperature":     g.cfg.Temperature,
		"maxOutputTokens": g.cfg.MaxOutputTokens,
	}
	if g.cfg.ThinkingLevel != "" {
		gen["thinkingConfig"] = map[string]any{"thinkingLevel": g.cfg.ThinkingLevel}
	}
	return json.Marshal(map[string]any{
		"systemInstruction": map[string]any{"parts": []map[string]any{{"text": system}}},
		"contents":          []map[string]any{{"role": "user", "parts": []map[string]any{{"text": user}}}},
		"generationConfig":  gen,
	})
}

func (g *Gemini) Generate(ctx context.Context, system, user string) (Result, error) {
	body, err := g.requestBody(system, user)
	if err != nil {
		return Result{}, ErrUpstream
	}
	var last error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		res, err := g.once(ctx, body)
		if err == nil {
			return res, nil
		}
		var r *retryable
		if !errors.As(err, &r) {
			return Result{}, err
		}
		last = r.err
		if attempt == maxAttempts-1 {
			break
		}
		wait := backoff[min(attempt, len(backoff)-1)]
		if r.wait > 0 {
			wait = min(r.wait, maxRetryAfter)
		}
		if err := g.sleep(ctx, wait); err != nil {
			return Result{}, err
		}
	}
	return Result{}, last
}

func (g *Gemini) once(ctx context.Context, body []byte) (Result, error) {
	reqCtx, cancel := context.WithTimeout(ctx, g.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, g.url, bytes.NewReader(body))
	if err != nil {
		return Result{}, ErrUpstream
	}
	req.Header.Set("x-goog-api-key", g.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		slog.Warn("answer model request failed", "error_type", fmt.Sprintf("%T", err)) // the error text would contain the URL
		return Result{}, &retryable{err: ErrUnavailable}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, &retryable{err: ErrUnavailable}
	}

	if resp.StatusCode == http.StatusOK {
		return parse(raw)
	}
	slog.Warn("answer model returned an error status", "status", resp.StatusCode, "body", snippet(raw))
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return Result{}, &retryable{err: ErrRateLimited, wait: retryAfter(resp.Header.Get("Retry-After"))}
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return Result{}, &retryable{err: ErrUnavailable, wait: retryAfter(resp.Header.Get("Retry-After"))}
	case http.StatusUnauthorized, http.StatusForbidden:
		return Result{}, ErrUnauthorized
	case http.StatusNotFound:
		return Result{}, ErrNotFound
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
		return Result{}, ErrInputRejected
	}
	return Result{}, ErrUpstream
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

type response struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text    string `json:"text"`
				Thought bool   `json:"thought"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

func parse(raw []byte) (Result, error) {
	var r response
	if err := json.Unmarshal(raw, &r); err != nil {
		return Result{}, ErrUpstream
	}
	if r.PromptFeedback.BlockReason != "" {
		return Result{}, ErrBlocked
	}
	if len(r.Candidates) == 0 {
		return Result{}, ErrEmpty
	}
	cand := r.Candidates[0]
	if blockedFinishReasons[cand.FinishReason] {
		return Result{}, ErrBlocked
	}
	var text strings.Builder
	for _, p := range cand.Content.Parts {
		if !p.Thought {
			text.WriteString(p.Text)
		}
	}
	answer := strings.TrimSpace(text.String())
	if answer == "" {
		return Result{}, ErrEmpty
	}
	return Result{
		Text:         answer,
		FinishReason: cand.FinishReason,
		InputTokens:  r.UsageMetadata.PromptTokenCount,
		OutputTokens: r.UsageMetadata.CandidatesTokenCount,
	}, nil
}
