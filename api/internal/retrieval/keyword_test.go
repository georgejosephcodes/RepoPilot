package retrieval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func addTextChunk(t *testing.T, pool *pgxpool.Pool, repoID int64, file, symbol, content string) int64 {
	t.Helper()
	literal, err := VectorLiteral(basis(0), dim)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	err = pool.QueryRow(context.Background(),
		`INSERT INTO chunks (repo_id, commit_sha, file_path, language, symbol, kind, start_line, end_line, content, embedding, embed_model)
		 VALUES ($1, 'c', $2, 'python', NULLIF($3, ''), 'function', 1, 3, $4, $5::text::halfvec, $6) RETURNING id`,
		repoID, file, symbol, content, literal, model).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func seedKeywordRepo(t *testing.T, pool *pgxpool.Pool) int64 {
	repo := newRepo(t, pool)
	addTextChunk(t, pool, repo, "src/encoding.py", "want_bytes", "def want_bytes(s, encoding='utf-8'):\n    return s.encode(encoding)")
	addTextChunk(t, pool, repo, "src/signer.py", "Signer.sign", "def sign(self, value):\n    value = want_bytes(value)\n    return value + self.sep")
	// Decoy: the same words many times, but never "want" directly before "bytes".
	addTextChunk(t, pool, repo, "tests/test_bytes.py", "test_bytes", "def test_bytes():\n    assert bytes(1) == b'\\x00'\n    # bytes we want: more bytes, bytes, bytes")
	addTextChunk(t, pool, repo, "src/index.ts", "isAbsoluteModule2", "function isAbsoluteModule2(remainder) {\n  return value => isInteger(value)\n}")
	addTextChunk(t, pool, repo, "src/detect.ts", "detect", "throw new TypeError('Please don\\'t use object wrappers for primitive types');")
	return repo
}

func files(chunks []Chunk) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.FilePath
	}
	return out
}

func TestKeywordFindsExactIdentifiersFirst(t *testing.T) {
	pool := testPool(t)
	repo := seedKeywordRepo(t, pool)
	cases := map[string]string{
		"What does want_bytes do?":                                                "src/encoding.py",
		"What does isAbsoluteModule2 do?":                                         "src/index.ts",
		"Where is 'Please don't use object wrappers for primitive types' thrown?": "src/detect.ts",
	}
	for _, scorer := range []Scorer{ScoreTSRank, ScoreBM25} {
		for q, want := range cases {
			got, err := NewKeywordRetriever(pool, scorer).Retrieve(context.Background(), repo, q, nil, 5)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) == 0 || got[0].FilePath != want {
				t.Errorf("scorer %d, %q: got %v, want %s first", scorer, q, files(got), want)
			}
			if len(got) > 0 && got[0].Score <= 0 {
				t.Errorf("scorer %d: score must be positive, got %v", scorer, got[0].Score)
			}
		}
	}
}

