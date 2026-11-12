package worklimit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBoundedAndCanceledAdmission(t *testing.T) {
	l := New(1, 0)
	release, err := l.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Acquire(t.Context()); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected busy, got %v", err)
	}
	release()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := l.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	release, err = l.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestCanceledWaitReleasesAdmission(t *testing.T) {
	l := New(1, 1)
	release, err := l.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := l.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	release()
	release, err = l.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	release()
}
