package cache

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
)

var errTransientStateChanged = errors.New("cache transient admission state changed")

const (
	transientHashCharge             = int64(256)
	transientBatchFixedCharge       = int64(512)
	transientBatchEntryCharge       = int64(512)
	transientInitialStackCharge     = int64(2 << 10)
	transientStateRuntimeCharge     = int64(4 << 10)
	transientTimedRuntimeCharge     = int64(8 << 10)
	transientOperationRuntimeCharge = transientStateRuntimeCharge + transientTimedRuntimeCharge
	transientAdmissionSlotCharge    = int64(8 << 10)
	transientFlightRuntimeCharge    = int64(12 << 10)
	transientFlightFixedCharge      = transientFlightRuntimeCharge + 2*transientInitialStackCharge + transientOperationRuntimeCharge
	transientWaiterFixedCharge      = 2*transientInitialStackCharge + transientOperationRuntimeCharge
)

type transientPlan struct {
	key                    int64
	lookup                 int64
	batchItem              int64
	batchValue             int64
	batchScratch           int64
	batchStructuralPerItem int64
	encode                 int64
	lookupOperation        int64
	putOperation           int64
	forgetOperation        int64
	build                  int64
	waiter                 int64
	fallback               int64
	retained               int64
	background             int64
	resolve                int64
	reserved               int64
	minimum                int64
}

type transientBudget struct {
	mu      sync.Mutex
	limit   int64
	reserve int64
	used    int64
	waiters int
	changed chan struct{}
	slots   chan struct{}
}

type transientAdmission struct {
	timer Timer
	slot  bool
}

type transientLease struct {
	mu        sync.Mutex
	budget    *transientBudget
	remaining int64
}

func newTransientBudget(limit, reserve int64, slots int) *transientBudget {
	return &transientBudget{limit: limit, reserve: reserve, changed: make(chan struct{}), slots: make(chan struct{}, slots)}
}

