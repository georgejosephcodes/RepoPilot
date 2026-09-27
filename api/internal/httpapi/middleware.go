package httpapi

import (
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// bodyLimit caps request bodies. Reads past the cap fail with *http.MaxBytesError.
func bodyLimit(max int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max)
		c.Next()
	}
}

// requestLog logs method, path (no query string), status, and duration.
func requestLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		slog.Info("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"ms", time.Since(start).Milliseconds(),
		)
	}
}

type bucket struct {
	tokens float64
	last   time.Time
}

// limiter is a per-key token bucket. Burst equals the per-minute rate.
type limiter struct {
	mu     sync.Mutex
	m      map[string]*bucket
	perSec float64
	burst  float64
	now    func() time.Time
}

func newLimiter(perMinute int) *limiter {
	return &limiter{
		m:      map[string]*bucket{},
		perSec: float64(perMinute) / 60,
		burst:  float64(perMinute),
		now:    time.Now,
	}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if len(l.m) > 10000 { // bound memory: drop idle keys
		for k, b := range l.m {
			if now.Sub(b.last) > 10*time.Minute {
				delete(l.m, k)
			}
		}
	}
	b := l.m[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.m[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.perSec)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// rateLimit limits requests per client IP. perMinute <= 0 disables it.
func rateLimit(perMinute int) gin.HandlerFunc {
	if perMinute <= 0 {
		return func(c *gin.Context) { c.Next() }
	}
	l := newLimiter(perMinute)
	return func(c *gin.Context) {
		if !l.allow(c.ClientIP()) {
			writeError(c, http.StatusTooManyRequests, "rate_limited", "too many requests, slow down")
			return
		}
		c.Next()
	}
}
