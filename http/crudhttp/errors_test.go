package crudhttp

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/shardit-io/vv/crud"
	"github.com/shardit-io/vv/errs"
)

// A fault is additive, and this is where that claim is made against a real crud
// sentinel: Status is the mapping every binding shares, and it must keep
// working on the day errs lands, before a single renderer is written
// ([[D-038]]).
//
// The mechanism's own tests live in errs, against a sentinel declared there.
// This one has to be here — errs is a package of the root module until the
// first tag, and go mod tidy counts test imports, so a crud import inside its
// test package would become errs requiring the root that requires errs
// ([[D-036]]).
func TestAFaultKeepsItsSentinelReachableThroughStatus(t *testing.T) {
	f := errs.Conflict().Op("Save").Entity("User").Code(errs.CodeUnique).
		Field("email").Code(errs.CodeUnique).
		Wrapping(crud.ErrConflict).
		Fault()

	if got := Status(f); got != http.StatusConflict {
		t.Fatalf("a fault wrapping crud.ErrConflict answered %d, not 409 — every binding that had not learned about faults would answer 500 for a duplicate key", got)
	}
	if got := Status(fmt.Errorf("saving user: %w", f)); got != http.StatusConflict {
		t.Fatalf("the same fault, wrapped once more by a service layer, answered %d", got)
	}
}

// The control. Without it the test above passes for a Status that answered 409
// to anything it did not recognise, and for one whose first arm swallowed
// everything.
func TestAFaultWrappingNoSentinelIsStillAnInternalError(t *testing.T) {
	novel := errs.Validation().Field("age").Code("too_young").Fault()
	if got := Status(novel); got != http.StatusInternalServerError {
		t.Fatalf("a fault wrapping nothing answered %d — a fault must not become a status by being a fault", got)
	}

	missing := errs.NotFound().Code(errs.CodeNotFound).Wrapping(crud.ErrNotFound).Fault()
	if got := Status(missing); got != http.StatusNotFound {
		t.Fatalf("a fault wrapping crud.ErrNotFound answered %d, not 404", got)
	}
}
