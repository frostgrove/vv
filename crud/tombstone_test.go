package crud_test

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/utils"
)

type lifecycleRecord struct {
	ID        string               `db:"id,pk"`
	Title     string               `db:"title"`
	Digest    string               `db:"digest,serverowned"`
	DeletedAt utils.Opt[time.Time] `db:"deleted_at,serverowned,tombstone"`
}

func TestServerOwnedTombstoneFreezesEveryGenericWriteShape(t *testing.T) {
	s, err := crud.SchemaOf[lifecycleRecord]()
	if err != nil {
		t.Fatal(err)
	}
	if s.Tombstone == nil || s.Tombstone.Name != "DeletedAt" || !s.Tombstone.ServerOwned {
		t.Fatalf("tombstone = %+v", s.Tombstone)
	}
	for _, fields := range [][]*crud.Field{s.Insert, s.InsertGen, s.Update} {
		for _, field := range fields {
			if field.Name == "DeletedAt" || field.Name == "Digest" {
				t.Fatalf("server-owned field %s is in a generic write plan: %+v", field.Name, fields)
			}
		}
	}

	type maliciousPatch struct {
		DeletedAt utils.Opt[time.Time]
	}
	if _, err := crud.PlanFor[maliciousPatch](s); err == nil || !strings.Contains(err.Error(), "tombstone") {
		t.Fatalf("PlanFor(tombstone patch) = %v", err)
	}
	type serverPatch struct{ Digest *string }
	if _, err := crud.PlanFor[serverPatch](s); err == nil || !strings.Contains(err.Error(), "serverowned") {
		t.Fatalf("PlanFor(server-owned patch) = %v", err)
	}
}

func TestTombstoneMetadataIsCompleteAndUnambiguous(t *testing.T) {
	t.Run("nullable", func(t *testing.T) {
		type bad struct {
			ID      string    `db:"id,pk"`
			Deleted time.Time `db:"deleted_at,tombstone"`
		}
		if _, err := crud.SchemaOf[bad](); err == nil || !strings.Contains(err.Error(), "nullable") {
			t.Fatalf("SchemaOf(non-null tombstone) = %v", err)
		}
	})

	t.Run("single", func(t *testing.T) {
		type bad struct {
			ID       string     `db:"id,pk"`
			Deleted  *time.Time `db:"deleted_at,tombstone"`
			Archived *time.Time `db:"archived_at,tombstone"`
		}
		if _, err := crud.SchemaOf[bad](); err == nil || !strings.Contains(err.Error(), "only one") {
			t.Fatalf("SchemaOf(two tombstones) = %v", err)
		}
	})

	t.Run("timestamp compatible", func(t *testing.T) {
		type stringTombstone struct {
			ID      string  `db:"id,pk"`
			Deleted *string `db:"deleted_at,tombstone"`
		}
		if _, err := crud.SchemaOf[stringTombstone](); err == nil || !strings.Contains(err.Error(), "time.Time") {
			t.Fatalf("SchemaOf(*string tombstone) = %v", err)
		}

		type integerTombstone struct {
			ID      string         `db:"id,pk"`
			Deleted utils.Opt[int] `db:"deleted_at,tombstone"`
		}
		if _, err := crud.SchemaOf[integerTombstone](); err == nil || !strings.Contains(err.Error(), "time.Time") {
			t.Fatalf("SchemaOf(Opt[int] tombstone) = %v", err)
		}

		type scannerTombstone struct {
			ID      string       `db:"id,pk"`
			Deleted sql.NullTime `db:"deleted_at,tombstone"`
		}
		if _, err := crud.SchemaOf[scannerTombstone](); err != nil {
			t.Fatalf("SchemaOf(sql.NullTime tombstone) = %v", err)
		}
	})
}
