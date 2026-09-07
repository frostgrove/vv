package event

import (
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
)

type connection struct {
	dsn string
}

type identityHolder struct {
	rows []int
}

func nilRoutes() []struct {
	name  string
	value any
} {
	return []struct {
		name  string
		value any
	}{
		{"a nil interface", nil},
		{"a nil pointer", (*connection)(nil)},
		{"a nil map", map[string]int(nil)},
		{"a nil slice", []int(nil)},
		{"a nil channel", chan int(nil)},
		{"a nil func", (func())(nil)},
	}
}

func uncomparableIdentities() []struct {
	name  string
	value any
} {
	return []struct {
		name  string
		value any
	}{
		{"a map", map[string]int{"rows": 1}},
		{"a slice", []int{1}},
		{"a func", func() {}},
		{"a struct holding a slice", identityHolder{rows: []int{1}}},
	}
}

func TestABackingAndAnAuthorityAreComparedAndNeverIdentical(t *testing.T) {
	first := &connection{dsn: "the ledger database"}
	second := &connection{dsn: "the reporting database"}

	over := func(t *testing.T, identity any) Backing {
		t.Helper()
		backing, err := NewBacking(identity)
		if err != nil {
			t.Fatalf("a backing over %v was refused: %v", identity, err)
		}
		return backing
	}

	t.Run("two stores over one thing are one store, and over two things are two", func(t *testing.T) {
		if !over(t, first).Equal(over(t, first)) {
			t.Fatal("two store values over one database do not share a backing, so a token minted by one is refused by the other")
		}
		if over(t, first).Equal(over(t, second)) {
			t.Fatal("two stores over two databases share a backing, so a write could land in the wrong one")
		}
		if (Backing{}).Equal(Backing{}) {
			t.Fatal("two backings a store never stated compare equal, which is what == answers and what Equal must not")
		}
		if (Backing{}).valid() {
			t.Fatal("a backing a store never stated is usable as an identity")
		}
		if !over(t, first).valid() {
			t.Fatal("a stated backing is not usable as an identity")
		}
	})

	t.Run("the comparison is the framework's own and not a second answer", func(t *testing.T) {
		for _, pair := range []struct {
			name  string
			left  any
			right any
		}{
			{"one identity twice", first, first},
			{"two identities", first, second},
			{"two nil interfaces", nil, nil},
			{"two equal slices", []int{1}, []int{1}},
			{"two typed nil pointers", (*connection)(nil), (*connection)(nil)},
			{"two dynamic types holding one number", int(1), int64(1)},
		} {
			framework := crud.SameDataSource(pair.left, pair.right)
			ours := Backing{identity: pair.left}.Equal(Backing{identity: pair.right})
			if framework != ours {
				t.Fatalf("%s: the framework answers %v to whether these write to the same place and a backing answers %v, so this subsystem admits a program the rest of it refuses",
					pair.name, framework, ours)
			}
		}
	})

	t.Run("an identity that is nil by any route is refused where it is minted", func(t *testing.T) {
		for _, route := range nilRoutes() {
			for _, mint := range []struct {
				name string
				call func(any) error
			}{
				{"a backing", func(identity any) error { _, err := NewBacking(identity); return err }},
				{"an authority", func(identity any) error {
					_, err := NewAuthority(over(t, first), identity)
					return err
				}},
			} {
				if err := mint.call(route.value); !errors.Is(err, ErrWrongStore) {
					t.Fatalf("%s over %s was accepted, and two stores that both forgot to set one would then compare equal: %v",
						mint.name, route.name, err)
				}
			}
		}
		for _, accepted := range []struct {
			name  string
			value any
		}{
			{"a pointer", first},
			{"a channel", make(chan int)},
			{"a string", "the ledger database"},
			{"a comparable struct", connection{dsn: "the ledger database"}},
		} {
			if _, err := NewBacking(accepted.value); err != nil {
				t.Fatalf("%s was refused, so the nil check refuses more than nil: %v", accepted.name, err)
			}
		}
	})

	t.Run("an identity nothing can compare is refused at construction", func(t *testing.T) {
		for _, uncomparable := range uncomparableIdentities() {
			if _, err := NewBacking(uncomparable.value); !errors.Is(err, ErrWrongStore) {
				t.Fatalf("a backing over %s was accepted, and it would then not match itself at the first door: %v", uncomparable.name, err)
			}
			if _, err := NewAuthority(over(t, first), uncomparable.value); !errors.Is(err, ErrWrongStore) {
				t.Fatalf("an authority over %s was accepted: %v", uncomparable.name, err)
			}
		}
		_, byNil := NewBacking(nil)
		_, byShape := NewBacking([]int{1})
		if byNil.Error() == byShape.Error() {
			t.Fatal("a nil identity and one nothing can compare are refused in the same words, so a store cannot tell which rule it broke")
		}
	})

	t.Run("an authority is its transaction and answers for no other", func(t *testing.T) {
		ledger, reporting := over(t, first), over(t, second)
		transaction := &connection{dsn: "one open transaction"}
		other := &connection{dsn: "a second open transaction"}

		held, err := NewAuthority(ledger, transaction)
		if err != nil {
			t.Fatalf("an authority over a live transaction was refused: %v", err)
		}
		again, err := NewAuthority(ledger, transaction)
		if err != nil {
			t.Fatalf("a second authority over the same transaction was refused: %v", err)
		}
		if !held.Same(again) {
			t.Fatal("two authorities resolved from one transaction are not the same, so a savepoint and its parent would read as two transactions")
		}
		if !held.Valid() {
			t.Fatal("an authority over a live transaction is not valid")
		}

		elsewhere, err := NewAuthority(ledger, other)
		if err != nil {
			t.Fatalf("an authority over a second transaction was refused: %v", err)
		}
		if held.Same(elsewhere) {
			t.Fatal("two live transactions answer one authority, so two writes in two transactions would claim to be atomic together")
		}

		acrossBackings, err := NewAuthority(reporting, transaction)
		if err != nil {
			t.Fatalf("an authority over another backing was refused: %v", err)
		}
		if held.Same(acrossBackings) {
			t.Fatal("one transaction value on two backings answers one authority")
		}

		if _, err := NewAuthority(Backing{}, transaction); !errors.Is(err, ErrWrongStore) {
			t.Fatalf("an authority over a backing no store stated was accepted: %v", err)
		}
		if (Authority{}).Valid() || (Authority{}).Same(Authority{}) {
			t.Fatal("an authority nobody minted is valid or answers for another")
		}
		if held.Same(Authority{}) {
			t.Fatal("a live authority is the same as one nobody minted")
		}
	})

	t.Run("an authority does not survive a wire", func(t *testing.T) {
		held, err := NewAuthority(over(t, first), &connection{dsn: "one open transaction"})
		if err != nil {
			t.Fatalf("an authority was refused: %v", err)
		}
		encoded, err := held.MarshalJSON()
		if err == nil {
			t.Fatalf("an authority serialised itself as %q, and what comes back is a transaction that is not open anywhere", encoded)
		}
		if !errors.Is(err, errAuthorityNotSerialisable) {
			t.Fatalf("an authority refused to serialise with an error nobody named: %v", err)
		}
	})
}
