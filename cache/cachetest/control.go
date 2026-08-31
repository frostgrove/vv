package cachetest

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/frostgrove/vv/cache"
)

var ErrPauseCanceled = errors.New("cachetest: pause canceled")

type Operation string

const (
	GetOperation     Operation = "get"
	GetManyOperation Operation = "get_many"
	PutOperation     Operation = "put"
	DeleteOperation  Operation = "delete"
)

type Record struct {
	Operation  Operation
	Address    cache.Address
	Items      int
	ValueBytes int64
	Found      bool
	Failed     bool
}

type Pause struct {
	entered     chan struct{}
	canceled    chan struct{}
	release     chan struct{}
	enteredFlag atomic.Bool
	releaseOnce sync.Once
	cancelOnce  sync.Once
}

func (pause *Pause) Wait(ctx context.Context) error {
	if pause == nil {
		return fmt.Errorf("cachetest: pause is nil")
	}
	if ctx == nil {
		return fmt.Errorf("cachetest: context is nil")
	}
	select {
	case <-pause.entered:
		return nil
	case <-pause.canceled:
		return ErrPauseCanceled
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (pause *Pause) HasEntered() bool {
	return pause != nil && pause.enteredFlag.Load()
}

func (pause *Pause) Release() {
	if pause == nil {
		return
	}
	pause.releaseOnce.Do(func() { close(pause.release) })
}

func (pause *Pause) cancel() {
	pause.cancelOnce.Do(func() { close(pause.canceled) })
}

type Controller struct {
	next    cache.Backend
	wrapped cache.Backend
	batch   cache.BatchReader

	mu       sync.Mutex
	failures map[Operation][]error
	pauses   map[Operation][]*Pause
	active   map[*Pause]struct{}
	records  []Record
}

type controlledBackend struct {
	controller *Controller
}

type controlledBatchBackend struct {
	*controlledBackend
}

func NewController(next cache.Backend) (*Controller, error) {
	if nilValue(next) {
		return nil, fmt.Errorf("cachetest: backend is nil")
	}
	controller := &Controller{
		next:     next,
		failures: make(map[Operation][]error),
		pauses:   make(map[Operation][]*Pause),
		active:   make(map[*Pause]struct{}),
	}
	base := &controlledBackend{controller: controller}
	controller.wrapped = base
	if batch, ok := cache.BatchReaderOf(next); ok {
		controller.batch = batch
		controller.wrapped = &controlledBatchBackend{controlledBackend: base}
	}
	return controller, nil
}

func MustController(next cache.Backend) *Controller {
	controller, err := NewController(next)
	if err != nil {
		panic(err)
	}
	return controller
}

func (controller *Controller) Backend() cache.Backend {
	if controller == nil {
		return nil
	}
	return controller.wrapped
}

func (controller *Controller) FailNext(operation Operation, err error) error {
	if controller == nil {
		return fmt.Errorf("cachetest: controller is nil")
	}
	if !validOperation(operation) || err == nil {
		return fmt.Errorf("cachetest: failure operation or error is invalid")
	}
	controller.mu.Lock()
	controller.failures[operation] = append(controller.failures[operation], err)
	controller.mu.Unlock()
	return nil
}

func (controller *Controller) MustFailNext(operation Operation, err error) {
	if failure := controller.FailNext(operation, err); failure != nil {
		panic(failure)
	}
}

func (controller *Controller) PauseNext(operation Operation) (*Pause, error) {
	if controller == nil {
		return nil, fmt.Errorf("cachetest: controller is nil")
	}
	if !validOperation(operation) {
		return nil, fmt.Errorf("cachetest: operation is invalid")
	}
	pause := &Pause{entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	controller.mu.Lock()
	controller.pauses[operation] = append(controller.pauses[operation], pause)
	controller.mu.Unlock()
	return pause, nil
}

func (controller *Controller) MustPauseNext(operation Operation) *Pause {
	pause, err := controller.PauseNext(operation)
	if err != nil {
		panic(err)
	}
	return pause
}

func (controller *Controller) Records() []Record {
	if controller == nil {
		return nil
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return append([]Record(nil), controller.records...)
}

func (controller *Controller) Reset() {
	if controller == nil {
		return
	}
	controller.mu.Lock()
	for _, queue := range controller.pauses {
		for _, pause := range queue {
			pause.cancel()
			pause.Release()
		}
	}
	for pause := range controller.active {
		pause.Release()
	}
	controller.failures = make(map[Operation][]error)
	controller.pauses = make(map[Operation][]*Pause)
	controller.active = make(map[*Pause]struct{})
	controller.records = nil
	controller.mu.Unlock()
}

func (backend *controlledBackend) Next() cache.Backend {
	if backend == nil || backend.controller == nil {
		return nil
	}
	return backend.controller.next
}

func (backend *controlledBackend) Get(ctx context.Context, address cache.Address, limit cache.ReadLimit) ([]byte, bool, error) {
	controller := backend.controller
	if err := controller.before(ctx, GetOperation); err != nil {
		controller.record(Record{Operation: GetOperation, Address: address, Failed: true})
		return nil, false, err
	}
	value, found, err := controller.next.Get(ctx, address, limit)
	controller.record(Record{Operation: GetOperation, Address: address, Items: boolInt(found), ValueBytes: int64(len(value)), Found: found, Failed: err != nil})
	return value, found, err
}

func (backend *controlledBackend) Put(ctx context.Context, address cache.Address, value []byte, expiry cache.Expiry) error {
	controller := backend.controller
	if err := controller.before(ctx, PutOperation); err != nil {
		controller.record(Record{Operation: PutOperation, Address: address, Items: 1, ValueBytes: int64(len(value)), Failed: true})
		return err
	}
	err := controller.next.Put(ctx, address, value, expiry)
	controller.record(Record{Operation: PutOperation, Address: address, Items: 1, ValueBytes: int64(len(value)), Failed: err != nil})
	return err
}

func (backend *controlledBackend) Delete(ctx context.Context, address cache.Address) error {
	controller := backend.controller
	if err := controller.before(ctx, DeleteOperation); err != nil {
		controller.record(Record{Operation: DeleteOperation, Address: address, Items: 1, Failed: true})
		return err
	}
	err := controller.next.Delete(ctx, address)
	controller.record(Record{Operation: DeleteOperation, Address: address, Items: 1, Failed: err != nil})
	return err
}

func (backend *controlledBatchBackend) GetMany(ctx context.Context, addresses []cache.Address, limit cache.BatchReadLimit) (map[cache.Address][]byte, error) {
	controller := backend.controller
	if err := controller.before(ctx, GetManyOperation); err != nil {
		controller.record(Record{Operation: GetManyOperation, Items: len(addresses), Failed: true})
		return nil, err
	}
	values, err := controller.batch.GetMany(ctx, addresses, limit)
	controller.record(Record{Operation: GetManyOperation, Items: len(addresses), ValueBytes: mapBytes(values), Found: len(values) > 0, Failed: err != nil})
	return values, err
}

func (controller *Controller) before(ctx context.Context, operation Operation) error {
	if ctx == nil {
		return fmt.Errorf("cachetest: context is nil")
	}
	controller.mu.Lock()
	var pause *Pause
	if queue := controller.pauses[operation]; len(queue) > 0 {
		pause = queue[0]
		controller.pauses[operation] = queue[1:]
		controller.active[pause] = struct{}{}
	}
	var failure error
	if queue := controller.failures[operation]; len(queue) > 0 {
		failure = queue[0]
		controller.failures[operation] = queue[1:]
	}
	controller.mu.Unlock()
	if pause != nil {
		pause.enteredFlag.Store(true)
		close(pause.entered)
		var err error
		select {
		case <-pause.release:
		case <-ctx.Done():
			err = ctx.Err()
		}
		controller.mu.Lock()
		delete(controller.active, pause)
		controller.mu.Unlock()
		if err != nil {
			return err
		}
	}
	if failure != nil {
		return failure
	}
	return ctx.Err()
}

func (controller *Controller) record(record Record) {
	controller.mu.Lock()
	controller.records = append(controller.records, record)
	controller.mu.Unlock()
}

func validOperation(operation Operation) bool {
	switch operation {
	case GetOperation, GetManyOperation, PutOperation, DeleteOperation:
		return true
	default:
		return false
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func mapBytes(values map[cache.Address][]byte) int64 {
	var total int64
	for _, value := range values {
		if int64(len(value)) > math.MaxInt64-total {
			return math.MaxInt64
		}
		total += int64(len(value))
	}
	return total
}

func nilValue(value any) bool {
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