func transientPlanFor(policy Policy) (transientPlan, error) {
	envelopeBytes, err := maxEnvelopeBytes(policy)
	if err != nil {
		return transientPlan{}, err
	}
	envelope := int64(envelopeBytes)
	key := int64(policy.MaxKeyBytes)
	value := int64(policy.MaxValueBytes)
	batch := int64(policy.MaxBatchResultBytes)
	twoKeys, ok := addTransientBytes(key, key)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient key charge overflows", ErrTooLarge)
	}
	keyCharge, ok := addTransientBytes(twoKeys, transientHashCharge)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient key hash charge overflows", ErrTooLarge)
	}
	twoValues, ok := addTransientBytes(value, value)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient codec charge overflows", ErrTooLarge)
	}
	decodeCopies, ok := addTransientBytes(twoValues, value)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient decode charge overflows", ErrTooLarge)
	}
	decodeCopies, ok = addTransientBytes(decodeCopies, value)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient decode charge overflows", ErrTooLarge)
	}
	lookup, ok := addTransientBytes(envelope, decodeCopies)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient lookup charge overflows", ErrTooLarge)
	}
	lookup, ok = addTransientBytes(lookup, jsonSafeRuntimeBytes)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient codec runtime charge overflows", ErrTooLarge)
	}
	twoEnvelopes, ok := addTransientBytes(envelope, envelope)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient envelope handoff charge overflows", ErrTooLarge)
	}
	encode, ok := addTransientBytes(twoEnvelopes, twoValues)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient encode charge overflows", ErrTooLarge)
	}
	encode, ok = addTransientBytes(encode, jsonSafeRuntimeBytes)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient codec runtime charge overflows", ErrTooLarge)
	}
	build, ok := addTransientBytes(encode, value)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient build charge overflows", ErrTooLarge)
	}
	build, ok = addTransientBytes(build, transientFlightFixedCharge)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient flight charge overflows", ErrTooLarge)
	}
	background, ok := addTransientBytes(transientWaiterFixedCharge, lookup)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient background charge overflows", ErrTooLarge)
	}
	resolve, ok := addTransientBytes(background, build)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient resolve charge overflows", ErrTooLarge)
	}
	retained, ok := addTransientBytes(transientWaiterFixedCharge, value)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient stale charge overflows", ErrTooLarge)
	}
	lookupOperation, ok := addTransientBytes(lookup, transientOperationRuntimeCharge)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient lookup operation charge overflows", ErrTooLarge)
	}
	putOperation, ok := addTransientBytes(encode, transientOperationRuntimeCharge)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient put operation charge overflows", ErrTooLarge)
	}
	reserved := int64(0)
	if policy.TransientSaturation.mode == WaitForTransientMode {
		reserved, ok = multiplyTransientBytes(int64(policy.MaxTransientWaiters), transientAdmissionSlotCharge)
		if !ok {
			return transientPlan{}, fmt.Errorf("%w: transient admission reservation overflows", ErrTooLarge)
		}
	}
	batchScratch, ok := addTransientBytes(decodeCopies, jsonSafeRuntimeBytes)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient codec runtime charge overflows", ErrTooLarge)
	}
	plan := transientPlan{
		key:             keyCharge,
		lookup:          lookup,
		batchItem:       envelope,
		batchValue:      value,
		batchScratch:    batchScratch,
		encode:          encode,
		lookupOperation: lookupOperation,
		putOperation:    putOperation,
		forgetOperation: transientOperationRuntimeCharge,
		build:           build,
		waiter:          transientWaiterFixedCharge,
		fallback:        value,
		retained:        retained,
		background:      background,
		resolve:         resolve,
		reserved:        reserved,
		minimum:         maxTransientBytes(keyCharge, lookupOperation, putOperation, transientOperationRuntimeCharge, build, transientWaiterFixedCharge, background, resolve),
	}
	batchMaximum, err := plan.batch(policy.MaxBatchKeys, batch)
	if err != nil {
		return transientPlan{}, fmt.Errorf("%w: transient batch charge overflows", err)
	}
	flightMaximum, err := plan.flightCapacity(policy.MaxFlights)
	if err != nil {
		return transientPlan{}, fmt.Errorf("%w: transient flight capacity charge overflows", err)
	}
	plan.minimum = maxTransientBytes(plan.minimum, batchMaximum, flightMaximum)
	plan.minimum, ok = addTransientBytes(plan.minimum, plan.reserved)
	if !ok {
		return transientPlan{}, fmt.Errorf("%w: transient minimum overflows", ErrTooLarge)
	}
	return plan, nil
}

func (this transientPlan) batch(items int, maximum int64) (int64, error) {
	if items < 0 || maximum <= 0 {
		return 0, ErrInvalid
	}
	itemCount := int64(items)
	itemsBytes, ok := multiplyTransientBytes(itemCount, this.batchItem)
	if !ok {
		return 0, ErrTooLarge
	}
	if itemsBytes > maximum {
		itemsBytes = maximum
	}
	decodedBytes, ok := multiplyTransientBytes(itemCount, this.batchValue)
	if !ok {
		return 0, ErrTooLarge
	}
	if decodedBytes > maximum {
		decodedBytes = maximum
	}
	structural, ok := multiplyTransientBytes(itemCount, this.batchStructuralPerItem)
	if !ok {
		return 0, ErrTooLarge
	}
	charge, ok := addTransientBytes(this.key, transientBatchFixedCharge+transientTimedRuntimeCharge)
	if !ok {
		return 0, ErrTooLarge
	}
	for _, value := range []int64{structural, itemsBytes, decodedBytes, this.batchScratch} {
		charge, ok = addTransientBytes(charge, value)
		if !ok {
			return 0, ErrTooLarge
		}
	}
	return charge, nil
}

