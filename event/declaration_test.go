package event

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type accountID struct {
	tenant string
	number string
}

type account struct {
	Balance int64
	Applied map[string]bool
	Tags    []string
}

type opened struct{ Owner string }

type creditedV1 struct{ Minor int64 }

type creditedV2 struct {
	Minor  int64
	Reason string
}

type closed struct{ Reason string }

// A payload whose own marshaller decides what its interface field becomes, so
// the codec's walk has nothing left to answer for and must stop rather than
// refuse a type it encodes perfectly well.
type stamped struct{ Meta any }

func (this stamped) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct{ Meta string }{"stamped"})
}

func (this *stamped) UnmarshalJSON([]byte) error { this.Meta = "stamped"; return nil }

func accountKey(id accountID) Key { return Compose(id.tenant, id.number) }

func toCreditedV2(from creditedV1) (creditedV2, error) {
	return creditedV2{Minor: from.Minor, Reason: "migrated"}, nil
}

func openAccount(this account, event opened) account {
	if this.Applied == nil {
		this.Applied = map[string]bool{}
	}
	this.Tags = append(this.Tags, event.Owner)
	return this
}

func creditAccount(this account, event creditedV2) account {
	if this.Applied == nil {
		this.Applied = map[string]bool{}
	}
	this.Balance += event.Minor
	this.Applied[event.Reason] = true
	return this
}

type accountDeclaration struct {
	aggregate *Aggregate[account, accountID]
	opened    *Fact[account, accountID, opened]
	credited  *Fact[account, accountID, creditedV2]
	closed    *Fact[account, accountID, closed]
}

func declareAccounts(t *testing.T) accountDeclaration {
	t.Helper()
	aggregate := Define[account]("accounts.account", accountKey)
	return accountDeclaration{
		aggregate: aggregate,
		opened:    Declare(aggregate, "accounts.opened", From(JSON[opened]()), openAccount),
		credited: Declare(aggregate, "accounts.credited",
			Then(From(JSON[creditedV1]()), JSON[creditedV2](), toCreditedV2), creditAccount),
		closed: Declare(aggregate, "accounts.closed", From(JSON[closed]()),
			func(this account, _ closed) account { return this }),
	}
}

