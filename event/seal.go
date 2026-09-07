package event

import "fmt"

// A declaration's fact table is mutable until the first party observes it and
// read-only for the whole of its observable life afterwards. Sealing on first
// observation rather than on first bind is what makes that true: two of the
// three testing entry points read the table without ever binding it, so a
// bind-triggered seal would leave it mutable while it is being read and race a
// concurrent fold.
//
// The readers are exhaustive, and this is the enumeration:
//
//	Aggregate.Family    the suite's family check
//	Aggregate.Key       the conformance suite's key proxy
//	Aggregate.Fold      the store-free fold
//	Bind                it reads the family and holds the fold table
//	Fact.New            it encodes, and it renders the key
//	Fact.RoundTrip      it reads the reader chain
//
// The conformance suite is still to arrive, and the two readers it brings reach
// the seal through the exported method named beside each, because an unexported
// one cannot be called from another package. A reader without a row here, and a
// row without a reader, are both what the seal test fails on.
//
// A declaration is sealed once and never unsealed, so the second observation
// and every one after it answer without the lock: one *Aggregate per aggregate
// type is shared by every request, and taking a mutex to write a boolean that
// is already true made one declaration serialise the process — measured 2.5x
// against the same load over distinct aggregates. declare still reads it under
// the lock, so a fact arriving with the first observation is either accepted or
// refused and never both.
func (this *Aggregate[S, ID]) seal() {
	if this.sealed.Load() {
		return
	}
	this.mutex.Lock()
	this.sealed.Store(true)
	this.mutex.Unlock()
}

func (this *Aggregate[S, ID]) declare(name string, apply func(S, int, []byte) (S, error)) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.sealed.Load() {
		return fmt.Errorf("%w: %q arrived after the aggregate %q had been read", ErrSealed, name, this.family)
	}
	if _, declared := this.facts[name]; declared {
		return fmt.Errorf("%w: the aggregate %q declares %q twice", ErrDeclaration, this.family, name)
	}
	this.facts[name] = apply
	return nil
}
