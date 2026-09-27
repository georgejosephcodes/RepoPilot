package embed

import (
	"context"
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB-backed tests run only when TEST_DATABASE_URL is set (the dev database with migrations/002 applied).
// Each test uses its own model name and deletes its rows afterwards.
func testStore(t *testing.T) (*PgQueryStore, *pgxpool.Pool, string) {
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
	model := fmt.Sprintf("zztest-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM query_embedding_cache WHERE embed_model LIKE $1`, model+"%")
	})
	return NewPgQueryStore(pool, 2048), pool, model
}

func unitVec(seed int) []float32 {
	v, _ := NewFake(2048).vector(context.Background(), fmt.Sprint(seed))
	return v
}

func TestPgQueryStoreRoundTrip(t *testing.T) {
	s, _, model := testStore(t)
	ctx := context.Background()
	want := map[string][]float32{TextHash("a"): unitVec(1), TextHash("b"): unitVec(2)}
	if err := s.Put(ctx, model, want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, model, []string{TextHash("a"), TextHash("b"), TextHash("missing")})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d vectors, want 2", len(got))
	}
	for h, w := range want {
		g := got[h]
		if len(g) != 2048 {
			t.Fatalf("len = %d", len(g))
		}
		for i := range w { // halfvec keeps about 3 significant digits
			if math.Abs(float64(g[i]-w[i])) > 1e-3 {
				t.Fatalf("value %d: got %v want %v", i, g[i], w[i])
			}
		}
	}
}

func TestPgQueryStoreSeparatesModelsAndUpserts(t *testing.T) {
	s, pool, model := testStore(t)
	ctx := context.Background()
	h := TextHash("q")
	if err := s.Put(ctx, model+"-a", map[string][]float32{h: unitVec(1)}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, model+"-b", []string{h}); len(got) != 0 {
		t.Fatal("another model must not see this vector")
	}
	var before time.Time
	pool.QueryRow(ctx, `SELECT last_used FROM query_embedding_cache WHERE embed_model = $1`, model+"-a").Scan(&before)
	time.Sleep(10 * time.Millisecond)
	if err := s.Put(ctx, model+"-a", map[string][]float32{h: unitVec(2)}); err != nil {
		t.Fatalf("second put must upsert, got %v", err)
	}
	var n int
	var after time.Time
	pool.QueryRow(ctx, `SELECT count(*), max(last_used) FROM query_embedding_cache WHERE embed_model = $1`, model+"-a").Scan(&n, &after)
	if n != 1 || !after.After(before) {
		t.Fatalf("rows = %d, last_used moved = %v; want 1 and true", n, after.After(before))
	}
}

func TestPgQueryStoreEmptyCallsDoNothing(t *testing.T) {
	s, _, model := testStore(t)
	if got, err := s.Get(context.Background(), model, nil); err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
	if err := s.Put(context.Background(), model, nil); err != nil {
		t.Fatal(err)
	}
}

func TestPersistentOverPostgres(t *testing.T) {
	s, _, model := testStore(t)
	ctx := context.Background()
	fake := NewFake(2048)
	fake.Model = model
	p := NewPersistent(fake, s, 0)
	if _, err := p.EmbedQueries(ctx, []string{"x", "y", "x"}); err != nil {
		t.Fatal(err)
	}
	fresh := NewFake(2048) // a restarted API
	fresh.Model = model
	if _, err := NewPersistent(fresh, s, 0).EmbedQueries(ctx, []string{"y", "x"}); err != nil {
		t.Fatal(err)
	}
	if fake.Calls() != 1 || fresh.Calls() != 0 {
		t.Fatalf("upstream calls = %d then %d, want 1 then 0", fake.Calls(), fresh.Calls())
	}
}