func TestADeclaration(t *testing.T) {
	t.Run("a revision is the chain's position and is never typed", func(t *testing.T) {
		declared := declareAccounts(t)
		for _, retained := range []struct {
			fact      interface{ Revisions() int }
			name      string
			revisions int
		}{
			{declared.opened, "accounts.opened", 1},
			{declared.credited, "accounts.credited", 2},
			{declared.closed, "accounts.closed", 1},
		} {
			if got := retained.fact.Revisions(); got != retained.revisions {
				t.Fatalf("%s reports %d retained revisions against the %d its chain declares, so the current revision is not the chain's position and an event will be read by the wrong codec",
					retained.name, got, retained.revisions)
			}
		}
		if got := declared.credited.Name(); got != "accounts.credited" {
			t.Fatalf("a fact reports the wire name %q rather than the one it was declared with", got)
		}
		if got := declared.aggregate.Family(); got != "accounts.account" {
			t.Fatalf("an aggregate reports the family %q rather than the one it was declared with", got)
		}
	})

	t.Run("a second aggregate shares no name space with the first", func(t *testing.T) {
		first := declareAccounts(t)
		orders := Define[[]string]("orders.order", func(id string) Key { return Compose(id) })
		reused, err := TryDeclare(orders, "accounts.credited",
			Then(From(JSON[creditedV1]()), JSON[creditedV2](), toCreditedV2), func(this []string, _ creditedV2) []string { return this })
		if err != nil {
			t.Fatalf("a wire name reused on a second aggregate was refused, and the tables are per aggregate: %v", err)
		}
		if reused.Revisions() != first.credited.Revisions() {
			t.Fatal("two aggregates declaring one wire name report different revision counts for it")
		}
		_, err = TryDeclare(first.aggregate, "accounts.credited", From(JSON[closed]()),
			func(this account, _ closed) account { return this })
		if !errors.Is(err, ErrDeclaration) || errors.Is(err, ErrSealed) {
			t.Fatalf("a wire name reused WITHIN one aggregate answered %v rather than the duplicate refusal, so one name reads two payload shapes", err)
		}
	})

	t.Run("an upcaster whose input and output are one type is ordinary", func(t *testing.T) {
		aggregate := Define[account]("accounts.reread", accountKey)
		reread := Declare(aggregate, "accounts.reread.credited",
			Then(From(JSON[creditedV2]()), JSON[creditedV2](), func(from creditedV2) (creditedV2, error) {
				from.Reason = "reread"
				return from, nil
			}), creditAccount)
		if reread.Revisions() != 2 {
			t.Fatalf("two revisions of one Go type collapsed into %d", reread.Revisions())
		}
		carried, err := reread.RoundTrip(creditedV2{Minor: 5}, creditedV2{Minor: 7, Reason: "now"})
		if err != nil {
			t.Fatalf("a revision bump that changed meaning and not shape was refused: %v", err)
		}
		if carried[0].Reason != "reread" || carried[1].Reason != "now" {
			t.Fatalf("the upcaster did not carry revision 1 and leave revision 2 alone: %+v", carried)
		}
	})

	t.Run("a composite identity renders through the mapper, and the two spellings are not one", func(t *testing.T) {
		declared := declareAccounts(t)
		key, err := declared.aggregate.Key(accountID{tenant: "acme", number: "A-17"})
		if err != nil {
			t.Fatalf("a composite identity was refused: %v", err)
		}
		if key != "acme/A-17" {
			t.Fatalf("the mapper rendered %q rather than the composition of its parts", key)
		}
		ambiguous := Define[account]("accounts.ambiguous", func(id accountID) Key { return Key(id.tenant + id.number) })
		first, _ := ambiguous.Key(accountID{tenant: "ab", number: "c1"})
		second, _ := ambiguous.Key(accountID{tenant: "a", number: "bc1"})
		if first != second {
			t.Fatal("the concatenating mapper this framework warns about no longer collides, so eventtest.Keys has nothing to catch")
		}
		plain := Define[account]("accounts.plain", func(id accountID) Key { return Key(id.number) })
		composed := Define[account]("accounts.composed", func(id accountID) Key { return Compose(id.number) })
		crossing := accountID{number: "a/b"}
		bare, _ := plain.Key(crossing)
		escaped, _ := composed.Key(crossing)
		if bare == escaped {
			t.Fatal("the plain conversion and the composition render one key for an identity carrying a separator, so swapping one for the other after a stream exists would look safe")
		}
	})

	t.Run("a state that reaches a slice, a map or is one costs no declaration", func(t *testing.T) {
		type header struct{ Tags []string }
		type nested struct {
			Header  header
			Items   [2]creditedV2
			Applied map[string]bool
		}
		for _, shape := range []func() error{
			func() error {
				aggregate := Define[nested]("shapes.nested", func(id string) Key { return Compose(id) })
				_, err := TryDeclare(aggregate, "shapes.credited", From(JSON[creditedV2]()),
					func(this nested, _ creditedV2) nested { return this })
				return err
			},
			func() error {
				aggregate := Define[map[string]int64]("shapes.ledger", func(id string) Key { return Compose(id) })
				_, err := TryDeclare(aggregate, "shapes.credited", From(JSON[creditedV2]()),
					func(this map[string]int64, _ creditedV2) map[string]int64 { return this })
				return err
			},
			func() error {
				type recorded struct {
					Raw     json.RawMessage
					Balance int64
				}
				aggregate := Define[recorded]("shapes.recorded", func(id string) Key { return Compose(id) })
				_, err := TryDeclare(aggregate, "shapes.credited", From(JSON[creditedV2]()),
					func(this recorded, _ creditedV2) recorded { return this })
				return err
			},
		} {
			if err := shape(); err != nil {
				t.Fatalf("a state type was refused for the kinds it contains, and the kernel walks no state type: %v", err)
			}
		}
	})

	t.Run("the shipped codec answers for the whole type graph, and a custom marshaller ends the walk", func(t *testing.T) {
		type carriesAny struct{ Meta any }
		type node struct {
			Label string
			Next  *node
		}
		for _, refused := range []struct {
			what   string
			answer error
		}{
			{"a channel field", JSON[struct{ Signal chan int }]().CanEncode()},
			{"a function field", JSON[struct{ Apply func() }]().CanEncode()},
			{"an interface field", JSON[carriesAny]().CanEncode()},
			{"a field typed as an interface that marshals itself", JSON[struct{ Meta json.Marshaler }]().CanEncode()},
			{"a map keyed by a float", JSON[map[float64]int]().CanEncode()},
			{"a map keyed by a struct", JSON[map[accountID]int]().CanEncode()},
			{"a payload that is an interface", JSON[any]().CanEncode()},
		} {
			if !errors.Is(refused.answer, ErrCodecType) {
				t.Fatalf("the shipped codec accepted %s (%v), and what it cannot encode must be refused before main", refused.what, refused.answer)
			}
		}
		for _, accepted := range []struct {
			what   string
			answer error
		}{
			{"a recursive type", JSON[node]().CanEncode()},
			{"raw JSON", JSON[json.RawMessage]().CanEncode()},
			{"a map keyed by a string", JSON[map[string]int64]().CanEncode()},
			{"a type that marshals itself", JSON[stamped]().CanEncode()},
		} {
			if accepted.answer != nil {
				t.Fatalf("the shipped codec refused %s: %v", accepted.what, accepted.answer)
			}
		}
		aggregate := Define[account]("accounts.unwritable", accountKey)
		panicked := recoverDeclaration(t, "a fact whose reader type the shipped codec cannot encode", func() {
			Declare(aggregate, "accounts.tagged", From(JSON[carriesAny]()),
				func(this account, _ carriesAny) account { return this })
		})
		if !errors.Is(panicked, ErrCodecType) {
			t.Fatalf("the declaration answered %v rather than naming the codec that cannot encode its reader type", panicked)
		}
		if _, err := TryDeclare(aggregate, "accounts.stamped", From(JSON[stamped]()),
			func(this account, _ stamped) account { return this }); err != nil {
			t.Fatalf("a reader type its codec encodes perfectly well was refused: %v", err)
		}
	})

	t.Run("a revision the fact does not retain is refused rather than read by the wrong codec", func(t *testing.T) {
		declared := declareAccounts(t)
		encoded := []byte(`{"Minor":250,"Reason":"deposit"}`)
		for _, revision := range []int{0, -1, 3} {
			if _, err := declared.credited.apply(account{}, revision, encoded); !errors.Is(err, ErrRevision) {
				t.Fatalf("revision %d against a fact retaining 2 answered %v", revision, err)
			}
		}
		if _, err := declared.credited.apply(account{}, 2, encoded); err != nil {
			t.Fatalf("a retained revision was refused: %v", err)
		}
	})

	t.Run("a payload that is not a struct is the codec's business", func(t *testing.T) {
		type amount int64
		aggregate := Define[int64]("shapes.amounts", func(id string) Key { return Compose(id) })
		scalar, err := TryDeclare(aggregate, "shapes.credited", From(JSON[amount]()),
			func(this int64, event amount) int64 { return this + int64(event) })
		if err != nil {
			t.Fatalf("the kernel refused a non-struct payload, and encodability is the codec's question: %v", err)
		}
		carried, err := scalar.RoundTrip(amount(42))
		if err != nil || carried[0] != 42 {
			t.Fatalf("a non-struct payload did not round-trip: %v %v", carried, err)
		}
	})
}

