package cachetest

import (
	"sync"
	"testing"
	"time"
)

func TestClockAdvancesAndFiresInOrder(t *testing.T) {
	start := time.Unix(100, 0).UTC()
	clock := ClockAt(start)
	second := clock.NewTimer(2 * time.Second)
	first := clock.NewTimer(time.Second)
	same := clock.NewTimer(time.Second)

	clock.MustAdvance(time.Second)
	if got := receive(t, first.C(), "first timer"); !got.Equal(start.Add(time.Second)) {
		t.Fatalf("first timer = %v", got)
	}
	if got := receive(t, same.C(), "same-deadline timer"); !got.Equal(start.Add(time.Second)) {
		t.Fatalf("same timer = %v", got)
	}
	select {
	case value := <-second.C():
		t.Fatalf("second timer fired early at %v", value)
	default:
	}
	clock.MustAdvance(time.Second)
	if got := receive(t, second.C(), "second timer"); !got.Equal(start.Add(2 * time.Second)) {
		t.Fatalf("second timer = %v", got)
	}
	if clock.PendingTimers() != 0 {
		t.Fatalf("pending timers = %d", clock.PendingTimers())
	}
}

func TestClockTimerStopAndImmediate(t *testing.T) {
	clock := NewClock()
	stopped := clock.NewTimer(time.Hour)
	if !stopped.Stop() || stopped.Stop() {
		t.Fatal("timer stop result is invalid")
	}
	immediate := clock.NewTimer(0)
	if got := receive(t, immediate.C(), "immediate timer"); !got.Equal(clock.Now()) {
		t.Fatalf("immediate timer = %v", got)
	}
	if immediate.Stop() {
		t.Fatal("fired timer reported stopped")
	}
	if err := clock.Advance(-time.Nanosecond); err == nil {
		t.Fatal("negative advance succeeded")
	}
}

func TestClockConcurrentTimers(t *testing.T) {
	clock := NewClock()
	const count = 64
	timers := make([]<-chan time.Time, count)
	var wait sync.WaitGroup
	wait.Add(count)
	for index := range count {
		go func() {
			defer wait.Done()
			timers[index] = clock.NewTimer(time.Second).C()
		}()
	}
	wait.Wait()
	clock.MustAdvance(time.Second)
	for index, channel := range timers {
		select {
		case <-channel:
		default:
			t.Fatalf("timer %d did not fire", index)
		}
	}
}

func TestRandomSequenceAndFallback(t *testing.T) {
	random := NewRandom(3, 5)
	if got := random.Uint64(); got != 3 {
		t.Fatalf("first = %d", got)
	}
	if got := random.Uint64(); got != 5 {
		t.Fatalf("second = %d", got)
	}
	first := random.Uint64()
	second := random.Uint64()
	if first == 0 || second == 0 || first == second {
		t.Fatalf("fallback values = %d, %d", first, second)
	}
}
