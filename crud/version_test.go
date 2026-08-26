package crud_test

import (
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
)

// A `version` column is an optimistic lock, and every part of that has to be
// true at declaration time or it is not one: the repository must own the column,
// it must be able to add one to it, and there must be exactly one.

type versioned struct {
	ID      int64  `db:"id,pk,auto"`
	Title   string `db:"title"`
	Version int    `db:"version,version"`
}

func TestAVersionColumnIsOwnedByTheRepository(t *testing.T) {
	s := crud.MustSchemaOf[versioned]()

	if s.Version == nil || s.Version.Name != "Version" {
		t.Fatalf("the version column was not recognised: %+v", s.Version)
	}
	// It is written on INSERT — a row has to start somewhere.
	if !contains(s.Insert, "Version") {
		t.Fatal("the version column is not written on insert, so a new row has no version to check against")
	}
	// And it is kept out of the columns an upsert overwrites, so a Save built
	// from a model somebody has been holding cannot wind the counter back.
	if contains(s.Update, "Version") {
		t.Fatal("the version column is in the upsert's SET list: a stale Save could lower it")
	}
}

func TestADeclarationThatCannotBeALockIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func() (*crud.Schema, error)
		want  string
	}{
		{"a version on the primary key", func() (*crud.Schema, error) {
			type m struct {
				ID    int64  `db:"id,pk,version"`
				Title string `db:"title"`
			}
			return crud.SchemaOf[m]()
		}, "primary key"},

		{"a version that is also immutable", func() (*crud.Schema, error) {
			type m struct {
				ID      int64 `db:"id,pk,auto"`
				Version int   `db:"version,version,immutable"`
			}
			return crud.SchemaOf[m]()
		}, "advance"},

		{"a version that is also generated", func() (*crud.Schema, error) {
			type m struct {
				ID      int64 `db:"id,pk,auto"`
				Version int   `db:"version,version,generated"`
			}
			return crud.SchemaOf[m]()
		}, "generated"},

		{"a version that is not a number", func() (*crud.Schema, error) {
			type m struct {
				ID      int64  `db:"id,pk,auto"`
				Version string `db:"version,version"`
			}
			return crud.SchemaOf[m]()
		}, "integer"},

		{"two versions", func() (*crud.Schema, error) {
			type m struct {
				ID int64 `db:"id,pk,auto"`
				A  int   `db:"a,version"`
				B  int   `db:"b,version"`
			}
			return crud.SchemaOf[m]()
		}, "only one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.build()
			if err == nil {
				t.Fatal("the declaration was accepted, and the lock it promises would never fire")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to explain %q", err, tc.want)
			}
		})
	}
}

// The version is the repository's to advance. A DTO that offers to set it is a
// caller who can set their own lock value, which is no lock at all — so the
// mistake is refused where every other mapping mistake is, at start-up.
func TestAnUpdateDTOCannotSetTheVersion(t *testing.T) {
	type dto struct {
		Title   *string
		Version *int
	}
	_, err := crud.PlanFor[dto](crud.MustSchemaOf[versioned]())
	if err == nil {
		t.Fatal("an update DTO was allowed to write the version column")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Fatalf("err = %v, want it to name the version column", err)
	}
}

func contains(fields []*crud.Field, name string) bool {
	for _, f := range fields {
		if f.Name == name {
			return true
		}
	}
	return false
}
