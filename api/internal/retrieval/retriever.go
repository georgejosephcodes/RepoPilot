package retrieval

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Retriever returns up to k chunks of one repository for a question, best first. A retriever may use the
// question text, its vector, or both.
type Retriever interface {
	Retrieve(ctx context.Context, repoID int64, question string, queryVec []float32, k int) ([]Chunk, error)
}

// VectorRetriever is Phase 1 retrieval: exact cosine search on the question vector.
type VectorRetriever struct{ Searcher Searcher }

func (v VectorRetriever) Retrieve(ctx context.Context, repoID int64, _ string, queryVec []float32, k int) ([]Chunk, error) {
	return v.Searcher.Search(ctx, repoID, queryVec, k)
}

// Scorer picks how keyword matches are ranked.
type Scorer int

const (
	// ScoreTSRank is PostgreSQL's cover density rank, divided by 1 + log(chunk length) (normalisation flag 1).
	ScoreTSRank Scorer = iota
	// ScoreBM25 is Okapi BM25 over the question's lexemes, with statistics from the repository's own chunks.
	ScoreBM25
)

// BM25 parameters, fixed in PHASE2.md 4.4 before any evaluation run.
const (
	bm25K1 = 1.2
	bm25B  = 0.75
	// A chunk that matches the whole tsquery (phrases and all) is multiplied by this, so exact identifiers and
	// literals win over chunks that only share words with them.
	bm25PhraseBoost = 1.5
)

// KeywordRetriever searches chunks.search_tsv (migrations/003_chunk_search.sql). It ignores the vector.
type KeywordRetriever struct {
	pool   *pgxpool.Pool
	scorer Scorer
}

func NewKeywordRetriever(pool *pgxpool.Pool, scorer Scorer) *KeywordRetriever {
	return &KeywordRetriever{pool: pool, scorer: scorer}
}

var ErrUnknownScorer = errors.New("unknown keyword scorer")

const chunkColumns = `c.id, c.file_path, c.language, COALESCE(c.symbol, ''), c.kind, c.start_line, c.end_line, c.content`

const tsRankSQL = `
SELECT ` + chunkColumns + `, ts_rank_cd(c.search_tsv, q, 1) AS score
FROM chunks c, to_tsquery('simple', $2) q
WHERE c.repo_id = $1 AND c.search_tsv @@ q
ORDER BY score DESC, c.id
LIMIT $3`

// BM25 with statistics computed at query time from this repository's chunks. Term frequency counts positions,
// with weight-A positions (symbol and path) counted twice; chunk length is its number of distinct lexemes.
const bm25SQL = `
WITH q AS (SELECT to_tsquery('simple', $2) AS tq),
docs AS (SELECT id, length(search_tsv)::float8 AS dl FROM chunks WHERE repo_id = $1),
stats AS (SELECT count(*)::float8 AS n, avg(dl) AS avgdl FROM docs),
tf AS (
    SELECT c.id, u.lexeme,
           (SELECT sum(CASE WHEN w = 'A' THEN 2 ELSE 1 END) FROM unnest(u.weights) AS w)::float8 AS tf
    FROM chunks c, unnest(c.search_tsv) AS u
    WHERE c.repo_id = $1 AND u.lexeme = ANY($3::text[])
),
df AS (SELECT lexeme, count(*)::float8 AS df FROM tf GROUP BY lexeme),
scored AS (
    SELECT tf.id,
           sum(ln(1 + (s.n - df.df + 0.5) / (df.df + 0.5))
               * tf.tf * ($5::float8 + 1) / (tf.tf + $5::float8 * (1 - $6::float8 + $6::float8 * d.dl / s.avgdl))) AS score
    FROM tf JOIN df USING (lexeme) JOIN docs d ON d.id = tf.id CROSS JOIN stats s
    GROUP BY tf.id
)
SELECT ` + chunkColumns + `,
       sc.score * CASE WHEN c.search_tsv @@ q.tq THEN $7::float8 ELSE 1 END AS score
FROM scored sc JOIN chunks c ON c.id = sc.id CROSS JOIN q
ORDER BY score DESC, c.id
LIMIT $4`

func (r *KeywordRetriever) Retrieve(ctx context.Context, repoID int64, question string, _ []float32, k int) ([]Chunk, error) {
	if k < 1 || k > MaxK {
		return nil, ErrInvalidK
	}
	kq := QueryTerms(question)
	if kq.TSQuery == "" {
		return []Chunk{}, nil
	}
	var sql string
	var args []any
	switch r.scorer {
	case ScoreTSRank:
		sql, args = tsRankSQL, []any{repoID, kq.TSQuery, k}
	case ScoreBM25:
		sql, args = bm25SQL, []any{repoID, kq.TSQuery, kq.Lexemes, k, bm25K1, bm25B, bm25PhraseBoost}
	default:
		return nil, ErrUnknownScorer
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) // read-only; the transaction only scopes the timeout
	if _, err := tx.Exec(ctx, "SET LOCAL statement_timeout = '"+statementTimeout+"'"); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("keyword search: %w", err)
	}
	defer rows.Close()
	out := make([]Chunk, 0, k)
	for rows.Next() {
		var c Chunk
		if err := rows.Scan(&c.ID, &c.FilePath, &c.Language, &c.Symbol, &c.Kind, &c.StartLine, &c.EndLine, &c.Content, &c.Score); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
