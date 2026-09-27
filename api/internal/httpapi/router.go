package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"repopilot/api/internal/rag"
	"repopilot/api/internal/repos"
	"repopilot/api/internal/retrieval"
)

// Pinger reports whether a dependency (the database) is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Asker answers a question about a repository, optionally filtered. *rag.Service implements it; tests use a fake.
type Asker interface {
	Ask(ctx context.Context, repoID int64, question string, filter retrieval.Filter) (rag.Response, error)
}

type Deps struct {
	DB                   Pinger
	Repos                repos.Store // repository routes are registered only when set
	RateLimitPerMin      int         // POST /api/repositories per client IP; 0 disables
	Query                Asker       // nil answers 503 not_configured on the query route
	QueryRateLimitPerMin int         // POST /api/repositories/:id/query per client IP; 0 disables
}

const (
	maxBodyBytes      = 4 << 10
	maxQueryBodyBytes = 8 << 10 // a 1,000-character question can be 4 KB of UTF-8 plus JSON
)

func NewRouter(d Deps) *gin.Engine {
	r := gin.New()
	_ = r.SetTrustedProxies(nil) // ClientIP is the socket peer, X-Forwarded-For is not trusted
	r.Use(gin.Recovery(), requestLog())

	r.GET("/healthz", healthz(d.DB))
	registerWeb(r)

	if d.Repos != nil {
		h := repoHandlers{store: d.Repos}
		q := queryHandler{asker: d.Query}
		api := r.Group("/api")
		api.POST("/repositories", bodyLimit(maxBodyBytes), rateLimit(d.RateLimitPerMin), h.create)
		api.GET("/repositories", h.list)
		api.GET("/repositories/:id", h.get)
		api.POST("/repositories/:id/query", bodyLimit(maxQueryBodyBytes), rateLimit(d.QueryRateLimitPerMin), q.query)
	}
	return r
}

func healthz(db Pinger) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable", "db": "down"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok", "db": "up"})
	}
}
