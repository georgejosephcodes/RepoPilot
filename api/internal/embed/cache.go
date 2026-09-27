package embed

import (
	"container/list"
	"context"
	"strings"
	"sync"
)

// CachedEmbedder remembers question vectors. Each question costs one request on a free key with a
// small daily budget, and a given text and model always give the same vector, so repeats are free.
// Concurrent calls for the same text share one upstream request.
type CachedEmbedder struct {
	inner Embedder
	limit int

	mu       sync.Mutex
	order    *list.List // front = most recently used
	items    map[string]*list.Element
	inflight map[string]*call
}

type entry struct {
	key string
	vec []float32
}

type call struct {
	done chan struct{}
	vec  []float32
	err  error
}

func NewCached(inner Embedder, capacity int) *CachedEmbedder {
	if capacity < 1 {
		capacity = 1
	}
	return &CachedEmbedder{
		inner:    inner,
		limit:    capacity,
		order:    list.New(),
		items:    map[string]*list.Element{},
		inflight: map[string]*call{},
	}
}

func (c *CachedEmbedder) ModelName() string { return c.inner.ModelName() }
func (c *CachedEmbedder) Dimension() int    { return c.inner.Dimension() }

func copyVec(v []float32) []float32 { return append([]float32(nil), v...) }

func (c *CachedEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	text = strings.TrimSpace(text)
	key := c.inner.ModelName() + "\x00" + text

	c.mu.Lock()
	if el, ok := c.items[key]; ok {
		c.order.MoveToFront(el)
		vec := copyVec(el.Value.(*entry).vec)
		c.mu.Unlock()
		return vec, nil
	}
	cl, running := c.inflight[key]
	if !running {
		cl = &call{done: make(chan struct{})}
		c.inflight[key] = cl
		// Detached from the first caller's context: if that caller gives up, the others still get an answer.
		go c.run(context.WithoutCancel(ctx), key, text, cl)
	}
	c.mu.Unlock()

	select {
	case <-cl.done:
		if cl.err != nil {
			return nil, cl.err
		}
		return copyVec(cl.vec), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *CachedEmbedder) run(ctx context.Context, key, text string, cl *call) {
	vec, err := c.inner.EmbedQuery(ctx, text)

	c.mu.Lock()
	delete(c.inflight, key)
	if err == nil { // errors are never cached
		c.items[key] = c.order.PushFront(&entry{key: key, vec: copyVec(vec)})
		for c.order.Len() > c.limit {
			oldest := c.order.Back()
			c.order.Remove(oldest)
			delete(c.items, oldest.Value.(*entry).key)
		}
	}
	cl.vec, cl.err = vec, err
	c.mu.Unlock()
	close(cl.done)
}

// Len reports how many vectors are cached (used by tests).
func (c *CachedEmbedder) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
