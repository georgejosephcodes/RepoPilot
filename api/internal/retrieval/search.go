package retrieval

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Chunk is one retrieved piece of a repository, ready to cite.
type Chunk struct {
	ID        int64
	FilePath  string
	Language  string
	Symbol    string
	Kind      string
	StartLine int
	EndLine   int
	Content   string
	Distance  float64 // cosine distance to the question; 0 means identical direction
}

type Searcher interface {
	// Search returns up to k chunks of one repository, nearest first. An empty repository gives an empty slice.
	Search(ctx context.Context, repoID int64, queryVec []float32, k int) ([]Chunk, error)
}

// The vector goes in as text and is cast on the server, so pgx needs no knowledge of the halfvec type.
//
// The search is EXACT on purpose. ORDER BY (distance) + 0 is an expression no index can serve, so the
// planner filters by repo_id (btree) and sorts that one repository's chunks by true distance. An HNSW
// index cannot do this reliably: it returns its nearest candidates across ALL repositories and only then
// applies the repo filter, so a small repository can get nothing back. Iterative scan reduces that but is
// capped by hnsw.max_scan_tuples and a work_mem-based memory budget, and with 2048-dimension vectors the
// budget is spent after about a thousand rows. Measured: with iterative scan on, a filtered search
// returned 0 rows when it needed 10. Per-repository scans are small, so exact
// search costs milliseconds and always returns k rows. `id` makes ties deterministic.
const searchSQL = `
SELECT id, file_path, language, COALESCE(symbol, ''), kind, start_line, end_line, content,
       embedding <=> $1::text::halfvec AS distance
FROM chunks
WHERE repo_id = $2
ORDER BY (embedding <=> $1::text::halfvec) + 0, id
LIMIT $3`

// A guard for a very large repository: fail instead of holding a connection for long.
const statementTimeout = "15s"

type PgSearcher struct {
	pool  *pgxpool.Pool
	model string
	dim   int
}

// NewPgSearcher searches chunks embedded with `model` at `dim` dimensions.
func NewPgSearcher(pool *pgxpool.Pool, model string, dim int) *PgSearcher {
	return &PgSearcher{pool: pool, model: model, dim: dim}
}

func (s *PgSearcher) Search(ctx context.Context, repoID int64, queryVec []float32, k int) ([]Chunk, error) {
	if k < 1 || k > MaxK {
		return nil, ErrInvalidK
	}
	literal, err := VectorLiteral(queryVec, s.dim)
	if err != nil {
		return nil, err
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) // nothing to commit; the transaction only scopes the SET LOCAL timeout

	models, err := indexedModels(ctx, tx, repoID)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return []Chunk{}, nil
	}
	for _, m := range models {
		if m != s.model {
			return nil, ErrModelMismatch
		}
	}

	if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = '"+statementTimeout+"'"); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, searchSQL, literal, repoID, k)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Chunk, 0, k)
	for rows.Next() {
		var c Chunk
		if err := rows.Scan(&c.ID, &c.FilePath, &c.Language, &c.Symbol, &c.Kind, &c.StartLine, &c.EndLine, &c.Content, &c.Distance); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func indexedModels(ctx context.Context, tx pgx.Tx, repoID int64) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT DISTINCT embed_model FROM chunks WHERE repo_id = $1`, repoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var models []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, err
		}
		models = append(models, m)
	}
	return models, rows.Err()
}

// CheckDimension fails when EMBED_DIM differs from the declared size of chunks.embedding.
func CheckDimension(ctx context.Context, pool *pgxpool.Pool, want int) error {
	var typmod int32
	err := pool.QueryRow(ctx,
		`SELECT atttypmod FROM pg_attribute WHERE attrelid = 'chunks'::regclass AND attname = 'embedding'`).Scan(&typmod)
	if err != nil {
		return fmt.Errorf("read the embedding column size: %w", err)
	}
	if int(typmod) != want {
		return fmt.Errorf("%w: EMBED_DIM is %d but the database column is %d", ErrDimensionMismatch, want, typmod)
	}
	return nil
}

// CheckPgvector fails on pgvector older than 0.8.0. The schema needs halfvec (0.7); 0.8 is the tested version.
func CheckPgvector(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var version string
	if err := pool.QueryRow(ctx, `SELECT extversion FROM pg_extension WHERE extname = 'vector'`).Scan(&version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errors.New("pgvector extension is not installed")
		}
		return "", err
	}
	if !versionAtLeast(version, 0, 8, 0) {
		return version, fmt.Errorf("pgvector %s is older than the tested 0.8.0", version)
	}
	return version, nil
}
