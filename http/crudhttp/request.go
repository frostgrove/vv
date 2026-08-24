package crudhttp

import (
	"encoding/json"
	"io"
	"reflect"

	"github.com/shardit-io/rx/query"
)

// BulkDeleteRequest is the body of POST /bulk-delete.
type BulkDeleteRequest[ID comparable] struct {
	IDs []ID `json:"ids"`
}

// CoerceID converts a path parameter to the repository's key type, which is why
// a uuid or a slug key works in a URL with no extra code.
func CoerceID[ID comparable](raw string) (ID, error) {
	var zero ID
	if raw == "" {
		return zero, BadRequestf("missing id")
	}
	v, err := query.Coerce(raw, reflect.TypeOf(zero))
	if err != nil {
		return zero, BadRequestf("%q is not a valid id", raw)
	}
	id, ok := v.(ID)
	if !ok {
		return zero, BadRequestf("%q is not a valid id", raw)
	}
	return id, nil
}

// NarrowForCount drops everything that means nothing to a COUNT. Leaving paging
// in would make the answer the size of one page rather than of the result.
func NarrowForCount(req *query.Request) {
	req.Page, req.Limit, req.Offset = 0, 0, 0
	req.Sort, req.Preload, req.Select = nil, nil, nil
}

// NarrowForEntity keeps only the shaping options. A single entity is addressed
// by its key, so a filter or a sort on the way to it is meaningless, and paging
// it would be a way to ask for row two of one row.
func NarrowForEntity(req *query.Request) {
	req.Filter, req.Terms, req.Search, req.Sort = query.Filter{}, nil, "", nil
	req.Page, req.Limit, req.Offset = 0, 0, 0
}

// DecodeJSON reads a JSON body onto v. An empty body is not an error: POST
// /count and POST /query both mean "no narrowing" when sent with no body.
//
// It decodes with encoding/json rather than a framework's binder because the
// binders validate: a `binding:"required"` tag on the consumer's model would
// start changing what the CRUD routes accept, and only under one transport.
func DecodeJSON(r io.Reader, v any) error {
	if r == nil {
		return nil
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return BadRequest(err)
	}
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return BadRequest(err)
	}
	return nil
}
