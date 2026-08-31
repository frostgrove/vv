package cache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

type readTicket struct {
	state      *addressState
	generation uint64
}

type batchDecoded[V any] struct {
	results      []Result[V]
	encodedBytes int64
	payloadBytes int64
	reason       Reason
	err          error
}

func (this *Cache[K, V]) Lookup(ctx context.Context, key K) (Result[V], error) {
	core, err := this.core()
	if err != nil {
		return Result[V]{}, err
	}
	if nilInterface(ctx) {
		return Result[V]{}, failure("lookup", fmt.Errorf("%w: context is nil", ErrInvalid))
	}
	if err := ctx.Err(); err != nil {
		return Result[V]{}, failure("lookup", err)
	}
	if core.policy.disabled {
		return Result[V]{State: Miss}, nil
	}
	address, _, err := core.transientAddress(ctx, key, LookupOperation)
	if err != nil {
		return Result[V]{}, err
	}
	return core.lookupStable(ctx, address)
}

func (this *cacheCore[K, V]) lookupStable(ctx context.Context, address Address) (Result[V], error) {
	lease, err := this.transient.acquire(ctx, this.runtime.Clock, this.policy.TransientSaturation, this.transientPlan.lookupOperation)
	if err != nil {
		return Result[V]{}, failure("lookup", err)
	}
	defer lease.release()
	state := this.acquireState(address)
	defer this.releaseState(address, state)
	return this.lookupStableStateAdmitted(ctx, address, state)
}

func (this *cacheCore[K, V]) lookupStableStateAdmitted(ctx context.Context, address Address, state *addressState) (Result[V], error) {
	for {
		ticket, err := this.beginRead(ctx, state)
		if err != nil {
			return Result[V]{}, failure("lookup", err)
		}
		result, err := this.lookupAddressAdmitted(ctx, address)
		if !this.readCurrent(ticket) {
			continue
		}
		return result, err
	}
}

func (this *cacheCore[K, V]) lookupAddressAdmitted(ctx context.Context, address Address) (Result[V], error) {
	backendCtx, cancel, contextErr := this.backendContext(ctx)
	if contextErr != nil {
		this.observe(ctx, Event{Operation: LookupOperation, Outcome: ErrorOutcome, Reason: RuntimeReason, Items: 1})
		return Result[V]{}, failure("lookup", contextErr)
	}
	encoded, found, err := backendGet(this.backend, backendCtx, address, ReadLimit{MaxBytes: this.maxEnvelopeBytes})
	cancel()
	if err != nil {
		return this.readFailure(ctx, LookupOperation, err)
	}
	if !found {
		if len(encoded) != 0 {
			return this.corruptRead(ctx, LookupOperation)
		}
		this.observe(ctx, Event{Operation: LookupOperation, Outcome: MissOutcome, Items: 1})
		return Result[V]{State: Miss}, nil
	}
	if len(encoded) == 0 || len(encoded) > this.maxEnvelopeBytes {
		return this.corruptRead(ctx, LookupOperation)
	}
	result, payloadBytes, err := decodeEnvelope(encoded, this.runtime, this.values, this.valueDescriptor, this.policy)
	if err != nil {
		if !errors.Is(err, ErrCorrupt) {
			this.observe(ctx, Event{Operation: LookupOperation, Outcome: ErrorOutcome, Reason: RuntimeReason, Items: 1})
			return Result[V]{}, failure("lookup", err)
		}
		return this.corruptRead(ctx, LookupOperation)
	}
	this.observe(ctx, Event{
		Operation:    LookupOperation,
		Outcome:      outcomeForState(result.State),
		Items:        1,
		EncodedBytes: int64(len(encoded)),
		PayloadBytes: int64(payloadBytes),
	})
	return result, nil
}

func (this *cacheCore[K, V]) readFailure(ctx context.Context, operation Operation, err error) (Result[V], error) {
	if ctx.Err() != nil {
		return Result[V]{}, failure(string(operation), ctx.Err())
	}
	if errors.Is(err, ErrTooLarge) || errors.Is(err, ErrCorrupt) {
		return this.corruptRead(ctx, operation)
	}
	this.observe(ctx, Event{Operation: operation, Outcome: ErrorOutcome, Reason: BackendReason, Items: 1})
	if this.policy.ReadFailure == AsMiss {
		return Result[V]{State: Miss}, nil
	}
	return Result[V]{}, failure(string(operation), err)
}

