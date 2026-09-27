package rag

import (
	"context"
	"errors"
	"net/http"

	"repopilot/api/internal/embed"
	"repopilot/api/internal/llm"
	"repopilot/api/internal/repos"
	"repopilot/api/internal/retrieval"
)

// Problem is an API error: an HTTP status, a stable code, and a message that is safe to show.
type Problem struct {
	Status  int
	Code    string
	Message string
}

// Classify maps an error from Service.Ask to a Problem. Provider errors keep their own safe sentences, which
// already name the stage ("embedding ..." or "answer model ..."). Anything unknown becomes a generic 500.
func Classify(err error) Problem {
	switch {
	case errors.Is(err, ErrQuestionInvalid):
		return Problem{http.StatusBadRequest, "invalid_request", "question is empty or too long"}
	case errors.Is(err, repos.ErrNotFound):
		return Problem{http.StatusNotFound, "not_found", "repository not found"}
	case errors.Is(err, ErrNotReady):
		return Problem{http.StatusConflict, "repo_not_ready", "repository is not indexed yet"}
	case errors.Is(err, retrieval.ErrModelMismatch):
		return Problem{http.StatusUnprocessableEntity, "model_mismatch", "repository was indexed with a different embedding model, re-index required"}
	case errors.Is(err, llm.ErrBlocked):
		return Problem{http.StatusUnprocessableEntity, "answer_blocked", llm.ErrBlocked.Error()}
	case errors.Is(err, embed.ErrRateLimited), errors.Is(err, llm.ErrRateLimited):
		return Problem{http.StatusTooManyRequests, "rate_limited", "free-tier request limit reached, try again later"}
	case errors.Is(err, context.DeadlineExceeded):
		return Problem{http.StatusGatewayTimeout, "timeout", "the request took too long"}
	case errors.Is(err, context.Canceled):
		return Problem{499, "cancelled", "the request was cancelled"}
	}
	for _, sentinel := range []error{
		embed.ErrUnauthorized, embed.ErrNotFound, embed.ErrInputRejected, embed.ErrUnavailable, embed.ErrInvalidResponse, embed.ErrUpstream,
		llm.ErrUnauthorized, llm.ErrNotFound, llm.ErrInputRejected, llm.ErrUnavailable, llm.ErrEmpty, llm.ErrUpstream,
	} {
		if errors.Is(err, sentinel) {
			return Problem{http.StatusBadGateway, "upstream_error", sentinel.Error()}
		}
	}
	return Problem{http.StatusInternalServerError, "internal", "internal error"}
}
