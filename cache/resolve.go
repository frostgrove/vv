package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

var errSuperseded = errors.New("cache operation was superseded")

type loaderFailure struct {
	cause error
}

func (this *loaderFailure) Error() string { return "cache loader failed" }

func (this *loaderFailure) Is(target error) bool {
	return target == ErrLoader || safeErrorIs(this.cause, target)
}

func (this *loaderFailure) opaqueErrors() (error, error) { return ErrLoader, this.cause }

type resultSnapshot struct {
	state        State
	payload      []byte
	encodedBytes int
}

type flightGroup struct {
	base        context.Context
	root        context.Context
	loaderCtx   context.Context
	cancel      context.CancelFunc
	timerCancel func()
	members     map[Address]*flightMember
}

type flightMember struct {
	done        chan struct{}
	group       *flightGroup
	state       *addressState
	address     Address
	generation  uint64
	waiters     int
	background  bool
	invalidated bool
	snapshot    resultSnapshot
	err         error
}

func (this *Cache[K, V]) Resolve(ctx context.Context, key K, load Loader[K, V]) (Result[V], error) {
	core, err := this.core()
	if err != nil {
		return Result[V]{}, err
	}
	if nilInterface(ctx) || load == nil {
		return Result[V]{}, failure("resolve", fmt.Errorf("%w: context and loader are required", ErrInvalid))
	}
	if err := ctx.Err(); err != nil {
		return Result[V]{}, failure("resolve", err)
	}
	if core.policy.disabled {
		return disabledLoad(ctx, key, load)
	}
	address, _, err := addressOf(core.scope, core.keys, core.keyVersion, key, core.policy.MaxKeyBytes)
	if err != nil {
		return Result[V]{}, err
	}
	return core.resolveAddress(ctx, address, key, load)
}

func disabledLoad[K, V any](ctx context.Context, key K, load Loader[K, V]) (Result[V], error) {
	loaded, err := invokeLoader(ctx, key, load)
	if err != nil {
		return Result[V]{}, &loaderFailure{cause: err}
	}
	switch loaded.Presence {
	case Found:
		return Result[V]{Value: loaded.Value, State: Loaded}, nil
	case CleanAbsent:
		return Result[V]{State: Negative}, nil
	default:
		return Result[V]{}, failure("load", fmt.Errorf("%w: loader presence is invalid", ErrInvalid))
	}
}

