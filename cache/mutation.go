package cache

import (
	"context"
	"fmt"
)

type mutation struct {
	address    Address
	state      *addressState
	generation uint64
	claimed    bool
}

func (this *Cache[K, V]) Put(ctx context.Context, key K, value V) error {
	core, err := this.core()
	if err != nil {
		return err
	}
	if nilInterface(ctx) {
		return failure("put", fmt.Errorf("%w: context is nil", ErrInvalid))
	}
	if err := ctx.Err(); err != nil {
		return failure("put", err)
	}
	if core.policy.disabled {
		return nil
	}
	address, _, err := core.transientAddress(ctx, key, PutOperation)
	if err != nil {
		return err
	}
	lease, err := core.transient.acquire(ctx, core.runtime.Clock, core.policy.TransientSaturation, core.transientPlan.putOperation)
	if err != nil {
		return failure("put", err)
	}
	defer lease.release()
	defer core.forgetMemoized(ctx, address)
	write, err := core.beginMutation(ctx, address)
	if err != nil {
		return err
	}
	encoded, _, expiry, err := encodeEnvelope(core.runtime, core.values, core.valueDescriptor, core.policy, Present(value))
	if err != nil {
		core.abandonMutation(write)
		return err
	}
	err = core.commitMutation(ctx, write, encoded, expiry)
	if safeErrorIs(err, errSuperseded) {
		core.observe(ctx, Event{Operation: PutOperation, Outcome: SupersededOutcome, Items: 1})
		return nil
	}
	return err
}

func (this *cacheCore[K, V]) beginMutation(ctx context.Context, address Address) (mutation, error) {
	return this.beginMutationAs(ctx, address, PutOperation)
}

func (this *cacheCore[K, V]) beginMutationAs(ctx context.Context, address Address, operation Operation) (mutation, error) {
	state := this.acquireState(address)
	for {
		this.coord.mu.Lock()
		if ctx.Err() != nil {
			this.coord.mu.Unlock()
			this.releaseState(address, state)
			return mutation{}, failure(string(operation), ctx.Err())
		}
		if !state.invalidating && state.stagedMutation == 0 {
			state.generation++
			state.stagedMutation = state.generation
			this.invalidateMemberLocked(state)
			this.signalStateLocked(state)
			generation := state.generation
			this.coord.mu.Unlock()
			return mutation{address: address, state: state, generation: generation}, nil
		}
		changed := state.changed
		this.coord.mu.Unlock()
		if err := this.waitCoordination(ctx, changed); err != nil {
			this.releaseState(address, state)
			return mutation{}, failure(string(operation), err)
		}
	}
}

func (this *cacheCore[K, V]) commitMutation(ctx context.Context, write mutation, encoded []byte, expiry Expiry) error {
	return this.commitMutationAs(ctx, write, encoded, expiry, PutOperation)
}

func (this *cacheCore[K, V]) commitMutationAs(ctx context.Context, write mutation, encoded []byte, expiry Expiry, operation Operation) error {
	claimed, err := this.claimMutationAs(ctx, write, operation)
	if err != nil {
		return err
	}
	write = claimed
	ended := false
	defer func() {
		if !ended {
			this.finishMutation(write)
		}
	}()
	backendCtx, cancel, contextErr := this.backendContext(ctx)
	if contextErr != nil {
		return failure(string(operation), contextErr)
	}
	currentExpiry, live, expiryErr := expiryForWrite(this.runtime, expiry)
	if expiryErr != nil {
		cancel()
		return failure(string(operation), expiryErr)
	}
	if !live {
		cancel()
		this.finishMutation(write)
		ended = true
		return failure(string(operation), errSuperseded)
	}
	err = backendPut(this.backend, backendCtx, write.address, encoded, currentExpiry)
	cancel()
	this.finishMutation(write)
	ended = true
	if err == nil {
		this.observe(ctx, Event{
			Operation:    operation,
			Outcome:      StoredOutcome,
			Items:        1,
			EncodedBytes: int64(len(encoded)),
		})
		return nil
	}
	this.observe(ctx, Event{Operation: operation, Outcome: ErrorOutcome, Reason: BackendReason, Items: 1})
	if ctx.Err() != nil {
		return failure(string(operation), ctx.Err())
	}
	if this.policy.WriteFailure == Ignore {
		return nil
	}
	return failure(string(operation), err)
}

