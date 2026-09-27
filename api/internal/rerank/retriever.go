package rerank

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"repopilot/api/internal/retrieval"
)

// DefaultTimeout bounds one upstream rerank call. On timeout the base order is used.
const DefaultTimeout = 10 * time.Second

// Store caches rankings by key, with the time the upstream call took when the ranking was made (so a re-run
// from the cache still reports real latency). Implementations must be safe for concurrent use.
type Store interface {
	Get(ctx context.Context, key string) (e Entry, ok bool, err error)
	Put(ctx context.Context, key, model string, e Entry) error
}

// Entry is one cached ranking.
type Entry struct {
	Ranking []int
	Took    time.Duration // the upstream call that produced it
}

// Retriever reranks the first Depth chunks of Base with Reranker. Chunks the reranker lists come first, in its
// order; the rest follow in Base order, so nothing Base found is lost. Any reranker failure (error, timeout,
// unparseable reply) returns Base's order and is counted as a fallback: a question is never lost to the reranker.
type Retriever struct {
	Base     retrieval.Retriever
	Reranker Reranker
	Depth    int           // candidates shown to the reranker, 1..retrieval.MaxK
	Cache    Store         // optional; a failing cache is logged and ignored
	Timeout  time.Duration // per upstream call; <= 0 means DefaultTimeout
	// Pace, when set, is called before every upstream call (never on a cache hit), outside the timeout.
	Pace func(ctx context.Context) error

	stats Stats
	mu    sync.Mutex
}

// Stats counts what the reranker did. Durations exclude pacing.
type Stats struct {
	Calls     int            // upstream calls
	CacheHits int            // rankings reused from the cache
	Fallbacks map[string]int // reason -> count: "timeout", "unparseable", "error"
	Durations []time.Duration
	// CachedDurations are the upstream times recorded when the cached rankings were made.
	CachedDurations []time.Duration
}

var ErrBadConfig = errors.New("invalid rerank settings")

// Stats returns a copy of the counters.
func (r *Retriever) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := Stats{Calls: r.stats.Calls, CacheHits: r.stats.CacheHits, Fallbacks: map[string]int{},
		Durations:       append([]time.Duration(nil), r.stats.Durations...),
		CachedDurations: append([]time.Duration(nil), r.stats.CachedDurations...)}
	for k, v := range r.stats.Fallbacks {
		s.Fallbacks[k] = v
	}
	return s
}

// FallbackCount is the total number of fallbacks for any reason.
func (s Stats) FallbackCount() int {
	n := 0
	for _, v := range s.Fallbacks {
		n += v
	}
	return n
}