func (this *cacheCore[K, V]) corruptRead(ctx context.Context, operation Operation) (Result[V], error) {
	this.observe(ctx, Event{Operation: operation, Outcome: ErrorOutcome, Reason: CorruptReason, Items: 1})
	if this.policy.Corruption == CorruptAsMiss {
		return Result[V]{State: Miss}, nil
	}
	return Result[V]{}, failure(string(operation), ErrCorrupt)
}

func (this *Cache[K, V]) LookupMany(ctx context.Context, keys []K) ([]Result[V], error) {
	core, err := this.core()
	if err != nil {
		return nil, err
	}
	if nilInterface(ctx) {
		return nil, failure("lookup many", fmt.Errorf("%w: context is nil", ErrInvalid))
	}
	if err := ctx.Err(); err != nil {
		return nil, failure("lookup many", err)
	}
	if len(keys) > core.policy.MaxBatchKeys {
		return nil, failure("lookup many", ErrTooLarge)
	}
	if len(keys) == 0 {
		return []Result[V]{}, nil
	}
	charge, err := core.transientPlan.batch(len(keys), int64(core.policy.MaxBatchResultBytes))
	if err != nil {
		return nil, failure("lookup many", err)
	}
	lease, err := core.transient.acquire(ctx, core.runtime.Clock, core.policy.TransientSaturation, charge)
	if err != nil {
		return nil, failure("lookup many", err)
	}
	defer lease.release()
	if core.policy.disabled {
		return misses[V](len(keys)), nil
	}
	addresses, unique, err := core.batchAddresses(ctx, keys)
	if err != nil {
		return nil, err
	}
	states := core.acquireStates(unique)
	defer core.releaseStates(unique, states)
	results := misses[V](len(keys))
	for {
		resetMisses(results)
		generations, err := core.beginBatchRead(ctx, states)
		if err != nil {
			return nil, failure("lookup many", err)
		}
		encoded, err := core.batchGet(ctx, unique)
		if !core.batchReadCurrent(states, generations) {
			continue
		}
		if err != nil {
			return nil, err
		}
		decoded := core.decodeBatch(ctx, addresses, encoded, results)
		current := core.batchReadCurrent(states, generations)
		if !current {
			continue
		}
		if decoded.err != nil {
			if decoded.reason != "" {
				core.observe(ctx, Event{Operation: LookupManyOperation, Outcome: ErrorOutcome, Reason: decoded.reason, Items: len(addresses)})
			}
			return nil, decoded.err
		}
		core.observe(ctx, Event{
			Operation:    LookupManyOperation,
			Outcome:      CompleteOutcome,
			Items:        len(addresses),
			EncodedBytes: decoded.encodedBytes,
			PayloadBytes: decoded.payloadBytes,
		})
		return decoded.results, nil
	}
}

func misses[V any](count int) []Result[V] {
	results := make([]Result[V], count)
	resetMisses(results)
	return results
}

func resetMisses[V any](results []Result[V]) {
	for index := range results {
		results[index] = Result[V]{State: Miss}
	}
}

func (this *cacheCore[K, V]) batchAddresses(ctx context.Context, keys []K) ([]Address, []Address, error) {
	addresses := make([]Address, len(keys))
	unique := make([]Address, 0, len(keys))
	seen := make(map[Address]struct{}, len(keys))
	total := 0
	for index, key := range keys {
		if err := ctx.Err(); err != nil {
			return nil, nil, failure("lookup many", err)
		}
		address, size, err := addressOf(this.scope, this.keys, this.keyVersion, key, this.policy.MaxKeyBytes)
		if err != nil {
			return nil, nil, err
		}
		if size > this.policy.MaxBatchKeyBytes-total {
			return nil, nil, failure("lookup many", ErrTooLarge)
		}
		total += size
		addresses[index] = address
		if _, ok := seen[address]; !ok {
			seen[address] = struct{}{}
			unique = append(unique, address)
		}
	}
	return addresses, unique, nil
}

