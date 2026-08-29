package port

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/errs"
)

// shipment is a model mounted straight onto the wire — the json tag is what the
// client sent — with one of everything the domain rule has to reason about: a
// database-generated key, an insert-only column, the optimistic lock and a
// column the database fills.
//
// Origin's tag is deliberately not any mechanical transform of its field name.
// A derivation that answered "origin" or "Origin" would look right in every
// other test here, and that one entry is what says the tag was read.
type shipment struct {
	ID        int64     `db:"id,pk,auto" json:"id"`
	Recipient string    `db:"recipient" json:"recipient"`
	Weight    int       `db:"weight" json:"weight,omitempty"`
	Origin    string    `db:"origin,immutable" json:"dispatchedFrom"`
	Revision  int       `db:"revision,version" json:"revision"`
	CreatedAt time.Time `db:"created_at,generated" json:"createdAt"`
}

// The map somebody would otherwise have typed, derived instead.
//
// Weight carries the option-stripping and Origin carries the proof that a tag
// was read at all. What is *absent* matters as much: the lock and the generated
// column both carry a json tag and neither gets an entry, because no request
// carries them and MustPathMap refuses a map that claims otherwise.
func TestADerivedMapIsTheOneSomebodyWouldHaveTyped(t *testing.T) {
	got, err := Paths[shipment]().Build()
	if err != nil {
		t.Fatalf("deriving the map: %v", err)
	}

	want := PathMap{
		"ID":        At("id"),
		"Recipient": At("recipient"),
		"Weight":    At("weight"),
		"Origin":    At("dispatchedFrom"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the derived map is %v, want %v", got, want)
	}
}

// `json:",omitempty"` keeps the Go field name as the key — that is what the
// decoder does with it, so it is what the map has to say. It is not the
// OrFieldName fallback arriving by the back door: the test below shows a field
// with no tag at all is still refused here.
func TestATagWithOptionsAndNoNameMeansTheFieldName(t *testing.T) {
	type crate struct {
		ID       int64  `db:"id,pk,auto" json:"id"`
		Contents string `db:"contents" json:",omitempty"`
	}

	got, err := Paths[crate]().Build()
	if err != nil {
		t.Fatalf("deriving the map: %v", err)
	}
	if want := At("Contents"); !reflect.DeepEqual(got["Contents"], want) {
		t.Fatalf("a tag naming no key answered %v, want %v — that is what encoding/json does with it", got["Contents"], want)
	}
}

// A column no tag names is a refusal naming the column, not a key invented from
// the field name. The failure that rule prevents is a map that looks complete
// and reports "SourceFilename" where the wire says "sourceFilename" — nothing
// tells anybody until a violation lands in a production error body.
func TestAColumnNoTagNamesIsRefusedRatherThanGuessed(t *testing.T) {
	type pallet struct {
		ID     int64 `db:"id,pk,auto"`
		Height int   `db:"height"`
	}

	_, err := Paths[pallet]().Build()
	if err == nil {
		t.Fatal("a model with no wire tags derived a map; every key in it would be a guess")
	}
	for _, name := range []string{"pallet", "ID", "Height", "json"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the refusal does not name %q: %v", name, err)
		}
	}

	// The control: the same model, with the consumer stating that the field
	// name *is* the key. Without this the test above would pass on a builder
	// that could never derive anything.
	got, err := Paths[pallet]().OrFieldName().Build()
	if err != nil {
		t.Fatalf("OrFieldName did not rescue an untagged model: %v", err)
	}
	want := PathMap{"ID": At("ID"), "Height": At("Height")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OrFieldName derived %v, want %v", got, want)
	}
}

// A tag that says `-` is the author stating the column is not on the wire, and
// that is a different answer from saying nothing. OrFieldName covers silence;
// it must not cover this, because a key for a column the client never sends
// points a violation at something it cannot find in its own body.
func TestAColumnTakenOffTheWireIsNotRescuedByTheFieldNameFallback(t *testing.T) {
	type strongbox struct {
		ID   int64  `db:"id,pk,auto" json:"id"`
		Seal string `db:"seal" json:"-"`
	}

	if _, err := Paths[strongbox]().Build(); err == nil || !strings.Contains(err.Error(), "Seal") {
		t.Fatalf("a column tagged `-` was given a key: %v", err)
	}

	// The distinction under test: the same call that rescues an untagged model
	// leaves this one refused.
	_, err := Paths[strongbox]().OrFieldName().Build()
	if err == nil {
		t.Fatal("OrFieldName gave a key to a column the tag says is not on the wire")
	}
	if !strings.Contains(err.Error(), "Except") {
		t.Fatalf("the refusal does not say how to resolve it: %v", err)
	}

	// Both cures, because a refusal with no way out is a feature nobody can
	// adopt: the column is either genuinely off the wire, or it arrives under a
	// name the tag does not give.
	excluded, err := Paths[strongbox]().Except("Seal").Build()
	if err != nil {
		t.Fatalf("Except did not drop the column: %v", err)
	}
	if _, entered := excluded["Seal"]; entered {
		t.Fatalf("an excluded column still got an entry: %v", excluded)
	}
	named, err := Paths[strongbox]().Override(PathMap{"Seal": At("seal")}).Build()
	if err != nil {
		t.Fatalf("Override did not name the column: %v", err)
	}
	if !reflect.DeepEqual(named["Seal"], At("seal")) {
		t.Fatalf("the override answered %v", named["Seal"])
	}
}

