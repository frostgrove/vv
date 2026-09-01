package jobs

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type stepController struct {
	pulses atomic.Int32
	err    error
}

func (c *stepController) Pulse(context.Context) error {
	c.pulses.Add(1)
	return c.err
}

func (*stepController) Guard(context.Context, LeaseFence) error { return nil }

func TestStepPulsesAroundWork(t *testing.T) {
	controller := &stepController{}
	called := false
	if err := Step(t.Context(), controller, 0, func(context.Context) error {
		called = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !called || controller.pulses.Load() != 2 {
		t.Fatalf("called = %t, pulses = %d", called, controller.pulses.Load())
	}
}

func TestStepEnforcesItsTimeout(t *testing.T) {
	controller := &stepController{}
	err := Step(t.Context(), controller, time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, ErrStepTimeout) || controller.pulses.Load() != 1 {
		t.Fatalf("error = %v, pulses = %d", err, controller.pulses.Load())
	}
}

func TestStepStopsBeforeWorkWhenProgressFails(t *testing.T) {
	want := errors.New("lease lost")
	controller := &stepController{err: want}
	called := false
	err := Step(t.Context(), controller, 0, func(context.Context) error {
		called = true
		return nil
	})
	if !errors.Is(err, want) || called || controller.pulses.Load() != 1 {
		t.Fatalf("error = %v, called = %t, pulses = %d", err, called, controller.pulses.Load())
	}
}

func TestStepRunsWhenProgressTrackingIsDisabled(t *testing.T) {
	controller := &stepController{err: ErrUnsupported}
	called := false
	if err := Step(t.Context(), controller, 0, func(context.Context) error {
		called = true
		return nil
	}); err != nil || !called || controller.pulses.Load() != 2 {
		t.Fatalf("called = %t, pulses = %d, error = %v", called, controller.pulses.Load(), err)
	}
}

type cancellingStepReporter struct{ cancel context.CancelFunc }

func (r cancellingStepReporter) Pulse(context.Context) error {
	r.cancel()
	return nil
}

func TestStepDoesNotStartWorkAfterProgressReportsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	called := false
	err := Step(ctx, cancellingStepReporter{cancel: cancel}, 0, func(context.Context) error {
		called = true
		return nil
	})
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("error = %v, called = %t", err, called)
	}
}

func TestStepRejectsInvalidInputs(t *testing.T) {
	controller := &stepController{}
	if !errors.Is(Step(nil, controller, 0, func(context.Context) error { return nil }), ErrInvalid) {
		t.Fatal("nil context was accepted")
	}
	if !errors.Is(Step(t.Context(), nil, 0, func(context.Context) error { return nil }), ErrInvalid) {
		t.Fatal("nil controller was accepted")
	}
	if !errors.Is(Step(t.Context(), controller, -1, func(context.Context) error { return nil }), ErrInvalid) {
		t.Fatal("negative timeout was accepted")
	}
}