func (this *cacheCore[K, V]) batchGet(ctx context.Context, addresses []Address) (map[Address][]byte, error) {
	limit := BatchReadLimit{
		MaxItems:      len(addresses),
		MaxItemBytes:  this.maxEnvelopeBytes,
		MaxTotalBytes: int64(this.policy.MaxBatchResultBytes),
	}
	if reader, ok := BatchReaderOf(this.backend); ok {
		backendCtx, cancel, contextErr := this.backendContext(ctx)
		if contextErr != nil {
			this.observe(ctx, Event{Operation: LookupManyOperation, Outcome: ErrorOutcome, Reason: RuntimeReason, Items: len(addresses)})
			return nil, failure("lookup many", contextErr)
		}
		encoded, err := backendGetMany(reader, backendCtx, append([]Address(nil), addresses...), limit)
		cancel()
		if err != nil {
			return this.batchReadFailure(ctx, addresses, err)
		}
		if err := validateBatchResponse(addresses, encoded, limit); err != nil {
			return this.batchReadFailure(ctx, addresses, err)
		}
		return encoded, nil
	}
	backendCtx, cancel, contextErr := this.backendContext(ctx)
	if contextErr != nil {
		this.observe(ctx, Event{Operation: LookupManyOperation, Outcome: ErrorOutcome, Reason: RuntimeReason, Items: len(addresses)})
		return nil, failure("lookup many", contextErr)
	}
	defer cancel()
	encoded := make(map[Address][]byte, len(addresses))
	var total int64
	for _, address := range addresses {
		value, found, err := backendGet(this.backend, backendCtx, address, ReadLimit{MaxBytes: limit.MaxItemBytes})
		if err != nil {
			return this.batchReadFailure(ctx, addresses, err)
		}
		if !found {
			if len(value) != 0 {
				return this.batchReadFailure(ctx, addresses, ErrCorrupt)
			}
			continue
		}
		if len(value) == 0 || len(value) > limit.MaxItemBytes {
			return this.batchReadFailure(ctx, addresses, ErrCorrupt)
		}
		if int64(len(value)) > limit.MaxTotalBytes-total {
			return this.batchReadFailure(ctx, addresses, ErrTooLarge)
		}
		total += int64(len(value))
		encoded[address] = value
	}
	return encoded, nil
}

func validateBatchResponse(addresses []Address, encoded map[Address][]byte, limit BatchReadLimit) error {
	if len(encoded) > limit.MaxItems {
		return ErrCorrupt
	}
	requested := make(map[Address]struct{}, len(addresses))
	for _, address := range addresses {
		requested[address] = struct{}{}
	}
	var total int64
	for address, value := range encoded {
		if _, ok := requested[address]; !ok || len(value) == 0 {
			return ErrCorrupt
		}
		if len(value) > limit.MaxItemBytes || int64(len(value)) > limit.MaxTotalBytes-total {
			return ErrCorrupt
		}
		total += int64(len(value))
	}
	return nil
}

func (this *cacheCore[K, V]) batchReadFailure(ctx context.Context, addresses []Address, err error) (map[Address][]byte, error) {
	if ctx.Err() != nil {
		return nil, failure("lookup many", ctx.Err())
	}
	if errors.Is(err, ErrTooLarge) {
		this.observe(ctx, Event{Operation: LookupManyOperation, Outcome: ErrorOutcome, Reason: LimitReason, Items: len(addresses)})
		return nil, failure("lookup many", ErrTooLarge)
	}
	if errors.Is(err, ErrCorrupt) {
		this.observe(ctx, Event{Operation: LookupManyOperation, Outcome: ErrorOutcome, Reason: CorruptReason, Items: len(addresses)})
		if this.policy.Corruption == CorruptAsMiss {
			return make(map[Address][]byte), nil
		}
		return nil, failure("lookup many", ErrCorrupt)
	}
	this.observe(ctx, Event{Operation: LookupManyOperation, Outcome: ErrorOutcome, Reason: BackendReason, Items: len(addresses)})
	if this.policy.ReadFailure == AsMiss {
		return make(map[Address][]byte), nil
	}
	return nil, failure("lookup many", err)
}