// The tag list is preference and not a merge: the first tag that names a field
// wins, and a field only the second one names still gets a key.
func TestTheFirstTagThatNamesAColumnWins(t *testing.T) {
	type consignment struct {
		ID     int64  `db:"id,pk,auto" json:"id" form:"id"`
		Note   string `db:"note" form:"note"`
		Weight int    `db:"weight" json:"weight" form:"mass"`
	}

	got, err := Paths[consignment]().From("json", "form").Build()
	if err != nil {
		t.Fatalf("deriving from two tags: %v", err)
	}
	want := PathMap{"ID": At("id"), "Note": At("note"), "Weight": At("weight")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the derived map is %v, want %v", got, want)
	}

	// The control: reverse the order and the column carrying both changes its
	// answer. Without it the test above would pass on a builder that read only
	// whichever tag happened to be first in the struct.
	reversed, err := Paths[consignment]().From("form", "json").Build()
	if err != nil {
		t.Fatalf("deriving from two tags in the other order: %v", err)
	}
	if !reflect.DeepEqual(reversed["Weight"], At("mass")) {
		t.Fatalf("reversing the tag order answered %v for Weight, want the form key", reversed["Weight"])
	}
}

// Any tag key at all, because a house convention is a tag somebody chose and
// this has no business holding a list of the acceptable ones.
func TestAnyTagKeyCanBeTheSource(t *testing.T) {
	type ledger struct {
		ID     int64  `db:"id,pk,auto" my_custom_tag:"id"`
		Amount string `db:"amount" my_custom_tag:"amount"`
	}

	got, err := Paths[ledger]().From("my_custom_tag").Build()
	if err != nil {
		t.Fatalf("deriving from a custom tag: %v", err)
	}
	want := PathMap{"ID": At("id"), "Amount": At("amount")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the derived map is %v, want %v", got, want)
	}
}

// An override replaces what the tag says, and is validated like anything else:
// deriving the rest of the map does not make the one hand-written entry
// unchecked.
func TestAnOverrideBeatsTheTagAndIsStillChecked(t *testing.T) {
	got, err := Paths[shipment]().Override(PathMap{"Origin": At("file")}).Build()
	if err != nil {
		t.Fatalf("overriding one entry: %v", err)
	}
	if !reflect.DeepEqual(got["Origin"], At("file")) {
		t.Fatalf("the override answered %v, want the path it named", got["Origin"])
	}
	if !reflect.DeepEqual(got["Recipient"], At("recipient")) {
		t.Fatalf("an override changed a column it did not name: %v", got["Recipient"])
	}

	// A misspelled override is the mistake this whole feature otherwise makes
	// easier: the map is derived, so the one line somebody typed is the only
	// one that can be wrong, and it silently does nothing without this.
	if _, err := Paths[shipment]().Override(PathMap{"Orign": At("file")}).Build(); err == nil ||
		!strings.Contains(err.Error(), "Orign") {
		t.Fatalf("an override naming no column was accepted: %v", err)
	}

	// The other direction: a real column that no request carries. An entry for
	// it translates a violation to a key the client cannot find, which is the
	// same wrong answer as a missing one ([[D-050]]).
	if _, err := Paths[shipment]().Override(PathMap{"CreatedAt": At("createdAt")}).Build(); err == nil ||
		!strings.Contains(err.Error(), "CreatedAt") {
		t.Fatalf("an override for a generated column was accepted: %v", err)
	}
}

// Excepting a column and overriding it are opposite instructions. Resolving one
// in favour of the other would make half of what somebody wrote do nothing.
func TestOverridingAndExceptingTheSameColumnIsRefused(t *testing.T) {
	_, err := Paths[shipment]().
		Except("Origin").
		Override(PathMap{"Origin": At("file")}).
		Build()
	if err == nil || !strings.Contains(err.Error(), "Origin") {
		t.Fatalf("a column both excluded and overridden was accepted: %v", err)
	}
}

