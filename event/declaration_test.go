package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
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

type creditedV3 struct {
	Minor  int64
	Reason string
	By     string
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

// The ordinary Go idiom — the pair on the pointer receiver — which encoding/json
// reaches only where it can take the value's address.
type guarded struct{ fields map[string]string }

func (this *guarded) MarshalJSON() ([]byte, error) { return json.Marshal(this.fields) }

func (this *guarded) UnmarshalJSON(payload []byte) error {
	return json.Unmarshal(payload, &this.fields)
}

// Exported fields and a working pair on the pointer receiver, which writes a
// shape its own fields do not: where encoding/json cannot take its address it is
// written field by field and read back through UnmarshalJSON, and the two halves
// disagree with nothing else in the walk to notice.
type posted struct {
	Amount int64
	Reason string
}

func (this *posted) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]any{this.Amount, this.Reason})
}

func (this *posted) UnmarshalJSON(payload []byte) error {
	var pair [2]json.RawMessage
	if err := json.Unmarshal(payload, &pair); err != nil {
		return err
	}
	if err := json.Unmarshal(pair[0], &this.Amount); err != nil {
		return err
	}
	return json.Unmarshal(pair[1], &this.Reason)
}

type cents struct{ Amount int64 }

func (this cents) MarshalJSON() ([]byte, error) { return json.Marshal(this.Amount) }

// A shared value type carrying a working pair, which is how Go composes value
// types — and Go promotes that pair onto everything that embeds it, so the
// struct writes itself as this value and nothing else. Tagging the embedded
// field does not undo the promotion; naming it does.
type money struct{ Cents int64 }

func (this money) MarshalJSON() ([]byte, error) { return json.Marshal(this.Cents) }

func (this *money) UnmarshalJSON(payload []byte) error {
	return json.Unmarshal(payload, &this.Cents)
}

type lined struct {
	money
	SKU string
}

type noted struct {
	time.Time
	Note string
}

type itemised struct{ money }

type invoiced struct {
	Money money
	SKU   string
}

type dueAt struct {
	At   time.Time
	Note string
}

// The mistake the compiler allows and go vet does not report, in both of its
// spellings — a payload type and a map key. encoding/json calls the
// unmarshaller through the addressable pointer, so a method on the value
// receiver runs against a copy, everything it writes is discarded and Unmarshal
// answers nil. The receiver each one writes into is the whole difference from
// money and settled below.
type sloppy struct{ Amount int64 }

func (this sloppy) MarshalJSON() ([]byte, error) { return json.Marshal(this.Amount) }

func (this sloppy) UnmarshalJSON(payload []byte) error {
	return json.Unmarshal(payload, &this.Amount)
}

type casual string

func (this casual) MarshalText() ([]byte, error) { return []byte("cur:" + this), nil }

func (this casual) UnmarshalText(text []byte) error {
	this = casual(text)
	return nil
}

type region struct{ Code string }

func (this region) MarshalText() ([]byte, error) { return []byte(this.Code), nil }

type district struct{ Code string }

func (this district) MarshalText() ([]byte, error) { return []byte(this.Code), nil }

func (this *district) UnmarshalText(text []byte) error { this.Code = string(text); return nil }

type withheld struct{ owner string }

// A map key type that renders itself for display, which is the most ordinary
// shape a domain value type takes and the one nothing asked about: JSON routes
// a key through MarshalText whatever the key's kind is.
type currency string

func (this currency) MarshalText() ([]byte, error) { return []byte("cur:" + string(this)), nil }

type grade int

func (this grade) MarshalText() ([]byte, error) { return []byte("g" + strconv.Itoa(int(this))), nil }

type kept string

func (this *kept) UnmarshalText(text []byte) error {
	*this = kept(strings.ToUpper(string(text)))
	return nil
}

type settled string

func (this settled) MarshalText() ([]byte, error) { return []byte(this), nil }

func (this *settled) UnmarshalText(text []byte) error { *this = settled(text); return nil }

