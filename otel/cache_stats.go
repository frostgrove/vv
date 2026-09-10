package vvotel

import (
	"context"
	"errors"
	"math"
	"sync"

	"github.com/frostgrove/vv/cache/cachememory"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const MaxCacheMemoryStatsBackends = 64

var (
	ErrInvalidRegistration   = errors.New("vvotel: invalid registration")
	ErrDuplicateRegistration = errors.New("vvotel: duplicate registration")
	ErrCallbackRegistration  = errors.New("vvotel: callback registration failed")
	ErrCallbackUnregister    = errors.New("vvotel: callback unregister failed")
)

type Registration interface {
	Unregister() error
}

type cacheRegistrationError struct {
	kind error
}

func (e *cacheRegistrationError) Error() string {
	if e == nil {
		return ErrAssembly.Error()
	}
	return ErrAssembly.Error() + ": " + e.kind.Error()
}

func (e *cacheRegistrationError) Is(target error) bool {
	return e != nil && (target == ErrAssembly || target == e.kind)
}

type cacheMemoryStatsSlot struct {
	mu     sync.Mutex
	active uint64
	next   uint64
}

func (s *cacheMemoryStatsSlot) reserve() (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != 0 {
		return 0, false
	}
	s.next++
	if s.next == 0 {
		s.next++
	}
	s.active = s.next
	return s.active, true
}

func (s *cacheMemoryStatsSlot) release(token uint64) {
	if s == nil || token == 0 {
		return
	}
	s.mu.Lock()
	if s.active == token {
		s.active = 0
	}
	s.mu.Unlock()
}

type cacheMemoryGauge struct {
	instrument metric.Int64ObservableGauge
	signal     Signal
}

type cacheMemoryStatsState struct {
	mu            sync.Mutex
	cond          *sync.Cond
	active        bool
	tearingDown   bool
	done          bool
	inFlight      int
	backends      []*cachememory.Backend
	gauges        []cacheMemoryGauge
	native        metric.Registration
	slot          *cacheMemoryStatsSlot
	token         uint64
	unregisterErr error
}

type cacheMemoryRegistration struct {
	state *cacheMemoryStatsState
}

type inertRegistration struct{}

func (inertRegistration) Unregister() error { return nil }

func CacheMemoryStats(tel *Telemetry, backends ...*cachememory.Backend) (Registration, error) {
	if cacheMemoryStatsDisabled(tel) {
		return inertRegistration{}, nil
	}
	if len(backends) == 0 || len(backends) > MaxCacheMemoryStatsBackends {
		return nil, newCacheRegistrationError(ErrInvalidRegistration)
	}
	for index, backend := range backends {
		if backend == nil {
			return nil, newCacheRegistrationError(ErrInvalidRegistration)
		}
		for prior := 0; prior < index; prior++ {
			if backends[prior] == backend {
				return nil, newCacheRegistrationError(ErrDuplicateRegistration)
			}
		}
	}
	if !initialCacheMemorySnapshotsValid(backends) {
		return nil, newCacheRegistrationError(ErrInvalidRegistration)
	}
	if tel.cacheMemoryStats == nil {
		return nil, newCacheRegistrationError(ErrInvalidRegistration)
	}
	token, reserved := tel.cacheMemoryStats.reserve()
	if !reserved {
		return nil, newCacheRegistrationError(ErrDuplicateRegistration)
	}

	state := &cacheMemoryStatsState{
		active:   true,
		backends: append([]*cachememory.Backend(nil), backends...),
		gauges:   enabledCacheMemoryGauges(tel),
		slot:     tel.cacheMemoryStats,
		token:    token,
	}
	state.cond = sync.NewCond(&state.mu)
	registration := &cacheMemoryRegistration{state: state}
	observables := make([]metric.Observable, len(state.gauges))
	for index, gauge := range state.gauges {
		observables[index] = gauge.instrument
	}

	native, registerErr, panicked := safeRegisterCallback(tel.meter, state.callback, observables...)
	if panicked || registerErr != nil || nilInterface(native) {
		cleanupErr := state.failRegistration(native)
		failure := newCacheRegistrationError(ErrCallbackRegistration)
		if cleanupErr != nil {
			return nil, errors.Join(failure, cleanupErr)
		}
		return nil, failure
	}
	state.mu.Lock()
	state.native = native
	state.mu.Unlock()
	return registration, nil
}

func MustCacheMemoryStats(tel *Telemetry, backends ...*cachememory.Backend) Registration {
	registration, err := CacheMemoryStats(tel, backends...)
	if err != nil {
		panic(err)
	}
	return registration
}

func newCacheRegistrationError(kind error) error {
	return &cacheRegistrationError{kind: kind}
}

func cacheMemoryStatsDisabled(tel *Telemetry) bool {
	if tel == nil {
		return true
	}
	for _, signal := range cacheMemoryGaugeSignals() {
		if tel.signalEnabled(signal) {
			return false
		}
	}
	return true
}

func cacheMemoryGaugeSignals() [6]Signal {
	return [6]Signal{
		SignalCacheMemoryEntries,
		SignalCacheMemoryBytes,
		SignalCacheMemoryEntryLimit,
		SignalCacheMemoryByteLimit,
		SignalCacheMemoryActive,
		SignalCacheMemoryClosed,
	}
}

func enabledCacheMemoryGauges(tel *Telemetry) []cacheMemoryGauge {
	gauges := make([]cacheMemoryGauge, 0, len(cacheMemoryGaugeSignals()))
	for _, signal := range cacheMemoryGaugeSignals() {
		if !tel.signalEnabled(signal) {
			continue
		}
		gauges = append(gauges, cacheMemoryGauge{instrument: tel.int64Gauge(signal), signal: signal})
	}
	return gauges
}

func initialCacheMemorySnapshotsValid(backends []*cachememory.Backend) bool {
	var entryLimits int64
	var byteLimits int64
	ctx := context.Background()
	for _, backend := range backends {
		stats, ok := backend.StatsContext(ctx)
		if !ok || !validCacheMemoryStats(stats) {
			return false
		}
		var added bool
		entryLimits, added = addCacheMemoryValue(entryLimits, int64(stats.Limits.MaxEntries))
		if !added {
			return false
		}
		byteLimits, added = addCacheMemoryValue(byteLimits, stats.Limits.MaxBytes)
		if !added {
			return false
		}
	}
	return true
}

func validCacheMemoryStats(stats cachememory.Stats) bool {
	if stats.Entries < 0 || stats.ChargedBytes < 0 || stats.Limits.MaxEntries <= 0 || stats.Limits.MaxBytes <= 0 || stats.Limits.MaxItemBytes <= 0 {
		return false
	}
	if stats.Entries > stats.Limits.MaxEntries || stats.ChargedBytes > stats.Limits.MaxBytes {
		return false
	}
	if stats.Closed && (stats.Entries != 0 || stats.ChargedBytes != 0) {
		return false
	}
	return true
}

func addCacheMemoryValue(total int64, value int64) (int64, bool) {
	if value < 0 || total > math.MaxInt64-value {
		return 0, false
	}
	return total + value, true
}

func (s *cacheMemoryStatsState) callback(ctx context.Context, observer metric.Observer) (err error) {
	if !s.admit() {
		return nil
	}
	defer s.leave()
	defer func() { _ = recover() }()
	s.observe(ctx, observer)
	return nil
}

func (s *cacheMemoryStatsState) admit() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return false
	}
	s.inFlight++
	return true
}

