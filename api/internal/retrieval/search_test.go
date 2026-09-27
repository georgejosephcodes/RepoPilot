package retrieval

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	dim   = 2048
	model = "test-model"
)

// DB-backed tests run only when TEST_DATABASE_URL is set. Each test creates its own repositories and
// deletes them afterwards, so they are safe on the dev database.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newRepo(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	owner := fmt.Sprintf("zzretr%d", time.Now().UnixNano())
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO repositories (url, owner, name) VALUES ($1, $2, 'r') RETURNING id`,
		"https://github.com/"+owner+"/r", owner).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM repositories WHERE id = $1`, id) })
	return id
}

func basis(i int) []float32 {
	v := make([]float32, dim)
	v[i] = 1
	return v
}

// mix returns a*e_i + b*e_j.
func mix(i, j int, a, b float32) []float32 {
	v := make([]float32, dim)
	v[i], v[j] = a, b
	return v
}

func addChunk(t *testing.T, pool *pgxpool.Pool, repoID int64, name, embedModel string, vec []float32) int64 {
	t.Helper()
	literal, err := VectorLiteral(vec, dim)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	err = pool.QueryRow(context.Background(),
		`INSERT INTO chunks (repo_id, commit_sha, file_path, language, symbol, kind, start_line, end_line, content, embedding, embed_model)
		 VALUES ($1, 'c', $2, 'go', $2, 'function', 1, 3, $3, $4::text::halfvec, $5) RETURNING id`,
		repoID, name, "content of "+name, literal, embedModel).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func searcher(pool *pgxpool.Pool) *PgSearcher { return NewPgSearcher(pool, model, dim) }

func names(chunks []Chunk) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.FilePath
	}
	return out
}

func TestSearchOrdersByCosineDistance(t *testing.T) {
	pool := testPool(t)
	repo := newRepo(t, pool)
	addChunk(t, pool, repo, "far", model, basis(2))
	addChunk(t, pool, repo, "near", model, basis(0))
	addChunk(t, pool, repo, "middle", model, mix(0, 1, 0.6, 0.8))

	got, err := searcher(pool).Search(context.Background(), repo, basis(0), 10)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names(got), ",") != "near,middle,far" {
		t.Fatalf("order = %v", names(got))
	}
	for i, want := range []float64{0, 0.4, 1} {
		if math.Abs(got[i].Distance-want) > 2e-3 {
			t.Errorf("distance[%d] = %v, want about %v", i, got[i].Distance, want)
		}
	}
	c := got[0]
	if c.Symbol != "near" || c.Kind != "function" || c.StartLine != 1 || c.EndLine != 3 || c.Language != "go" || c.Content != "content of near" || c.ID == 0 {
		t.Fatalf("fields not filled in: %+v", c)
	}
}

func TestSearchNeverLeaksAnotherRepository(t *testing.T) {
	pool := testPool(t)
	a, b := newRepo(t, pool), newRepo(t, pool)
	for i := 0; i < 5; i++ {
		addChunk(t, pool, a, fmt.Sprintf("a%d", i), model, basis(i))
		addChunk(t, pool, b, fmt.Sprintf("b%d", i), model, basis(i)) // identical vectors in the other repository
	}
	got, err := searcher(pool).Search(context.Background(), a, basis(0), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5", len(got))
	}
	for _, c := range got {
		if !strings.HasPrefix(c.FilePath, "a") {
			t.Fatalf("leaked chunk %q from another repository", c.FilePath)
		}
	}
}

func TestSearchRespectsKAndReturnsFewerWhenFewerExist(t *testing.T) {
	pool := testPool(t)
	repo := newRepo(t, pool)
	for i := 0; i < 5; i++ {
		addChunk(t, pool, repo, fmt.Sprintf("c%d", i), model, basis(i))
	}
	s := searcher(pool)
	if got, _ := s.Search(context.Background(), repo, basis(0), 3); len(got) != 3 {
		t.Fatalf("k=3 gave %d", len(got))
	}
	if got, _ := s.Search(context.Background(), repo, basis(0), 10); len(got) != 5 {
		t.Fatalf("k=10 gave %d", len(got))
	}
	if got, _ := s.Search(context.Background(), repo, basis(0), 1); len(got) != 1 || got[0].FilePath != "c0" {
		t.Fatalf("k=1 gave %v", names(got))
	}
}

func TestSearchOnEmptyRepositoryReturnsEmptySlice(t *testing.T) {
	pool := testPool(t)
	repo := newRepo(t, pool)
	got, err := searcher(pool).Search(context.Background(), repo, basis(0), 5)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %v err %v", got, err)
	}
	got, err = searcher(pool).Search(context.Background(), -1, basis(0), 5) // unknown repository
	if err != nil || len(got) != 0 {
		t.Fatalf("unknown repo: got %v err %v", got, err)
	}
}

func TestSearchRefusesAModelMismatch(t *testing.T) {
	pool := testPool(t)
	repo := newRepo(t, pool)
	addChunk(t, pool, repo, "one", model, basis(0))
	addChunk(t, pool, repo, "two", "some-other-model", basis(1))
	if _, err := searcher(pool).Search(context.Background(), repo, basis(0), 5); !errors.Is(err, ErrModelMismatch) {
		t.Fatalf("err = %v, want ErrModelMismatch", err)
	}
	other := NewPgSearcher(pool, "some-other-model", dim)
	if _, err := other.Search(context.Background(), repo, basis(0), 5); !errors.Is(err, ErrModelMismatch) {
		t.Fatalf("mixed models must fail for every searcher, got %v", err)
	}
}

