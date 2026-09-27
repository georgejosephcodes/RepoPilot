package rag

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"repopilot/api/internal/embed"
	"repopilot/api/internal/llm"
	"repopilot/api/internal/repos"
	"repopilot/api/internal/retrieval"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
		phrase string
	}{
		{ErrQuestionInvalid, 400, "invalid_request", "question"},
		{fmt.Errorf("%w: unknown language \"rust\"", retrieval.ErrInvalidFilter), 400, "invalid_request", "unknown language"},
		{repos.ErrNotFound, 404, "not_found", "not found"},
		{ErrNotReady, 409, "repo_not_ready", "not indexed"},
		{retrieval.ErrModelMismatch, 422, "model_mismatch", "re-index"},
		{llm.ErrBlocked, 422, "answer_blocked", "declined"},
		{embed.ErrRateLimited, 429, "rate_limited", "limit"},
		{llm.ErrRateLimited, 429, "rate_limited", "limit"},
		{embed.ErrUnauthorized, 502, "upstream_error", "embedding API key"},
		{embed.ErrNotFound, 502, "upstream_error", "embedding model"},
		{embed.ErrUnavailable, 502, "upstream_error", "embedding service"},
		{embed.ErrInvalidResponse, 502, "upstream_error", "embedding service"},
		{embed.ErrInputRejected, 502, "upstream_error", "embedding input"},
		{embed.ErrUpstream, 502, "upstream_error", "embedding service"},
		{llm.ErrUnauthorized, 502, "upstream_error", "answer model API key"},
		{llm.ErrNotFound, 502, "upstream_error", "answer model not found"},
		{llm.ErrUnavailable, 502, "upstream_error", "answer model unavailable"},
		{llm.ErrEmpty, 502, "upstream_error", "no answer"},
		{llm.ErrInputRejected, 502, "upstream_error", "answer model"},
		{llm.ErrUpstream, 502, "upstream_error", "answer model"},
		{context.DeadlineExceeded, 504, "timeout", "too long"},
		{context.Canceled, 499, "cancelled", "cancelled"},
		{errors.New("pq: connection refused at 10.0.0.5 password=hunter2"), 500, "internal", "internal error"},
	}
	for _, tc := range tests {
		t.Run(tc.err.Error(), func(t *testing.T) {
			p := Classify(tc.err)
			if p.Status != tc.status || p.Code != tc.code || !strings.Contains(p.Message, tc.phrase) {
				t.Fatalf("Classify(%v) = %+v, want %d %s containing %q", tc.err, p, tc.status, tc.code, tc.phrase)
			}
		})
	}
}

func TestClassifyUnwrapsWrappedErrors(t *testing.T) {
	wrapped := fmt.Errorf("stage failed: %w", llm.ErrUnauthorized)
	if p := Classify(wrapped); p.Status != http.StatusBadGateway || p.Code != "upstream_error" {
		t.Fatalf("p = %+v", p)
	}
}

func TestInternalErrorsNeverLeakTheirText(t *testing.T) {
	p := Classify(errors.New("dial tcp 10.0.0.5:5432: password=hunter2"))
	if strings.Contains(p.Message, "hunter2") || strings.Contains(p.Message, "10.0.0.5") {
		t.Fatalf("leaked: %+v", p)
	}
}
