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
	transient   *transientLease
	finished    bool
	observed    bool
	decodeToken chan struct{}
}

type resolveReservation struct {
	lease  *transientLease
	weight int64
}

func (this *resolveReservation) release() {
	if this == nil || this.lease == nil {
		return
	}
	this.lease.release()
	this.lease = nil
	this.weight = 0
}

func (this *resolveReservation) reduceTo(weight int64) bool {
	if this == nil || this.lease == nil || weight < 0 || weight > this.weight || !this.lease.reduceTo(weight) {
		return false
	}
	this.weight = weight
	return true
}

func (this *resolveReservation) tryGrowTo(weight int64) bool {
	if this == nil || this.lease == nil || weight < this.weight || !this.lease.tryGrowTo(weight) {
		return false
	}
	this.weight = weight
	return true
}

func (this *resolveReservation) split(weight int64) (*transientLease, bool) {
	if this == nil || this.lease == nil || weight <= 0 || weight > this.weight {
		return nil, false
	}
	lease, ok := this.lease.split(weight)
	if !ok {
		return nil, false
	}
	this.weight -= weight
	return lease, true
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
		return core.disabledLoad(ctx, key, load)
	}
	address, _, err := core.transientAddress(ctx, key, LoadOperation)
	if err != nil {
		return Result[V]{}, err
	}
	defer core.forgetMemoized(ctx, address)
	return core.resolveAddress(ctx, address, key, load)
}

func (this *cacheCore[K, V]) disabledLoad(ctx context.Context, key K, load Loader[K, V]) (Result[V], error) {
	lease, err := this.transient.acquire(ctx, this.runtime.Clock, this.policy.TransientSaturation, this.transientPlan.build)
	if err != nil {
		return Result[V]{}, failure("load", err)
	}
	defer lease.release()
	loaderCtx, cancel, err := this.loaderContext(valueBlindContext{Context: ctx})
	if err != nil {
		this.observe(context.Background(), Event{Operation: LoadOperation, Outcome: ErrorOutcome, Items: 1})
		if callerErr := ctx.Err(); callerErr != nil {
			return Result[V]{}, failure("resolve", callerErr)
		}
		return Result[V]{}, failure("load", err)
	}
	callerDeadline, hasCallerDeadline := ctx.Deadline()
	effectiveDeadline, _ := loaderCtx.Deadline()
	loaded, loadErr, clockErr, termination := invokeTimedLoader(this.runtime, loaderCtx, key, load, context.Canceled)
	cancel()
	if callerErr := ctx.Err(); callerErr != nil {
		this.observe(context.Background(), Event{Operation: LoadOperation, Outcome: ErrorOutcome, Items: 1})
		return Result[V]{}, failure("resolve", callerErr)
	}
	if clockErr != nil {
		this.observe(context.Background(), Event{Operation: LoadOperation, Outcome: ErrorOutcome, Items: 1})
		return Result[V]{}, failure("load", clockErr)
	}
	if errors.Is(termination, context.DeadlineExceeded) && hasCallerDeadline && !callerDeadline.After(effectiveDeadline) {
		this.observe(context.Background(), Event{Operation: LoadOperation, Outcome: ErrorOutcome, Items: 1})
		return Result[V]{}, failure("resolve", context.DeadlineExceeded)
	}
	if loadErr != nil {
		this.observe(context.Background(), Event{Operation: LoadOperation, Outcome: ErrorOutcome, Items: 1})
		return Result[V]{}, &loaderFailure{cause: loadErr}
	}
	switch loaded.Presence {
	case Found:
		this.observe(context.Background(), Event{Operation: LoadOperation, Outcome: LoadedOutcome, Items: 1})
		return Result[V]{Value: loaded.Value, State: Loaded}, nil
	case CleanAbsent:
		this.observe(context.Background(), Event{Operation: LoadOperation, Outcome: NegativeOutcome, Items: 1})
		return Result[V]{State: Negative}, nil
	default:
		this.observe(context.Background(), Event{Operation: LoadOperation, Outcome: ErrorOutcome, Items: 1})
		return Result[V]{}, failure("load", fmt.Errorf("%w: loader presence is invalid", ErrInvalid))
	}
}