func (this *cacheCore[K, V]) resolveAddress(ctx context.Context, address Address, key K, load Loader[K, V]) (Result[V], error) {
	state := this.acquireState(address)
	defer this.releaseState(address, state)
	var saturationTimer Timer
	defer func() { stopTimer(saturationTimer) }()
	for {
		ticket, err := this.beginRead(ctx, state)
		if err != nil {
			return Result[V]{}, failure("resolve", err)
		}
		cached, readErr := this.lookupAddress(ctx, address)
		this.coord.mu.Lock()
		if state.generation != ticket.generation || state.writeActive || state.invalidating {
			this.coord.mu.Unlock()
			continue
		}
		if readErr != nil {
			this.coord.mu.Unlock()
			return Result[V]{}, readErr
		}
		member := state.member
		if cached.State == Hit || cached.State == Negative {
			this.coord.mu.Unlock()
			return cached, nil
		}
		if cached.State == Stale && this.policy.Stale == ServeWhileRefreshing {
			if member != nil {
				this.coord.mu.Unlock()
				return cached, nil
			}
			if state.stagedMutation != 0 {
				this.coord.mu.Unlock()
				return cached, nil
			}
			if this.coord.activeFlights < this.policy.MaxFlights {
				start := this.registerFlightLocked(ctx, address, state, true)
				this.coord.mu.Unlock()
				this.startFlight(start, key, load)
				return cached, nil
			}
		}
		if state.stagedMutation != 0 {
			changed := state.changed
			this.coord.mu.Unlock()
			if err := waitChannel(ctx, changed); err != nil {
				return Result[V]{}, failure("resolve", err)
			}
			continue
		}
		if member != nil {
			member.waiters++
			this.coord.mu.Unlock()
			result, err := this.awaitMember(ctx, member)
			if safeErrorIs(err, errSuperseded) {
				continue
			}
			if isLoaderFailure(err) && this.staleStillUsable(cached) && this.policy.Stale == ServeOnLoaderError && ctx.Err() == nil {
				return cached, nil
			}
			return result, err
		}
		if this.coord.activeFlights < this.policy.MaxFlights {
			member = this.registerFlightLocked(ctx, address, state, false)
			member.waiters++
			this.coord.mu.Unlock()
			this.startFlight(member, key, load)
			result, err := this.awaitMember(ctx, member)
			if safeErrorIs(err, errSuperseded) {
				continue
			}
			if isLoaderFailure(err) && this.staleStillUsable(cached) && this.policy.Stale == ServeOnLoaderError && ctx.Err() == nil {
				return cached, nil
			}
			return result, err
		}
		capacityChanged := this.coord.capacityChanged
		stateChanged := state.changed
		this.coord.mu.Unlock()
		if cached.State == Stale && this.policy.FlightSaturation.mode == ServeStaleFlight {
			return cached, nil
		}
		if this.policy.FlightSaturation.mode != WaitForFlight {
			return Result[V]{}, failure("resolve", ErrSaturated)
		}
		if saturationTimer == nil {
			saturationTimer, err = runtimeTimer(this.runtime.Clock, this.policy.FlightSaturation.timeout)
			if err != nil {
				return Result[V]{}, failure("resolve", err)
			}
		}
		if err := waitForCapacity(ctx, saturationTimer, capacityChanged, stateChanged); err != nil {
			return Result[V]{}, failure("resolve", err)
		}
	}
}

func (this *cacheCore[K, V]) staleStillUsable(result Result[V]) bool {
	if result.State != Stale || result.validUntil.IsZero() {
		return false
	}
	now, err := conservativeNow(this.runtime)
	return err == nil && now.Before(result.validUntil)
}

func isLoaderFailure(err error) bool {
	_, ok := err.(*loaderFailure)
	return ok
}

func (this *cacheCore[K, V]) registerFlightLocked(ctx context.Context, address Address, state *addressState, background bool) *flightMember {
	base := context.WithoutCancel(ctx)
	root, cancel := context.WithCancel(base)
	group := &flightGroup{
		base:    base,
		root:    root,
		cancel:  cancel,
		members: make(map[Address]*flightMember, 1),
	}
	member := &flightMember{
		done:       make(chan struct{}),
		group:      group,
		state:      state,
		address:    address,
		generation: state.generation,
		background: background,
	}
	group.members[address] = member
	state.member = member
	state.refs++
	this.coord.activeFlights++
	this.signalStateLocked(state)
	return member
}

func (this *cacheCore[K, V]) startFlight(member *flightMember, key K, load Loader[K, V]) {
	go func() {
		loaderCtx, timerCancel, err := this.loaderContext(member.group.root)
		if err != nil {
			member.group.cancel()
			this.finishFlight(member, resultSnapshot{}, failure("load", err))
			return
		}
		member.group.loaderCtx = loaderCtx
		member.group.timerCancel = timerCancel
		this.runFlight(member, key, load)
	}()
}