// A promoted column is one column to crud, so its tag has to be found through
// however many embeddings it sits behind.
func TestAPromotedColumnCarriesTheTagOfTheFieldItIs(t *testing.T) {
	type stamped struct {
		CreatedBy string `db:"created_by" json:"createdBy"`
	}
	type tracked struct {
		stamped
		TrackingNo string `db:"tracking_no" json:"trackingNo"`
	}
	type manifest struct {
		tracked
		ID int64 `db:"id,pk,auto" json:"id"`
	}

	got, err := Paths[manifest]().Build()
	if err != nil {
		t.Fatalf("deriving through two embeddings: %v", err)
	}
	want := PathMap{"ID": At("id"), "TrackingNo": At("trackingNo"), "CreatedBy": At("createdBy")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the derived map is %v, want %v", got, want)
	}
}

// Only a column may lend a tag to a column.
//
// A field crud is told to ignore, and a relation, can each share a name with a
// promoted column without crud objecting — it never mapped the other one. Both
// carry a tag of their own, and reading it would answer a key from a field that
// is not the column, which is the silent wrong answer this whole type exists to
// avoid.
func TestOnlyAColumnLendsItsTagToAColumn(t *testing.T) {
	type stamped struct {
		Label string `db:"label" json:"label"`
	}
	type ignored struct {
		stamped
		ID    int64  `db:"id,pk,auto" json:"id"`
		Label string `db:"-" json:"scratch"`
	}

	got, err := Paths[ignored]().Build()
	if err != nil {
		t.Fatalf("deriving past an ignored field: %v", err)
	}
	if !reflect.DeepEqual(got["Label"], At("label")) {
		t.Fatalf("the column took the tag of the `db:\"-\"` field that shadows it: %v", got["Label"])
	}

	type line struct {
		ID    int64 `db:"id,pk,auto" json:"id"`
		BoxID int64 `db:"box_id" json:"boxId"`
	}
	type manifested struct {
		Items string `db:"items" json:"itemsColumn"`
	}
	// The relation is the *outer* field and the column is promoted from below,
	// so shallowest-wins would hand the column the relation's tag. That is the
	// arrangement under test; the other way round it never gets the chance.
	type carried struct {
		manifested
		ID    int64  `db:"id,pk,auto" json:"id"`
		Items []line `rel:"has_many,fk=box_id" json:"items"`
	}

	related, err := Paths[carried]().Build()
	if err != nil {
		t.Fatalf("deriving past a relation: %v", err)
	}
	if !reflect.DeepEqual(related["Items"], At("itemsColumn")) {
		t.Fatalf("the column took the tag of the relation that shares its name: %v", related["Items"])
	}
}

// The result is a PathMap and behaves like one: a declared head is rewritten
// with its tail kept, and an undeclared head declines rather than passing
// through, because a derived map is total for the same reason a generated one
// is ([[D-050]]).
func TestADerivedMapResolvesLikeTheHandWrittenOne(t *testing.T) {
	m, err := Paths[shipment]().Build()
	if err != nil {
		t.Fatal(err)
	}

	got, ok := m.Resolve(errs.Path{errs.Named("Origin"), errs.Named("Country")})
	want := errs.Path{errs.Named("dispatchedFrom"), errs.Named("Country")}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("a declared head resolved to %v, %v, want %v", got, ok, want)
	}
	if _, ok := m.Resolve(errs.Path{errs.Named("Revision")}); ok {
		t.Fatal("a derived map accepted a head it has no entry for; a total map that accepts one is guessing")
	}
}

// Both misuses of the builder itself are held until Build, where the message
// can name the model. A method that panicked would name the builder and the
// stack, and the model is what has to change.
func TestABuilderMisuseIsAnswerLater(t *testing.T) {
	if _, err := Paths[shipment]().From().Build(); err == nil {
		t.Fatal("From with no tag was accepted; the map would be derived from nothing")
	}
	if _, err := Paths[shipment]().From("json", "  ").Build(); err == nil {
		t.Fatal("From with an empty tag name was accepted")
	}
}

// MustBuild is the package-level declaration, so what Build reports as an error
// has to stop the process ([[D-021]]).
func TestMustBuildRefusesAtDeclaration(t *testing.T) {
	type pallet struct {
		ID     int64 `db:"id,pk,auto"`
		Height int   `db:"height"`
	}

	defer func() {
		if recover() == nil {
			t.Fatal("MustBuild returned for a model whose map cannot be derived")
		}
	}()
	_ = Paths[pallet]().MustBuild()
}
