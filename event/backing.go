package event

import (
	"fmt"
	"reflect"

	"github.com/frostgrove/vv/crud"
)

type Backing struct{ identity any }

func NewBacking(identity any) (Backing, error) {
	if nilByAnyRoute(identity) {
		return Backing{}, fmt.Errorf("%w: a backing is the thing a store writes to, and this identity is nil", ErrWrongStore)
	}
	if !reflect.ValueOf(identity).Comparable() {
		return Backing{}, fmt.Errorf("%w: a backing identity is compared, so it must be comparable", ErrWrongStore)
	}
	return Backing{identity: identity}, nil
}

func (this Backing) Equal(other Backing) bool {
	return crud.SameDataSource(this.identity, other.identity)
}

func (this Backing) String() string {
	if !this.valid() {
		return "[event backing invalid]"
	}
	return "[event backing]"
}

func (this Backing) valid() bool { return crud.SameDataSource(this.identity, this.identity) }

// crud.SameDataSource answers true for two typed nils of one type, which is
// right for its own question and useless as a validity test, and crud's own
// six-kind predicate is unexported. So it is restated here, where the two
// answers can only ever diverge into a refusal: this one runs once, at
// construction, while the comparison runs on every append.
func nilByAnyRoute(value any) bool {
	if value == nil {
		return true
	}
	switch reflected := reflect.ValueOf(value); reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
