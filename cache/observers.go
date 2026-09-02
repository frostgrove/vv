package cache

import (
	"context"
	"fmt"
)

const MaxObservers = 8

type observerFanOut struct {
	children []Observer
}

func Observers(children ...Observer) (Observer, error) {
	if len(children) > MaxObservers {
		return nil, failure("build observers", fmt.Errorf("%w: at most %d observers may be composed", ErrTooLarge, MaxObservers))
	}
	present := make([]Observer, 0, len(children))
	for _, child := range children {
		if nilInterface(child) {
			continue
		}
		present = append(present, child)
	}
	return &observerFanOut{children: present}, nil
}

func MustObservers(children ...Observer) Observer {
	observer, err := Observers(children...)
	if err != nil {
		panic(err)
	}
	return observer
}

func (this *observerFanOut) Observe(ctx context.Context, event Event) {
	for _, child := range this.children {
		observeIsolated(child, ctx, event)
	}
}

func observeIsolated(child Observer, ctx context.Context, event Event) {
	defer func() { _ = recover() }()
	child.Observe(ctx, event)
}
