package port

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/errs"
)

type shipment struct {
	ID        int64     `db:"id,pk,auto" json:"id"`
	Recipient string    `db:"recipient" json:"recipient"`
	Weight    int       `db:"weight" json:"weight,omitempty"`
	Origin    string    `db:"origin,immutable" json:"dispatchedFrom"`
	Revision  int       `db:"revision,version" json:"revision"`
	CreatedAt time.Time `db:"created_at,generated" json:"createdAt"`
}

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

	got, err := Paths[pallet]().OrFieldName().Build()
	if err != nil {
		t.Fatalf("OrFieldName did not rescue an untagged model: %v", err)
	}
	want := PathMap{"ID": At("ID"), "Height": At("Height")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OrFieldName derived %v, want %v", got, want)
	}
}

func TestAColumnTakenOffTheWireIsNotRescuedByTheFieldNameFallback(t *testing.T) {
	type strongbox struct {
		ID   int64  `db:"id,pk,auto" json:"id"`
		Seal string `db:"seal" json:"-"`
	}

	if _, err := Paths[strongbox]().Build(); err == nil || !strings.Contains(err.Error(), "Seal") {
		t.Fatalf("a column tagged `-` was given a key: %v", err)
	}

	_, err := Paths[strongbox]().OrFieldName().Build()
	if err == nil {
		t.Fatal("OrFieldName gave a key to a column the tag says is not on the wire")
	}
	if !strings.Contains(err.Error(), "Except") {
		t.Fatalf("the refusal does not say how to resolve it: %v", err)
	}

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

	reversed, err := Paths[consignment]().From("form", "json").Build()
	if err != nil {
		t.Fatalf("deriving from two tags in the other order: %v", err)
	}
	if !reflect.DeepEqual(reversed["Weight"], At("mass")) {
		t.Fatalf("reversing the tag order answered %v for Weight, want the form key", reversed["Weight"])
	}
}

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

	if _, err := Paths[shipment]().Override(PathMap{"Orign": At("file")}).Build(); err == nil ||
		!strings.Contains(err.Error(), "Orign") {
		t.Fatalf("an override naming no column was accepted: %v", err)
	}

	if _, err := Paths[shipment]().Override(PathMap{"CreatedAt": At("createdAt")}).Build(); err == nil ||
		!strings.Contains(err.Error(), "CreatedAt") {
		t.Fatalf("an override for a generated column was accepted: %v", err)
	}
}

func TestOverridingAndExceptingTheSameColumnIsRefused(t *testing.T) {
	_, err := Paths[shipment]().
		Except("Origin").
		Override(PathMap{"Origin": At("file")}).
		Build()
	if err == nil || !strings.Contains(err.Error(), "Origin") {
		t.Fatalf("a column both excluded and overridden was accepted: %v", err)
	}
}

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

func TestABuilderMisuseIsAnswerLater(t *testing.T) {
	if _, err := Paths[shipment]().From().Build(); err == nil {
		t.Fatal("From with no tag was accepted; the map would be derived from nothing")
	}
	if _, err := Paths[shipment]().From("json", "  ").Build(); err == nil {
		t.Fatal("From with an empty tag name was accepted")
	}
}

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