func TestKeywordIsRepositoryScoped(t *testing.T) {
	pool := testPool(t)
	a := seedKeywordRepo(t, pool)
	b := newRepo(t, pool)
	addTextChunk(t, pool, b, "other/want_bytes.py", "want_bytes", "def want_bytes(): pass")
	for _, scorer := range []Scorer{ScoreTSRank, ScoreBM25} {
		got, err := NewKeywordRetriever(pool, scorer).Retrieve(context.Background(), a, "want_bytes", nil, 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range got {
			if strings.HasPrefix(c.FilePath, "other/") {
				t.Fatalf("scorer %d returned another repository's chunk", scorer)
			}
		}
	}
}

func TestKeywordRespectsKAndIsDeterministic(t *testing.T) {
	pool := testPool(t)
	repo := seedKeywordRepo(t, pool)
	for _, scorer := range []Scorer{ScoreTSRank, ScoreBM25} {
		r := NewKeywordRetriever(pool, scorer)
		first, err := r.Retrieve(context.Background(), repo, "bytes want value", nil, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(first) != 2 {
			t.Fatalf("scorer %d: got %d chunks, want 2", scorer, len(first))
		}
		again, _ := r.Retrieve(context.Background(), repo, "bytes want value", nil, 2)
		if strings.Join(files(first), ",") != strings.Join(files(again), ",") {
			t.Fatalf("scorer %d: order changed between runs", scorer)
		}
	}
}

func TestKeywordEmptyQueryAndBadInput(t *testing.T) {
	pool := testPool(t)
	repo := seedKeywordRepo(t, pool)
	got, err := NewKeywordRetriever(pool, ScoreBM25).Retrieve(context.Background(), repo, "where is the", nil, 5)
	if err != nil || len(got) != 0 {
		t.Fatalf("stopwords only: got %v, %v", got, err)
	}
	if _, err := NewKeywordRetriever(pool, ScoreBM25).Retrieve(context.Background(), repo, "x", nil, 0); !errors.Is(err, ErrInvalidK) {
		t.Fatalf("k=0: %v", err)
	}
	if _, err := NewKeywordRetriever(pool, Scorer(9)).Retrieve(context.Background(), repo, "x", nil, 5); !errors.Is(err, ErrUnknownScorer) {
		t.Fatalf("unknown scorer: %v", err)
	}
}

func TestGeneratedColumnFillsOnInsert(t *testing.T) {
	pool := testPool(t)
	repo := newRepo(t, pool)
	id := addTextChunk(t, pool, repo, "pkg/HelpFormatter.go", "getObjectType", "HMACAlgorithm.get_signature(x)")
	var lexemes string
	if err := pool.QueryRow(context.Background(),
		`SELECT array_to_string(tsvector_to_array(search_tsv), ' ') FROM chunks WHERE id = $1`, id).Scan(&lexemes); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"getobjecttype", "object", "pkg", "helpformatter", "hmacalgorithm", "get", "signature"} {
		if !strings.Contains(" "+lexemes+" ", " "+want+" ") {
			t.Errorf("lexeme %q missing from %q", want, lexemes)
		}
	}
	if strings.Contains(lexemes, "hmacalgorithm.get") {
		t.Error("dotted names must be split, not read as a host name")
	}
}

// Every evaluation question must produce a tsquery PostgreSQL accepts.
func TestEveryEvalQuestionParses(t *testing.T) {
	pool := testPool(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "phase2", "eval.json"))
	if err != nil {
		t.Skip("docs/phase2/eval.json not found")
	}
	var set struct {
		Questions []struct{ ID, Question string } `json:"questions"`
	}
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	for _, q := range set.Questions {
		kq := QueryTerms(q.Question)
		if kq.TSQuery == "" {
			t.Errorf("%s: no keyword terms", q.ID)
			continue
		}
		var ok bool
		if err := pool.QueryRow(context.Background(), `SELECT numnode(to_tsquery('simple', $1)) > 0`, kq.TSQuery).Scan(&ok); err != nil || !ok {
			t.Errorf("%s: %q rejected: %v", q.ID, kq.TSQuery, err)
		}
	}
}

// A known limitation, kept visible: the parser splits want_bytes into want, bytes, so prose with the words
// "want bytes" side by side matches the identifier's phrase exactly. Both chunks must be found; their order is
// left to the scorer and to the evaluation.
func TestKeywordCannotTellAnIdentifierFromTheSameWordsInProse(t *testing.T) {
	pool := testPool(t)
	repo := newRepo(t, pool)
	addTextChunk(t, pool, repo, "src/encoding.py", "want_bytes", "def want_bytes(s):\n    return s.encode()")
	addTextChunk(t, pool, repo, "docs/notes.md", "", "Sometimes you want bytes, not text.")
	for _, scorer := range []Scorer{ScoreTSRank, ScoreBM25} {
		got, err := NewKeywordRetriever(pool, scorer).Retrieve(context.Background(), repo, "want_bytes", nil, 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("scorer %d: got %v, want both chunks", scorer, files(got))
		}
	}
}

func TestHybridOverPostgresUsesBothLists(t *testing.T) {
	pool := testPool(t)
	repo := seedKeywordRepo(t, pool)
	h := HybridRetriever{
		Vector:  VectorRetriever{Searcher: searcher(pool)},
		Keyword: NewKeywordRetriever(pool, ScoreBM25),
		RRFK:    60, KeywordWeight: 1, TestPenalty: 0.5, Pool: 20,
	}
	got, err := h.Retrieve(context.Background(), repo, "What does want_bytes do?", basis(0), 10)
	if err != nil {
		t.Fatal(err)
	}
	// every seeded chunk has the same vector, so the vector list alone would order by id; the keyword list
	// must lift encoding.py (want_bytes) to the top
	if len(got) != 5 || got[0].FilePath != "src/encoding.py" {
		t.Fatalf("got %v", files(got))
	}
}
