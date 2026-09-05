package cachememory

import (
	"context"
	"fmt"
	"reflect"

	"github.com/frostgrove/vv/cache"
)

type Operation string

const (
	GetOperation     Operation = "get"
	GetManyOperation Operation = "get_many"
	PutOperation     Operation = "put"
	DeleteOperation  Operation = "delete"
	EvictOperation   Operation = "evict"
	ResetOperation   Operation = "reset"
	CloseOperation   Operation = "close"
)

type Outcome string

const (
	HitOutcome      Outcome = "hit"
	MissOutcome     Outcome = "miss"
	StoredOutcome   Outcome = "stored"
	ReplacedOutcome Outcome = "replaced"
	DeletedOutcome  Outcome = "deleted"
	EvictedOutcome  Outcome = "evicted"
	RejectedOutcome Outcome = "rejected"
	CompleteOutcome Outcome = "complete"
)

type Reason string

const (
	ExpiredReason         Reason = "expired"
	MaxEntriesReason      Reason = "max_entries"
	MaxBytesReason        Reason = "max_bytes"
	MaxItemBytesReason    Reason = "max_item_bytes"
	ReadLimitReason       Reason = "read_limit"
	BatchItemLimitReason  Reason = "batch_item_limit"
	BatchTotalLimitReason Reason = "batch_total_limit"
	ResetReason           Reason = "reset"
	CloseReason           Reason = "close"
)

type Event struct {
	Operation    Operation
	Outcome      Outcome
	Reason       Reason
	Items        int
	ValueBytes   int64
	ChargedBytes int64
}

type Observer interface {
	Observe(context.Context, Event)
}

const MaxObservers = 8

type observerFanOut struct {
	children []Observer
}

func Observers(children ...Observer) (Observer, error) {
	if len(children) > MaxObservers {
		return nil, fail("build observers", fmt.Errorf("%w: at most %d observers may be composed", cache.ErrTooLarge, MaxObservers))
	}
	present := make([]Observer, 0, len(children))
	for _, child := range children {
		if nilInterface(child) {
			continue
		}
		present = append(present, child)
	}
	return &observerFanOut{children: present}, nil
}

func MustObservers(children ...Observer) Observer {
	observer, err := Observers(children...)
	if err != nil {
		panic(err)
	}
	return observer
}

func (this *observerFanOut) Observe(ctx context.Context, event Event) {
	for _, child := range this.children {
		observeIsolated(child, ctx, event)
	}
}

func observeIsolated(child Observer, ctx context.Context, event Event) {
	defer func() { _ = recover() }()
	child.Observe(ctx, event)
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