func (this *cacheCore[K, V]) claimMutationAs(ctx context.Context, write mutation, operation Operation) (mutation, error) {
	for {
		this.coord.mu.Lock()
		if write.state.generation != write.generation || write.state.stagedMutation != write.generation || write.state.invalidating {
			this.coord.mu.Unlock()
			this.abandonMutation(write)
			return mutation{}, failure(string(operation), errSuperseded)
		}
		if err := ctx.Err(); err != nil {
			this.coord.mu.Unlock()
			this.abandonMutation(write)
			return mutation{}, failure(string(operation), err)
		}
		if !write.state.writeActive {
			write.state.stagedMutation = 0
			write.state.generation++
			write.generation = write.state.generation
			this.invalidateMemberLocked(write.state)
			write.state.writeActive = true
			this.coord.activeWrites++
			write.claimed = true
			this.signalStateLocked(write.state)
			this.coord.mu.Unlock()
			return write, nil
		}
		changed := write.state.changed
		this.coord.mu.Unlock()
		if err := this.waitCoordination(ctx, changed); err != nil {
			this.abandonMutation(write)
			return mutation{}, failure(string(operation), err)
		}
	}
}

func (this *cacheCore[K, V]) finishMutation(write mutation) bool {
	this.coord.mu.Lock()
	superseded := write.state.generation != write.generation || write.state.invalidating
	if write.claimed && write.state.writeActive {
		write.state.writeActive = false
		this.coord.activeWrites--
		this.signalStateLocked(write.state)
	}
	this.coord.mu.Unlock()
	this.releaseState(write.address, write.state)
	return superseded
}

func (this *cacheCore[K, V]) abandonMutation(write mutation) {
	this.coord.mu.Lock()
	if write.state.stagedMutation == write.generation {
		write.state.stagedMutation = 0
		this.signalStateLocked(write.state)
	}
	this.coord.mu.Unlock()
	this.releaseState(write.address, write.state)
}

func (this *Cache[K, V]) Forget(ctx context.Context, key K) error {
	core, err := this.core()
	if err != nil {
		return err
	}
	if nilInterface(ctx) {
		return failure("forget", fmt.Errorf("%w: context is nil", ErrInvalid))
	}
	if err := ctx.Err(); err != nil {
		return failure("forget", err)
	}
	if core.policy.disabled {
		return nil
	}
	address, _, err := core.transientAddress(ctx, key, ForgetOperation)
	if err != nil {
		return err
	}
	lease, err := core.transient.acquire(ctx, core.runtime.Clock, core.policy.TransientSaturation, core.transientPlan.forgetOperation)
	if err != nil {
		return failure("forget", err)
	}
	defer lease.release()
	defer core.forgetMemoized(ctx, address)
	state, err := core.beginInvalidation(ctx, address)
	if err != nil {
		return err
	}
	return core.completeInvalidation(ctx, address, state)
}

func (this *cacheCore[K, V]) beginInvalidation(ctx context.Context, address Address) (*addressState, error) {
	state := this.acquireState(address)
	for {
		this.coord.mu.Lock()
		if ctx.Err() != nil {
			this.coord.mu.Unlock()
			this.releaseState(address, state)
			return nil, failure("forget", ctx.Err())
		}
		if !state.invalidating {
			state.invalidating = true
			state.generation++
			state.stagedMutation = 0
			this.invalidateMemberLocked(state)
			this.signalStateLocked(state)
			this.coord.mu.Unlock()
			return state, nil
		}
		changed := state.changed
		this.coord.mu.Unlock()
		if err := this.waitCoordination(ctx, changed); err != nil {
			this.releaseState(address, state)
			return nil, failure("forget", err)
		}
	}
}

