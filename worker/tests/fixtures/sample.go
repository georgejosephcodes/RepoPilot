package main

import (
	"context"
	"fmt"
)

const Max = 10

var (
	counter int
	name    string
)

// Scheduler runs jobs.
type Scheduler struct {
	n int
}

type Runner interface {
	Run() error
}

type (
	A int
	// B is a struct.
	B struct{}
)

type Alias = string

// Run starts it.
func (s *Scheduler) Run(ctx context.Context) error {
	fmt.Println("run")
	return nil
}

func (s Scheduler) Value() int { return s.n }

func (Scheduler) Anon() {}

type Box[T any] struct{ v T }

func (b *Box[T]) Get() T { return b.v }

func Top() {}

func Generic[T any](x T) T { return x }
