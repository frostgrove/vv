package auditcrudbridge

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
)

const maxValues = 256

var (
	errInvalid  = errors.New("auditcrudbridge: invalid value")
	errTooLarge = errors.New("auditcrudbridge: value exceeds a bound")
	errConsumed = errors.New("auditcrudbridge: value is already consumed")
	errProtocol = errors.New("auditcrudbridge: runner protocol was refused")
)

type Spec struct{ value spec }

type Batch struct{ value batch }

type Guard struct{ value guard }

type Carrier struct{ value carrier }

type Mutation func(context.Context) (Batch, error)

type Runner func(context.Context, Mutation) error

type Prepare func(context.Context, Spec) (Runner, error)

type spec struct{ cell *specCell }

type batch struct{ cell *batchCell }

type guard struct{ cell *guardCell }

type carrier struct{ cell *carrierCell }

type specCell struct {
	mu               sync.Mutex
	state            uint8
	policy           any
	operation        any
	subjects         []any
	generatedSubject bool
	guard            *guardCell
	carrier          *carrierCell
}

type batchCell struct {
	mu     sync.Mutex
	state  uint8
	values []any
	guard  *guardCell
}

type guardCell struct {
	state      atomic.Uint32
	runner     Runner
	carrier    *carrierCell
	owner      *Carrier
	recoveryMu sync.Mutex
	recovery   any
}

type carrierCell struct {
	mu      sync.Mutex
	prepare Prepare
	owner   *Carrier
}

type carrierAccess struct {
	cell  *carrierCell
	owner *Carrier
}

type carrierSource interface {
	auditCRUDCarrier() carrierAccess
}

const (
	specFresh uint8 = iota + 1
	specBound
	specInspected
	specConsumed
)

const (
	batchFresh uint8 = iota + 1
	batchBound
	batchInspected
	batchConsumed
)

const (
	guardReady uint32 = iota + 1
	guardRunning
	guardConsumed
)

const (
	invocationOpen uint32 = iota
	invocationRunning
	invocationReturned
	invocationSealed
)

func NewSpec(policy any, operation any, subjects []any, generatedSubject bool) (Spec, error) {
	if nilByAnyRoute(policy) || nilByAnyRoute(operation) {
		return Spec{}, errInvalid
	}
	if len(subjects) > maxValues {
		return Spec{}, errTooLarge
	}
	if generatedSubject && len(subjects) != 0 || !generatedSubject && len(subjects) == 0 {
		return Spec{}, errInvalid
	}
	owned := append([]any(nil), subjects...)
	for _, subject := range owned {
		if nilByAnyRoute(subject) {
			return Spec{}, errInvalid
		}
	}
	return Spec{value: spec{cell: &specCell{
		state:            specFresh,
		policy:           policy,
		operation:        operation,
		subjects:         owned,
		generatedSubject: generatedSubject,
	}}}, nil
}

func NewBatch(values ...any) (Batch, error) {
	if len(values) > maxValues {
		return Batch{}, errTooLarge
	}
	owned := append([]any(nil), values...)
	for _, value := range owned {
		if nilByAnyRoute(value) {
			return Batch{}, errInvalid
		}
	}
	return Batch{value: batch{cell: &batchCell{state: batchFresh, values: owned}}}, nil
}

func NewCarrier(prepare Prepare) Carrier {
	if prepare == nil {
		return Carrier{}
	}
	return Carrier{value: carrier{cell: &carrierCell{prepare: prepare}}}
}

func Preflight(ctx context.Context, recorder any, value Spec) (Guard, error) {
	if nilByAnyRoute(ctx) || nilByAnyRoute(recorder) {
		return Guard{}, errInvalid
	}
	if err := ctx.Err(); err != nil {
		return Guard{}, err
	}
	cell := value.value.cell
	if cell == nil || !cell.available() {
		return Guard{}, errInvalid
	}
	access, ok := carrierAccessOf(recorder)
	if !ok || !access.claim() {
		return Guard{}, errInvalid
	}
	bound := &guardCell{carrier: access.cell, owner: access.owner}
	bound.state.Store(guardReady)
	if !cell.bind(bound, access.cell) {
		bound.state.Store(guardConsumed)
		return Guard{}, errConsumed
	}
	runner, err, inspected := prepare(access.cell.prepare, ctx, value, cell)
	if err != nil {
		bound.state.Store(guardConsumed)
		return Guard{}, err
	}
	if contextErr := ctx.Err(); contextErr != nil {
		bound.state.Store(guardConsumed)
		return Guard{}, contextErr
	}
	if runner == nil || !inspected {
		bound.state.Store(guardConsumed)
		return Guard{}, errProtocol
	}
	bound.runner = runner
	return Guard{value: guard{cell: bound}}, nil
}

