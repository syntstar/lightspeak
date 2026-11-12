// Package worklimit bounds both active work and queued requests.
package worklimit

import (
	"context"
	"errors"
)

var ErrBusy = errors.New("work queue full")

type Limiter struct{ active, admitted chan struct{} }

func New(active, waiting int) *Limiter {
	return &Limiter{make(chan struct{}, active), make(chan struct{}, active+waiting)}
}
func (l *Limiter) Acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case l.admitted <- struct{}{}:
	default:
		return nil, ErrBusy
	}
	select {
	case l.active <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-l.active
			<-l.admitted
			return nil, err
		}
		return func() { <-l.active; <-l.admitted }, nil
	case <-ctx.Done():
		<-l.admitted
		return nil, ctx.Err()
	}
}