func (this *cacheCore[K, V]) resolveAddress(ctx context.Context, address Address, key K, load Loader[K, V]) (Result[V], error) {
	var state *addressState
	var flightTimer Timer
	stopFlightTimer := func() {
		stopTimer(flightTimer)
		flightTimer = nil
	}
	reservation := resolveReservation{}
	defer func() {
		if state != nil {
			this.releaseState(address, state)
		}
		stopFlightTimer()
		reservation.release()
	}()

restart:
	for {
		stopFlightTimer()
		if state != nil {
			this.releaseState(address, state)
			state = nil
		}
		reservation.release()
		probe, err := this.transient.acquire(ctx, this.runtime.Clock, this.policy.TransientSaturation, this.transientPlan.background)
		if err != nil {
			return Result[V]{}, failure("resolve", err)
		}
		reservation = resolveReservation{lease: probe, weight: this.transientPlan.background}
		state = this.acquireState(address)
		for {
			ticket, err := this.beginRead(ctx, state)
			if err != nil {
				return Result[V]{}, failure("resolve", err)
			}
			cached, readErr := this.lookupAddressAdmitted(ctx, address)
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
				if member != nil || state.stagedMutation != 0 {
					this.coord.mu.Unlock()
					return cached, nil
				}
			}
			if state.stagedMutation != 0 {
				changed := state.changed
				this.coord.mu.Unlock()
				stopFlightTimer()
				if err := this.waitCoordination(ctx, changed); err != nil {
					return Result[V]{}, failure("resolve", err)
				}
				continue
			}
			if member != nil {
				this.addWaiterLocked(member)
				this.coord.mu.Unlock()
				stopFlightTimer()
				weight := this.transientPlan.waiter
				if cached.State == Stale {
					weight = this.transientPlan.retained
				}
				reservation.reduceTo(weight)
				result, memberErr := this.awaitMember(ctx, member)
				if isLoaderFailure(memberErr) && this.staleStillUsable(cached) && this.policy.Stale == ServeOnLoaderError && ctx.Err() == nil {
					return cached, nil
				}
				if safeErrorIs(memberErr, errSuperseded) {
					continue restart
				}
				return result, memberErr
			}
			if this.coord.activeFlights < this.policy.MaxFlights {
				background := cached.State == Stale && this.policy.Stale == ServeWhileRefreshing
				member, grown, admissionErr := this.prepareProbedFlightLocked(ctx, address, state, background, flightTimer, &reservation)
				this.coord.mu.Unlock()
				if admissionErr != nil {
					return Result[V]{}, failure("resolve", admissionErr)
				}
				if !grown {
					break
				}
				stopFlightTimer()
				if background {
					reservation.reduceTo(this.transientPlan.retained)
					this.startFlight(member, key, load)
					return cached, nil
				}
				if cached.State == Stale {
					reservation.reduceTo(this.transientPlan.retained)
				} else {
					reservation.reduceTo(this.transientPlan.waiter)
				}
				this.startFlight(member, key, load)
				result, memberErr := this.awaitMember(ctx, member)
				if safeErrorIs(memberErr, errSuperseded) {
					continue restart
				}
				if isLoaderFailure(memberErr) && this.staleStillUsable(cached) && this.policy.Stale == ServeOnLoaderError && ctx.Err() == nil {
					return cached, nil
				}
				return result, memberErr
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
			if flightTimer == nil {
				flightTimer, err = runtimeTimer(this.runtime.Clock, this.policy.FlightSaturation.timeout)
				if err != nil {
					return Result[V]{}, failure("resolve", err)
				}
			}
			if err := this.waitCoordinationCapacity(ctx, flightTimer, capacityChanged, stateChanged); err != nil {
				return Result[V]{}, failure("resolve", err)
			}
		}
		stopFlightTimer()
		this.releaseState(address, state)
		state = nil
		reservation.release()
		lease, err := this.transient.acquire(ctx, this.runtime.Clock, this.policy.TransientSaturation, this.transientPlan.resolve)
		if err != nil {
			return Result[V]{}, failure("resolve", err)
		}
		reservation = resolveReservation{lease: lease, weight: this.transientPlan.resolve}
		state = this.acquireState(address)
		for {
			ticket, err := this.beginRead(ctx, state)
			if err != nil {
				return Result[V]{}, failure("resolve", err)
			}
			cached, readErr := this.lookupAddressAdmitted(ctx, address)
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
					reservation.reduceTo(this.transientPlan.retained)
					return cached, nil
				}
				if state.stagedMutation != 0 {
					this.coord.mu.Unlock()
					return cached, nil
				}
				if this.coord.activeFlights < this.policy.MaxFlights {
					generation := state.generation
					this.coord.mu.Unlock()
					start, admitted, admissionErr := this.prepareFlight(ctx, address, state, generation, true, flightTimer, &reservation)
					if admissionErr != nil {
						if this.policy.FlightSaturation.mode == ServeStaleFlight && ctx.Err() == nil {
							return cached, nil
						}
						return Result[V]{}, failure("resolve", admissionErr)
					}
					if !admitted {
						continue
					}
					reservation.reduceTo(this.transientPlan.retained)
					this.startFlight(start, key, load)
					return cached, nil
				}
			}
			if state.stagedMutation != 0 {
				changed := state.changed
				this.coord.mu.Unlock()
				stopFlightTimer()
				if err := this.waitCoordination(ctx, changed); err != nil {
					return Result[V]{}, failure("resolve", err)
				}
				continue
			}
			if member != nil {
				this.addWaiterLocked(member)
				this.coord.mu.Unlock()
				if cached.State == Stale {
					reservation.reduceTo(this.transientPlan.retained)
				} else {
					reservation.reduceTo(this.transientPlan.waiter)
				}
				stopFlightTimer()
				result, err := this.awaitMember(ctx, member)
				if safeErrorIs(err, errSuperseded) {
					continue restart
				}
				if isLoaderFailure(err) && this.staleStillUsable(cached) && this.policy.Stale == ServeOnLoaderError && ctx.Err() == nil {
					return cached, nil
				}
				return result, err
			}
			if this.coord.activeFlights < this.policy.MaxFlights {
				generation := state.generation
				this.coord.mu.Unlock()
				member, admitted, admissionErr := this.prepareFlight(ctx, address, state, generation, false, flightTimer, &reservation)
				if admissionErr != nil {
					if cached.State == Stale && this.policy.FlightSaturation.mode == ServeStaleFlight && ctx.Err() == nil {
						return cached, nil
					}
					return Result[V]{}, failure("resolve", admissionErr)
				}
				if !admitted {
					continue
				}
				if cached.State == Stale {
					reservation.reduceTo(this.transientPlan.retained)
				} else {
					reservation.reduceTo(this.transientPlan.waiter)
				}
				stopFlightTimer()
				this.startFlight(member, key, load)
				result, err := this.awaitMember(ctx, member)
				if safeErrorIs(err, errSuperseded) {
					continue restart
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
			if flightTimer == nil {
				flightTimer, err = runtimeTimer(this.runtime.Clock, this.policy.FlightSaturation.timeout)
				if err != nil {
					return Result[V]{}, failure("resolve", err)
				}
			}
			if err := this.waitCoordinationCapacity(ctx, flightTimer, capacityChanged, stateChanged); err != nil {
				return Result[V]{}, failure("resolve", err)
			}
		}
	}
}