func (value Guard) Run(ctx context.Context, mutation Mutation) error {
	cell := value.value.cell
	if cell == nil || nilByAnyRoute(ctx) || mutation == nil {
		return errInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !cell.state.CompareAndSwap(guardReady, guardRunning) {
		return errConsumed
	}
	defer cell.state.Store(guardConsumed)
	if !cell.carrier.ownedBy(cell.owner) || cell.runner == nil {
		return errConsumed
	}
	call := newInvocation(cell, mutation)
	runnerErr := cell.runner(ctx, call.invoke)
	if report, ok := runnerErr.(*runnerReport); ok && report != nil {
		cell.recoveryMu.Lock()
		cell.recovery = report.recovery
		cell.recoveryMu.Unlock()
		runnerErr = report.err
	}
	result := call.finish()
	if result.batch != nil {
		result.inspected = result.batch.consume(cell)
	}
	if result.violation != nil {
		return result.violation
	}
	if runnerErr != nil {
		return runnerErr
	}
	if !result.called {
		return errProtocol
	}
	if result.mutationErr != nil {
		return result.mutationErr
	}
	if !result.inspected {
		return errProtocol
	}
	return nil
}

func InspectSpec(value Spec) (policy, operation any, subjects []any, generatedSubject bool, ok bool) {
	cell := value.value.cell
	if cell == nil {
		return nil, nil, nil, false, false
	}
	cell.mu.Lock()
	defer cell.mu.Unlock()
	if cell.state != specBound || cell.guard == nil || cell.carrier == nil || cell.guard.state.Load() != guardReady || cell.guard.carrier != cell.carrier {
		return nil, nil, nil, false, false
	}
	cell.state = specInspected
	return cell.policy, cell.operation, append([]any(nil), cell.subjects...), cell.generatedSubject, true
}

func InspectBatch(value Batch) ([]any, bool) {
	cell := value.value.cell
	if cell == nil {
		return nil, false
	}
	cell.mu.Lock()
	defer cell.mu.Unlock()
	if cell.state != batchBound || cell.guard == nil || cell.guard.state.Load() != guardRunning {
		return nil, false
	}
	cell.state = batchInspected
	return append([]any(nil), cell.values...), true
}

func (value *Carrier) auditCRUDCarrier() carrierAccess {
	if value == nil {
		return carrierAccess{}
	}
	return carrierAccess{cell: value.value.cell, owner: value}
}

func (access carrierAccess) claim() bool {
	if access.cell == nil || access.owner == nil || access.cell.prepare == nil || access.owner.value.cell != access.cell {
		return false
	}
	access.cell.mu.Lock()
	defer access.cell.mu.Unlock()
	if access.cell.owner == nil {
		access.cell.owner = access.owner
	}
	return access.cell.owner == access.owner
}

func (cell *carrierCell) ownedBy(owner *Carrier) bool {
	if cell == nil || owner == nil || owner.value.cell != cell {
		return false
	}
	cell.mu.Lock()
	defer cell.mu.Unlock()
	return cell.owner == owner && cell.prepare != nil
}

func (cell *specCell) bind(guard *guardCell, carrier *carrierCell) bool {
	cell.mu.Lock()
	defer cell.mu.Unlock()
	if cell.state != specFresh || guard == nil || carrier == nil {
		return false
	}
	cell.guard = guard
	cell.carrier = carrier
	cell.state = specBound
	return true
}

func (cell *specCell) available() bool {
	cell.mu.Lock()
	defer cell.mu.Unlock()
	return cell.state == specFresh && cell.policy != nil && cell.operation != nil
}

func (cell *specCell) finish() bool {
	cell.mu.Lock()
	defer cell.mu.Unlock()
	inspected := cell.state == specInspected
	cell.state = specConsumed
	cell.policy = nil
	cell.operation = nil
	cell.subjects = nil
	cell.guard = nil
	cell.carrier = nil
	return inspected
}

func (cell *batchCell) bind(guard *guardCell) bool {
	cell.mu.Lock()
	defer cell.mu.Unlock()
	if cell.state != batchFresh || guard == nil || guard.state.Load() != guardRunning {
		return false
	}
	cell.guard = guard
	cell.state = batchBound
	return true
}

func (cell *batchCell) reject() {
	cell.mu.Lock()
	defer cell.mu.Unlock()
	if cell.state == batchFresh {
		cell.state = batchConsumed
		cell.values = nil
	}
}

func (cell *batchCell) consume(guard *guardCell) bool {
	cell.mu.Lock()
	defer cell.mu.Unlock()
	inspected := cell.state == batchInspected && cell.guard == guard
	cell.state = batchConsumed
	cell.values = nil
	cell.guard = nil
	return inspected
}

func prepare(callback Prepare, ctx context.Context, value Spec, cell *specCell) (runner Runner, err error, inspected bool) {
	defer func() { inspected = cell.finish() }()
	runner, err = callback(ctx, value)
	return runner, err, false
}

func carrierAccessOf(recorder any) (access carrierAccess, ok bool) {
	defer func() {
		if recover() != nil {
			access = carrierAccess{}
			ok = false
		}
	}()
	source, ok := recorder.(carrierSource)
	if !ok {
		return carrierAccess{}, false
	}
	access = source.auditCRUDCarrier()
	return access, access.cell != nil && access.owner != nil
}

func nilByAnyRoute(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

type invocation struct {
	state     atomic.Uint32
	done      chan struct{}
	guard     *guardCell
	mutation  Mutation
	mu        sync.Mutex
	violation error
	result    invocationResult
}

type invocationResult struct {
	called      bool
	inspected   bool
	batch       *batchCell
	mutationErr error
	violation   error
}

type runnerReport struct {
	recovery any
	err      error
}

func (report *runnerReport) Error() string {
	if report.err != nil {
		return report.err.Error()
	}
	return "auditcrudbridge: runner reported recovery"
}

func (report *runnerReport) Is(target error) bool {
	return errors.Is(report.err, target)
}

func ReportRecovery(recovery any, err error) error {
	if nilByAnyRoute(recovery) {
		return err
	}
	return &runnerReport{recovery: recovery, err: err}
}

func (value Guard) TakeRecovery() (any, bool) {
	cell := value.value.cell
	if cell == nil {
		return nil, false
	}
	cell.recoveryMu.Lock()
	defer cell.recoveryMu.Unlock()
	recovery := cell.recovery
	cell.recovery = nil
	return recovery, !nilByAnyRoute(recovery)
}

func newInvocation(guard *guardCell, mutation Mutation) *invocation {
	call := &invocation{done: make(chan struct{}), guard: guard, mutation: mutation}
	call.state.Store(invocationOpen)
	return call
}

func (call *invocation) invoke(ctx context.Context) (returned Batch, returnedErr error) {
	if !call.state.CompareAndSwap(invocationOpen, invocationRunning) {
		call.refuse(errConsumed)
		return Batch{}, errConsumed
	}
	defer func() {
		call.state.Store(invocationReturned)
		close(call.done)
	}()
	if nilByAnyRoute(ctx) {
		call.refuse(errInvalid)
		return Batch{}, errInvalid
	}
	if err := ctx.Err(); err != nil {
		call.storeResult(nil, err)
		return Batch{}, err
	}
	returned, returnedErr = call.mutation(ctx)
	batchCell := returned.value.cell
	if returnedErr != nil {
		if batchCell != nil {
			batchCell.reject()
			call.refuse(errProtocol)
			return Batch{}, errProtocol
		}
		call.storeResult(nil, returnedErr)
		return Batch{}, returnedErr
	}
	if batchCell == nil || !batchCell.bind(call.guard) {
		if batchCell != nil {
			batchCell.reject()
		}
		call.refuse(errConsumed)
		return Batch{}, errConsumed
	}
	call.storeResult(batchCell, nil)
	return returned, nil
}

func (call *invocation) finish() invocationResult {
	for {
		switch call.state.Load() {
		case invocationOpen:
			if call.state.CompareAndSwap(invocationOpen, invocationSealed) {
				call.mu.Lock()
				result := call.result
				result.violation = call.violation
				call.mu.Unlock()
				return result
			}
		case invocationRunning:
			<-call.done
		case invocationReturned:
			if call.state.CompareAndSwap(invocationReturned, invocationSealed) {
				call.mu.Lock()
				result := call.result
				result.violation = call.violation
				call.mu.Unlock()
				return result
			}
		case invocationSealed:
			call.mu.Lock()
			result := call.result
			result.violation = call.violation
			call.mu.Unlock()
			return result
		}
	}
}

func (call *invocation) storeResult(batch *batchCell, err error) {
	call.mu.Lock()
	call.result.called = true
	call.result.batch = batch
	call.result.mutationErr = err
	call.mu.Unlock()
}

func (call *invocation) refuse(err error) {
	call.mu.Lock()
	if call.violation == nil {
		call.violation = err
	}
	call.result.called = true
	call.mu.Unlock()
}