func (this *cacheCore[K, V]) decodeBatch(ctx context.Context, addresses []Address, encoded map[Address][]byte, results []Result[V]) batchDecoded[V] {
	var encodedTotal, payloadTotal, decodedTotal int64
	maximum := int64(this.policy.MaxBatchResultBytes)
	for index, address := range addresses {
		if err := ctx.Err(); err != nil {
			return batchDecoded[V]{err: failure("lookup many", err)}
		}
		raw, ok := encoded[address]
		if !ok {
			continue
		}
		if int64(len(raw)) > maximum-encodedTotal {
			return batchDecoded[V]{reason: LimitReason, err: failure("lookup many", ErrTooLarge)}
		}
		encodedTotal += int64(len(raw))
		var decodedBytes int64
		result, payloadBytes, err := decodeEnvelopeAccounting(bytes.Clone(raw), this.runtime, this.values, this.valueDescriptor, this.policy, &decodedBytes)
		if err != nil {
			if !errors.Is(err, ErrCorrupt) {
				return batchDecoded[V]{reason: RuntimeReason, err: failure("lookup many", err)}
			}
			if this.policy.Corruption == RefuseCorrupt {
				return batchDecoded[V]{reason: CorruptReason, err: failure("lookup many", ErrCorrupt)}
			}
			continue
		}
		if int64(payloadBytes) > maximum-payloadTotal {
			return batchDecoded[V]{reason: LimitReason, err: failure("lookup many", ErrTooLarge)}
		}
		if result.State == Hit || result.State == Stale {
			if decodedBytes > maximum-decodedTotal {
				return batchDecoded[V]{reason: LimitReason, err: failure("lookup many", ErrTooLarge)}
			}
			decodedTotal += decodedBytes
		}
		payloadTotal += int64(payloadBytes)
		results[index] = result
	}
	if err := ctx.Err(); err != nil {
		return batchDecoded[V]{err: failure("lookup many", err)}
	}
	return batchDecoded[V]{results: results, encodedBytes: encodedTotal, payloadBytes: payloadTotal}
}

func outcomeForState(state State) Outcome {
	switch state {
	case Hit:
		return HitOutcome
	case Miss:
		return MissOutcome
	case Negative:
		return NegativeOutcome
	case Stale:
		return StaleOutcome
	case Loaded:
		return LoadedOutcome
	default:
		return ErrorOutcome
	}
}

func (this *cacheCore[K, V]) beginRead(ctx context.Context, state *addressState) (readTicket, error) {
	for {
		this.coord.mu.Lock()
		if !state.writeActive && !state.invalidating {
			ticket := readTicket{state: state, generation: state.generation}
			this.coord.mu.Unlock()
			return ticket, nil
		}
		changed := state.changed
		this.coord.mu.Unlock()
		if err := this.waitCoordination(ctx, changed); err != nil {
			return readTicket{}, err
		}
	}
}

func (this *cacheCore[K, V]) readCurrent(ticket readTicket) bool {
	this.coord.mu.Lock()
	defer this.coord.mu.Unlock()
	return ticket.state.generation == ticket.generation && !ticket.state.writeActive && !ticket.state.invalidating
}

func (this *cacheCore[K, V]) beginBatchRead(ctx context.Context, states []*addressState) ([]uint64, error) {
	for {
		this.coord.mu.Lock()
		var changed <-chan struct{}
		for _, state := range states {
			if state.writeActive || state.invalidating {
				changed = state.changed
				break
			}
		}
		if changed == nil {
			generations := make([]uint64, len(states))
			for index, state := range states {
				generations[index] = state.generation
			}
			this.coord.mu.Unlock()
			return generations, nil
		}
		this.coord.mu.Unlock()
		if err := this.waitCoordination(ctx, changed); err != nil {
			return nil, err
		}
	}
}

func (this *cacheCore[K, V]) batchReadCurrent(states []*addressState, generations []uint64) bool {
	this.coord.mu.Lock()
	defer this.coord.mu.Unlock()
	for index, state := range states {
		if state.writeActive || state.invalidating || state.generation != generations[index] {
			return false
		}
	}
	return true
}
