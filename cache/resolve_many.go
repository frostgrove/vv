package cache

import (
	"context"
	"fmt"
)

type BatchLoader[K, V any] func(context.Context, []K) ([]LoadResult[V], error)

type missingPlan[K any] struct {
	keys      []K
	addresses []Address
	targets   [][]int
}

type pendingWrite[V any] struct {
	address Address
	encoded []byte
	expiry  Expiry
	state   State
	value   V
}

func (this *Cache[K, V]) ResolveMany(ctx context.Context, keys []K, load BatchLoader[K, V]) ([]Result[V], error) {
	core, err := this.core()
	if err != nil {
		return nil, err
	}
	if nilInterface(ctx) || load == nil {
		return nil, failure("resolve many", fmt.Errorf("%w: context and loader are required", ErrInvalid))
	}
	if err := ctx.Err(); err != nil {
		return nil, failure("resolve many", err)
	}
	if len(keys) > core.policy.MaxBatchKeys {
		return nil, failure("resolve many", ErrTooLarge)
	}
	if len(keys) == 0 {
		return []Result[V]{}, nil
	}
	if core.policy.disabled {
		return core.disabledLoadMany(ctx, keys, load)
	}
	results, err := this.LookupMany(ctx, keys)
	if err != nil {
		return nil, err
	}
	return core.fillMissing(ctx, keys, results, load)
}

func (this *cacheCore[K, V]) disabledLoadMany(ctx context.Context, keys []K, load BatchLoader[K, V]) ([]Result[V], error) {
	charge, err := this.transientPlan.batch(len(keys), int64(this.policy.MaxBatchResultBytes))
	if err != nil {
		return nil, failure("resolve many", err)
	}
	lease, err := this.transient.acquire(ctx, this.runtime.Clock, this.policy.TransientSaturation, charge)
	if err != nil {
		return nil, failure("resolve many", err)
	}
	defer lease.release()
	loaded, err := this.loadBatch(ctx, keys, load)
	if err != nil {
		this.observe(ctx, Event{Operation: LoadManyOperation, Outcome: ErrorOutcome, Items: len(keys)})
		return nil, err
	}
	results := make([]Result[V], len(keys))
	for index, result := range loaded {
		if result.Presence == CleanAbsent {
			results[index] = Result[V]{State: Negative}
			continue
		}
		results[index] = Result[V]{Value: result.Value, State: Loaded}
	}
	this.observe(ctx, Event{Operation: LoadManyOperation, Outcome: CompleteOutcome, Items: len(keys)})
	return results, nil
}

func (this *cacheCore[K, V]) fillMissing(ctx context.Context, keys []K, results []Result[V], load BatchLoader[K, V]) ([]Result[V], error) {
	charge, err := this.transientPlan.batch(len(keys), int64(this.policy.MaxBatchResultBytes))
	if err != nil {
		return nil, failure("resolve many", err)
	}
	lease, err := this.transient.acquire(ctx, this.runtime.Clock, this.policy.TransientSaturation, charge)
	if err != nil {
		return nil, failure("resolve many", err)
	}
	defer lease.release()
	plan, err := this.planMissing(ctx, keys, results)
	if err != nil {
		return nil, err
	}
	if len(plan.keys) == 0 {
		this.observe(ctx, Event{Operation: LoadManyOperation, Outcome: CompleteOutcome})
		return results, nil
	}
	loaded, err := this.loadBatch(ctx, plan.keys, load)
	if err != nil {
		this.observe(ctx, Event{Operation: LoadManyOperation, Outcome: ErrorOutcome, Items: len(plan.keys)})
		return nil, err
	}
	writes, encodedBytes, err := this.encodeLoaded(plan, loaded)
	if err != nil {
		this.observe(ctx, Event{Operation: LoadManyOperation, Outcome: ErrorOutcome, Reason: LimitReason, Items: len(plan.keys)})
		return nil, err
	}
	if err := this.commitLoaded(ctx, writes); err != nil {
		return nil, err
	}
	for index := range writes {
		filled := Result[V]{Value: writes[index].value, State: writes[index].state}
		for _, target := range plan.targets[index] {
			results[target] = filled
		}
	}
	this.observe(ctx, Event{
		Operation:    LoadManyOperation,
		Outcome:      CompleteOutcome,
		Items:        len(plan.keys),
		EncodedBytes: encodedBytes,
	})
	return results, nil
}

