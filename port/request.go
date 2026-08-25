package port

import (
	"encoding"
	"fmt"
	"reflect"
	"time"

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

// FormatID renders a key as the text a URL path or a request document carries.
// It is [CoerceID]'s inverse, and the pair is what makes a key survive a round
// trip through a transport that only speaks strings.
//
// The order matches CoerceID's arm for arm. encoding.TextMarshaler first,
// because CoerceID tries encoding.TextUnmarshaler first, and a uuid or an enum
// key that parses with its own rules has to render with them. time.Time before
// that, for the same reason CoerceID puts it there.
//
// A type with neither falls through to fmt.Sprint, which is right for the
// numbers and strings that are almost every primary key and is the wrong answer
// for a struct — but a struct that is a key and cannot spell itself as text was
// never going to survive CoerceID either, and the failure is visible in the URL
// rather than silent.
func FormatID[ID comparable](id ID) string {
	switch v := any(id).(type) {
	case string:
		return v
	case time.Time:
		return v.Format(time.RFC3339Nano)
	case encoding.TextMarshaler:
		b, err := v.MarshalText()
		if err == nil {
			return string(b)
		}
	}
	return fmt.Sprint(id)
}
