package jobs

import "context"

const MaxWorkerObservers = 8

type workerObserverFanOut struct {
	children []WorkerObserver
}

func WorkerObservers(children ...WorkerObserver) (WorkerObserver, error) {
	if len(children) > MaxWorkerObservers {
		return nil, tooLarge("composed worker observers")
	}
	present := make([]WorkerObserver, 0, len(children))
	for _, child := range children {
		if nilInterface(child) {
			continue
		}
		present = append(present, child)
	}
	return &workerObserverFanOut{children: present}, nil
}

func MustWorkerObservers(children ...WorkerObserver) WorkerObserver {
	observer, err := WorkerObservers(children...)
	if err != nil {
		panic(err)
	}
	return observer
}

func (this *workerObserverFanOut) Observe(ctx context.Context, event WorkerEvent) {
	for _, child := range this.children {
		observeWorkerIsolated(child, ctx, event)
	}
}

func observeWorkerIsolated(child WorkerObserver, ctx context.Context, event WorkerEvent) {
	defer func() { _ = recover() }()
	child.Observe(ctx, event)
}