func (this transientPlan) flightCapacity(maxFlights int) (int64, error) {
	if maxFlights <= 0 {
		return 0, ErrInvalid
	}
	owner, ok := addTransientBytes(this.build, max(this.waiter, this.retained))
	if !ok {
		return 0, ErrTooLarge
	}
	steady, ok := multiplyTransientBytes(int64(maxFlights), owner)
	if !ok {
		return 0, ErrTooLarge
	}
	prior, ok := multiplyTransientBytes(int64(maxFlights-1), owner)
	if !ok {
		return 0, ErrTooLarge
	}
	admission, ok := addTransientBytes(prior, this.resolve)
	if !ok {
		return 0, ErrTooLarge
	}
	decision, ok := addTransientBytes(steady, this.background)
	if !ok {
		return 0, ErrTooLarge
	}
	return max(steady, admission, decision), nil
}

func typedTransientPlan[K, V any](policy Policy, plan transientPlan) (transientPlan, error) {
	keyBytes, err := transientTypeBytes[K]()
	if err != nil {
		return transientPlan{}, err
	}
	valueBytes, err := transientTypeBytes[V]()
	if err != nil {
		return transientPlan{}, err
	}
	if policy.TransientSaturation.mode == WaitForTransientMode {
		admissionValue, ok := addTransientBytes(transientAdmissionSlotCharge, keyBytes)
		if !ok {
			return transientPlan{}, ErrTooLarge
		}
		admissionValue, ok = addTransientBytes(admissionValue, valueBytes)
		if !ok {
			return transientPlan{}, ErrTooLarge
		}
		plan.reserved, ok = multiplyTransientBytes(int64(policy.MaxTransientWaiters), admissionValue)
		if !ok {
			return transientPlan{}, ErrTooLarge
		}
	}
	loadResultBytes, err := transientTypeBytes[LoadResult[V]]()
	if err != nil {
		return transientPlan{}, err
	}
	addressBytes, err := transientTypeBytes[Address]()
	if err != nil {
		return transientPlan{}, err
	}
	resultBytes, err := transientTypeBytes[Result[V]]()
	if err != nil {
		return transientPlan{}, err
	}
	sliceBytes, err := transientTypeBytes[[]byte]()
	if err != nil {
		return transientPlan{}, err
	}
	pointerBytes, err := transientTypeBytes[*addressState]()
	if err != nil {
		return transientPlan{}, err
	}
	addresses, ok := multiplyTransientBytes(addressBytes, 5)
	if !ok {
		return transientPlan{}, ErrTooLarge
	}
	perItem := resultBytes
	for _, value := range []int64{addresses, sliceBytes, pointerBytes, 8, transientBatchEntryCharge, transientStateRuntimeCharge} {
		perItem, ok = addTransientBytes(perItem, value)
		if !ok {
			return transientPlan{}, ErrTooLarge
		}
	}
	plan.key, ok = addTransientBytes(plan.key, keyBytes)
	if !ok {
		return transientPlan{}, ErrTooLarge
	}
	for _, value := range []int64{keyBytes, resultBytes} {
		plan.lookup, ok = addTransientBytes(plan.lookup, value)
		if !ok {
			return transientPlan{}, ErrTooLarge
		}
	}
	plan.lookupOperation, ok = addTransientBytes(plan.lookup, transientOperationRuntimeCharge)
	if !ok {
		return transientPlan{}, ErrTooLarge
	}
	plan.fallback, ok = addTransientBytes(plan.fallback, resultBytes)
	if !ok {
		return transientPlan{}, ErrTooLarge
	}
	for _, value := range []int64{keyBytes, valueBytes} {
		plan.encode, ok = addTransientBytes(plan.encode, value)
		if !ok {
			return transientPlan{}, ErrTooLarge
		}
	}
	plan.putOperation, ok = addTransientBytes(plan.encode, transientOperationRuntimeCharge)
	if !ok {
		return transientPlan{}, ErrTooLarge
	}
	plan.forgetOperation, ok = addTransientBytes(plan.forgetOperation, keyBytes)
	if !ok {
		return transientPlan{}, ErrTooLarge
	}
	for _, value := range []int64{valueBytes, loadResultBytes, keyBytes} {
		plan.build, ok = addTransientBytes(plan.build, value)
		if !ok {
			return transientPlan{}, ErrTooLarge
		}
	}
	for _, value := range []int64{keyBytes, resultBytes} {
		plan.waiter, ok = addTransientBytes(plan.waiter, value)
		if !ok {
			return transientPlan{}, ErrTooLarge
		}
	}
	plan.background, ok = addTransientBytes(plan.waiter, plan.lookup)
	if !ok {
		return transientPlan{}, ErrTooLarge
	}
	plan.resolve, ok = addTransientBytes(plan.background, plan.build)
	if !ok {
		return transientPlan{}, ErrTooLarge
	}
	plan.retained, ok = addTransientBytes(plan.waiter, plan.fallback)
	if !ok {
		return transientPlan{}, ErrTooLarge
	}
	plan.batchStructuralPerItem = perItem
	batchMaximum, err := plan.batch(policy.MaxBatchKeys, int64(policy.MaxBatchResultBytes))
	if err != nil {
		return transientPlan{}, err
	}
	flightMaximum, err := plan.flightCapacity(policy.MaxFlights)
	if err != nil {
		return transientPlan{}, err
	}
	plan.minimum = maxTransientBytes(plan.key, plan.lookupOperation, plan.putOperation, plan.forgetOperation, plan.build, plan.waiter, plan.background, plan.resolve, batchMaximum, flightMaximum)
	plan.minimum, ok = addTransientBytes(plan.minimum, plan.reserved)
	if !ok {
		return transientPlan{}, ErrTooLarge
	}
	return plan, nil
}

