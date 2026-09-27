package httpapi

import (
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	now := time.Unix(1000, 0)
	l := newLimiter(6) // burst 6, refill 1 token per 10s
	l.now = func() time.Time { return now }

	for i := 0; i < 6; i++ {
		if !l.allow("a") {
			t.Fatalf("request %d denied within burst", i+1)
		}
	}
	if l.allow("a") {
		t.Fatal("request over burst allowed")
	}
	if !l.allow("b") {
		t.Fatal("other key must have its own bucket")
	}

	now = now.Add(9 * time.Second)
	if l.allow("a") {
		t.Fatal("allowed before a token refilled")
	}
	now = now.Add(2 * time.Second) // 11s total, one token
	if !l.allow("a") {
		t.Fatal("denied after refill")
	}
	if l.allow("a") {
		t.Fatal("second request after single refill allowed")
	}

	now = now.Add(time.Hour) // refill is capped at burst
	for i := 0; i < 6; i++ {
		if !l.allow("a") {
			t.Fatalf("request %d denied after long idle", i+1)
		}
	}
	if l.allow("a") {
		t.Fatal("burst cap exceeded after long idle")
	}
}