func (s *cacheMemoryStatsState) leave() {
	s.mu.Lock()
	s.inFlight--
	if s.inFlight == 0 {
		s.cond.Broadcast()
	}
	s.mu.Unlock()
}

type cacheMemoryAggregate struct {
	entries    int64
	bytes      int64
	entryLimit int64
	byteLimit  int64
	active     int64
	closed     int64
}

func (s *cacheMemoryStatsState) observe(ctx context.Context, observer metric.Observer) {
	if nilInterface(ctx) || ctx.Err() != nil {
		return
	}
	var aggregate cacheMemoryAggregate
	for _, backend := range s.backends {
		if ctx.Err() != nil {
			return
		}
		stats, ok := backend.StatsContext(ctx)
		if !ok || !validCacheMemoryStats(stats) {
			return
		}
		if stats.Closed {
			aggregate.closed++
			continue
		}
		aggregate.active++
		if aggregate.entries, ok = addCacheMemoryValue(aggregate.entries, int64(stats.Entries)); !ok {
			return
		}
		if aggregate.bytes, ok = addCacheMemoryValue(aggregate.bytes, stats.ChargedBytes); !ok {
			return
		}
		if aggregate.entryLimit, ok = addCacheMemoryValue(aggregate.entryLimit, int64(stats.Limits.MaxEntries)); !ok {
			return
		}
		if aggregate.byteLimit, ok = addCacheMemoryValue(aggregate.byteLimit, stats.Limits.MaxBytes); !ok {
			return
		}
	}
	if ctx.Err() != nil {
		return
	}
	for _, gauge := range s.gauges {
		value, emit := aggregate.value(gauge.signal)
		if !emit {
			continue
		}
		attributes, admitted := admitSignalAttributes(gauge.signal, "", []attribute.KeyValue{AttrComponent.String(ComponentCacheMemory)})
		if admitted {
			safeObserveInt64(observer, gauge.instrument, value, metric.WithAttributes(attributes...))
		}
	}
}

