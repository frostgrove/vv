package runtime_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/runtime"
)

type manualTicker struct {
	ticks chan time.Time
}

func (this *manualTicker) Ticks() <-chan time.Time { return this.ticks }

func (this *manualTicker) Stop() {}

func (this *manualTicker) tick() { this.ticks <- time.Now() }

func newManualTicker() *manualTicker { return &manualTicker{ticks: make(chan time.Time)} }

func periodic(t *testing.T, spec runtime.PeriodicSpec) runtime.Runner {
	t.Helper()
	built, err := runtime.NewPeriodic(spec)
	if err != nil {
		t.Fatalf("a well-formed periodic runner was refused: %v", err)
	}
	return built
}

func running(t *testing.T, runner runtime.Runner) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = runner.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-stopped
	})
	return cancel
}

func TestAPeriodicRunnerWorksOnceForEveryTick(t *testing.T) {
	ticker := newManualTicker()
	passes := make(chan struct{}, 4)
	runner := periodic(t, runtime.PeriodicSpec{
		Name:     "translation-debt",
		Interval: time.Hour,
		Ticks:    func(time.Duration) runtime.Ticker { return ticker },
		Pass: func(context.Context) error {
			passes <- struct{}{}
			return nil
		},
	})
	running(t, runner)

	ticker.tick()
	ticker.tick()

	for range 2 {
		select {
		case <-passes:
		case <-time.After(2 * time.Second):
			t.Fatal("a tick did not produce a pass")
		}
	}
}

func TestAFailedPassDoesNotEndTheSchedule(t *testing.T) {
	ticker := newManualTicker()
	var passes atomic.Int64
	done := make(chan struct{}, 2)
	runner := periodic(t, runtime.PeriodicSpec{
		Name:     "translation-debt",
		Interval: time.Hour,
		Ticks:    func(time.Duration) runtime.Ticker { return ticker },
		Pass: func(context.Context) error {
			passes.Add(1)
			done <- struct{}{}
			return errors.New("the database is restarting")
		},
	})
	running(t, runner)

	ticker.tick()
	<-done
	ticker.tick()
	<-done

	if passes.Load() != 2 {
		t.Fatalf("a sweep that failed once stopped sweeping: %d passes", passes.Load())
	}
}

func TestAPanickingPassDoesNotEndTheSchedule(t *testing.T) {
	ticker := newManualTicker()
	var passes atomic.Int64
	done := make(chan struct{}, 2)
	runner := periodic(t, runtime.PeriodicSpec{
		Name:     "translation-debt",
		Interval: time.Hour,
		Ticks:    func(time.Duration) runtime.Ticker { return ticker },
		Pass: func(context.Context) error {
			passes.Add(1)
			defer func() { done <- struct{}{} }()
			panic("nil translator")
		},
	})
	running(t, runner)

	ticker.tick()
	<-done
	ticker.tick()
	<-done

	if passes.Load() != 2 {
		t.Fatalf("a panic in one pass ended the schedule for the life of the process: %d passes", passes.Load())
	}
}

func TestAPassIsBoundedByItsOwnBudget(t *testing.T) {
	ticker := newManualTicker()
	overrun := make(chan error, 1)
	runner := periodic(t, runtime.PeriodicSpec{
		Name:     "translation-debt",
		Interval: time.Hour,
		Timeout:  20 * time.Millisecond,
		Ticks:    func(time.Duration) runtime.Ticker { return ticker },
		Pass: func(ctx context.Context) error {
			<-ctx.Done()
			overrun <- ctx.Err()
			return ctx.Err()
		},
	})
	running(t, runner)

	ticker.tick()

	select {
	case err := <-overrun:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("the pass ended for the wrong reason: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a pass with a 20ms budget was still running, so one stuck sweep blocks every later one")
	}
}

func TestAPeriodicRunnerRunsOnEveryReplicaAndSurvivesNothing(t *testing.T) {
	runner := periodic(t, runtime.PeriodicSpec{
		Name:     "translation-debt",
		Interval: time.Hour,
		Pass:     func(context.Context) error { return nil },
	})

	declaration := runtime.DeclarationOf(runner)

	if declaration.Placement != runtime.PerReplica || declaration.Durability != runtime.NonDurable {
		t.Fatalf("a process ticker claims %v, so a reader cannot tell it from a durable schedule", declaration)
	}
}

func TestAPeriodicRunnerStopsWhenItsContextDoes(t *testing.T) {
	runner := periodic(t, runtime.PeriodicSpec{
		Name:     "translation-debt",
		Interval: time.Hour,
		Pass:     func(context.Context) error { return nil },
	})

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() { returned <- runner.Run(ctx) }()
	cancel()

	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("the runner returned %v instead of the cancellation it was given", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the runner ignored cancellation, so shutdown waits for the next tick")
	}
}

func TestTheShortFormBuildsTheSameRunnerAsTheSpec(t *testing.T) {
	pass := func(context.Context) error { return nil }

	short, err := runtime.Every("translation-debt", time.Minute, pass)
	if err != nil {
		t.Fatalf("Every refused a well-formed schedule: %v", err)
	}
	explicit := periodic(t, runtime.PeriodicSpec{Name: "translation-debt", Interval: time.Minute, Pass: pass})

	if short.Name() != explicit.Name() {
		t.Fatalf("the short form named the runner %q and the spec named it %q", short.Name(), explicit.Name())
	}
	if runtime.DeclarationOf(short) != runtime.DeclarationOf(explicit) {
		t.Fatal("the short form promises something the explicit constructor does not")
	}
}

func TestAScheduleWithNoPeriodOrNoWorkIsRefusedWithBothReasons(t *testing.T) {
	_, err := runtime.NewPeriodic(runtime.PeriodicSpec{Name: "translation-debt"})

	if err == nil {
		t.Fatal("a periodic runner with no interval and nothing to do was accepted")
	}
	if !errorMentions(err, "interval", "nothing to do") {
		t.Fatalf("the refusal does not name both problems: %v", err)
	}
}

func TestTheShortFormRefusesWhatTheSpecRefuses(t *testing.T) {
	if _, err := runtime.Every("", 0, nil); err == nil {
		t.Fatal("the short form accepted a schedule the explicit constructor refuses")
	}
}

func errorMentions(err error, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(err.Error(), fragment) {
			return false
		}
	}
	return true
}
