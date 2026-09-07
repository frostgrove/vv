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
//	Aggregate.Family    the binding's family check, and eventtest.Families
//	Aggregate.Key       eventtest.Keys
//	Aggregate.Fold      the store-free fold
//	Fact.New            it encodes, and it renders the key
//	Fact.RoundTrip      it reads the reader chain
//
// Bind reads the family and the fold table and seals too; it arrives with the
// caller seam and takes the sixth row then. A reader without a row here, and a
// row without a reader, are both what the seal test fails on.
func (this *Aggregate[S, ID]) seal() {
	this.mutex.Lock()
	this.sealed = true
	this.mutex.Unlock()
}

func (this *Aggregate[S, ID]) declare(name string, apply func(S, int, []byte) (S, error)) error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.sealed {
		return fmt.Errorf("%w: %q arrived after the aggregate %q had been read", ErrSealed, name, this.family)
	}
	if _, declared := this.facts[name]; declared {
		return fmt.Errorf("%w: the aggregate %q declares %q twice", ErrDeclaration, this.family, name)
	}
	this.facts[name] = apply
	return nil
}
