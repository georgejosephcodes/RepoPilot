package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"repopilot/api/internal/repos"
)

type repoHandlers struct {
	store repos.Store
}

func (h repoHandlers) create(c *gin.Context) {
	var req struct {
		URL string `json:"url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(c, http.StatusRequestEntityTooLarge, "invalid_request", "request body too large")
			return
		}
		writeError(c, http.StatusBadRequest, "invalid_request", `body must be JSON like {"url": "https://github.com/<owner>/<repo>"}`)
		return
	}

	ref, err := repos.ParseGitHubURL(req.URL)
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_url", err.Error()+". Use https://github.com/<owner>/<repo>")
		return
	}

	res, err := h.store.CreateOrGet(c.Request.Context(), ref)
	if err != nil {
		slog.Error("create repository", "err", err)
		writeError(c, http.StatusInternalServerError, "internal", "could not create repository")
		return
	}

	// 202: work was queued. 200: repository already existed, nothing queued.
	status := http.StatusOK
	if res.Created || res.Requeued {
		status = http.StatusAccepted
	}
	c.JSON(status, gin.H{
		"id":       res.Repo.ID,
		"status":   res.Repo.Status,
		"created":  res.Created,
		"requeued": res.Requeued,
	})
}

func (h repoHandlers) list(c *gin.Context) {
	list, err := h.store.List(c.Request.Context())
	if err != nil {
		slog.Error("list repositories", "err", err)
		writeError(c, http.StatusInternalServerError, "internal", "could not list repositories")
		return
	}
	c.JSON(http.StatusOK, list)
}

func (h repoHandlers) get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(c, http.StatusBadRequest, "invalid_request", "id must be a positive integer")
		return
	}
	d, err := h.store.Get(c.Request.Context(), id)
	if errors.Is(err, repos.ErrNotFound) {
		writeError(c, http.StatusNotFound, "not_found", "repository not found")
		return
	}
	if err != nil {
		slog.Error("get repository", "err", err, "id", id)
		writeError(c, http.StatusInternalServerError, "internal", "could not load repository")
		return
	}
	c.JSON(http.StatusOK, d)
}