func TestSearchValidatesItsInput(t *testing.T) {
	pool := testPool(t)
	repo := newRepo(t, pool)
	addChunk(t, pool, repo, "one", model, basis(0))
	s := searcher(pool)
	ctx := context.Background()
	for _, k := range []int{0, -1, 51, 1000} {
		if _, err := s.Search(ctx, repo, basis(0), k); !errors.Is(err, ErrInvalidK) {
			t.Errorf("k=%d: err = %v", k, err)
		}
	}
	if _, err := s.Search(ctx, repo, []float32{1, 2, 3}, 5); !errors.Is(err, ErrDimensionMismatch) {
		t.Errorf("short vector: err = %v", err)
	}
	bad := basis(0)
	bad[5] = float32(math.NaN())
	if _, err := s.Search(ctx, repo, bad, 5); !errors.Is(err, ErrInvalidVector) {
		t.Errorf("NaN: err = %v", err)
	}
}

func TestSearchHonoursContextCancellation(t *testing.T) {
	pool := testPool(t)
	repo := newRepo(t, pool)
	addChunk(t, pool, repo, "one", model, basis(0))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := searcher(pool).Search(ctx, repo, basis(0), 5); err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
}

// The scenario that broke the HNSW approach. Repository B holds 400 chunks all very near the question and
// repository A holds 20 chunks, 10 of them related to the question and 10 unrelated. Searching A must return
// A's 10 best chunks, in order, every time, however crowded the rest of the table is. (With an HNSW index and
// iterative scan on, this returned 0 rows on repeated runs.)
func TestSearchIsExactWhenAnotherRepositoryCrowdsTheNeighbourhood(t *testing.T) {
	pool := testPool(t)
	a, b := newRepo(t, pool), newRepo(t, pool)
	for i := 0; i < 400; i++ {
		addChunk(t, pool, b, fmt.Sprintf("b%d", i), model, mix(0, 1, 1, float32(i)*0.0005))
	}
	// A's related chunks sit at angles 0.1, 0.2, ... 1.0 radians from the question (well separated distances)
	for i := 1; i <= 10; i++ {
		theta := float64(i) * 0.1
		addChunk(t, pool, a, fmt.Sprintf("a-near-%02d", i), model, mix(0, 1, float32(math.Cos(theta)), float32(math.Sin(theta))))
	}
	for i := 0; i < 10; i++ {
		addChunk(t, pool, a, fmt.Sprintf("a-far-%02d", i), model, basis(20+i))
	}

	for run := 0; run < 3; run++ {
		got, err := searcher(pool).Search(context.Background(), a, basis(0), 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 10 {
			t.Fatalf("run %d: got %d rows, want 10", run, len(got))
		}
		for i, c := range got {
			if want := fmt.Sprintf("a-near-%02d", i+1); c.FilePath != want {
				t.Fatalf("run %d: rank %d is %q, want %q (order %v)", run, i+1, c.FilePath, want, names(got))
			}
		}
	}

	// k larger than the number of related chunks reaches into the unrelated ones, still only from A
	got, _ := searcher(pool).Search(context.Background(), a, basis(0), 20)
	if len(got) != 20 {
		t.Fatalf("k=20 gave %d rows", len(got))
	}
	for _, c := range got {
		if !strings.HasPrefix(c.FilePath, "a-") {
			t.Fatalf("leaked %q", c.FilePath)
		}
	}
}

// The search must not go through the HNSW index; that is what makes it exact.
func TestSearchQueryDoesNotUseTheHNSWIndex(t *testing.T) {
	pool := testPool(t)
	repo := newRepo(t, pool)
	for i := 0; i < 50; i++ {
		addChunk(t, pool, repo, fmt.Sprintf("c%d", i), model, basis(i))
	}
	literal, _ := VectorLiteral(basis(0), dim)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil { // the strongest push toward an index
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, "EXPLAIN "+searchSQL, literal, repo, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var line string
		_ = rows.Scan(&line)
		plan.WriteString(line + "\n")
	}
	if strings.Contains(plan.String(), "chunks_embedding_hnsw") {
		t.Fatalf("the exact search must not use the approximate index:\n%s", plan.String())
	}
}

func TestSearchOnASharedTableAlwaysFillsK(t *testing.T) {
	pool := testPool(t)
	a, b := newRepo(t, pool), newRepo(t, pool)
	for i := 0; i < 150; i++ {
		addChunk(t, pool, b, fmt.Sprintf("b%d", i), model, mix(0, 1, 1, float32(i)*0.001))
	}
	for i := 0; i < 12; i++ {
		addChunk(t, pool, a, fmt.Sprintf("a%d", i), model, basis(20+i))
	}
	got, err := searcher(pool).Search(context.Background(), a, basis(0), 10)
	if err != nil || len(got) != 10 {
		t.Fatalf("got %d rows, err %v", len(got), err)
	}
}

func TestCheckDimension(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if err := CheckDimension(ctx, pool, dim); err != nil {
		t.Fatalf("2048 should pass: %v", err)
	}
	err := CheckDimension(ctx, pool, 768)
	if !errors.Is(err, ErrDimensionMismatch) || !strings.Contains(err.Error(), "768") || !strings.Contains(err.Error(), "2048") {
		t.Fatalf("768 should fail with both numbers, got %v", err)
	}
}

func TestCheckPgvector(t *testing.T) {
	version, err := CheckPgvector(context.Background(), testPool(t))
	if err != nil {
		t.Fatalf("pgvector %s: %v", version, err)
	}
	if !versionAtLeast(version, 0, 8, 0) {
		t.Fatalf("version %q", version)
	}
}
