package port

import (
	"encoding"
	"fmt"
	"reflect"
	"time"

	"github.com/frostgrove/vv/crud/query"
	"github.com/frostgrove/vv/errs"
)

func requestCopy(request *query.Request) *query.Request {
	if request == nil {
		return &query.Request{}
	}
	copy := *request
	return &copy
}

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

func NarrowForCount(request *query.Request) {
	request.Page, request.Limit, request.Offset = 0, 0, 0
	request.Sort, request.Preload, request.Select = nil, nil, nil
	request.ClearCursors()
	request.Unpaged, request.SkipTotal, request.Distinct = false, false, false
	request.OmitPaging()
}

func NarrowForEntity(request *query.Request) {
	request.Sort = nil
	request.Page, request.Limit, request.Offset = 0, 0, 0
	request.ClearCursors()
	request.Unpaged, request.SkipTotal, request.Distinct = false, false, false
	request.OmitPaging()
}

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
