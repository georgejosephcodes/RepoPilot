package embed

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math/rand"
	"sync/atomic"
)

// Fake is a deterministic Embedder for tests: unit vectors derived from the text, and a call counter.
type Fake struct {
	Model string
	Dim   int
	Err   error         // returned by every call when set
	Gate  chan struct{} // when set, a call blocks until the channel is closed or receives a value
	calls atomic.Int64
}

func NewFake(dim int) *Fake { return &Fake{Model: "fake-embed", Dim: dim} }

func (f *Fake) ModelName() string { return f.Model }
func (f *Fake) Dimension() int    { return f.Dim }
func (f *Fake) Calls() int        { return int(f.calls.Load()) }

// EmbedQueries counts as one call, like one upstream request.
func (f *Fake) EmbedQueries(ctx context.Context, texts []string) ([][]float32, error) {
	f.calls.Add(1)
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v, err := f.vector(ctx, t)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func (f *Fake) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	f.calls.Add(1)
	return f.vector(ctx, text)
}

func (f *Fake) vector(ctx context.Context, text string) ([]float32, error) {
	if f.Gate != nil {
		select {
		case <-f.Gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.Err != nil {
		return nil, f.Err
	}
	sum := sha256.Sum256([]byte(text))
	rng := rand.New(rand.NewSource(int64(binary.BigEndian.Uint64(sum[:8]))))
	raw := make([]float64, f.Dim)
	for i := range raw {
		raw[i] = rng.NormFloat64()
	}
	return Normalize(raw)
}