func (a cacheMemoryAggregate) value(signal Signal) (int64, bool) {
	switch signal {
	case SignalCacheMemoryEntries:
		return a.entries, a.active != 0
	case SignalCacheMemoryBytes:
		return a.bytes, a.active != 0
	case SignalCacheMemoryEntryLimit:
		return a.entryLimit, a.active != 0
	case SignalCacheMemoryByteLimit:
		return a.byteLimit, a.active != 0
	case SignalCacheMemoryActive:
		return a.active, true
	case SignalCacheMemoryClosed:
		return a.closed, true
	default:
		return 0, false
	}
}

func safeRegisterCallback(meter metric.Meter, callback metric.Callback, instruments ...metric.Observable) (registration metric.Registration, err error, panicked bool) {
	completed := false
	defer func() {
		if !completed {
			_ = recover()
			registration = nil
			err = nil
			panicked = true
		}
	}()
	registration, err = meter.RegisterCallback(callback, instruments...)
	completed = true
	return registration, err, false
}

func safeUnregister(registration metric.Registration) (failed bool) {
	if nilInterface(registration) {
		return false
	}
	completed := false
	defer func() {
		if !completed {
			_ = recover()
			failed = true
		}
	}()
	err := registration.Unregister()
	completed = true
	return err != nil
}

func safeObserveInt64(observer metric.Observer, instrument metric.Int64Observable, value int64, options ...metric.ObserveOption) {
	defer func() { _ = recover() }()
	observer.ObserveInt64(instrument, value, options...)
}

func (s *cacheMemoryStatsState) failRegistration(native metric.Registration) error {
	s.mu.Lock()
	s.active = false
	s.tearingDown = true
	s.mu.Unlock()
	failed := safeUnregister(native)
	s.mu.Lock()
	for s.inFlight != 0 {
		s.cond.Wait()
	}
	s.backends = nil
	s.gauges = nil
	s.native = nil
	slot := s.slot
	token := s.token
	s.slot = nil
	s.token = 0
	s.mu.Unlock()
	slot.release(token)
	s.mu.Lock()
	if failed {
		s.unregisterErr = ErrCallbackUnregister
	}
	s.done = true
	s.cond.Broadcast()
	err := s.unregisterErr
	s.mu.Unlock()
	return err
}

func (r *cacheMemoryRegistration) Unregister() error {
	if r == nil || r.state == nil {
		return nil
	}
	s := r.state
	s.mu.Lock()
	if s.done {
		err := s.unregisterErr
		s.mu.Unlock()
		return err
	}
	if s.tearingDown {
		for !s.done {
			s.cond.Wait()
		}
		err := s.unregisterErr
		s.mu.Unlock()
		return err
	}
	s.tearingDown = true
	s.active = false
	native := s.native
	s.mu.Unlock()

	failed := safeUnregister(native)
	s.mu.Lock()
	for s.inFlight != 0 {
		s.cond.Wait()
	}
	s.backends = nil
	s.gauges = nil
	s.native = nil
	slot := s.slot
	token := s.token
	s.slot = nil
	s.token = 0
	s.mu.Unlock()
	slot.release(token)

	s.mu.Lock()
	if failed {
		s.unregisterErr = ErrCallbackUnregister
	}
	s.done = true
	s.cond.Broadcast()
	err := s.unregisterErr
	s.mu.Unlock()
	return err
}