// Two embedded structs rendering one JSON name at one depth. encoding/json
// writes neither, the field list of the struct that embeds them names neither
// collision, and go vet's structtag reads tags against tags — so an untagged
// pair, which is the ordinary way a Go struct is embedded, is seen by nothing
// at all.
type identified struct{ ID int }

type numbered struct{ ID int }

type twice struct {
	identified
	numbered
}

// The same, with a field of its own that survives — so the payload carries data
// and reads back as an ordinary fact forever, minus the two identifiers.
type placed struct {
	identified
	numbered
	Amount int
}

type dated struct{ At string }

type carried struct{ dated }

// An embedded struct with a JSON name of its own is a member and not a
// promotion, so the name it renders is that one and its fields are written
// underneath it.
type labelled struct {
	dated `json:"at"`
	At    string
}

// encoding/json writes the fields promoted through an embedded pointer and
// cannot read one of them back: reflect will not allocate a pointer it may not
// set. The named field beside it is the same type through a door json can open.
type pointed struct{ *dated }

type holdingDated struct{ Held *dated }

// Legal Go, and a claim walk that follows embedded fields without remembering
// where it has been does not terminate on it.
type ring struct {
	*ring
	N int
}

// The pair encoding/json reaches, beside a text marshaller it does not: a type
// declaring both is written as JSON, so asking which route it writes by in the
// other order refuses a type that round-trips perfectly.
type priced struct{ Amount int64 }

func (this *priced) MarshalJSON() ([]byte, error) { return json.Marshal([1]int64{this.Amount}) }

func (this *priced) UnmarshalJSON(payload []byte) error {
	var read [1]int64
	if err := json.Unmarshal(payload, &read); err != nil {
		return err
	}
	this.Amount = read[0]
	return nil
}

func (this priced) MarshalText() ([]byte, error) { return []byte("priced"), nil }

// A tag's name ends at the first comma, and a tag with no name renders the
// field's own — so both of these render one name twice and encoding/json writes
// at most one of the two fields.
type omitted struct {
	Amount int `json:"Reason,omitempty"`
	Reason string
}

type defaulted struct {
	Amount int `json:",omitempty"`
	Reason int `json:"Amount"`
}

type ignored struct {
	Signal chan int `json:"-"`
	Amount int
}

type dropped struct {
	First  int `json:"-"`
	Second int `json:"-"`
	Amount int
}

type erased struct {
	Amount int `json:"-"`
}

// What a pointer buys the field underneath it: encoding/json takes the pointer's
// element address, so the pair on posted's pointer receiver is reached through
// this and is not reached through the same struct held by value.
type held struct{ Posted posted }

// One type at two positions of one walk — addressable where it stands, and not
// one field over. A visited set keyed by the type alone answers for whichever of
// the two it met first, and the field order here is what makes that answer the
// wrong one.
type twinned struct {
	Direct posted
	Held   map[string]posted
}

type paired struct {
	Direct posted
	Held   map[string]*posted
}

// One name at two depths, which is encoding/json's depth rule: the shallower is
// written and the deeper is dropped.
type shadowed struct {
	carried
	dated
}

type distinct struct {
	identified
	dated
	Amount int
}

// A tag that renders the name of the field beside it. go vet's structtag check
// reads tags against tags and never a tag against a field name, so nothing but
// the codec's own walk sees this one: encoding/json writes the tagged field and
// drops the other without a word.
type collided struct {
	Amount int `json:"Reason"`
	Reason string
}

type separated struct {
	Amount int `json:"amount"`
	Reason string
}

func accountKey(id accountID) Key { return Compose(id.tenant, id.number) }

func toCreditedV2(from creditedV1) (creditedV2, error) {
	return creditedV2{Minor: from.Minor, Reason: "migrated"}, nil
}

