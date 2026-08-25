// Package versionstore is the model the generator had no case for: one that
// carries an optimistic lock.
//
// It exists because the version column was a real bug in the generated output
// and nothing in this tree could notice — every other model here is unversioned,
// so a DTO naming the lock would have panicked at Define time in a consumer's
// package and in none of ours. The test next door declares the repository the
// generated DTO implies, which is what turns "the generator knows about the
// lock" from a sentence into a measurement.
//
// It is generated with -adapter as well, so the inverse path map and its
// start-up refusal run under `make unit`.
package versionstore

//go:generate go run github.com/shardit-io/vv/cmd/vv -adapter -readonly ArchivedAt

import (
	"time"

	"github.com/shardit-io/vv/crud"
)

// A Document is a row two people edit at once, which is what the lock is for.
// The other columns are here to make the artefacts say something: Origin is
// insert-only, ArchivedAt is server-owned through a flag the model cannot carry,
// and CreatedAt is the database's.
type Document struct {
	ID         int64               `db:"id,pk,auto"`
	OwnerID    int64               `db:"owner_id"`
	Title      string              `db:"title"`
	Body       string              `db:"body"`
	Revision   int                 `db:"revision,version"`
	Origin     string              `db:"origin,immutable"`
	ArchivedAt crud.Opt[time.Time] `db:"archived_at"`
	CreatedAt  time.Time           `db:"created_at,generated"`
}