type refusingCodec struct {
	answer error
}

func (refusingCodec) Encode(opened) ([]byte, error) { return []byte("{}"), nil }
func (refusingCodec) Decode([]byte) (opened, error) { return opened{}, nil }
func (this refusingCodec) CanEncode() error         { return this.answer }

type panickingCodec struct{ Codec[opened] }

func (panickingCodec) CanEncode() error { panic("a codec that cannot answer") }

func recoverDeclaration(t *testing.T, what string, declare func()) error {
	t.Helper()
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		declare()
	}()
	if recovered == nil {
		t.Fatalf("%s was accepted, so a malformed declaration survives to be discovered at load or append time", what)
	}
	err, ok := recovered.(error)
	if !ok {
		t.Fatalf("%s panicked with a %T, which a test can only match by string", what, recovered)
	}
	return err
}

func TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt(t *testing.T) {
	long := strings.Repeat("f", MaxNameBytes+1)
	fold := func(this account, _ opened) account { return this }
	var missing *refusingCodec

	aggregates := []struct {
		what     string
		family   string
		mapper   func(accountID) Key
		sentinel error
	}{
		{"an empty family", "", accountKey, ErrDeclaration},
		{"a family over the kernel cap", long, accountKey, ErrDeclaration},
		{"a family carrying a NUL", "accounts\x00account", accountKey, ErrDeclaration},
		{"a family carrying a newline", "accounts\naccount", accountKey, ErrDeclaration},
		{"a family that is not valid UTF-8", "accounts\xffaccount", accountKey, ErrDeclaration},
		{"an aggregate with no identity mapper", "accounts.account", nil, ErrDeclaration},
	}
	for _, malformed := range aggregates {
		panicked := recoverDeclaration(t, malformed.what, func() {
			Define[account](malformed.family, malformed.mapper)
		})
		if !errors.Is(panicked, malformed.sentinel) {
			t.Fatalf("%s panicked with %v, which does not carry the declaration sentinel", malformed.what, panicked)
		}
		_, returned := TryDefine[account](malformed.family, malformed.mapper)
		if !errors.Is(returned, malformed.sentinel) || returned.Error() != panicked.Error() {
			t.Fatalf("%s: Define panicked with %v and TryDefine returned %v, so the two are not one code path", malformed.what, panicked, returned)
		}
	}

	facts := []struct {
		what     string
		name     string
		chain    Chain[opened]
		fold     func(account, opened) account
		sentinel error
	}{
		{"an empty wire type name", "", From(JSON[opened]()), fold, ErrDeclaration},
		{"a wire type name over the kernel cap", long, From(JSON[opened]()), fold, ErrDeclaration},
		{"a wire type name carrying a control character", "accounts.\topened", From(JSON[opened]()), fold, ErrDeclaration},
		{"a fact with no fold", "accounts.opened", From(JSON[opened]()), nil, ErrDeclaration},
		{"a fact with no reader chain", "accounts.opened", Chain[opened]{}, fold, ErrDeclaration},
		{"a chain whose first codec is nil", "accounts.opened", From[opened](nil), fold, ErrDeclaration},
		{"a chain whose first codec is a nil pointer", "accounts.opened", From[opened](missing), fold, ErrDeclaration},
		{"a codec that cannot encode its own reader type", "accounts.opened", From[opened](refusingCodec{answer: errors.New("a channel is not JSON")}), fold, ErrCodecType},
		{"a codec that panics when it is asked", "accounts.opened", From[opened](panickingCodec{}), fold, ErrCodecType},
	}
	for _, malformed := range facts {
		aggregate := Define[account]("accounts.account", accountKey)
		panicked := recoverDeclaration(t, malformed.what, func() {
			Declare(aggregate, malformed.name, malformed.chain, malformed.fold)
		})
		if !errors.Is(panicked, malformed.sentinel) || !errors.Is(panicked, ErrDeclaration) {
			t.Fatalf("%s panicked with %v, which does not carry the declaration sentinel", malformed.what, panicked)
		}
		if malformed.sentinel != ErrCodecType && errors.Is(panicked, ErrCodecType) {
			t.Fatalf("%s was diagnosed as a codec that cannot encode its reader type (%v), so a check ran against a codec that is not there", malformed.what, panicked)
		}
		second := Define[account]("accounts.account", accountKey)
		_, returned := TryDeclare(second, malformed.name, malformed.chain, malformed.fold)
		if !errors.Is(returned, malformed.sentinel) || returned.Error() != panicked.Error() {
			t.Fatalf("%s: Declare panicked with %v and TryDeclare returned %v, so the two are not one code path", malformed.what, panicked, returned)
		}
	}

	t.Run("a chain refuses what it cannot carry", func(t *testing.T) {
		aggregate := Define[account]("accounts.account", accountKey)
		for _, malformed := range []struct {
			what  string
			chain Chain[creditedV2]
		}{
			{"a revision whose codec is nil", Then[creditedV1, creditedV2](From(JSON[creditedV1]()), nil, toCreditedV2)},
			{"a revision whose upcaster is nil", Then[creditedV1, creditedV2](From(JSON[creditedV1]()), JSON[creditedV2](), nil)},
			{"a chain that begins at Then", Then(Chain[creditedV1]{}, JSON[creditedV2](), toCreditedV2)},
			{"a revision after one whose codec was refused", Then(From[creditedV1](nil), JSON[creditedV2](), toCreditedV2)},
		} {
			_, err := TryDeclare(aggregate, "accounts.credited", malformed.chain, creditAccount)
			if !errors.Is(err, ErrDeclaration) || errors.Is(err, ErrCodecType) {
				t.Fatalf("%s answered %v rather than the declaration refusal that names what is missing", malformed.what, err)
			}
		}
	})

	t.Run("a fact declared on no aggregate is refused rather than dereferenced", func(t *testing.T) {
		_, err := TryDeclare[account, accountID](nil, "accounts.opened", From(JSON[opened]()), fold)
		if !errors.Is(err, ErrDeclaration) {
			t.Fatalf("a fact declared on a nil aggregate answered %v", err)
		}
	})

	t.Run("the valid declaration in the same test builds", func(t *testing.T) {
		declared := declareAccounts(t)
		if declared.credited.Revisions() != 2 {
			t.Fatal("the control declaration did not build, so every case above passes by refusing everything")
		}
	})
}