func toCreditedV3(from creditedV2) (creditedV3, error) {
	return creditedV3{Minor: from.Minor, Reason: from.Reason, By: "unknown"}, nil
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
		for _, family := range []string{"accounts.account", "savings.account"} {
			aggregate := Define[account](family, accountKey)
			if got := aggregate.Family(); got != family {
				t.Fatalf("an aggregate declared as %q reports the family %q; a Family answering a constant makes two declarations one family, and two aggregates over one history fold each other's facts with no error at any point", family, got)
			}
			var seam Declaration = aggregate
			if got := seam.Family(); got != family {
				t.Fatalf("the non-generic view of the aggregate declared as %q reports %q, and it is what a binding and the conformance suite key their bound families by", family, got)
			}
		}
	})

	t.Run("three revisions report three, and the oldest is carried through both upcasters", func(t *testing.T) {
		aggregate := Define[account]("accounts.three", accountKey)
		credited := Declare(aggregate, "accounts.credited",
			Then(Then(From(JSON[creditedV1]()), JSON[creditedV2](), toCreditedV2), JSON[creditedV3](), toCreditedV3),
			func(this account, _ creditedV3) account { return this })
		if got := credited.Revisions(); got != 3 {
			t.Fatalf("a chain of three reports %d retained revisions, and a revision is nothing but the chain's position", got)
		}
		carried, err := credited.RoundTrip(creditedV1{Minor: 1}, creditedV2{Minor: 2, Reason: "second"}, creditedV3{Minor: 3, Reason: "third", By: "acme"})
		if err != nil {
			t.Fatalf("a chain of three did not round trip: %v", err)
		}
		want := []creditedV3{
			{Minor: 1, Reason: "migrated", By: "unknown"},
			{Minor: 2, Reason: "second", By: "unknown"},
			{Minor: 3, Reason: "third", By: "acme"},
		}
		if !slices.Equal(carried, want) {
			t.Fatalf("the chain carried %+v rather than %+v, so a retained revision is read by the wrong codec or crosses the wrong number of upcasters", carried, want)
		}
	})

	t.Run("a type graph larger than the walk is refused rather than walked", func(t *testing.T) {
		if codecGraphDepth != 1024 || codecGraphNodes != 1024 || codecGraphEdges != 4096 {
			t.Fatalf("the walk is bounded at %d deep, %d types and %d edges; the shapes below were built against 1024, 1024 and 4096, and a bound nothing pins is a bound that can be raised out of reach",
				codecGraphDepth, codecGraphNodes, codecGraphEdges)
		}

		nested := reflect.TypeFor[int]()
		for range 1100 {
			nested = reflect.ArrayOf(1, nested)
		}
		if chargeJSON(nested) == nil {
			t.Fatal("a type graph of 1100 distinct types was walked to the end, and the graph is an application's rather than the kernel's to trust")
		}

		fields := make([]reflect.StructField, 0, 1100)
		for index := range 1100 {
			fields = append(fields, reflect.StructField{Name: fmt.Sprintf("F%d", index), Type: reflect.TypeFor[int]()})
		}
		if chargeJSON(reflect.StructOf(fields)) == nil {
			t.Fatal("a struct rendering 1100 JSON names was collected to the end")
		}

		shallow := reflect.TypeFor[int]()
		for range 8 {
			shallow = reflect.ArrayOf(1, shallow)
		}
		if err := chargeJSON(shallow); err != nil {
			t.Fatalf("a graph well inside the bound was refused (%v), so the two cases above pass by refusing every nested type", err)
		}
		if err := chargeJSON(reflect.StructOf(fields[:1000])); err != nil {
			t.Fatalf("a struct of 1000 names, inside the bound, was refused: %v", err)
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

	t.Run("the current revision writes with the codec that revision declared", func(t *testing.T) {
		acme := accountID{tenant: "acme", number: "A-17"}
		aggregate := Define[account]("accounts.compact", accountKey)
		compact := Declare(aggregate, "accounts.credited",
			Then(From(JSON[creditedV1]()), decimalCodec{}, func(from creditedV1) (creditedV1, error) { return from, nil }),
			func(this account, event creditedV1) account { this.Balance += event.Minor; return this })
		change := compact.New(acme, creditedV1{Minor: 250})
		if change.Err() != nil {
			t.Fatalf("a decision at the current revision was refused: %v", change.Err())
		}
		if string(change.payload) != "250" {
			t.Fatalf("the current revision froze %s rather than the compact form its own codec writes; each revision carries its own codec so that a bump may change the encoding without a data migration, and a chain that discards the codec it was handed goes on writing the previous one while the round trip still passes", change.payload)
		}
		state, err := aggregate.Fold(acme, account{}, change)
		if err != nil || state.Balance != 250 {
			t.Fatalf("the fact written by the current revision's own codec folded to %+v (%v)", state, err)
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

		illegal := Define[account]("accounts.illegal", func(id accountID) Key { return Key(id.number) })
		for _, refused := range []struct {
			what   string
			number string
		}{
			{"a blank rendering", ""},
			{"a rendering carrying a newline", "A\n17"},
			{"a rendering over the kernel cap", strings.Repeat("k", MaxKeyBytes+1)},
		} {
			rendered, err := illegal.Key(accountID{number: refused.number})
			if !errors.Is(err, ErrKey) {
				t.Fatalf("%s answered %v", refused.what, err)
			}
			if rendered != "" {
				t.Fatalf("%s was refused and the raw rendering came back beside the refusal, %d bytes of it; a refusal carries a classification and never the value it refused, and the conformance suite's key proxy reads this one", refused.what, len(rendered))
			}
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
			{"a type that writes itself and declares no reader", JSON[cents]().CanEncode()},
			{"a map key that writes itself and declares no reader", JSON[map[region]int]().CanEncode()},
			{"a marshalling pair on the pointer receiver, held where JSON cannot address it", JSON[map[string]posted]().CanEncode()},
			{"a struct with fields and none encoding/json writes", JSON[withheld]().CanEncode()},
			{"a payload reaching a struct with fields and none it writes", JSON[struct{ Held withheld }]().CanEncode()},
			{"two fields rendering one JSON name", JSON[collided]().CanEncode()},
			{"two embedded structs rendering one JSON name", JSON[twice]().CanEncode()},
			{"the same, where the fields of the struct itself survive", JSON[placed]().CanEncode()},
			{"one JSON name promoted from two depths", JSON[shadowed]().CanEncode()},
			{"a string-kind map key that writes itself and declares no reader", JSON[map[currency]int]().CanEncode()},
			{"an integer-kind map key that writes itself and declares no reader", JSON[map[grade]int]().CanEncode()},
			{"a map key that reads itself and is written as its kind", JSON[map[kept]int]().CanEncode()},
			{"a complex field", JSON[struct{ Rate complex128 }]().CanEncode()},
			{"a pair held in an array a map holds", JSON[map[string][2]posted]().CanEncode()},
			{"a tag that names the field beside it and carries an option", JSON[omitted]().CanEncode()},
			{"a tag naming no name, colliding with the field beside it", JSON[defaulted]().CanEncode()},
			{"a struct whose only field encoding/json is told to skip", JSON[erased]().CanEncode()},
			{"a pair reached through a struct a map holds", JSON[map[string]held]().CanEncode()},
			{"a type legal where it stands and illegal one field over", JSON[twinned]().CanEncode()},
			{"a struct embedding a type whose JSON pair it promotes", JSON[lined]().CanEncode()},
			{"the textbook embedding, whose pair arrives from the standard library", JSON[noted]().CanEncode()},
			{"the same shape tagged, which does not undo the promotion", JSON[struct {
				money `json:"money"`
				SKU   string
			}]().CanEncode()},
			{"the same promotion reached through a pointer", JSON[struct{ Line *lined }]().CanEncode()},
			{"a type whose unmarshaller is on the value receiver", JSON[sloppy]().CanEncode()},
			{"the same type held in a field", JSON[struct{ Amount sloppy }]().CanEncode()},
			{"the same type held behind a pointer", JSON[struct{ Amount *sloppy }]().CanEncode()},
			{"a map key whose UnmarshalText is on the value receiver", JSON[map[casual]int]().CanEncode()},
			{"a struct promoted through an embedded pointer to an unexported type", JSON[pointed]().CanEncode()},
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
			{"a type that marshals itself and reads itself back", JSON[stamped]().CanEncode()},
			{"a map key that writes itself and reads itself back", JSON[map[district]int]().CanEncode()},
			{"a marshaller on the pointer receiver, where the reader type is addressable", JSON[guarded]().CanEncode()},
			{"the same pair, where the reader type is addressable", JSON[posted]().CanEncode()},
			{"the same, behind a pointer and in a slice", JSON[struct {
				Held *guarded
				Kept []guarded
			}]().CanEncode()},
			{"a marker fact that carries no data at all", JSON[struct{}]().CanEncode()},
			{"two fields rendering two JSON names", JSON[separated]().CanEncode()},
			{"one embedded struct per JSON name, beside a field of its own", JSON[distinct]().CanEncode()},
			{"a struct promoted through one path only", JSON[carried]().CanEncode()},
			{"a map keyed by an integer", JSON[map[int64]int]().CanEncode()},
			{"a string-kind map key declaring both text methods", JSON[map[settled]int]().CanEncode()},
			{"a pair held behind a pointer a map holds", JSON[map[string]*posted]().CanEncode()},
			{"a pair reached through a struct a map holds behind a pointer", JSON[map[string]*held]().CanEncode()},
			{"the same two positions, both of them legal", JSON[paired]().CanEncode()},
			{"a pair held in a slice a map holds", JSON[map[string][]posted]().CanEncode()},
			{"a type that writes itself as JSON and as text", JSON[priced]().CanEncode()},
			{"a field encoding/json is told to skip", JSON[ignored]().CanEncode()},
			{"two fields encoding/json is told to skip", JSON[dropped]().CanEncode()},
			{"an embedded struct with a JSON name of its own", JSON[labelled]().CanEncode()},
			{"a struct promoted through an embedded pointer to an exported type", JSON[struct{ *Stream }]().CanEncode()},
			{"the unexported one held in a named field instead", JSON[holdingDated]().CanEncode()},
			{"a type embedding a pointer to itself, which renders no promoted name", JSON[ring]().CanEncode()},
			{"an embedded marshalling type with no field beside it", JSON[itemised]().CanEncode()},
			{"the same type named rather than embedded", JSON[invoiced]().CanEncode()},
			{"a time.Time held in a named field", JSON[dueAt]().CanEncode()},
			{"the same pair with the unmarshaller on the pointer receiver", JSON[money]().CanEncode()},
			{"the same, held in a field", JSON[struct{ Amount money }]().CanEncode()},
			{"the same, held behind a pointer", JSON[struct{ Amount *money }]().CanEncode()},
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

	t.Run("what the shipped codec writes is frozen, because a fact log is", func(t *testing.T) {
		for _, sample := range []struct {
			what  string
			write func() ([]byte, error)
			want  string
		}{
			{"an integer", func() ([]byte, error) { return JSON[int]().Encode(42) }, "42"},
			{"a nil slice", func() ([]byte, error) { return JSON[[]int]().Encode(nil) }, "null"},
			{"a map", func() ([]byte, error) { return JSON[map[string]int]().Encode(map[string]int{"a": 1}) }, `{"a":1}`},
			{"raw JSON", func() ([]byte, error) { return JSON[json.RawMessage]().Encode(json.RawMessage(`{"q":1}`)) }, `{"q":1}`},
			{"a value-receiver marshaller", func() ([]byte, error) { return JSON[stamped]().Encode(stamped{}) }, `{"Meta":"stamped"}`},
			{"a slice element", func() ([]byte, error) {
				return JSON[[]creditedV2]().Encode([]creditedV2{{Minor: 1, Reason: "x"}})
			}, `[{"Minor":1,"Reason":"x"}]`},
			{"a marshaller on the pointer receiver", func() ([]byte, error) {
				return JSON[guarded]().Encode(guarded{fields: map[string]string{"owner": "acme"}})
			}, `{"owner":"acme"}`},
		} {
			written, err := sample.write()
			if err != nil {
				t.Fatalf("%s was refused by the codec that accepted it at declaration: %v", sample.what, err)
			}
			if string(written) != sample.want {
				t.Fatalf("%s now encodes as %s and every fact already recorded reads %s; the codec marshals a pointer so that the last row is not an empty object, and the other six say what that cost", sample.what, written, sample.want)
			}
		}
	})

	t.Run("a refusal names which asymmetry it found, because the three share one sentinel", func(t *testing.T) {
		for _, refused := range []struct {
			what   string
			answer error
			names  string
		}{
			{"a type that writes itself and declares no reader", JSON[cents]().CanEncode(), "declares no UnmarshalJSON"},
			{"a map key that writes itself and declares no reader", JSON[map[region]int]().CanEncode(), "declares no UnmarshalText"},
			{"a pair held where JSON cannot address it", JSON[map[string]posted]().CanEncode(), "cannot take its address"},
			{"a struct with fields and none encoding/json writes", JSON[withheld]().CanEncode(), "writes none of them"},
			{"two fields rendering one JSON name", JSON[collided]().CanEncode(), `render the JSON name "Reason"`},
			{"two embedded structs rendering one JSON name", JSON[twice]().CanEncode(), `render the JSON name "ID"`},
			{"one JSON name promoted from two depths", JSON[shadowed]().CanEncode(), "carried.dated.At and dated.At"},
			{"a tag that names the field beside it and carries an option", JSON[omitted]().CanEncode(), `render the JSON name "Reason"`},
			{"a tag naming no name, colliding with the field beside it", JSON[defaulted]().CanEncode(), `render the JSON name "Amount"`},
			{"a string-kind map key that writes itself and declares no reader", JSON[map[currency]int]().CanEncode(), "declares no UnmarshalText"},
			{"a map key that reads itself and is written as its kind", JSON[map[kept]int]().CanEncode(), "declares no MarshalText"},
			{"a struct embedding a type whose JSON pair it promotes", JSON[lined]().CanEncode(), "the MarshalJSON of the embedded event.money"},
			{"the textbook embedding", JSON[noted]().CanEncode(), "the MarshalJSON of the embedded time.Time"},
			{"a type whose unmarshaller is on the value receiver", JSON[sloppy]().CanEncode(), "declares UnmarshalJSON on the value receiver"},
			{"the same type held behind a pointer", JSON[struct{ Amount *sloppy }]().CanEncode(), "declares UnmarshalJSON on the value receiver"},
			{"a map key whose UnmarshalText is on the value receiver", JSON[map[casual]int]().CanEncode(), "UnmarshalText on the value receiver"},
			{"a struct promoted through an embedded pointer to an unexported type", JSON[pointed]().CanEncode(), "through the embedded pointer dated"},
		} {
			if refused.answer == nil {
				t.Fatalf("%s was accepted, so nothing here says which repair it needs", refused.what)
			}
			if !strings.Contains(refused.answer.Error(), refused.names) {
				t.Fatalf("%s was refused with %q, which never says %q; the repairs are a different one each and every case here would still be ErrCodecType through the arm beside it", refused.what, refused.answer, refused.names)
			}
		}
	})

	t.Run("what the shipped codec accepts, it records with its contents", func(t *testing.T) {
		aggregate := Define[[]string]("documents.document", func(id string) Key { return Compose(id) })
		written := Declare(aggregate, "documents.written", From(JSON[guarded]()),
			func(this []string, event guarded) []string { return append(this, event.fields["owner"]) })
		change := written.New("one", guarded{fields: map[string]string{"owner": "acme"}})
		if change.Err() != nil {
			t.Fatalf("a payload the codec accepted at declaration was refused at the decision: %v", change.Err())
		}
		state, err := aggregate.Fold("one", nil, change)
		if err != nil {
			t.Fatalf("the fold was refused: %v", err)
		}
		if len(state) != 1 || state[0] != "acme" {
			t.Fatalf("the recorded fact folded to %q: a marshaller on the pointer receiver that encoding/json was never able to reach writes an empty object, and every load of that stream then replays a zero value with no refusal at any door", state)
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

type refusingCredit struct {
	Codec[creditedV2]
	answer error
}

func (this refusingCredit) CanEncode() error { return this.answer }

type panickingCredit struct{ Codec[creditedV2] }

func (panickingCredit) CanEncode() error { panic("a codec that cannot answer") }

type panickingEncode struct{ Codec[opened] }

func (panickingEncode) Encode(opened) ([]byte, error) { panic("an encoder that cannot answer") }

type panickingDecode struct{ Codec[opened] }

func (panickingDecode) Decode([]byte) (opened, error) { panic("a decoder that cannot answer") }

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

	t.Run("a codec is asked at every revision, and the refusal names which one", func(t *testing.T) {
		unencodable := errors.New("a channel is not JSON")
		for _, malformed := range []struct {
			what  string
			chain Chain[creditedV2]
			names string
		}{
			{"a refusing codec at revision 1", From[creditedV2](refusingCredit{answer: unencodable}), "revision 1"},
			{"a refusing codec at revision 2", Then[creditedV1, creditedV2](From(JSON[creditedV1]()), refusingCredit{answer: unencodable}, toCreditedV2), "revision 2"},
			{"a panicking codec at revision 2", Then[creditedV1, creditedV2](From(JSON[creditedV1]()), panickingCredit{}, toCreditedV2), "revision 2"},
		} {
			aggregate := Define[account]("accounts.account", accountKey)
			_, err := TryDeclare(aggregate, "accounts.credited", malformed.chain, creditAccount)
			if !errors.Is(err, ErrCodecType) {
				t.Fatalf("%s answered %v, so a revision reads bytes through a codec that was never asked whether it can write them", malformed.what, err)
			}
			if !strings.Contains(err.Error(), malformed.names) {
				t.Fatalf("%s was refused with %q, which names no revision; a chain of several is refused once and the caller has to find which one", malformed.what, err)
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

func TestACodecPanicBecomesThatMethodsOwnRefusal(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	declare := func(t *testing.T, family string, codec Codec[opened]) (*Aggregate[account, accountID], *Fact[account, accountID, opened]) {
		t.Helper()
		aggregate := Define[account](family, accountKey)
		return aggregate, Declare(aggregate, "accounts.opened", From(codec), openAccount)
	}

	t.Run("an encoder that panics is the refusal its own error would have been", func(t *testing.T) {
		aggregate, opening := declare(t, "accounts.panicking.encode", panickingEncode{JSON[opened]()})
		change := opening.New(acme, opened{Owner: "acme"})
		if !errors.Is(change.Err(), ErrEncode) {
			t.Fatalf("an encoder's panic left the change carrying %v, so a decision nobody can encode either takes the process down or is minted as a fact", change.Err())
		}
		if CauseOf(change.Err()) != nil {
			t.Fatalf("a recovered panic value travelled as an application error in %v", change.Err())
		}
		state, err := aggregate.Fold(acme, account{Balance: 7}, change)
		if !errors.Is(err, ErrEncode) {
			t.Fatalf("the fold of a change that never encoded answered %v", err)
		}
		if state.Balance != 7 {
			t.Fatalf("a refused fold advanced the state to %d", state.Balance)
		}
	})

	t.Run("a decoder that panics is the refusal its own error would have been", func(t *testing.T) {
		aggregate, opening := declare(t, "accounts.panicking.decode", panickingDecode{JSON[opened]()})
		change := opening.New(acme, opened{Owner: "acme"})
		if change.Err() != nil {
			t.Fatalf("the encoder of the same codec refused the decision: %v", change.Err())
		}
		state, err := aggregate.Fold(acme, account{Balance: 7}, change)
		if !errors.Is(err, ErrPayload) {
			t.Fatalf("a decoder's panic answered %v, and the recorded bytes are what cannot be read", err)
		}
		if CauseOf(err) != nil {
			t.Fatalf("a recovered panic value travelled as an application error in %v", err)
		}
		if state.Balance != 7 {
			t.Fatalf("a refused fold advanced the state to %d", state.Balance)
		}
	})

	t.Run("the codec that answers is the control", func(t *testing.T) {
		aggregate, opening := declare(t, "accounts.answering", JSON[opened]())
		state, err := aggregate.Fold(acme, account{}, opening.New(acme, opened{Owner: "acme"}))
		if err != nil || len(state.Tags) != 1 {
			t.Fatalf("the same declaration over the shipped codec folded to %+v (%v), so the two cases above pass by refusing every decision", state, err)
		}
	})
}