func (this *cacheCore[K, V]) planMissing(ctx context.Context, keys []K, results []Result[V]) (missingPlan[K], error) {
	plan := missingPlan[K]{}
	positions := make(map[Address]int, len(keys))
	total := 0
	for index, key := range keys {
		if err := ctx.Err(); err != nil {
			return missingPlan[K]{}, failure("resolve many", err)
		}
		if results[index].State != Miss {
			continue
		}
		address, size, err := addressOf(this.scope, this.keys, this.keyVersion, key, this.policy.MaxKeyBytes)
		if err != nil {
			return missingPlan[K]{}, err
		}
		if position, seen := positions[address]; seen {
			plan.targets[position] = append(plan.targets[position], index)
			continue
		}
		if size > this.policy.MaxBatchKeyBytes-total {
			return missingPlan[K]{}, failure("resolve many", ErrTooLarge)
		}
		total += size
		positions[address] = len(plan.keys)
		plan.keys = append(plan.keys, key)
		plan.addresses = append(plan.addresses, address)
		plan.targets = append(plan.targets, []int{index})
	}
	return plan, nil
}

func (this *cacheCore[K, V]) loadBatch(ctx context.Context, keys []K, load BatchLoader[K, V]) ([]LoadResult[V], error) {
	loaderCtx, cancel, err := this.loaderContext(valueBlindContext{Context: ctx})
	if err != nil {
		if callerErr := ctx.Err(); callerErr != nil {
			return nil, failure("resolve many", callerErr)
		}
		return nil, failure("load many", err)
	}
	loaded, loadErr := invokeBatchLoader(loaderCtx, keys, load)
	loaderErr := loaderCtx.Err()
	cancel()
	if callerErr := ctx.Err(); callerErr != nil {
		return nil, failure("resolve many", callerErr)
	}
	if loadErr == nil && loaderErr != nil {
		loadErr = loaderErr
	}
	if loadErr != nil {
		return nil, &loaderFailure{cause: loadErr}
	}
	if len(loaded) != len(keys) {
		return nil, failure("load many", fmt.Errorf("%w: batch loader answered %d of %d keys", ErrInvalid, len(loaded), len(keys)))
	}
	for _, result := range loaded {
		if result.Presence != Found && result.Presence != CleanAbsent {
			return nil, failure("load many", fmt.Errorf("%w: loader presence is invalid", ErrInvalid))
		}
	}
	return loaded, nil
}

func invokeBatchLoader[K, V any](ctx context.Context, keys []K, load BatchLoader[K, V]) (results []LoadResult[V], err error) {
	defer func() {
		if recover() != nil {
			results = nil
			err = fmt.Errorf("batch loader panicked")
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return load(ctx, append([]K(nil), keys...))
}

func (this *cacheCore[K, V]) encodeLoaded(plan missingPlan[K], loaded []LoadResult[V]) ([]pendingWrite[V], int64, error) {
	writes := make([]pendingWrite[V], 0, len(loaded))
	maximum := int64(this.policy.MaxBatchResultBytes)
	total := int64(0)
	for index, result := range loaded {
		write := pendingWrite[V]{address: plan.addresses[index], state: Loaded, value: result.Value}
		if result.Presence == CleanAbsent {
			write.state = Negative
			write.value = *new(V)
			if this.policy.Negative.duration <= 0 {
				writes = append(writes, write)
				continue
			}
		}
		encoded, _, expiry, err := encodeEnvelope(this.runtime, this.values, this.valueDescriptor, this.policy, result)
		if err != nil {
			return nil, 0, err
		}
		if int64(len(encoded)) > maximum-total {
			return nil, 0, failure("resolve many", ErrTooLarge)
		}
		total += int64(len(encoded))
		write.encoded = encoded
		write.expiry = expiry
		writes = append(writes, write)
	}
	return writes, total, nil
}

func (this *cacheCore[K, V]) commitLoaded(ctx context.Context, writes []pendingWrite[V]) error {
	for index := range writes {
		if len(writes[index].encoded) == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return failure("resolve many", err)
		}
		this.forgetMemoized(ctx, writes[index].address)
		staged, err := this.beginMutationAs(ctx, writes[index].address, LoadManyOperation)
		if err != nil {
			return err
		}
		err = this.commitMutationAs(ctx, staged, writes[index].encoded, writes[index].expiry, LoadManyOperation)
		if err != nil && !safeErrorIs(err, errSuperseded) {
			return err
		}
	}
	return nil
}
