package crudhttp

import (
	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/port"
)

type BulkDeleteRequest[ID comparable] struct {
	IDs []ID `json:"ids"`
}

func CoerceID[ID comparable](raw string) (ID, error) { return port.CoerceID[ID](raw) }

func NarrowForCount(request *query.Request) { port.NarrowForCount(request) }

func NarrowForEntity(request *query.Request) { port.NarrowForEntity(request) }
