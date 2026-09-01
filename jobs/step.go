package jobs

import (
	"context"
	"errors"
	"time"
)

var ErrStepTimeout = errors.New("jobs: step timed out")

func Step(ctx context.Context, reporter ProgressReporter, timeout time.Duration, run func(context.Context) error) error {
	if ctx == nil || nilInterface(reporter) || run == nil || timeout < 0 || timeout > MaximumAttemptTimeout {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := pulseStep(ctx, reporter); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	running := ctx
	cancel := func() {}
	if timeout > 0 {
		running, cancel = context.WithTimeoutCause(ctx, timeout, ErrStepTimeout)
	}
	defer cancel()
	if err := run(running); err != nil {
		if errors.Is(context.Cause(running), ErrStepTimeout) {
			return ErrStepTimeout
		}
		return err
	}
	if errors.Is(context.Cause(running), ErrStepTimeout) {
		return ErrStepTimeout
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := pulseStep(ctx, reporter); err != nil {
		return err
	}
	return ctx.Err()
}

func pulseStep(ctx context.Context, reporter ProgressReporter) error {
	err := reporter.Pulse(ctx)
	if errors.Is(err, ErrUnsupported) {
		return nil
	}
	return err
}
