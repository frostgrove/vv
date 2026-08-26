package crudhttp

import (
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/port"
)

// BulkDeleteRequest is the body of POST /bulk-delete.
type BulkDeleteRequest[ID comparable] struct {
	IDs []ID `json:"ids"`
}

// CoerceID converts a path parameter to the repository's key type, which is why
// a uuid or a slug key works in a URL with no extra code. The compatibility hop
// over port.CoerceID.
func CoerceID[ID comparable](raw string) (ID, error) { return port.CoerceID[ID](raw) }

// NarrowForCount drops everything that means nothing to a COUNT. The service
// applies it now; this is the compatibility hop over port.NarrowForCount.
func NarrowForCount(req *query.Request) { port.NarrowForCount(req) }

// NarrowForEntity keeps only the shaping options. The service applies it now;
// this is the compatibility hop over port.NarrowForEntity.
func NarrowForEntity(req *query.Request) { port.NarrowForEntity(req) }
