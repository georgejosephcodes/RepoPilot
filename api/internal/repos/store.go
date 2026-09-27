package repos

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("repository not found")

type Repository struct {
	ID        int64     `json:"id"`
	URL       string    `json:"url"`
	Owner     string    `json:"owner"`
	Name      string    `json:"name"`
	CommitSHA *string   `json:"commit_sha"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type Progress struct {
	Phase      *string `json:"phase"`
	FilesDone  *int    `json:"files_done"`
	FilesTotal *int    `json:"files_total"`
}

// RepoDetail is a repository plus the state of its most recent indexing job.
type RepoDetail struct {
	Repository
	Progress Progress `json:"progress"`
	Error    *string  `json:"error"`
}

type CreateResult struct {
	Repo     Repository
	Created  bool // a new repository row was inserted
	Requeued bool // an existing failed repository got a new job
}

type Store interface {
	// CreateOrGet registers a repository and queues an indexing job. An existing
	// repository is returned unchanged, except a failed one is queued again.
	CreateOrGet(ctx context.Context, ref RepoRef) (CreateResult, error)
	List(ctx context.Context) ([]Repository, error)
	Get(ctx context.Context, id int64) (RepoDetail, error)
}

type PgStore struct {
	pool *pgxpool.Pool
}

func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

const repoCols = "id, url, owner, name, commit_sha, status, created_at"

func scanRepo(row pgx.Row) (Repository, error) {
	var r Repository
	err := row.Scan(&r.ID, &r.URL, &r.Owner, &r.Name, &r.CommitSHA, &r.Status, &r.CreatedAt)
	return r, err
}

func (s *PgStore) CreateOrGet(ctx context.Context, ref RepoRef) (CreateResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreateResult{}, err
	}
	defer tx.Rollback(ctx) // no-op after Commit

	repo, err := scanRepo(tx.QueryRow(ctx,
		`INSERT INTO repositories (url, owner, name) VALUES ($1, $2, $3)
		 ON CONFLICT (lower(owner), lower(name)) DO NOTHING
		 RETURNING `+repoCols, ref.URL, ref.Owner, ref.Name))

	res := CreateResult{}
	switch {
	case err == nil:
		res.Created = true
	case errors.Is(err, pgx.ErrNoRows):
		// Row lock so two concurrent submits cannot both re-queue a failed repo.
		repo, err = scanRepo(tx.QueryRow(ctx,
			`SELECT `+repoCols+` FROM repositories
			 WHERE lower(owner) = lower($1) AND lower(name) = lower($2) FOR UPDATE`,
			ref.Owner, ref.Name))
		if err != nil {
			return CreateResult{}, err
		}
		if repo.Status == "failed" {
			if _, err := tx.Exec(ctx, `UPDATE repositories SET status = 'queued' WHERE id = $1`, repo.ID); err != nil {
				return CreateResult{}, err
			}
			repo.Status = "queued"
			res.Requeued = true
		}
	default:
		return CreateResult{}, err
	}

	if res.Created || res.Requeued {
		if _, err := tx.Exec(ctx, `INSERT INTO index_jobs (repo_id) VALUES ($1)`, repo.ID); err != nil {
			return CreateResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateResult{}, err
	}
	res.Repo = repo
	return res, nil
}

func (s *PgStore) List(ctx context.Context) ([]Repository, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+repoCols+` FROM repositories ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Repository{}
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PgStore) Get(ctx context.Context, id int64) (RepoDetail, error) {
	var d RepoDetail
	err := s.pool.QueryRow(ctx,
		`SELECT r.id, r.url, r.owner, r.name, r.commit_sha, r.status, r.created_at,
		        j.phase, j.files_done, j.files_total, j.error
		 FROM repositories r
		 LEFT JOIN LATERAL (
		     SELECT phase, files_done, files_total, error
		     FROM index_jobs WHERE repo_id = r.id ORDER BY id DESC LIMIT 1
		 ) j ON true
		 WHERE r.id = $1`, id).
		Scan(&d.ID, &d.URL, &d.Owner, &d.Name, &d.CommitSHA, &d.Status, &d.CreatedAt,
			&d.Progress.Phase, &d.Progress.FilesDone, &d.Progress.FilesTotal, &d.Error)
	if errors.Is(err, pgx.ErrNoRows) {
		return RepoDetail{}, ErrNotFound
	}
	return d, err
}
