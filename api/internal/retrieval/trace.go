package retrieval

import (
	"context"
	"sync"
)

// TraceData is what one retrieval did: per-stage times, the rerank outcome, and the chunk ids of each list, best
// first. Stages that did not run stay zero or nil.
type TraceData struct {
	VectorMS  int64
	KeywordMS int64
	RerankMS  int64 // the upstream call, or the time recorded when a cached ranking was made
	// RerankSource is "upstream", "cache", or empty when no rerank ran.
	RerankSource string
	// RerankFallback is the reason the base order was used ("timeout", "unparseable", "error"), or empty.
	RerankFallback string

	Vector, Keyword, Fused, Reranked []int64
}

// Trace collects TraceData for one question. Retrievers fill it when the context carries one; hybrid retrieval
// fills it from two goroutines, so every access is locked.
type Trace struct {
	mu   sync.Mutex
	data TraceData
}

type traceKey struct{}

// WithTrace returns a context that carries a new Trace.
func WithTrace(ctx context.Context) (context.Context, *Trace) {
	t := &Trace{}
	return context.WithValue(ctx, traceKey{}, t), t
}

// TraceFrom returns the context's Trace, or nil. Record on a nil Trace does nothing.
func TraceFrom(ctx context.Context) *Trace {
	t, _ := ctx.Value(traceKey{}).(*Trace)
	return t
}

// Record changes the trace under its lock.
func (t *Trace) Record(fn func(*TraceData)) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	fn(&t.data)
}

// Snapshot returns a copy of what was recorded.
func (t *Trace) Snapshot() TraceData {
	if t == nil {
		return TraceData{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	d := t.data
	d.Vector = append([]int64(nil), d.Vector...)
	d.Keyword = append([]int64(nil), d.Keyword...)
	d.Fused = append([]int64(nil), d.Fused...)
	d.Reranked = append([]int64(nil), d.Reranked...)
	return d
}

// ChunkIDs lists the ids of chunks in order.
func ChunkIDs(cs []Chunk) []int64 {
	out := make([]int64, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}
