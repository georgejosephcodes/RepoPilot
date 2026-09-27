package repos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB-backed tests run only when TEST_DATABASE_URL is set. Each test uses a
// unique owner and deletes its rows afterwards, so it is safe on the dev database.
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

func testRef(t *testing.T, pool *pgxpool.Pool) RepoRef {
	t.Helper()
	owner := fmt.Sprintf("zztest%d", time.Now().UnixNano())
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM repositories WHERE lower(owner) = lower($1)`, owner)
	})
	return RepoRef{Owner: owner, Name: "Repo", URL: "https://github.com/" + owner + "/Repo"}
}

func countJobs(t *testing.T, pool *pgxpool.Pool, repoID int64) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM index_jobs WHERE repo_id = $1`, repoID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPgStore_CreateInsertsRepoAndJob(t *testing.T) {
	pool := testPool(t)
	s := NewPgStore(pool)
	ref := testRef(t, pool)

	res, err := s.CreateOrGet(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.Requeued || res.Repo.Status != "queued" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if n := countJobs(t, pool, res.Repo.ID); n != 1 {
		t.Fatalf("jobs = %d, want 1", n)
	}
	var jobStatus string
	pool.QueryRow(context.Background(), `SELECT status FROM index_jobs WHERE repo_id = $1`, res.Repo.ID).Scan(&jobStatus)
	if jobStatus != "queued" {
		t.Fatalf("job status = %q", jobStatus)
	}
}

func TestPgStore_DuplicateCreatesNothing(t *testing.T) {
	pool := testPool(t)
	s := NewPgStore(pool)
	ref := testRef(t, pool)

	first, err := s.CreateOrGet(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	// same repo, different casing
	upper := RepoRef{Owner: strings.ToUpper(ref.Owner), Name: "REPO", URL: strings.ToUpper(ref.URL)}
	for _, r := range []RepoRef{ref, upper} {
		again, err := s.CreateOrGet(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		if again.Created || again.Requeued || again.Repo.ID != first.Repo.ID {
			t.Fatalf("duplicate not detected: %+v", again)
		}
	}
	if n := countJobs(t, pool, first.Repo.ID); n != 1 {
		t.Fatalf("jobs = %d, want 1", n)
	}
}

func TestPgStore_FailedRepoIsRequeued(t *testing.T) {
	pool := testPool(t)
	s := NewPgStore(pool)
	ref := testRef(t, pool)

	first, err := s.CreateOrGet(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	pool.Exec(context.Background(), `UPDATE repositories SET status = 'failed' WHERE id = $1`, first.Repo.ID)

	again, err := s.CreateOrGet(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if again.Created || !again.Requeued || again.Repo.Status != "queued" {
		t.Fatalf("unexpected result: %+v", again)
	}
	if n := countJobs(t, pool, first.Repo.ID); n != 2 {
		t.Fatalf("jobs = %d, want 2", n)
	}
}

func TestPgStore_GetReturnsLatestJobProgress(t *testing.T) {
	pool := testPool(t)
	s := NewPgStore(pool)
	ref := testRef(t, pool)

	res, err := s.CreateOrGet(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	pool.Exec(context.Background(),
		`UPDATE index_jobs SET status = 'running', phase = 'embedding', files_done = 12, files_total = 40 WHERE repo_id = $1`,
		res.Repo.ID)

	d, err := s.Get(context.Background(), res.Repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Progress.Phase == nil || *d.Progress.Phase != "embedding" ||
		d.Progress.FilesDone == nil || *d.Progress.FilesDone != 12 ||
		d.Progress.FilesTotal == nil || *d.Progress.FilesTotal != 40 {
		t.Fatalf("unexpected progress: %+v", d.Progress)
	}
	if d.Owner != ref.Owner || d.Name != ref.Name {
		t.Fatalf("unexpected repo: %+v", d.Repository)
	}
}

func TestPgStore_GetUnknown(t *testing.T) {
	pool := testPool(t)
	_, err := NewPgStore(pool).Get(context.Background(), -1)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestPgStore_ListContainsCreated(t *testing.T) {
	pool := testPool(t)
	s := NewPgStore(pool)
	ref := testRef(t, pool)
	res, err := s.CreateOrGet(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range list {
		if r.ID == res.Repo.ID {
			return
		}
	}
	t.Fatalf("repo %d not in list", res.Repo.ID)
}
