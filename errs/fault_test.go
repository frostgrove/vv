package errs_test

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/errs"
)

// The sentinels are declared here rather than imported from crud on purpose.
// What is being pinned is the mechanism — a fault wraps, so errors.Is walks
// through it — and the mechanism does not care whose sentinel it is. The
// version against a real crud sentinel lives in port/porthttp/errors_test.go,
// where the status mapping it has to keep working is; putting it here would put
// a first-party import into this package's test dependencies, and at the first
// tag that becomes errs requiring the root module that requires errs
// ([[D-036]]).
var (
	errSentinel = errors.New("test: conflict")
	errOther    = errors.New("test: not found")
	errThird    = errors.New("test: forbidden")
)

// driverErr is the shape a driver error has, modelled on *pgconn.PgError and
// *mysql.MySQLError: every field exported, and a message that names things a
// client must never read. The exported fields are the point — a driver error
// whose fields were unexported would marshal to {} and a leak test over it
// would pass for nothing.
type driverErr struct {
	Code           string
	Message        string
	Detail         string
	ConstraintName string
	TableName      string
	SchemaName     string
}

func (this *driverErr) Error() string { return this.Message }

func TestAFaultWrappingASentinelMatchesIt(t *testing.T) {
	drv := unique()
	f := errs.Conflict().Code(errs.CodeUnique).Wrapping(errSentinel, drv).Fault()

	if !errors.Is(f, errSentinel) {
		t.Fatalf("the sentinel the fault wraps is no longer reachable: a caller's errors.Is branch stops working the day a fault is attached")
	}
	var got *driverErr
	if !errors.As(f, &got) {
		t.Fatalf("the driver error the fault wraps is no longer reachable: a caller who wants the SQLSTATE cannot get at it")
	}
	if got != drv {
		t.Fatalf("errors.As found a different driver error than the one that was wrapped")
	}

	// Without the negative below this test passes for errors.Join, for a fault
	// that embeds its cause, and for a dozen other wrong implementations.
}

func TestAFaultWrappingNothingMatchesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"built with no Wrapping step", errs.Validation().Field("Age").Code("too_young").Fault()},
		// Everything an implementer of errs.Classifier can construct: the
		// wrapped list is unexported, so a struct literal is the whole of what
		// a third party can build without going through the builder.
		{"built as a struct literal, as a third-party classifier would",
			&errs.Fault{Kind: errs.KindConflict, Code: errs.CodeUnique}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, sentinel := range []error{errSentinel, errOther, errThird} {
				if errors.Is(tc.err, sentinel) {
					t.Fatalf("a fault that wraps nothing answered yes to %v — the positive case above proves nothing", sentinel)
				}
			}
			f, ok := errs.AsFault(tc.err)
			if !ok {
				t.Fatalf("the fault is not reachable with errors.As")
			}
			if got := f.Unwrap(); got != nil {
				t.Fatalf("a fault that wraps nothing unwrapped to %v", got)
			}
		})
	}
}

func TestAFaultSurvivesBeingWrappedAgain(t *testing.T) {
	drv := unique()

	t.Run("a fault that wrapped a sentinel and a driver error", func(t *testing.T) {
		err := fmt.Errorf("saving user: %w",
			errs.Conflict().Code(errs.CodeUnique).Wrapping(errSentinel, drv).Fault())

		if !errors.Is(err, errSentinel) {
			t.Fatalf("the sentinel stopped being reachable once a service layer wrapped the fault")
		}
		if _, ok := errs.AsFault(err); !ok {
			t.Fatalf("the fault stopped being reachable once a service layer wrapped it")
		}
		var got *driverErr
		if !errors.As(err, &got) {
			t.Fatalf("the driver error stopped being reachable once a service layer wrapped the fault")
		}
	})

	// The control: one more level of nesting must not make an unrelated error
	// reachable. Without it the test above passes for an implementation that
	// answers yes to anything it can reach.
	t.Run("a fault that wrapped nothing", func(t *testing.T) {
		err := fmt.Errorf("saving user: %w", errs.Validation().Field("Age").Code("too_young").Fault())

		if _, ok := errs.AsFault(err); !ok {
			t.Fatalf("the fault itself must stay reachable through a wrapping")
		}
		if errors.Is(err, errSentinel) {
			t.Fatalf("a wrapped fault that wraps nothing answered yes to a sentinel it never saw")
		}
		var got *driverErr
		if errors.As(err, &got) {
			t.Fatalf("a wrapped fault that wraps nothing produced a driver error out of nowhere")
		}
	})
}