func (this *cacheCore[K, V]) runFlight(member *flightMember, key K, load Loader[K, V]) {
	var snapshot resultSnapshot
	var err error
	func() {
		defer func() {
			if recover() != nil {
				snapshot = resultSnapshot{}
				err = failure("load", fmt.Errorf("cache flight panicked"))
			}
		}()
		var loaded LoadResult[V]
		loadErr := member.group.loaderCtx.Err()
		if loadErr == nil {
			loaded, loadErr = invokeLoader(member.group.loaderCtx, key, load)
		}
		deadline, _ := member.group.loaderCtx.Deadline()
		loaderErr := member.group.loaderCtx.Err()
		now, clockErr := runtimeNow(this.runtime.Clock)
		if clockErr != nil {
			member.group.cancel()
			member.group.timerCancel()
			snapshot = resultSnapshot{}
			err = failure("load", clockErr)
			return
		} else if !now.Before(deadline) {
			loaderErr = context.DeadlineExceeded
		}
		if loaderErr != nil {
			if errors.Is(loaderErr, context.Canceled) {
				loadErr = errSuperseded
			} else {
				loadErr = loaderErr
			}
		}
		member.group.cancel()
		member.group.timerCancel()
		snapshot, err = this.storeLoaded(member, loaded, loadErr)
	}()
	member.group.cancel()
	if member.group.timerCancel != nil {
		member.group.timerCancel()
	}
	this.finishFlight(member, snapshot, err)
}

func invokeLoader[K, V any](ctx context.Context, key K, load Loader[K, V]) (result LoadResult[V], err error) {
	defer func() {
		if recover() != nil {
			result = LoadResult[V]{}
			err = failure("load", fmt.Errorf("loader panicked"))
		}
	}()
	return load(ctx, key)
}

func (this *cacheCore[K, V]) storeLoaded(member *flightMember, loaded LoadResult[V], loadErr error) (resultSnapshot, error) {
	if loadErr != nil {
		return resultSnapshot{}, &loaderFailure{cause: loadErr}
	}
	if loaded.Presence != Found && loaded.Presence != CleanAbsent {
		return resultSnapshot{}, failure("load", fmt.Errorf("%w: loader presence is invalid", ErrInvalid))
	}
	if loaded.Presence == CleanAbsent && this.policy.Negative.duration <= 0 {
		return resultSnapshot{state: Negative}, nil
	}
	encoded, payload, expiry, err := encodeEnvelope(this.runtime, this.values, this.valueDescriptor, this.policy, loaded)
	if err != nil {
		return resultSnapshot{}, err
	}
	if _, err := this.commitFlight(member, encoded, expiry); err != nil {
		return resultSnapshot{}, err
	}
	if loaded.Presence == CleanAbsent {
		return resultSnapshot{state: Negative, encodedBytes: len(encoded)}, nil
	}
	return resultSnapshot{state: Loaded, payload: payload, encodedBytes: len(encoded)}, nil
}

func (this *cacheCore[K, V]) commitFlight(member *flightMember, encoded []byte, expiry Expiry) (bool, error) {
	this.coord.mu.Lock()
	state := member.state
	allowed := state.member == member && state.generation == member.generation && !state.writeActive && !state.invalidating && !member.invalidated
	if allowed {
		state.writeActive = true
		this.coord.activeWrites++
	}
	this.coord.mu.Unlock()
	if !allowed {
		return false, errSuperseded
	}
	backendCtx, cancel, contextErr := this.backendContext(member.group.base)
	if contextErr != nil {
		this.finishFlightWrite(member)
		return false, failure("resolve", contextErr)
	}
	currentExpiry, live, expiryErr := expiryForWrite(this.runtime, expiry)
	if expiryErr != nil {
		cancel()
		this.finishFlightWrite(member)
		return false, failure("resolve", expiryErr)
	}
	if !live {
		cancel()
		if this.finishFlightWrite(member) {
			return false, errSuperseded
		}
		return false, nil
	}
	err := backendPut(this.backend, backendCtx, member.address, encoded, currentExpiry)
	cancel()
	superseded := this.finishFlightWrite(member)
	if superseded {
		return false, errSuperseded
	}
	if err == nil {
		return true, nil
	}
	if this.policy.WriteFailure == Ignore {
		return false, nil
	}
	return false, failure("resolve", err)
}

