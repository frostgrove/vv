package versionstore_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/test/versionstore"
)

// This file exists so the package is linked and its init runs. A package with
// no test file reports "[no test files]" and never builds a binary, so the
// start-up refusals the generated file carries — MustPathMap and
// MustCoverUpdate — would never execute and the model would prove nothing.

// The half UC-014 owed: the declaration the generated DTO implies is one
// sqlrepo.Define accepts, for a model that carries a lock.
func TestTheGeneratedDeclarationForAVersionedModelIsAccepted(t *testing.T) {
	if _, err := sqlrepo.TryDefine[versionstore.Document, int64, versionstore.DocumentUpdate]("documents"); err != nil {
		t.Fatalf("the generated DTO was refused at declaration: %v", err)
	}

	// The control. Name the lock and the declaration is refused, which is why
	// the generator has to leave it out rather than merely happen to.
	type withLock struct {
		Title    *string
		Revision *int
	}
	if _, err := sqlrepo.TryDefine[versionstore.Document, int64, withLock]("documents"); err == nil {
		t.Fatal("a DTO naming the version column was accepted; the generator's omission then proves nothing")
	}
}

// The wire shapes, field by field. Each column is here for one reason and the
// two artefacts disagree about three of them on purpose.
func TestTheGeneratedWireShapesLeaveOutWhatTheClientDoesNotOwn(t *testing.T) {
	input := fieldsOf[versionstore.DocumentInput]()
	update := fieldsOf[versionstore.DocumentUpdate]()

	for _, tc := range []struct {
		field      string
		wantInput  bool
		wantUpdate bool
		because    string
	}{
		{field: "Title", wantInput: true, wantUpdate: true,
			because: "an ordinary column is in both, or the assertions below measure an empty struct"},
		{field: "ID", wantInput: false, wantUpdate: false,
			because: "an auto key is assigned by the database and belongs in neither client body"},
		{field: "Origin", wantInput: true, wantUpdate: false,
			because: "`immutable` is insert-only: settable on create, refused on update"},
		{field: "Revision", wantInput: false, wantUpdate: false,
			because: "the lock is the repository's; no client sends it"},
		{field: "CreatedAt", wantInput: false, wantUpdate: false,
			because: "`generated` is the database's"},
		{field: "ArchivedAt", wantInput: false, wantUpdate: false,
			because: "-readonly named it, and the flag applies to both bodies"},
	} {
		if input[tc.field] != tc.wantInput {
			t.Errorf("DocumentInput has %s = %v, want %v: %s", tc.field, input[tc.field], tc.wantInput, tc.because)
		}
		if update[tc.field] != tc.wantUpdate {
			t.Errorf("DocumentUpdate has %s = %v, want %v: %s", tc.field, update[tc.field], tc.wantUpdate, tc.because)
		}
	}
}

// The mapper and its inverse are one artefact read in two directions, so they
// are asserted together: the key the mapper read a value out of is the key the
// map answers for the column.
func TestTheGeneratedMapperAndItsInverseAgree(t *testing.T) {
	in := versionstore.DocumentInput{OwnerID: 9, Title: "notes", Body: "…", Origin: "import"}
	got, err := versionstore.DocumentMapper{}.Model(context.Background(), in)
	if err != nil {
		t.Fatalf("the mapper refused an ordinary input: %v", err)
	}
	want := versionstore.Document{OwnerID: 9, Title: "notes", Body: "…", Origin: "import"}
	if got != want {
		t.Fatalf("the mapper produced %+v, want %+v", got, want)
	}

	// The inverse, through the interface the render layer actually reaches it
	// by. A mapper that did not satisfy errs.Resolver would be wired behind the
	// raw-body fallback instead of ahead of it, silently.
	var hop errs.Resolver = versionstore.DocumentMapper{}
	path, ok := hop.Resolve(errs.Path{errs.Named("OwnerID")})
	if !ok || !reflect.DeepEqual(path, errs.Path{errs.Named("ownerID")}) {
		t.Fatalf("the inverse answered %v, %v, want the key the input carries", path, ok)
	}

	// The control: a column the client never sends declines rather than being
	// invented. Without it a resolver that echoed every path would pass above.
	if _, ok := hop.Resolve(errs.Path{errs.Named("Revision")}); ok {
		t.Fatal("the inverse claimed a path for the version column, which no request carries")
	}
}

func fieldsOf[T any]() map[string]bool {
	var zero T
	t := reflect.TypeOf(&zero).Elem()
	out := make(map[string]bool, t.NumField())
	for i := range t.NumField() {
		out[t.Field(i).Name] = true
	}
	return out
}