// unique is one duplicate-key error, in the shape a driver hands one over.
func unique() *driverErr {
	return &driverErr{
		Code:           "23505",
		Message:        `ERROR: duplicate key value violates unique constraint "users_tenant_id_email_key" (SQLSTATE 23505)`,
		Detail:         "Key (tenant_id, email)=(7, test@example.com) already exists.",
		ConstraintName: "users_tenant_id_email_key",
		TableName:      "users",
		SchemaName:     "public",
	}
}

func TestAFaultsErrorTextCarriesNothingInternal(t *testing.T) {
	drv := unique()
	f := errs.Conflict().
		Op("Save").Entity("User").Code(errs.CodeUnique).
		Message("constraint users_tenant_id_email_key on table users").
		Detail(errs.Detail{
			Dialect:    "postgres",
			SQLState:   "23505",
			Native:     1062,
			Constraint: "users_tenant_id_email_key",
			Table:      "users",
			Columns:    []string{"tenant_id", "email"},
			Value:      "test@example.com",
			Driver:     fmt.Errorf("exec %q on postgres://vv:vv@localhost/vv: %w", "INSERT INTO users", drv),
		}).
		Field("email").Code(errs.CodeUnique).
		Wrapping(errSentinel, drv).
		Fault()

	// Half one of the control: every string searched for below is asserted to
	// be there on the value first. Without it this test passes for an empty
	// fixture, which is how a leak assertion usually rots.
	forbidden := map[string]string{
		"the constraint name":   f.Detail.Constraint,
		"the SQLSTATE":          f.Detail.SQLState,
		"the table":             f.Detail.Table,
		"a column":              f.Detail.Columns[0],
		"the offending value":   f.Detail.Value,
		"the developer message": f.Message,
		"the driver's own text": f.Detail.Driver.Error(),
	}
	// The native number is an int, so a zero would be searched for as "0" and
	// found in any digit Error() prints. It has to be non-zero before it can
	// join the set.
	if f.Detail.Native == 0 {
		t.Fatalf("the native error number is unset in the fixture, so finding it absent proves nothing")
	}
	forbidden["the native error number"] = strconv.Itoa(f.Detail.Native)
	for what, s := range forbidden {
		if s == "" {
			t.Fatalf("%s is empty in the fixture, so finding it absent proves nothing", what)
		}
	}

	got := f.Error()
	for what, s := range forbidden {
		if strings.Contains(got, s) {
			t.Fatalf("Error() carries %s (%q) — porthttp renders it into the body of every status below 500", what, s)
		}
	}

	// Half two: an Error() that returned "" would pass the leak check
	// perfectly. It has to still say what the failure was.
	for _, want := range []string{"Save", "User", "conflict", string(errs.CodeUnique), "1 violation"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Error() = %q, which does not say %q — the method is parsimonious, not silent", got, want)
		}
	}
}

func TestAFaultsErrorTextNamesWhicheverOfOpAndEntityItWas(t *testing.T) {
	// Three of the four arms are unreachable from the test above, which sets
	// both. A repository verb with no entity is what a decorator produces, and
	// an entity with no verb is what a service layer does.
	for _, tc := range []struct {
		name string
		b    *errs.Builder
		want string
	}{
		{"both", errs.Conflict().Op("Save").Entity("User"), "errs: Save User: conflict"},
		{"the op alone", errs.Conflict().Op("Save"), "errs: Save: conflict"},
		{"the entity alone", errs.Conflict().Entity("User"), "errs: User: conflict"},
		{"neither", errs.Conflict(), "errs: conflict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.b.Fault().Error(); got != tc.want {
				t.Fatalf("Error() = %q, want %q", got, tc.want)
			}
		})
	}
}

// AsFault is the only way anything reaches a fault, and three call sites in
// this package branch on its second return. Made to answer true always it
// hands every one of them a nil *Fault, and the suite stays green because no
// test asks it about an error that is not a fault.
func TestAsFaultAnswersFalseForAnythingThatIsNotAFault(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"a plain error", errSentinel},
		{"a wrapped plain error", fmt.Errorf("saving user: %w", errSentinel)},
		{"a driver error", unique()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, ok := errs.AsFault(tc.err)
			if ok {
				t.Fatalf("AsFault found a fault in %v", tc.err)
			}
			if f != nil {
				t.Fatalf("AsFault answered false and still returned %v", f)
			}
		})
	}

	// The control: it has to find one when there is one, or the rows above
	// pass for an AsFault that answers false to everything.
	if _, ok := errs.AsFault(errs.Conflict().Code(errs.CodeUnique).Fault()); !ok {
		t.Fatalf("AsFault did not find a fault that was right there")
	}
}
