package event

import (
	"fmt"
	"sync"
)

// The composition root's binding table. Open may be called any number of times,
// so what a Binding scopes is the value it returns: the families bound through
// this one. Two Binding values cannot see each other, and the only wider scope
// is a package-level registry, which would make declaration order matter and an
// import side effect load-bearing.
type Binding struct {
	store Store

	mutex    sync.Mutex
	families map[string]Declaration
}

func Open(store Store) *Binding {
	return &Binding{store: store, families: map[string]Declaration{}}
}

// Five constant-time checks and one allocation. No codec walk, no reflection and
// no state shared between requests, which is what makes a store per request over
// a borrowed tenant lease ordinary. The limits and the capabilities are read
// once and retained, so an empty append can cost no store call at all; the
// backing is not, because a remembered backing cannot see a store that
// re-pointed at another database.
func Bind[S, ID any](b *Binding, a *Aggregate[S, ID]) (*Repo[S, ID], error) {
	if b == nil || nilByAnyRoute(b.store) {
		return nil, fmt.Errorf("%w: a binding names the store its repositories write through, and this one names none", ErrWrongStore)
	}
	if a == nil {
		return nil, fmt.Errorf("%w: a repository is bound to an aggregate and this call names none", ErrDeclaration)
	}
	a.seal()
	if err := b.hold(a.family, a); err != nil {
		return nil, err
	}
	capabilities, limits, err := admit(b.store)
	if err != nil {
		return nil, err
	}
	return &Repo[S, ID]{store: b.store, aggregate: a, capabilities: capabilities, limits: limits}, nil
}

func (this *Binding) hold(family string, declared Declaration) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	held, bound := this.families[family]
	if bound && held != declared {
		return fmt.Errorf("%w: %q", ErrFamily, family)
	}
	this.families[family] = declared
	return nil
}

// The store-honesty checks, and they run at both doors a store enters the kernel
// by: Bind and Read. A deployment that only reads never binds, so at Bind alone
// a store answering a zero MaxRead would reach a projector unvalidated, honestly
// return at most zero envelopes, and drain having read nothing. A zero is
// refused rather than defaulted because Limits is what the kernel enforces on
// the store's behalf, so a zero there is a store that forgot to state a bound
// and a kernel default would enforce a number the store never chose.
func admit(log Log) (Capabilities, Limits, error) {
	if nilByAnyRoute(log) {
		return Capabilities{}, Limits{}, fmt.Errorf("%w: there is no store here to read through", ErrWrongStore)
	}
	if !log.Backing().valid() {
		return Capabilities{}, Limits{}, fmt.Errorf("%w: this store does not say what it writes to", ErrWrongStore)
	}
	limits := log.Limits()
	if err := admitLimits(limits); err != nil {
		return Capabilities{}, Limits{}, err
	}
	capabilities := log.Capabilities()
	if err := admitCapabilities(capabilities); err != nil {
		return Capabilities{}, Limits{}, err
	}
	return capabilities, limits, nil
}

func admitLimits(limits Limits) error {
	for _, bound := range []struct {
		name    string
		chosen  int
		ceiling int
	}{
		{"MaxPayload", limits.MaxPayload, MaxPayloadBytes},
		{"MaxBatch", limits.MaxBatch, MaxBatchCount},
		{"MaxKey", limits.MaxKey, MaxKeyBytes},
		{"StreamPage", limits.StreamPage, MaxPageCount},
		{"MaxRead", limits.MaxRead, MaxPageCount},
	} {
		if bound.chosen <= 0 {
			return fmt.Errorf("%w: this store states no %s", ErrWrongStore, bound.name)
		}
		if bound.chosen > bound.ceiling {
			return fmt.Errorf("%w: %s is %d, above the kernel ceiling of %d", ErrWrongStore, bound.name, bound.chosen, bound.ceiling)
		}
	}
	resident := ResidentPage(limits.MaxPayload)
	for _, page := range []struct {
		name   string
		chosen int
	}{
		{"StreamPage", limits.StreamPage},
		{"MaxRead", limits.MaxRead},
	} {
		if page.chosen > resident {
			return fmt.Errorf("%w: %s is %d at a payload bound of %d, more than the %d envelopes one read may hold",
				ErrWrongStore, page.name, page.chosen, limits.MaxPayload, resident)
		}
	}
	return nil
}

func admitCapabilities(capabilities Capabilities) error {
	for _, claim := range []struct {
		name   string
		stated Support
	}{
		{"Transactions", capabilities.Transactions},
		{"Persistence", capabilities.Persistence},
		{"MonotoneVisibility", capabilities.MonotoneVisibility},
		{"SharedBacking", capabilities.SharedBacking},
	} {
		if !claim.stated.stated() {
			return fmt.Errorf("%w: this store states nothing about %s, and a capability nobody stated is not one nobody has", ErrWrongStore, claim.name)
		}
	}
	return nil
}
