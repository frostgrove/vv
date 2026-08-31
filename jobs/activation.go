package jobs

import (
	"context"
	"fmt"
	"sync/atomic"
)

const (
	queueActivationPreparing uint32 = iota
	queueActivationActive
	queueActivationClosed
)

type QueueActivation struct {
	queue    *Queue
	bindings []automaticQueueBinding
	state    atomic.Uint32
}

func (*QueueActivation) String() string { return "[job queue activation]" }
func (a *QueueActivation) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, a.String())
}

type automaticQueueBinding interface {
	Declaration
	lockQueueActivation()
	unlockQueueActivation()
	boundActivation() *QueueActivation
	bindQueueActivation(*QueueActivation)
}

func (q *Queue) Activate() (*QueueActivation, error) {
	if q == nil || !q.namespace.valid() || q.catalog.Len() == 0 || q.catalog.Fingerprint() == "" || nilInterface(q.sender) || !q.description.valid() || !q.requirements.valid() || !q.durability.valid() || len(q.definitionDurability) != q.catalog.Len() || !q.digests.valid() || q.entropy == nil || q.catalog.RequiresTenantPartition() && nilInterface(q.contexts) {
		return nil, invalid("queue activation")
	}
	bindings := make([]automaticQueueBinding, 0, q.catalog.Len())
	for _, declaration := range q.catalog.Definitions() {
		if binding, ok := declaration.(automaticQueueBinding); ok {
			bindings = append(bindings, binding)
		}
	}
	for _, binding := range bindings {
		binding.lockQueueActivation()
	}
	defer func() {
		for index := len(bindings) - 1; index >= 0; index-- {
			bindings[index].unlockQueueActivation()
		}
	}()

	var existing *QueueActivation
	bound := 0
	for _, binding := range bindings {
		current := binding.boundActivation()
		if current == nil {
			continue
		}
		if current.queue != q || current.state.Load() != queueActivationActive || existing != nil && existing != current {
			return nil, fmt.Errorf("%w: automatic job %q is already bound to another queue activation", ErrConflict, binding.declarationName())
		}
		existing = current
		bound++
	}
	if existing != nil {
		if bound != len(bindings) {
			return nil, fmt.Errorf("%w: queue activation is inconsistent", ErrConflict)
		}
		return existing, nil
	}

	activation := &QueueActivation{queue: q, bindings: append([]automaticQueueBinding(nil), bindings...)}
	for _, binding := range bindings {
		binding.bindQueueActivation(activation)
	}
	activation.state.Store(queueActivationActive)
	return activation, nil
}

func (a *QueueActivation) Close() error {
	if a == nil {
		return invalid("queue activation")
	}
	if a.state.Load() == queueActivationClosed {
		return nil
	}
	for _, binding := range a.bindings {
		binding.lockQueueActivation()
	}
	defer func() {
		for index := len(a.bindings) - 1; index >= 0; index-- {
			a.bindings[index].unlockQueueActivation()
		}
	}()
	if !a.state.CompareAndSwap(queueActivationActive, queueActivationClosed) {
		if a.state.Load() == queueActivationClosed {
			return nil
		}
		return fmt.Errorf("%w: queue activation is not active", ErrNotActivated)
	}
	for _, binding := range a.bindings {
		if binding.boundActivation() == a {
			binding.bindQueueActivation(nil)
		}
	}
	return nil
}

func Go[P any](ctx context.Context, automatic *Automatic[P], payload P, options ...EnqueueOption) error {
	if automatic == nil || automatic.resolved() == nil {
		return ErrNotActivated
	}
	activation := automatic.activation.Load()
	if activation == nil || activation.state.Load() != queueActivationActive || activation.queue == nil {
		return ErrNotActivated
	}
	_, err := Enqueue(ctx, activation.queue, automatic, payload, options...)
	return err
}

func (a *Automatic[P]) lockQueueActivation() {
	a.activationMu.Lock()
}

func (a *Automatic[P]) unlockQueueActivation() {
	a.activationMu.Unlock()
}

func (a *Automatic[P]) boundActivation() *QueueActivation {
	if a == nil {
		return nil
	}
	return a.activation.Load()
}

func (a *Automatic[P]) bindQueueActivation(activation *QueueActivation) {
	a.activation.Store(activation)
}

func (a *Automatic[P]) boundQueue() *Queue {
	activation := a.boundActivation()
	if activation == nil || activation.state.Load() != queueActivationActive {
		return nil
	}
	return activation.queue
}
