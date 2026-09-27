package rerank

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"repopilot/api/internal/retrieval"
)

// DB-backed tests run only when TEST_DATABASE_URL is set (the dev database with migrations/004 applied).
// Each test uses its own key prefix and deletes its rows afterwards.
func testPool(t *testing.T) (*pgxpool.Pool, string) {
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
	prefix := fmt.Sprintf("zztest-%d-", time.Now().UnixNano())
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM rerank_cache WHERE cache_key LIKE $1`, prefix+"%")
	})
	return pool, prefix
}

func TestPgStoreRoundTripAndUpsert(t *testing.T) {
	pool, prefix := testPool(t)
	ctx := context.Background()
	if err := CheckSchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := NewPgStore(pool)

	if _, ok, err := s.Get(ctx, prefix+"missing"); ok || err != nil {
		t.Fatalf("missing key: ok %v err %v", ok, err)
	}
	if err := s.Put(ctx, prefix+"a", "rerank-v1/m", Entry{Ranking: []int{4, 0, 2}, Took: 1234 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get(ctx, prefix+"a")
	if err != nil || !ok || !reflect.DeepEqual(got.Ranking, []int{4, 0, 2}) || got.Took != 1234*time.Millisecond {
		t.Fatalf("got %+v %v %v", got, ok, err)
	}
	if err := s.Put(ctx, prefix+"a", "rerank-v1/m", Entry{Ranking: []int{}}); err != nil {
		t.Fatal(err)
	}
	got, ok, err = s.Get(ctx, prefix+"a")
	if err != nil || !ok || len(got.Ranking) != 0 || got.Took != 0 {
		t.Fatalf("empty ranking after upsert: %+v %v %v", got, ok, err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM rerank_cache WHERE cache_key LIKE $1`, prefix+"%").Scan(&n)
	if n != 1 {
		t.Errorf("%d rows, want 1", n)
	}
}

func TestRetrieverOverPgStore(t *testing.T) {
	pool, _ := testPool(t)
	ctx := context.Background()
	s := &scripted{ranking: []int{2, 0}}
	r := &Retriever{Base: &baseList{chunks: chunks(3)}, Reranker: s, Depth: 3, Cache: NewPgStore(pool)}
	question := fmt.Sprintf("zztest question %d", time.Now().UnixNano())
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM rerank_cache WHERE cache_key = $1`, Key(s.Name(), question, chunks(3)))
	})
	first, err := r.Retrieve(ctx, retrieval.Query{RepoID: 1, Text: question, K: 3})
	if err != nil {
		t.Fatal(err)
	}
	fresh := &Retriever{Base: &baseList{chunks: chunks(3)}, Reranker: &scripted{ranking: []int{1}}, Depth: 3, Cache: NewPgStore(pool)}
	second, err := fresh.Retrieve(ctx, retrieval.Query{RepoID: 1, Text: question, K: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids(first), []int64{102, 100, 101}) || !reflect.DeepEqual(ids(first), ids(second)) {
		t.Errorf("first %v second %v", ids(first), ids(second))
	}
	if st := fresh.Stats(); st.Calls != 0 || st.CacheHits != 1 {
		t.Errorf("second retriever stats %+v", st)
	}
}
