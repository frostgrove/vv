package port

import (
	"reflect"

	"github.com/shardit-io/vv/errs"
	"github.com/shardit-io/vv/query"
)

// CoerceID converts a path parameter to the repository's key type, which is why
// a uuid or a slug key works in a URL with no extra code.
func CoerceID[ID comparable](raw string) (ID, error) {
	var zero ID
	if raw == "" {
		return zero, BadRequestAs(errs.CodeInvalidID, nil, "missing id")
	}
	v, err := query.Coerce(raw, reflect.TypeOf(zero))
	if err != nil {
		return zero, BadRequestAs(errs.CodeInvalidID, nil, "%q is not a valid id", raw)
	}
	id, ok := v.(ID)
	if !ok {
		return zero, BadRequestAs(errs.CodeInvalidID, nil, "%q is not a valid id", raw)
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
