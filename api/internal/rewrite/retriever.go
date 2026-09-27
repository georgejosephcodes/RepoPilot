package rewrite

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"repopilot/api/internal/retrieval"
)

// DefaultTimeout bounds one upstream rewrite call. On timeout the question is searched as it is.
const DefaultTimeout = 10 * time.Second

// Entry is one cached rewrite, with the time its upstream call took (so a re-run from the cache reports it).
type Entry struct {
	Question string   `json:"question"` // for people reading the committed file
	Terms    []string `json:"terms"`
	Model    string   `json:"model"`
	TookMS   int64    `json:"took_ms"`
}

// Store caches rewrites by key. Implementations must be safe for concurrent use.
type Store interface {
	Get(key string) (Entry, bool)
	Put(key string, e Entry) error
}

// Key identifies one rewrite: the rewriter name (prompt version and model) and the trimmed question.
func Key(rewriter, question string) string {
	sum := sha256.Sum256([]byte(rewriter + "\x00" + strings.TrimSpace(question)))
	return hex.EncodeToString(sum[:])
}

// Retriever rewrites the question, sets Query.KeywordText, and calls Base. Any rewrite failure (error, timeout,
// unparseable reply) searches the plain question and is counted as a fallback. Only a cancelled context is an error.
type Retriever struct {
	Base     retrieval.Retriever
	Rewriter Rewriter
	Cache    Store         // optional
	Timeout  time.Duration // per upstream call; <= 0 means DefaultTimeout
	// Pace, when set, is called before every upstream call (never on a cache hit), outside the timeout.
	Pace func(ctx context.Context) error

	mu    sync.Mutex
	stats Stats
}

// Stats counts what the rewriter did. Durations are upstream call times; CachedDurations are the times recorded
// when the cached rewrites were made.
type Stats struct {
	Calls           int
	CacheHits       int
	Fallbacks       map[string]int // reason -> count: "timeout", "unparseable", "error"
	Durations       []time.Duration
	CachedDurations []time.Duration
}

func (s Stats) FallbackCount() int {
	n := 0
	for _, v := range s.Fallbacks {
		n += v
	}
	return n
}

// Percentile returns the p-th percentile (nearest rank) over every rewrite's upstream time; 0 with none.
func (s Stats) Percentile(p float64) time.Duration {
	d := append(append([]time.Duration(nil), s.Durations...), s.CachedDurations...)
	if len(d) == 0 {
		return 0
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	i := int(float64(len(d))*p/100+0.999999) - 1
	return d[max(0, min(i, len(d)-1))]
}

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

var ErrBadConfig = errors.New("invalid rewrite settings")

func (r *Retriever) Retrieve(ctx context.Context, q retrieval.Query) ([]retrieval.Chunk, error) {
	if r.Base == nil || r.Rewriter == nil {
		return nil, ErrBadConfig
	}
	terms, err := r.Terms(ctx, q.Text)
	if err != nil {
		return nil, err
	}
	q.KeywordText = KeywordText(q.Text, terms)
	return r.Base.Retrieve(ctx, q)
}

// Pending reports whether the question's rewrite is not cached (it would need an upstream call). It sends nothing.
func (r *Retriever) Pending(question string) bool {
	if r.Cache == nil {
		return true
	}
	_, ok := r.Cache.Get(Key(r.Rewriter.Name(), question))
	return !ok
}

// Terms returns the question's terms: from the cache, from an upstream call, or nil on failure. It returns an
// error only when ctx is done.
func (r *Retriever) Terms(ctx context.Context, question string) ([]string, error) {
	key := Key(r.Rewriter.Name(), question)
	if r.Cache != nil {
		if e, ok := r.Cache.Get(key); ok {
			r.count(func(s *Stats) {
				s.CacheHits++
				s.CachedDurations = append(s.CachedDurations, time.Duration(e.TookMS)*time.Millisecond)
			})
			return e.Terms, nil
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
	terms, err := r.Rewriter.Rewrite(callCtx, question)
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
		slog.Warn("rewrite failed, searching the plain question", "reason", reason, "error", err, "ms", took.Milliseconds())
		return nil, nil
	}
	if r.Cache != nil {
		e := Entry{Question: strings.TrimSpace(question), Terms: terms, Model: r.Rewriter.Name(), TookMS: took.Milliseconds()}
		if err := r.Cache.Put(key, e); err != nil {
			slog.Warn("rewrite cache write failed", "error", err)
		}
	}
	return terms, nil
}

func (r *Retriever) count(fn func(*Stats)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(&r.stats)
}

// FileStore is a JSON file cache ({key: Entry}), written whole on every Put (the file is small), so an interrupted
// run keeps every rewrite made before the interruption.
type FileStore struct {
	mu      sync.Mutex
	path    string
	entries map[string]Entry
}

// OpenFileStore reads path, or starts empty when it does not exist.
func OpenFileStore(path string) (*FileStore, error) {
	s := &FileStore{path: path, entries: map[string]Entry{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.entries); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return s, nil
}

func (s *FileStore) Get(key string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	return e, ok
}

func (s *FileStore) Put(key string, e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = e
	data, err := json.MarshalIndent(s.entries, "", " ") // map keys are sorted, so the file diffs cleanly
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Len is the number of cached rewrites.
func (s *FileStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
