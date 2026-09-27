package rerank

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStore is the Postgres Store (table rerank_cache, migrations/004_rerank_cache.sql).
type PgStore struct{ pool *pgxpool.Pool }

func NewPgStore(pool *pgxpool.Pool) *PgStore { return &PgStore{pool: pool} }

// Get also refreshes last_used, so a size cap added later can drop the least recently used rows.
func (s *PgStore) Get(ctx context.Context, key string) (Entry, bool, error) {
	var ranking []int32
	var tookMS int64
	err := s.pool.QueryRow(ctx,
		`UPDATE rerank_cache SET last_used = now() WHERE cache_key = $1 RETURNING ranking, took_ms`, key).Scan(&ranking, &tookMS)
	if errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, false, nil
	}
	if err != nil {
		return Entry{}, false, err
	}
	e := Entry{Ranking: make([]int, len(ranking)), Took: time.Duration(tookMS) * time.Millisecond}
	for i, v := range ranking {
		e.Ranking[i] = int(v)
	}
	return e, true, nil
}

func (s *PgStore) Put(ctx context.Context, key, model string, e Entry) error {
	vals := make([]int32, len(e.Ranking))
	for i, v := range e.Ranking {
		vals[i] = int32(v)
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO rerank_cache (cache_key, ranking, model, took_ms) VALUES ($1, $2::int[], $3, $4)
ON CONFLICT (cache_key) DO UPDATE SET ranking = EXCLUDED.ranking, model = EXCLUDED.model, took_ms = EXCLUDED.took_ms,
    last_used = now()`,
		key, vals, model, e.Took.Milliseconds())
	return err
}

// CheckSchema fails with a clear message when migrations/004_rerank_cache.sql has not been applied.
func CheckSchema(ctx context.Context, pool *pgxpool.Pool) error {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('rerank_cache') IS NOT NULL`).Scan(&exists); err != nil {
		return fmt.Errorf("check rerank_cache: %w", err)
	}
	if !exists {
		return errors.New("rerank_cache is missing: apply migrations/004_rerank_cache.sql")
	}
	return nil
}
