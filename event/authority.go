package event

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/frostgrove/vv/crud"
)

var errAuthorityNotSerialisable = errors.New("event: an append authority is the transaction it names, and a transaction does not survive a wire")

type Authority struct {
	backing  Backing
	identity any
}

// The transaction is the store's own value for this transaction — for a SQL
// store the *sql.Tx its executor resolved to. Nothing is minted and nothing is
// memoised: two calls that resolve to one live transaction answer authorities
// that compare Same because they hold one pointer, and a savepoint answers its
// parent's for the same reason.
func NewAuthority(over Backing, transaction any) (Authority, error) {
	if !over.valid() {
		return Authority{}, fmt.Errorf("%w: an authority names the backing its transaction runs on, and this one is invalid", ErrWrongStore)
	}
	if nilByAnyRoute(transaction) {
		return Authority{}, fmt.Errorf("%w: an authority is its transaction, and this one is nil", ErrWrongStore)
	}
	if !reflect.ValueOf(transaction).Comparable() {
		return Authority{}, fmt.Errorf("%w: a transaction identity is compared, so it must be comparable", ErrWrongStore)
	}
	return Authority{backing: over, identity: transaction}, nil
}

func (this Authority) Same(other Authority) bool {
	return this.backing.Equal(other.backing) && crud.SameDataSource(this.identity, other.identity)
}

func (this Authority) Valid() bool {
	return this.backing.valid() && crud.SameDataSource(this.identity, this.identity)
}

func (this Authority) String() string { return "[event authority]" }

func (Authority) MarshalJSON() ([]byte, error) { return nil, errAuthorityNotSerialisable }