func transientTypeBytes[T any]() (int64, error) {
	size := reflect.TypeFor[T]().Size()
	if uint64(size) > math.MaxInt64 {
		return 0, ErrTooLarge
	}
	return int64(size), nil
}

func (this *transientBudget) tryAcquire(weight int64) (*transientLease, bool) {
	if this == nil || weight <= 0 {
		return nil, false
	}
	this.mu.Lock()
	defer this.mu.Unlock()
	available := this.limit - this.reserve
	if available < 0 || weight > available || this.used > available-weight {
		return nil, false
	}
	this.used += weight
	return &transientLease{budget: this, remaining: weight}, true
}

func (this *transientBudget) acquire(ctx context.Context, clock Clock, policy TransientSaturationPolicy, weight int64) (*transientLease, error) {
	return this.acquireUntil(ctx, clock, policy, weight, nil)
}

func (this *transientBudget) acquireUntil(ctx context.Context, clock Clock, policy TransientSaturationPolicy, weight int64, wake <-chan struct{}) (*transientLease, error) {
	admission := transientAdmission{}
	defer admission.close(this)
	return this.acquireUntilAdmission(ctx, clock, policy, weight, wake, &admission)
}

func (this *transientBudget) acquireUntilAdmission(ctx context.Context, clock Clock, policy TransientSaturationPolicy, weight int64, wake <-chan struct{}, admission *transientAdmission) (*transientLease, error) {
	if this == nil || nilInterface(ctx) || nilInterface(clock) || weight <= 0 {
		return nil, ErrInvalid
	}
	if admission == nil {
		return nil, ErrInvalid
	}
	available := this.limit - this.reserve
	if available < 0 || weight > available {
		return nil, ErrTooLarge
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if admission.timer != nil {
			select {
			case <-admission.timer.C():
				return nil, ErrSaturated
			default:
			}
		}
		this.mu.Lock()
		if admission.timer != nil {
			select {
			case <-admission.timer.C():
				this.mu.Unlock()
				return nil, ErrSaturated
			default:
			}
		}
		if this.used <= available-weight {
			this.used += weight
			this.mu.Unlock()
			lease := &transientLease{budget: this, remaining: weight}
			if err := ctx.Err(); err != nil {
				lease.release()
				return nil, err
			}
			if admission.timer != nil {
				select {
				case <-admission.timer.C():
					lease.release()
					return nil, ErrSaturated
				default:
				}
			}
			return lease, nil
		}
		if policy.mode != WaitForTransientMode {
			this.mu.Unlock()
			return nil, ErrSaturated
		}
		if !admission.slot {
			select {
			case this.slots <- struct{}{}:
				admission.slot = true
			default:
				this.mu.Unlock()
				return nil, ErrSaturated
			}
		}
		if this.waiters == math.MaxInt {
			this.mu.Unlock()
			return nil, ErrSaturated
		}
		changed := this.changed
		this.mu.Unlock()
		if admission.timer == nil {
			var err error
			admission.timer, err = runtimeTimer(clock, policy.timeout)
			if err != nil {
				return nil, err
			}
		}
		this.mu.Lock()
		this.waiters++
		this.mu.Unlock()
		var err error
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-admission.timer.C():
			err = ErrSaturated
		case <-wake:
			err = errTransientStateChanged
		case <-changed:
		}
		this.mu.Lock()
		this.waiters--
		this.mu.Unlock()
		if err != nil {
			return nil, err
		}
	}
}

