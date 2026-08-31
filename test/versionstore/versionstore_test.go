package versionstore_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/test/versionstore"
)

func TestTheGeneratedDeclarationForAVersionedModelIsAccepted(t *testing.T) {
	if _, err := sqlrepo.TryDefine[versionstore.Document, int64, versionstore.DocumentUpdate]("documents"); err != nil {
		t.Fatalf("the generated DTO was refused at declaration: %v", err)
	}

	type withLock struct {
		Title    *string
		Revision *int
	}
	if _, err := sqlrepo.TryDefine[versionstore.Document, int64, withLock]("documents"); err == nil {
		t.Fatal("a DTO naming the version column was accepted; the generator's omission then proves nothing")
	}
}

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

	var hop errs.Resolver = versionstore.DocumentMapper{}
	path, ok := hop.Resolve(errs.Path{errs.Named("OwnerID")})
	if !ok || !reflect.DeepEqual(path, errs.Path{errs.Named("ownerID")}) {
		t.Fatalf("the inverse answered %v, %v, want the key the input carries", path, ok)
	}

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
