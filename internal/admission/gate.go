// Package admission bounds both active work and the number of waiting calls.
package admission

import (
	"context"
	"errors"
)

var ErrFull = errors.New("query capacity exhausted")

// Gate has no workers or background tasks. A caller owns its slot until Release.
type Gate struct {
	active chan struct{}
	total  chan struct{}
}

func New(active, waiting int) *Gate {
	if active < 1 || waiting < 0 {
		panic("invalid admission capacity")
	}
	return &Gate{active: make(chan struct{}, active), total: make(chan struct{}, active+waiting)}
}

func (g *Gate) Acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case g.total <- struct{}{}:
	default:
		return ErrFull
	}
	select {
	case g.active <- struct{}{}:
		if ctx.Err() == nil {
			return nil
		}
		<-g.active
	case <-ctx.Done():
	}
	<-g.total
	return ctx.Err()
}

func (g *Gate) Release() {
	<-g.active
	<-g.total
}