func (this *cacheCore[K, V]) completeInvalidation(ctx context.Context, address Address, state *addressState) error {
	this.waitForWrite(state)
	this.coord.mu.Lock()
	state.writeActive = true
	this.coord.activeWrites++
	this.coord.mu.Unlock()
	ended := false
	defer func() {
		if !ended {
			this.endInvalidation(address, state)
		}
	}()
	cleanupCtx, cancel, contextErr := this.cleanupContext()
	if contextErr != nil {
		return failure("forget", contextErr)
	}
	err := backendDelete(this.backend, cleanupCtx, address)
	cancel()
	this.endInvalidation(address, state)
	ended = true
	if err == nil {
		this.observe(ctx, Event{Operation: ForgetOperation, Outcome: DeletedOutcome, Items: 1})
		return nil
	}
	this.observe(ctx, Event{Operation: ForgetOperation, Outcome: ErrorOutcome, Reason: BackendReason, Items: 1})
	if this.policy.InvalidateFailure == Ignore {
		return nil
	}
	return failure("forget", err)
}

func (this *cacheCore[K, V]) endInvalidation(address Address, state *addressState) {
	this.coord.mu.Lock()
	state.writeActive = false
	state.invalidating = false
	state.generation++
	this.coord.activeWrites--
	this.signalStateLocked(state)
	this.coord.mu.Unlock()
	this.releaseState(address, state)
}

func (this *cacheCore[K, V]) waitForWrite(state *addressState) {
	for {
		this.coord.mu.Lock()
		if !state.writeActive {
			this.coord.mu.Unlock()
			return
		}
		changed := state.changed
		this.coord.mu.Unlock()
		this.waitCoordinationSignal(changed)
	}
}

func (this *cacheCore[K, V]) invalidateMemberLocked(state *addressState) {
	if state.member == nil {
		return
	}
	member := state.member
	state.member = nil
	member.invalidated = true
	member.group.cancel()
}

func (this *cacheCore[K, V]) acquireState(address Address) *addressState {
	this.coord.mu.Lock()
	defer this.coord.mu.Unlock()
	state := this.coord.states[address]
	if state == nil {
		state = &addressState{changed: make(chan struct{})}
		this.coord.states[address] = state
	}
	state.refs++
	return state
}

func (this *cacheCore[K, V]) acquireStates(addresses []Address) []*addressState {
	this.coord.mu.Lock()
	defer this.coord.mu.Unlock()
	states := make([]*addressState, len(addresses))
	for index, address := range addresses {
		state := this.coord.states[address]
		if state == nil {
			state = &addressState{changed: make(chan struct{})}
			this.coord.states[address] = state
		}
		state.refs++
		states[index] = state
	}
	return states
}

func (this *cacheCore[K, V]) releaseState(address Address, state *addressState) {
	this.coord.mu.Lock()
	defer this.coord.mu.Unlock()
	if state.refs > 0 {
		state.refs--
	}
	this.cleanupStateLocked(address, state)
}

func (this *cacheCore[K, V]) releaseStates(addresses []Address, states []*addressState) {
	this.coord.mu.Lock()
	defer this.coord.mu.Unlock()
	for index, state := range states {
		if state.refs > 0 {
			state.refs--
		}
		this.cleanupStateLocked(addresses[index], state)
	}
}

func (this *cacheCore[K, V]) signalStateLocked(state *addressState) {
	close(state.changed)
	state.changed = make(chan struct{})
}

func waitChannel(ctx context.Context, changed <-chan struct{}) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-changed:
		return nil
	}
}

func (this *cacheCore[K, V]) waitCoordination(ctx context.Context, changed <-chan struct{}) error {
	this.coord.mu.Lock()
	this.coord.coordWaiters++
	this.coord.mu.Unlock()
	err := waitChannel(ctx, changed)
	this.coord.mu.Lock()
	this.coord.coordWaiters--
	this.coord.mu.Unlock()
	return err
}

func (this *cacheCore[K, V]) waitCoordinationSignal(changed <-chan struct{}) {
	this.coord.mu.Lock()
	this.coord.coordWaiters++
	this.coord.mu.Unlock()
	<-changed
	this.coord.mu.Lock()
	this.coord.coordWaiters--
	this.coord.mu.Unlock()
}
