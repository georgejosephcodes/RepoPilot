package embed

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestCacheHitAvoidsUpstreamCall(t *testing.T) {
	f := NewFake(8)
	c := NewCached(f, 10)
	a, err := c.EmbedQuery(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := c.EmbedQuery(context.Background(), "hello")
	if f.Calls() != 1 {
		t.Fatalf("upstream calls = %d, want 1", f.Calls())
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("cached vector differs")
		}
	}
}

func TestCacheKeyIgnoresSurroundingWhitespaceOnly(t *testing.T) {
	f := NewFake(8)
	c := NewCached(f, 10)
	_, _ = c.EmbedQuery(context.Background(), "  hello \n")
	_, _ = c.EmbedQuery(context.Background(), "hello")
	if f.Calls() != 1 {
		t.Fatalf("calls = %d, want 1", f.Calls())
	}
	_, _ = c.EmbedQuery(context.Background(), "hello there")
	_, _ = c.EmbedQuery(context.Background(), "Hello")
	if f.Calls() != 3 {
		t.Fatalf("calls = %d, want 3", f.Calls())
	}
}

func TestCacheKeyIncludesModel(t *testing.T) {
	a, b := NewFake(8), NewFake(8)
	b.Model = "other-model"
	// two caches over different models must not share entries; a single cache key includes ModelName
	ca, cb := NewCached(a, 10), NewCached(b, 10)
	_, _ = ca.EmbedQuery(context.Background(), "q")
	_, _ = cb.EmbedQuery(context.Background(), "q")
	if a.Calls() != 1 || b.Calls() != 1 {
		t.Fatal("each model must embed once")
	}
	if ca.inner.ModelName() == cb.inner.ModelName() {
		t.Fatal("test setup wrong")
	}
}

func TestConcurrentIdenticalCallsShareOneRequest(t *testing.T) {
	f := NewFake(8)
	f.Gate = make(chan struct{})
	c := NewCached(f, 10)

	var wg sync.WaitGroup
	results := make([][]float32, 20)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := c.EmbedQuery(context.Background(), "same question")
			if err != nil {
				t.Errorf("call %d: %v", i, err)
			}
			results[i] = v
		}(i)
	}
	// let every goroutine reach the cache before the upstream call is released
	time.Sleep(100 * time.Millisecond)
	close(f.Gate)
	wg.Wait()

	if f.Calls() != 1 {
		t.Fatalf("upstream calls = %d, want 1", f.Calls())
	}
	for i, v := range results {
		if len(v) != 8 {
			t.Fatalf("result %d has length %d", i, len(v))
		}
	}
}

func TestErrorsAreNotCached(t *testing.T) {
	f := NewFake(8)
	f.Err = errors.New("boom")
	c := NewCached(f, 10)
	if _, err := c.EmbedQuery(context.Background(), "q"); err == nil {
		t.Fatal("expected an error")
	}
	f.Err = nil
	if _, err := c.EmbedQuery(context.Background(), "q"); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if f.Calls() != 2 || c.Len() != 1 {
		t.Fatalf("calls=%d cached=%d", f.Calls(), c.Len())
	}
}

func TestLRUEvictsTheOldest(t *testing.T) {
	f := NewFake(8)
	c := NewCached(f, 2)
	for _, q := range []string{"a", "b"} {
		_, _ = c.EmbedQuery(context.Background(), q)
	}
	_, _ = c.EmbedQuery(context.Background(), "a") // a is now the most recently used
	_, _ = c.EmbedQuery(context.Background(), "c") // evicts b
	if c.Len() != 2 {
		t.Fatalf("len = %d", c.Len())
	}
	before := f.Calls()
	_, _ = c.EmbedQuery(context.Background(), "a")
	if f.Calls() != before {
		t.Fatal("a should still be cached")
	}
	_, _ = c.EmbedQuery(context.Background(), "b")
	if f.Calls() != before+1 {
		t.Fatal("b should have been evicted")
	}
}

func TestReturnedVectorsAreCopies(t *testing.T) {
	c := NewCached(NewFake(4), 10)
	first, _ := c.EmbedQuery(context.Background(), "q")
	want := first[0]
	first[0] = 999
	second, _ := c.EmbedQuery(context.Background(), "q")
	if second[0] != want {
		t.Fatal("mutating a returned vector changed the cache")
	}
}

func TestCallerCancellationDoesNotKillOtherWaiters(t *testing.T) {
	f := NewFake(4)
	f.Gate = make(chan struct{})
	c := NewCached(f, 10)

	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan error, 1)
	go func() { _, err := c.EmbedQuery(ctx1, "q"); done1 <- err }()
	time.Sleep(50 * time.Millisecond)

	done2 := make(chan error, 1)
	go func() { _, err := c.EmbedQuery(context.Background(), "q"); done2 <- err }()
	time.Sleep(50 * time.Millisecond)

	cancel1()
	if err := <-done1; !errors.Is(err, context.Canceled) {
		t.Fatalf("first caller err = %v", err)
	}
	close(f.Gate)
	if err := <-done2; err != nil {
		t.Fatalf("second caller err = %v", err)
	}
	if f.Calls() != 1 {
		t.Fatalf("calls = %d, want 1", f.Calls())
	}
}

func TestZeroCapacityStillWorks(t *testing.T) {
	c := NewCached(NewFake(4), 0)
	if _, err := c.EmbedQuery(context.Background(), "q"); err != nil || c.Len() != 1 {
		t.Fatalf("err=%v len=%d", err, c.Len())
	}
}

func TestFakeIsDeterministicAndUnit(t *testing.T) {
	f := NewFake(16)
	a, _ := f.EmbedQuery(context.Background(), "x")
	b, _ := f.EmbedQuery(context.Background(), "x")
	c, _ := f.EmbedQuery(context.Background(), "y")
	same, diff := true, false
	for i := range a {
		same = same && a[i] == b[i]
		diff = diff || a[i] != c[i]
	}
	if !same || !diff || norm(a) < 0.999999 || norm(a) > 1.000001 {
		t.Fatal("fake embedder is not deterministic unit vectors")
	}
}
