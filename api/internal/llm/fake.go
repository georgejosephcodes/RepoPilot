package llm

import (
	"context"
	"sync"
)

// Fake is a scripted LLM for tests. It records what it was asked.
type Fake struct {
	Model  string
	Result Result
	Err    error
	mu     sync.Mutex
	calls  int
	System string
	User   string
}

func NewFake(text string) *Fake {
	return &Fake{Model: "fake-llm", Result: Result{Text: text, FinishReason: "STOP", InputTokens: 100, OutputTokens: 20}}
}

func (f *Fake) ModelName() string { return f.Model }

func (f *Fake) Generate(_ context.Context, system, user string) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.System, f.User = system, user
	if f.Err != nil {
		return Result{}, f.Err
	}
	return f.Result, nil
}

func (f *Fake) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}
