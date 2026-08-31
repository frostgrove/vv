package porthttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

func DecodeJSON(r io.Reader, v any) error {
	if r == nil {
		return nil
	}
	_, err := DecodeJSONKeep(r, v)
	return err
}

const MaxBody = 4 << 20

const MaxKeptBody = 64 << 10

func DecodeJSONKeep(r io.Reader, v any) ([]byte, error) {
	return DecodeJSONKeepLimit(r, v, MaxBody)
}

func DecodeJSONKeepLimit(r io.Reader, v any, limit int) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = MaxBody
	}
	body, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, MalformedBody(err)
	}
	if len(body) > limit {
		return nil, TooLarge(limit)
	}
	if len(body) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(body, v); err != nil {
		return KeepBody(body), MalformedBody(err)
	}
	return KeepBody(body), nil
}

func KeepBody(b []byte) []byte {
	if len(b) == 0 || len(b) > MaxKeptBody {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func MalformedBody(err error) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		path := errs.Path(nil)
		if typeErr.Field != "" {
			path = errs.ParsePath(typeErr.Field)
		}
		return port.BadRequestAs(errs.CodeMalformedBody, path,
			"expected %s", wanted(typeErr.Type))
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return port.BadRequestAs(errs.CodeMalformedBody, nil,
			"the body is not valid JSON, at byte %d", syntaxErr.Offset)
	}

	return port.BadRequestAs(errs.CodeMalformedBody, nil, "%s", err)
}

func wanted(t reflect.Type) string {
	if t == nil {
		return "a different value"
	}
	switch t.Kind() {
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "a whole number"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.String:
		return "a string"
	case reflect.Slice, reflect.Array:
		return "a list"
	case reflect.Map, reflect.Struct:
		return "an object"
	default:
		return "a different value"
	}
}

func TooLarge(limit int) error {
	return errs.TooLarge().
		Code(errs.CodeTooLarge).
		Message(fmt.Sprintf("the request body is larger than the %d bytes this endpoint reads", limit)).
		Wrapping(port.ErrBadRequest).Fault()
}

type ctxKey int

const bodyKey ctxKey = iota

func WithBody(ctx context.Context, body []byte) context.Context {
	if len(body) == 0 {
		return ctx
	}
	return context.WithValue(ctx, bodyKey, body)
}

func BodyFrom(ctx context.Context) []byte {
	if ctx == nil {
		return nil
	}
	b, _ := ctx.Value(bodyKey).([]byte)
	return b
}

func WithLocale(ctx context.Context, locale string) context.Context {
	return port.WithLocale(ctx, locale)
}

func LocaleFrom(ctx context.Context) string { return port.LocaleFrom(ctx) }
