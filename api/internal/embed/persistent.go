package embed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
)

// BatchEmbedder can embed several questions in one upstream request.
type BatchEmbedder interface {
	Embedder
	EmbedQueries(ctx context.Context, texts []string) ([][]float32, error)
}

// QueryStore keeps question vectors across restarts, keyed by model and the hash of the exact text sent.
type QueryStore interface {
	Get(ctx context.Context, model string, hashes []string) (map[string][]float32, error)
	Put(ctx context.Context, model string, vecs map[string][]float32) error
}

// DefaultBatchSize is the most questions sent in one request, the same batch size the worker uses.
const DefaultBatchSize = 64

// PersistentEmbedder answers from a QueryStore first and asks the inner embedder only for misses, which it
// then stores. The store is an optimisation: when it fails, the question is still answered and the failure is
// logged. Questions are trimmed and cut the same way the inner embedder cuts them, so the hash is taken over
// exactly the text that would be sent.
type PersistentEmbedder struct {
	inner     BatchEmbedder
	store     QueryStore
	prepare   func(string) string
	batchSize int
}

func NewPersistent(inner BatchEmbedder, store QueryStore, batchSize int) *PersistentEmbedder {
	if batchSize < 1 {
		batchSize = DefaultBatchSize
	}
	prepare := func(s string) string { return s }
	if p, ok := inner.(interface{ Prepare(string) string }); ok {
		prepare = p.Prepare
	}
	return &PersistentEmbedder{inner: inner, store: store, prepare: prepare, batchSize: batchSize}
}

func (p *PersistentEmbedder) ModelName() string { return p.inner.ModelName() }
func (p *PersistentEmbedder) Dimension() int    { return p.inner.Dimension() }

func (p *PersistentEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	vecs, err := p.EmbedQueries(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// TextHash is the cache key for a prepared question: sha256 hex of its UTF-8 bytes.
func TextHash(prepared string) string {
	sum := sha256.Sum256([]byte(prepared))
	return hex.EncodeToString(sum[:])
}

// Misses counts the distinct texts that are not cached, so a caller can see what a batch would cost before
// sending it. Upstream requests needed = ceil(misses / batch size).
func (p *PersistentEmbedder) Misses(ctx context.Context, texts []string) (int, error) {
	seen := map[string]bool{}
	var unique []string
	for _, t := range texts {
		h := TextHash(p.prepare(strings.TrimSpace(t)))
		if !seen[h] {
			seen[h] = true
			unique = append(unique, h)
		}
	}
	found, err := p.store.Get(ctx, p.inner.ModelName(), unique)
	if err != nil {
		return 0, err
	}
	return len(unique) - len(found), nil
}

// Requests is how many upstream requests n misses need.
func (p *PersistentEmbedder) Requests(misses int) int {
	return (misses + p.batchSize - 1) / p.batchSize
}

// EmbedQueries returns one vector per text, in input order. Repeated texts cost one lookup and at most one
// upstream slot; misses go upstream in batches of at most batchSize.
func (p *PersistentEmbedder) EmbedQueries(ctx context.Context, texts []string) ([][]float32, error) {
	model := p.inner.ModelName()
	prepared := make([]string, len(texts))
	hashes := make([]string, len(texts))
	var unique []string
	textOf := map[string]string{}
	for i, t := range texts {
		prepared[i] = p.prepare(strings.TrimSpace(t))
		hashes[i] = TextHash(prepared[i])
		if _, seen := textOf[hashes[i]]; !seen {
			textOf[hashes[i]] = prepared[i]
			unique = append(unique, hashes[i])
		}
	}

	found, err := p.store.Get(ctx, model, unique)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		slog.Warn("question cache read failed, embedding without it", "error", err.Error())
		found = map[string][]float32{}
	}

	var misses []string
	for _, h := range unique {
		if _, ok := found[h]; !ok {
			misses = append(misses, h)
		}
	}
	for start := 0; start < len(misses); start += p.batchSize {
		batch := misses[start:min(start+p.batchSize, len(misses))]
		batchTexts := make([]string, len(batch))
		for i, h := range batch {
			batchTexts[i] = textOf[h]
		}
		vecs, err := p.inner.EmbedQueries(ctx, batchTexts)
		if err != nil {
			return nil, err
		}
		fresh := make(map[string][]float32, len(batch))
		for i, h := range batch {
			fresh[h] = vecs[i]
			found[h] = vecs[i]
		}
		if err := p.store.Put(ctx, model, fresh); err != nil {
			slog.Warn("question cache write failed", "error", err.Error())
		}
	}

	out := make([][]float32, len(texts))
	for i, h := range hashes {
		out[i] = copyVec(found[h])
	}
	return out, nil
}
