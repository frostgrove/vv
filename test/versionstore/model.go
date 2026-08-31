package versionstore

//go:generate go run github.com/frostgrove/vv/cmd/vv -adapter -readonly ArchivedAt

import (
	"time"

	"github.com/frostgrove/vv/crud"
)

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