// Percentile returns the p-th percentile (0..100, nearest rank) of every ranking's upstream time, whether it was
// measured in this run or when a cached ranking was made; 0 with none.
func (s Stats) Percentile(p float64) time.Duration {
	d := append(append([]time.Duration(nil), s.Durations...), s.CachedDurations...)
	if len(d) == 0 {
		return 0
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	i := int(float64(len(d))*p/100+0.999999) - 1
	return d[max(0, min(i, len(d)-1))]
}

func (r *Retriever) validate() error {
	if r.Base == nil || r.Reranker == nil || r.Depth < 1 || r.Depth > retrieval.MaxK {
		return fmt.Errorf("%w: base, reranker and depth 1..%d are required", ErrBadConfig, retrieval.MaxK)
	}
	return nil
}

// candidates fetches enough from Base for both the rerank depth and k.
func (r *Retriever) candidates(ctx context.Context, repoID int64, question string, queryVec []float32, k int) ([]retrieval.Chunk, error) {
	if k < 1 || k > retrieval.MaxK {
		return nil, retrieval.ErrInvalidK
	}
	if err := r.validate(); err != nil {
		return nil, err
	}
	return r.Base.Retrieve(ctx, repoID, question, queryVec, max(k, r.Depth))
}

func (r *Retriever) Retrieve(ctx context.Context, repoID int64, question string, queryVec []float32, k int) ([]retrieval.Chunk, error) {
	base, err := r.candidates(ctx, repoID, question, queryVec, k)
	if err != nil {
		return nil, err
	}
	head := base[:min(r.Depth, len(base))]
	ranking, err := r.ranking(ctx, question, head)
	if err != nil {
		return nil, err // only a cancelled context gets here; every reranker failure falls back
	}
	out := Apply(base, ranking)
	return out[:min(k, len(out))], nil
}

// Pending reports whether this question would need an upstream call (its ranking is not cached). It runs Base,
// which is cheap, and never calls the reranker.
func (r *Retriever) Pending(ctx context.Context, repoID int64, question string, queryVec []float32, k int) (bool, error) {
	base, err := r.candidates(ctx, repoID, question, queryVec, k)
	if err != nil {
		return false, err
	}
	head := base[:min(r.Depth, len(base))]
	if len(head) == 0 || r.Cache == nil {
		return len(head) > 0, nil
	}
	_, ok, err := r.Cache.Get(ctx, Key(r.Reranker.Name(), question, head))
	if err != nil {
		return false, err
	}
	return !ok, nil
}

// ranking returns the reranker's order for head: from the cache, from an upstream call, or nil (Base order) on
// failure. It returns an error only when ctx is done.
func (r *Retriever) ranking(ctx context.Context, question string, head []retrieval.Chunk) ([]int, error) {
	if len(head) == 0 {
		return nil, nil
	}
	key := Key(r.Reranker.Name(), question, head)
	if r.Cache != nil {
		e, ok, err := r.Cache.Get(ctx, key)
		switch {
		case err != nil && ctx.Err() != nil:
			return nil, ctx.Err()
		case err != nil:
			slog.Warn("rerank cache read failed", "error", err)
		case ok:
			r.count(func(s *Stats) { s.CacheHits++; s.CachedDurations = append(s.CachedDurations, e.Took) })
			return e.Ranking, nil
		}
	}

	if r.Pace != nil {
		if err := r.Pace(ctx); err != nil {
			return nil, err
		}
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	start := time.Now()
	ranking, err := r.Reranker.Rerank(callCtx, question, head)
	took := time.Since(start)
	cancel()
	r.count(func(s *Stats) { s.Calls++; s.Durations = append(s.Durations, took) })

	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		reason := "error"
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			reason = "timeout"
		case errors.Is(err, ErrUnparseable):
			reason = "unparseable"
		}
		r.count(func(s *Stats) {
			if s.Fallbacks == nil {
				s.Fallbacks = map[string]int{}
			}
			s.Fallbacks[reason]++
		})
		slog.Warn("rerank failed, using base order", "reason", reason, "error", err, "ms", took.Milliseconds())
		return nil, nil
	}
	if r.Cache != nil {
		if err := r.Cache.Put(ctx, key, r.Reranker.Name(), Entry{Ranking: ranking, Took: took}); err != nil {
			slog.Warn("rerank cache write failed", "error", err)
		}
	}
	return ranking, nil
}

func (r *Retriever) count(fn func(*Stats)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(&r.stats)
}

// Apply puts the chunks named by ranking (indexes into base) first, in that order, then every other chunk in
// base order. Out-of-range and repeated indexes are ignored. base is not modified.
func Apply(base []retrieval.Chunk, ranking []int) []retrieval.Chunk {
	out := make([]retrieval.Chunk, 0, len(base))
	used := make([]bool, len(base))
	for _, i := range ranking {
		if i >= 0 && i < len(base) && !used[i] {
			used[i] = true
			out = append(out, base[i])
		}
	}
	for i, c := range base {
		if !used[i] {
			out = append(out, c)
		}
	}
	return out
}

// Key identifies one ranking: reranker name (prompt version and model), the trimmed question, and each
// candidate's id and content hash, in order. Any change to these gives a new key.
func Key(reranker, question string, cands []retrieval.Chunk) string {
	h := sha256.New()
	field := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	field(reranker)
	field(strings.TrimSpace(question))
	for _, c := range cands {
		sum := sha256.Sum256([]byte(c.Content))
		field(fmt.Sprintf("%d:%x", c.ID, sum))
	}
	return hex.EncodeToString(h.Sum(nil))
}