func (this *cacheCore[K, V]) finishFlightWrite(member *flightMember) bool {
	this.coord.mu.Lock()
	state := member.state
	superseded := member.invalidated || state.member != member || state.generation != member.generation || state.invalidating
	if !superseded {
		state.generation++
		member.generation = state.generation
	}
	state.writeActive = false
	this.coord.activeWrites--
	this.signalStateLocked(state)
	this.coord.mu.Unlock()
	return superseded
}

func (this *cacheCore[K, V]) finishFlight(member *flightMember, snapshot resultSnapshot, err error) {
	this.coord.mu.Lock()
	state := member.state
	if member.invalidated {
		snapshot = resultSnapshot{}
		err = errSuperseded
	}
	member.snapshot = snapshot
	member.err = err
	if state.member == member {
		state.member = nil
		state.generation++
		this.signalStateLocked(state)
	}
	close(member.done)
	if state.refs > 0 {
		state.refs--
	}
	this.cleanupStateLocked(member.address, state)
	this.coord.mu.Unlock()
	defer func() {
		this.coord.mu.Lock()
		this.coord.activeFlights--
		this.signalCapacityLocked()
		this.coord.mu.Unlock()
	}()
	outcome := outcomeForState(snapshot.state)
	if safeErrorIs(err, errSuperseded) {
		outcome = SupersededOutcome
	} else if err != nil {
		outcome = ErrorOutcome
	}
	this.observe(member.group.base, Event{
		Operation:    LoadOperation,
		Outcome:      outcome,
		Items:        1,
		EncodedBytes: int64(snapshot.encodedBytes),
		PayloadBytes: int64(len(snapshot.payload)),
	})
}

func (this *cacheCore[K, V]) awaitMember(ctx context.Context, member *flightMember) (Result[V], error) {
	select {
	case <-member.done:
		this.finishWaiter(member, false)
		if member.err != nil {
			return Result[V]{}, member.err
		}
		return this.materialize(member.snapshot)
	case <-ctx.Done():
		this.finishWaiter(member, true)
		return Result[V]{}, failure("resolve", ctx.Err())
	}
}

func (this *cacheCore[K, V]) materialize(snapshot resultSnapshot) (Result[V], error) {
	if snapshot.state == Negative {
		return Result[V]{State: Negative}, nil
	}
	if snapshot.state != Loaded {
		return Result[V]{}, failure("resolve", ErrCorrupt)
	}
	value, err := invokeDecode(this.values, bytes.Clone(snapshot.payload), ValueLimit{
		MaxBytes:        this.policy.MaxValueBytes,
		MaxDecodedBytes: this.policy.MaxValueBytes,
		MaxDepth:        this.policy.MaxValueDepth,
	})
	if err != nil {
		return Result[V]{}, failure("resolve", ErrCorrupt)
	}
	return Result[V]{Value: value, State: Loaded}, nil
}

func (this *cacheCore[K, V]) finishWaiter(member *flightMember, canceled bool) {
	this.coord.mu.Lock()
	if member.waiters > 0 {
		member.waiters--
	}
	if canceled && member.waiters == 0 && !member.background && this.policy.LastWaiter == CancelLoader {
		member.group.cancel()
	}
	this.coord.mu.Unlock()
}

func (this *cacheCore[K, V]) cleanupStateLocked(address Address, state *addressState) {
	if this.coord.states[address] == state && state.refs == 0 && state.stagedMutation == 0 && !state.writeActive && !state.invalidating && state.member == nil {
		delete(this.coord.states, address)
	}
}

func (this *cacheCore[K, V]) signalCapacityLocked() {
	close(this.coord.capacityChanged)
	this.coord.capacityChanged = make(chan struct{})
}

func waitForCapacity(ctx context.Context, timer Timer, capacityChanged, stateChanged <-chan struct{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C():
		return ErrSaturated
	case <-capacityChanged:
		return nil
	case <-stateChanged:
		return nil
	}
}

func stopTimer(timer Timer) {
	if !nilInterface(timer) {
		timer.Stop()
	}
}
