package ratelimit

import (
	"testing"
	"time"
)

func TestBucket(t *testing.T) {
	now := time.Unix(1000, 0)
	l := PerMinute(3)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if !l.Allow("a") {
			t.Fatalf("burst %d denied", i)
		}
	}
	if l.Allow("a") {
		t.Fatal("4th allowed")
	}
	if !l.Allow("b") {
		t.Fatal("keys must be independent")
	}
	now = now.Add(20 * time.Second) // refills one token
	if !l.Allow("a") || l.Allow("a") {
		t.Fatal("refill wrong")
	}
	// idle buckets are swept
	now = now.Add(time.Hour)
	l.Allow("c")
	if len(l.buckets) != 1 {
		t.Fatalf("sweep: %d buckets", len(l.buckets))
	}
}
