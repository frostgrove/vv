package cachetest

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/frostgrove/vv/cache"
)

var defaultClockStart = time.Unix(1_700_000_000, 0).UTC()

var _ cache.Clock = (*Clock)(nil)
var _ cache.Random = (*Random)(nil)

type Clock struct {
	mu     sync.Mutex
	now    time.Time
	timers map[*timer]struct{}
	next   uint64
}

type timer struct {
	clock  *Clock
	ch     chan time.Time
	due    time.Time
	order  uint64
	active bool
}

func NewClock() *Clock {
	return ClockAt(defaultClockStart)
}

func ClockAt(start time.Time) *Clock {
	if start.IsZero() {
		panic("cachetest: clock start is zero")
	}
	return &Clock{now: start.Round(0), timers: make(map[*timer]struct{})}
}

func (clock *Clock) Now() time.Time {
	if clock == nil {
		panic("cachetest: nil clock")
	}
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *Clock) NewTimer(duration time.Duration) cache.Timer {
	if clock == nil {
		panic("cachetest: nil clock")
	}
	clock.mu.Lock()
	defer clock.mu.Unlock()
	due := clock.now.Add(duration)
	if duration > 0 && !due.After(clock.now) {
		panic("cachetest: timer deadline overflow")
	}
	clock.next++
	result := &timer{clock: clock, ch: make(chan time.Time, 1), due: due, order: clock.next}
	if duration <= 0 {
		result.ch <- clock.now
		return result
	}
	result.active = true
	clock.timers[result] = struct{}{}
	return result
}

func (clock *Clock) Advance(duration time.Duration) error {
	if clock == nil {
		return fmt.Errorf("cachetest: nil clock")
	}
	if duration < 0 {
		return fmt.Errorf("cachetest: negative advance")
	}
	clock.mu.Lock()
	next := clock.now.Add(duration)
	if duration > 0 && !next.After(clock.now) {
		clock.mu.Unlock()
		return fmt.Errorf("cachetest: clock overflow")
	}
	clock.now = next
	fired := make([]*timer, 0)
	for candidate := range clock.timers {
		if candidate.due.After(next) {
			continue
		}
		delete(clock.timers, candidate)
		candidate.active = false
		fired = append(fired, candidate)
	}
	clock.mu.Unlock()
	sort.Slice(fired, func(left, right int) bool {
		if fired[left].due.Equal(fired[right].due) {
			return fired[left].order < fired[right].order
		}
		return fired[left].due.Before(fired[right].due)
	})
	for _, candidate := range fired {
		candidate.ch <- candidate.due
	}
	return nil
}

func (clock *Clock) MustAdvance(duration time.Duration) {
	if err := clock.Advance(duration); err != nil {
		panic(err)
	}
}

func (clock *Clock) PendingTimers() int {
	if clock == nil {
		return 0
	}
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return len(clock.timers)
}

func (timer *timer) C() <-chan time.Time {
	return timer.ch
}

func (timer *timer) Stop() bool {
	if timer == nil || timer.clock == nil {
		return false
	}
	timer.clock.mu.Lock()
	defer timer.clock.mu.Unlock()
	if !timer.active {
		return false
	}
	delete(timer.clock.timers, timer)
	timer.active = false
	return true
}

type Random struct {
	mu       sync.Mutex
	values   []uint64
	fallback uint64
}

func NewRandom(values ...uint64) *Random {
	return &Random{values: append([]uint64(nil), values...), fallback: 0x9e3779b97f4a7c15}
}

func (random *Random) Uint64() uint64 {
	if random == nil {
		panic("cachetest: nil random")
	}
	random.mu.Lock()
	defer random.mu.Unlock()
	if len(random.values) > 0 {
		value := random.values[0]
		random.values = random.values[1:]
		return value
	}
	random.fallback ^= random.fallback << 7
	random.fallback ^= random.fallback >> 9
	random.fallback ^= random.fallback << 8
	return random.fallback
}
