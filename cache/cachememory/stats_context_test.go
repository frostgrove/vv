package cachememory

import (
	"context"
	"testing"
	"time"
)

func TestStatsContext(t *testing.T) {
	backend := mustBackend(t, Limits{MaxEntries: 2, MaxBytes: 1024, MaxItemBytes: 64})
	mustPut(t, backend, testAddress(1), "value", testExpiry)

	stats, ok := backend.StatsContext(context.Background())
	if !ok || stats.Entries != 1 || stats.ChargedBytes != FixedEntryChargeBytes+5 || stats.Limits != backend.limits || stats.Closed {
		t.Fatalf("StatsContext() = %+v, %v", stats, ok)
	}

	if stats, ok = (*Backend)(nil).StatsContext(context.Background()); ok || stats != (Stats{}) {
		t.Fatalf("nil backend StatsContext() = %+v, %v", stats, ok)
	}
	if stats, ok = backend.StatsContext(nil); ok || stats != (Stats{}) {
		t.Fatalf("nil context StatsContext() = %+v, %v", stats, ok)
	}
	var typedNil *typedNilStatsContext
	if stats, ok = backend.StatsContext(typedNil); ok || stats != (Stats{}) {
		t.Fatalf("typed-nil context StatsContext() = %+v, %v", stats, ok)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if stats, ok = backend.StatsContext(canceled); ok || stats != (Stats{}) {
		t.Fatalf("canceled context StatsContext() = %+v, %v", stats, ok)
	}
	lateCanceled := &cancelOnSecondStatsContextCheck{}
	if stats, ok = backend.StatsContext(lateCanceled); ok || stats != (Stats{}) || lateCanceled.calls != 2 {
		t.Fatalf("late-canceled context StatsContext() = %+v, %v, calls=%d", stats, ok, lateCanceled.calls)
	}
}

func TestStatsContextReturnsImmediatelyWhenMutationMutexIsHeld(t *testing.T) {
	backend := mustBackend(t, Limits{MaxEntries: 2, MaxBytes: 1024, MaxItemBytes: 64})
	backend.mu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		stats, ok := backend.StatsContext(context.Background())
		if ok || stats != (Stats{}) {
			t.Errorf("StatsContext() = %+v, %v", stats, ok)
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("StatsContext blocked on the mutation mutex")
	}
	backend.mu.Unlock()
}

type typedNilStatsContext struct{}

func (*typedNilStatsContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*typedNilStatsContext) Done() <-chan struct{}       { return nil }
func (*typedNilStatsContext) Err() error                  { return nil }
func (*typedNilStatsContext) Value(any) any               { return nil }

type cancelOnSecondStatsContextCheck struct {
	calls int
}

func (*cancelOnSecondStatsContextCheck) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*cancelOnSecondStatsContextCheck) Done() <-chan struct{}       { return nil }
func (c *cancelOnSecondStatsContextCheck) Err() error {
	c.calls++
	if c.calls == 2 {
		return context.Canceled
	}
	return nil
}
func (*cancelOnSecondStatsContextCheck) Value(any) any { return nil }
