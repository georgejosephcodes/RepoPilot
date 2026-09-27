package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"repopilot/api/internal/rag"
)

// A question needs an embedding request plus a generation request, each with retries, so it gets more
// time than any other route. The http.Server WriteTimeout must be larger than this.
const queryTimeout = 90 * time.Second

type queryHandler struct {
	asker Asker
}

func (h queryHandler) query(c *gin.Context) {
	if h.asker == nil {
		writeError(c, http.StatusServiceUnavailable, "not_configured",
			"question answering is not configured: set EMBED_API_KEY and LLM_API_KEY")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(c, http.StatusBadRequest, "invalid_request", "id must be a positive integer")
		return
	}
	var req struct {
		Question string `json:"question"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(c, http.StatusRequestEntityTooLarge, "invalid_request", "request body too large")
			return
		}
		writeError(c, http.StatusBadRequest, "invalid_request", `body must be JSON like {"question": "..."}`)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), queryTimeout)
	defer cancel()
	resp, err := h.asker.Ask(ctx, id, req.Question)
	if err != nil {
		p := rag.Classify(err)
		if p.Status >= http.StatusInternalServerError {
			slog.Warn("question failed", "repo_id", id, "status", p.Status, "code", p.Code, "error", err)
		}
		writeError(c, p.Status, p.Code, p.Message)
		return
	}
	c.JSON(http.StatusOK, resp)
}