func (this *cacheCore[K, V]) prepareProbedFlightLocked(ctx context.Context, address Address, state *addressState, background bool, deadline Timer, reservation *resolveReservation) (*flightMember, bool, error) {
	if !background && ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	if timerExpired(deadline) {
		return nil, false, ErrSaturated
	}
	probeWeight := reservation.weight
	if !reservation.tryGrowTo(this.transientPlan.resolve) {
		return nil, false, nil
	}
	if !background && ctx.Err() != nil {
		reservation.reduceTo(probeWeight)
		return nil, false, ctx.Err()
	}
	if timerExpired(deadline) {
		reservation.reduceTo(probeWeight)
		return nil, false, ErrSaturated
	}
	lease, ok := reservation.split(this.transientPlan.build)
	if !ok {
		return nil, true, ErrTooLarge
	}
	if !background && ctx.Err() != nil {
		lease.release()
		return nil, false, ctx.Err()
	}
	if timerExpired(deadline) {
		lease.release()
		return nil, false, ErrSaturated
	}
	member := this.registerFlightLocked(address, state, background, lease)
	if !background {
		this.addWaiterLocked(member)
	}
	return member, true, nil
}

func (this *cacheCore[K, V]) prepareFlight(ctx context.Context, address Address, state *addressState, generation uint64, background bool, deadline Timer, reservation *resolveReservation) (*flightMember, bool, error) {
	this.coord.mu.Lock()
	if !background && ctx.Err() != nil {
		this.coord.mu.Unlock()
		return nil, false, ctx.Err()
	}
	if timerExpired(deadline) {
		this.coord.mu.Unlock()
		return nil, false, ErrSaturated
	}
	allowed := state.generation == generation && !state.writeActive && !state.invalidating && state.stagedMutation == 0 &&
		state.member == nil && this.coord.activeFlights < this.policy.MaxFlights
	if !allowed {
		this.coord.mu.Unlock()
		return nil, false, nil
	}
	lease, ok := reservation.split(this.transientPlan.build)
	if !ok {
		this.coord.mu.Unlock()
		return nil, false, ErrTooLarge
	}
	if !background && ctx.Err() != nil {
		this.coord.mu.Unlock()
		lease.release()
		return nil, false, ctx.Err()
	}
	if timerExpired(deadline) {
		this.coord.mu.Unlock()
		lease.release()
		return nil, false, ErrSaturated
	}
	member := this.registerFlightLocked(address, state, background, lease)
	if !background {
		this.addWaiterLocked(member)
	}
	this.coord.mu.Unlock()
	return member, true, nil
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

func (this *cacheCore[K, V]) registerFlightLocked(address Address, state *addressState, background bool, lease *transientLease) *flightMember {
	base := context.Background()
	root, cancel := context.WithCancel(base)
	group := &flightGroup{
		base:   base,
		root:   root,
		cancel: cancel,
	}
	member := &flightMember{
		done:       make(chan struct{}),
		group:      group,
		state:      state,
		address:    address,
		generation: state.generation,
		background: background,
		transient:  lease,
	}
	state.member = member
	state.refs++
	this.coord.activeFlights++
	this.signalStateLocked(state)
	return member
}

func (this *cacheCore[K, V]) addWaiterLocked(member *flightMember) {
	member.waiters++
	this.coord.flightWaiters++
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
		loaded, loadErr, clockErr, _ := invokeTimedLoader(this.runtime, member.group.loaderCtx, key, load, errSuperseded)
		if clockErr != nil {
			member.group.cancel()
			member.group.timerCancel()
			snapshot = resultSnapshot{}
			err = failure("load", clockErr)
			return
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

func invokeTimedLoader[K, V any](runtime Runtime, ctx context.Context, key K, load Loader[K, V], canceled error) (LoadResult[V], error, error, error) {
	var loaded LoadResult[V]
	loadErr := ctx.Err()
	if loadErr == nil {
		loaded, loadErr = invokeLoader(ctx, key, load)
	}
	deadline, hasDeadline := ctx.Deadline()
	loaderErr := ctx.Err()
	now, clockErr := runtimeNow(runtime.Clock)
	if clockErr != nil {
		return LoadResult[V]{}, nil, clockErr, nil
	}
	if hasDeadline && !now.Before(deadline) {
		loaderErr = context.DeadlineExceeded
	}
	if loaderErr != nil {
		if errors.Is(loaderErr, context.Canceled) {
			loadErr = canceled
		} else {
			loadErr = loaderErr
		}
	}
	return loaded, loadErr, nil, loaderErr
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
	member.finished = true
	if err == nil && snapshot.state == Loaded && member.waiters > 0 {
		member.decodeToken = make(chan struct{}, 1)
		member.decodeToken <- struct{}{}
	}
	if member.waiters == 0 {
		member.snapshot = resultSnapshot{}
	}
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
	this.coord.mu.Lock()
	member.observed = true
	var release *transientLease
	if member.waiters == 0 && member.transient != nil {
		release = member.transient
		member.transient = nil
	}
	this.coord.activeFlights--
	this.signalCapacityLocked()
	this.coord.mu.Unlock()
	release.release()
}

func (this *cacheCore[K, V]) awaitMember(ctx context.Context, member *flightMember) (Result[V], error) {
	select {
	case <-member.done:
		if err := ctx.Err(); err != nil {
			this.finishWaiter(member, true)
			return Result[V]{}, failure("resolve", err)
		}
		if member.err != nil {
			this.finishWaiter(member, false)
			return Result[V]{}, member.err
		}
	case <-ctx.Done():
		this.finishWaiter(member, true)
		return Result[V]{}, failure("resolve", ctx.Err())
	}
	if member.snapshot.state == Negative {
		this.finishWaiter(member, false)
		return Result[V]{State: Negative}, nil
	}
	if member.snapshot.state != Loaded || member.decodeToken == nil {
		this.finishWaiter(member, false)
		return Result[V]{}, failure("resolve", ErrCorrupt)
	}
	select {
	case <-member.decodeToken:
	case <-ctx.Done():
		this.finishWaiter(member, true)
		return Result[V]{}, failure("resolve", ctx.Err())
	}
	if err := ctx.Err(); err != nil {
		member.decodeToken <- struct{}{}
		this.finishWaiter(member, true)
		return Result[V]{}, failure("resolve", err)
	}
	result, err := this.materialize(member.snapshot)
	contextErr := ctx.Err()
	member.decodeToken <- struct{}{}
	this.finishWaiter(member, contextErr != nil)
	if contextErr != nil {
		return Result[V]{}, failure("resolve", contextErr)
	}
	return result, err
}

func (this *cacheCore[K, V]) materialize(snapshot resultSnapshot) (Result[V], error) {
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
	if _, err := invokeDecodeCharge(this.values, value, this.policy.MaxValueBytes); err != nil {
		return Result[V]{}, failure("resolve", ErrCorrupt)
	}
	return Result[V]{Value: value, State: Loaded}, nil
}

func (this *cacheCore[K, V]) finishWaiter(member *flightMember, canceled bool) {
	this.coord.mu.Lock()
	if member.waiters > 0 {
		member.waiters--
		this.coord.flightWaiters--
	}
	if canceled && member.waiters == 0 && !member.background && this.policy.LastWaiter == CancelLoader {
		member.group.cancel()
	}
	var release *transientLease
	if member.finished && member.observed && member.waiters == 0 && member.transient != nil {
		release = member.transient
		member.transient = nil
	}
	if member.finished && member.waiters == 0 {
		member.snapshot = resultSnapshot{}
		member.decodeToken = nil
	}
	this.coord.mu.Unlock()
	release.release()
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

func (this *cacheCore[K, V]) waitCoordinationCapacity(ctx context.Context, timer Timer, capacityChanged, stateChanged <-chan struct{}) error {
	this.coord.mu.Lock()
	this.coord.coordWaiters++
	this.coord.mu.Unlock()
	err := waitForCapacity(ctx, timer, capacityChanged, stateChanged)
	this.coord.mu.Lock()
	this.coord.coordWaiters--
	this.coord.mu.Unlock()
	return err
}

func stopTimer(timer Timer) {
	if !nilInterface(timer) {
		timer.Stop()
	}
}

func timerExpired(timer Timer) bool {
	if nilInterface(timer) {
		return false
	}
	select {
	case <-timer.C():
		return true
	default:
		return false
	}
}