func (this *transientAdmission) close(budget *transientBudget) {
	if this == nil {
		return
	}
	stopTimer(this.timer)
	this.timer = nil
	if this.slot && budget != nil {
		<-budget.slots
		this.slot = false
	}
}

func (this *transientBudget) release(weight int64) {
	if this == nil || weight <= 0 {
		return
	}
	this.mu.Lock()
	if weight > this.used {
		this.mu.Unlock()
		return
	}
	this.used -= weight
	close(this.changed)
	this.changed = make(chan struct{})
	this.mu.Unlock()
}

func (this *transientBudget) snapshot() (int64, int) {
	if this == nil {
		return 0, 0
	}
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.used, this.waiters
}

func (this *transientLease) release() {
	if this == nil {
		return
	}
	this.mu.Lock()
	weight := this.remaining
	this.remaining = 0
	budget := this.budget
	this.mu.Unlock()
	budget.release(weight)
}

func (this *transientLease) split(weight int64) (*transientLease, bool) {
	if this == nil || weight <= 0 {
		return nil, false
	}
	this.mu.Lock()
	defer this.mu.Unlock()
	if this.budget == nil || weight > this.remaining {
		return nil, false
	}
	this.remaining -= weight
	return &transientLease{budget: this.budget, remaining: weight}, true
}

func (this *transientLease) reduceTo(weight int64) bool {
	if this == nil || weight < 0 {
		return false
	}
	this.mu.Lock()
	if weight > this.remaining {
		this.mu.Unlock()
		return false
	}
	released := this.remaining - weight
	this.remaining = weight
	budget := this.budget
	this.mu.Unlock()
	budget.release(released)
	return true
}

func (this *transientLease) tryGrowTo(weight int64) bool {
	if this == nil || weight <= 0 {
		return false
	}
	this.mu.Lock()
	defer this.mu.Unlock()
	if this.budget == nil || weight < this.remaining {
		return false
	}
	delta := weight - this.remaining
	if delta == 0 {
		return true
	}
	this.budget.mu.Lock()
	defer this.budget.mu.Unlock()
	available := this.budget.limit - this.budget.reserve
	if available < 0 || this.budget.used > available || delta > available-this.budget.used {
		return false
	}
	this.budget.used += delta
	this.remaining = weight
	return true
}

func (this *cacheCore[K, V]) transientAddress(ctx context.Context, key K, operation Operation) (Address, int, error) {
	lease, err := this.transient.acquire(ctx, this.runtime.Clock, this.policy.TransientSaturation, this.transientPlan.key)
	if err != nil {
		return Address{}, 0, failure(string(operation), err)
	}
	defer lease.release()
	return addressOf(this.scope, this.keys, this.keyVersion, key, this.policy.MaxKeyBytes)
}

func addTransientBytes(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || left > math.MaxInt64-right {
		return 0, false
	}
	return left + right, true
}

func multiplyTransientBytes(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || (left != 0 && right > math.MaxInt64/left) {
		return 0, false
	}
	return left * right, true
}

func maxTransientBytes(values ...int64) int64 {
	var maximum int64
	for _, value := range values {
		if value > maximum {
			maximum = value
		}
	}
	return maximum
}
