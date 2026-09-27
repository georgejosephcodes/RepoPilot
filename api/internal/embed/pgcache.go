package embed

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PgQueryStore is the Postgres QueryStore (table query_embedding_cache, migrations/002_query_cache.sql).
//
// Vectors travel as text and are cast on the server, as in retrieval/search.go, so pgx needs no knowledge of
// halfvec. halfvec stores 16-bit floats, so a vector read back is rounded; search casts every question vector
// to halfvec anyway, so a cached vector and a fresh one give the same search results.
type PgQueryStore struct {
	pool *pgxpool.Pool
	dim  int
}

func NewPgQueryStore(pool *pgxpool.Pool, dim int) *PgQueryStore {
	return &PgQueryStore{pool: pool, dim: dim}
}

// Get also refreshes last_used on every hit, so a size cap added later can drop the least recently used rows.
func (s *PgQueryStore) Get(ctx context.Context, model string, hashes []string) (map[string][]float32, error) {
	out := make(map[string][]float32, len(hashes))
	if len(hashes) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
UPDATE query_embedding_cache SET last_used = now()
WHERE embed_model = $1 AND text_hash = ANY($2)
RETURNING text_hash, embedding::text`, model, hashes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var hash, literal string
		if err := rows.Scan(&hash, &literal); err != nil {
			return nil, err
		}
		vec, err := ParseVectorLiteral(literal, s.dim)
		if err != nil {
			return nil, fmt.Errorf("cached vector %s: %w", hash[:min(12, len(hash))], err)
		}
		out[hash] = vec
	}
	return out, rows.Err()
}

func (s *PgQueryStore) Put(ctx context.Context, model string, vecs map[string][]float32) error {
	if len(vecs) == 0 {
		return nil
	}
	hashes := make([]string, 0, len(vecs))
	literals := make([]string, 0, len(vecs))
	for h, v := range vecs {
		literal, err := FormatVectorLiteral(v, s.dim)
		if err != nil {
			return err
		}
		hashes = append(hashes, h)
		literals = append(literals, literal)
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO query_embedding_cache (embed_model, text_hash, embedding)
SELECT $1, h, v::halfvec FROM unnest($2::text[], $3::text[]) AS t(h, v)
ON CONFLICT (embed_model, text_hash) DO UPDATE SET embedding = EXCLUDED.embedding, last_used = now()`,
		model, hashes, literals)
	return err
}

var ErrBadVectorLiteral = errors.New("malformed vector literal")

// FormatVectorLiteral renders [v1,v2,...], the text form PostgreSQL parses into a vector or halfvec.
func FormatVectorLiteral(v []float32, dim int) (string, error) {
	if len(v) != dim {
		return "", fmt.Errorf("%w: %d values, want %d", ErrBadVectorLiteral, len(v), dim)
	}
	var b strings.Builder
	b.Grow(len(v) * 10)
	b.WriteByte('[')
	for i, x := range v {
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return "", fmt.Errorf("%w: non-finite value", ErrBadVectorLiteral)
		}
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(f, 'g', -1, 32))
	}
	b.WriteByte(']')
	return b.String(), nil
}

// ParseVectorLiteral reads the text form back and checks the length and that every value is finite.
func ParseVectorLiteral(s string, dim int) ([]float32, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return nil, ErrBadVectorLiteral
	}
	parts := strings.Split(s[1:len(s)-1], ",")
	if len(parts) != dim {
		return nil, fmt.Errorf("%w: %d values, want %d", ErrBadVectorLiteral, len(parts), dim)
	}
	out := make([]float32, dim)
	for i, p := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 32)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, ErrBadVectorLiteral
		}
		out[i] = float32(f)
	}
	return out, nil
}
