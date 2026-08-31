package errs_test

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/errs"
)

var (
	errSentinel = errors.New("test: conflict")
	errOther    = errors.New("test: not found")
	errThird    = errors.New("test: forbidden")
)

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

}

func TestAFaultWrappingNothingMatchesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"built with no Wrapping step", errs.Validation().Field("Age").Code("too_young").Fault()},

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

	forbidden := map[string]string{
		"the constraint name":   f.Detail.Constraint,
		"the SQLSTATE":          f.Detail.SQLState,
		"the table":             f.Detail.Table,
		"a column":              f.Detail.Columns[0],
		"the offending value":   f.Detail.Value,
		"the developer message": f.Message,
		"the driver's own text": f.Detail.Driver.Error(),
	}

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

	for _, want := range []string{"Save", "User", "conflict", string(errs.CodeUnique), "1 violation"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Error() = %q, which does not say %q — the method is parsimonious, not silent", got, want)
		}
	}
}

func TestAFaultsErrorTextNamesWhicheverOfOpAndEntityItWas(t *testing.T) {
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

	if _, ok := errs.AsFault(errs.Conflict().Code(errs.CodeUnique).Fault()); !ok {
		t.Fatalf("AsFault did not find a fault that was right there")
	}
}
