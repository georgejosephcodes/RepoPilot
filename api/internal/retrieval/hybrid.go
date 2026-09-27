package retrieval

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// HybridRetriever fuses a vector list and a keyword list with weighted Reciprocal Rank Fusion:
//
//	score = 1/(RRFK + vector rank) + KeywordWeight/(RRFK + keyword rank), times TestPenalty for test files.
//
// RRF needs no calibration between cosine distances and keyword scores; only ranks matter. Settings are chosen
// on the dev split (docs/phase2/hybrid-dev.md).
type HybridRetriever struct {
	Vector        Retriever
	Keyword       Retriever
	RRFK          int     // RRF constant; 60 is the usual value
	KeywordWeight float64 // in (0, 1]
	TestPenalty   float64 // in (0, 1]; 1 means no penalty
	Pool          int     // chunks taken from each list, 1..MaxK
}

var ErrBadHybridConfig = errors.New("invalid hybrid retrieval settings")

func (h HybridRetriever) validate() error {
	if h.Vector == nil || h.Keyword == nil || h.RRFK < 1 || h.Pool < 1 || h.Pool > MaxK ||
		h.KeywordWeight <= 0 || h.KeywordWeight > 1 || h.TestPenalty <= 0 || h.TestPenalty > 1 {
		return fmt.Errorf("%w: %+v", ErrBadHybridConfig, h)
	}
	return nil
}

func (h HybridRetriever) Retrieve(ctx context.Context, q Query) ([]Chunk, error) {
	k := q.K
	if k < 1 || k > MaxK {
		return nil, ErrInvalidK
	}
	if err := h.validate(); err != nil {
		return nil, err
	}
	var vec, kw []Chunk
	var vecErr, kwErr error
	var wg sync.WaitGroup
	wg.Add(2)
	pq := q
	pq.K = h.Pool
	go func() { defer wg.Done(); vec, vecErr = h.Vector.Retrieve(ctx, pq) }()
	go func() { defer wg.Done(); kw, kwErr = h.Keyword.Retrieve(ctx, pq) }()
	wg.Wait()
	if vecErr != nil {
		return nil, vecErr
	}
	if kwErr != nil {
		return nil, kwErr
	}

	fused := map[int64]*Chunk{}
	var order []int64
	add := func(list []Chunk, weight float64) {
		for i, c := range list {
			f, ok := fused[c.ID]
			if !ok {
				cc := c
				cc.Score = 0
				f = &cc
				fused[c.ID] = f
				order = append(order, c.ID)
			}
			f.Score += weight / float64(h.RRFK+i+1)
		}
	}
	add(vec, 1)
	add(kw, h.KeywordWeight)

	out := make([]Chunk, 0, len(order))
	for _, id := range order {
		c := fused[id]
		if IsTestPath(c.FilePath) {
			c.Score *= h.TestPenalty
		}
		out = append(out, *c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > k {
		out = out[:k]
	}
	TraceFrom(ctx).Record(func(d *TraceData) { d.Fused = ChunkIDs(out) })
	return out, nil
}
