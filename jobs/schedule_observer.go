package jobs

import (
	"context"
	"time"
)

const MaxScheduleObservers = 8

type ScheduleEvent struct {
	result  ScheduleRunResult
	err     error
	elapsed time.Duration
}

func (event ScheduleEvent) Result() ScheduleRunResult { return event.result }
func (event ScheduleEvent) Err() error                { return event.err }
func (event ScheduleEvent) Elapsed() time.Duration    { return event.elapsed }

type ScheduleObserver interface {
	Observe(context.Context, ScheduleEvent)
}

type ScheduleObserverFunc func(context.Context, ScheduleEvent)

func (observe ScheduleObserverFunc) Observe(ctx context.Context, event ScheduleEvent) {
	observe(ctx, event)
}

func ScheduleObservers(observers ...ScheduleObserver) (ScheduleObserver, error) {
	if len(observers) > MaxScheduleObservers {
		return nil, tooLarge("schedule observers")
	}
	children := make([]ScheduleObserver, 0, len(observers))
	for _, observer := range observers {
		if nilInterface(observer) {
			continue
		}
		children = append(children, observer)
	}
	return scheduleObserverFanOut(children), nil
}

func MustScheduleObservers(observers ...ScheduleObserver) ScheduleObserver {
	observer, err := ScheduleObservers(observers...)
	if err != nil {
		panic(err)
	}
	return observer
}

type scheduleObserverFanOut []ScheduleObserver

func (observers scheduleObserverFanOut) Observe(ctx context.Context, event ScheduleEvent) {
	for _, observer := range observers {
		observeSchedule(observer, ctx, event)
	}
}

func observeSchedule(observer ScheduleObserver, ctx context.Context, event ScheduleEvent) {
	if nilInterface(observer) || nilInterface(ctx) {
		return
	}
	defer func() { _ = recover() }()
	observer.Observe(ctx, event)
}
