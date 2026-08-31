package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/frostgrove/vv/crud/query"
)

type Method string

const (
	MethodList       Method = "List"
	MethodCount      Method = "Count"
	MethodGet        Method = "Get"
	MethodCreate     Method = "Create"
	MethodUpdate     Method = "Update"
	MethodReplace    Method = "Replace"
	MethodDelete     Method = "Delete"
	MethodBulkDelete Method = "BulkDelete"
)

type Call struct {
	Method Method

	ID string

	IDs json.RawMessage

	Query *query.Request

	Body json.RawMessage
}

type Transport interface {
	Do(ctx context.Context, call *Call) (json.RawMessage, error)
}

type ProtocolError struct {
	Method Method

	Where string

	Status string

	Body string
}

var ErrPartialResult = errors.New("remote: GetAll received a partial result")

type PartialResultError struct {
	Received int
	Total    int64
}

func (this *PartialResultError) Error() string {
	return fmt.Sprintf("%v: received %d of %d rows", ErrPartialResult, this.Received, this.Total)
}

func (this *PartialResultError) Unwrap() error { return ErrPartialResult }

func (this *ProtocolError) Error() string {
	s := "remote: " + string(this.Method) + " " + this.Where + " answered " + this.Status +
		", which is not this library speaking"
	if this.Body != "" {
		s += ": " + this.Body
	}
	return s
}

func Truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
