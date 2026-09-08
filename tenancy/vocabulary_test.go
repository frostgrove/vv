package tenancy_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/port/porthttp"
	"github.com/frostgrove/vv/tenancy"
)

func TestAReferenceIsRefusedWhenItCouldNotSurviveAKeyOrANamespace(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":                 "",
		"leading space":         " acme",
		"trailing space":        "acme ",
		"an interior space":     "acme corp",
		"a newline":             "acme\nglobex",
		"a control character":   "acme\x00",
		"invalid utf-8":         "acme\xff",
		"longer than the bound": strings.Repeat("a", tenancy.MaxReferenceBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tenancy.ParseReference(raw); !errors.Is(err, tenancy.ErrMalformed) {
				t.Fatalf("err = %v — this value would become an object prefix and a cache partition", err)
			}
		})
	}

	if _, err := tenancy.ParseReference("acme-7f3c"); err != nil {
		t.Fatalf("an ordinary reference was refused: %v — the cases above prove nothing if everything is refused", err)
	}
}

func TestTheReferenceIsCompareByteForByteAndNeverFolded(t *testing.T) {
	upper, err := tenancy.ParseReference("Acme")
	if err != nil {
		t.Fatal(err)
	}
	lower, err := tenancy.ParseReference("acme")
	if err != nil {
		t.Fatal(err)
	}
	if upper == lower {
		t.Fatal("two references the control plane keeps apart were merged into one tenant")
	}
}

func TestAnEpochOfZeroIsNotAnEpoch(t *testing.T) {
	if _, err := tenancy.NewEpoch(0); !errors.Is(err, tenancy.ErrMalformed) {
		t.Fatalf("err = %v — a zero epoch is what an uninitialised struct carries", err)
	}
	if _, err := tenancy.NewEpoch(1); err != nil {
		t.Fatal(err)
	}
}

func TestTheOutcomeVocabularyIsClosed(t *testing.T) {
	declared := tenancy.Outcomes()

	for _, refusal := range []error{
		tenancy.ErrNoScope, tenancy.ErrUntrusted, tenancy.ErrInactive, tenancy.ErrStale,
		tenancy.ErrIncompatible, tenancy.ErrUnmapped, tenancy.ErrGrantRequired,
		tenancy.ErrPinned, tenancy.ErrCapacity, tenancy.ErrUnavailable, tenancy.ErrMalformed,
	} {
		outcome := tenancy.OutcomeFor(refusal)
		if !slices.Contains(declared, outcome) {
			t.Fatalf("%v maps to %q, which is not in the declared vocabulary", refusal, outcome)
		}
		if outcome == tenancy.OutcomeError {
			t.Fatalf("%v has no name of its own, so a dashboard cannot tell it from a bug", refusal)
		}
	}

	if got := tenancy.OutcomeFor(nil); got != tenancy.OutcomeOk {
		t.Fatalf("success is %q", got)
	}

	leak := fmt.Errorf("tenant acme-7f3c on db_acme_7f3c: connection refused")
	if got := tenancy.OutcomeFor(leak); got != tenancy.OutcomeError {
		t.Fatalf("an error from outside the vocabulary became %q", got)
	}
	for _, outcome := range declared {
		if strings.ContainsAny(string(outcome), " \t\n") || len(outcome) > 16 {
			t.Fatalf("%q is not the shape a bounded label can take", outcome)
		}
	}
}

func TestALifecycleNobodyDeclaredStillNamesItselfSafely(t *testing.T) {
	if got := tenancy.Lifecycle(200).String(); got != "unknown" {
		t.Fatalf("an unrecognised state renders as %q, which a dashboard would key on", got)
	}
	if tenancy.Lifecycle(200).Valid() {
		t.Fatal("a state nobody declared claims to be valid")
	}
}

func TestManyTenantsMintDistinctScopes(t *testing.T) {
	const tenants = 1000
	authority, references := manyTenantAuthority(t, tenants)

	seen := make(map[string]string, tenants)
	for _, raw := range references {
		scope, err := authority.Lookup(t.Context(), reference(t, raw), tenancy.ClassRead)
		if err != nil {
			t.Fatalf("looking up %q failed: %v", raw, err)
		}
		bound, err := authority.With(t.Context(), scope)
		if err != nil {
			t.Fatalf("the authority refused a scope it minted for %q: %v", raw, err)
		}
		carried, _ := tenancy.From(bound)
		key := carried.Reference().Value()
		if previous, collided := seen[key]; collided {
			t.Fatalf("%q and %q resolve to one tenant", previous, raw)
		}
		seen[key] = raw
	}
	if len(seen) != tenants {
		t.Fatalf("%d tenants collapsed into %d", tenants, len(seen))
	}
}

type directory struct {
	byReference map[tenancy.Reference]tenancy.Resolution
}

func (this directory) Resolve(ctx context.Context) (tenancy.Resolution, error) {
	return tenancy.Resolution{}, tenancy.ErrNoScope
}

func (this directory) Lookup(_ context.Context, ref tenancy.Reference) (tenancy.Resolution, error) {
	resolution, ok := this.byReference[ref]
	if !ok {
		return tenancy.Resolution{}, tenancy.ErrUnmapped
	}
	return resolution, nil
}

func manyTenantAuthority(t *testing.T, count int) (*tenancy.Authority, []string) {
	t.Helper()
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	byReference := make(map[tenancy.Reference]tenancy.Resolution, count)
	raws := make([]string, 0, count)
	for i := range count {
		raw := fmt.Sprintf("tenant-%04d", i)
		ref := reference(t, raw)
		byReference[ref] = tenancy.Resolution{Reference: ref, Lifecycle: tenancy.Active, Epoch: epoch}
		raws = append(raws, raw)
	}
	authority, err := tenancy.New(tenancy.Spec{Resolver: directory{byReference: byReference}})
	if err != nil {
		t.Fatal(err)
	}
	return authority, raws
}

// A full directory and a control plane that will not answer are operational: the
// tenant is entitled to the work and the answer is "not now". Rendered as an
// internal error they are indistinguishable from a panic, so an operator pages
// on them and a client never retries.
func TestAnOperationalRefusalRendersAsRetryableRatherThanAsABug(t *testing.T) {
	for name, refusal := range map[string]error{
		"a full directory": tenancy.ErrCapacity,
		"a control plane that is up but will not answer": tenancy.ErrUnavailable,
	} {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(refusal, crud.ErrUnavailable) {
				t.Fatalf("%v wraps no retryable sentinel, so port renders it 500", refusal)
			}
			if kind := port.KindOf(refusal); kind != errs.KindRetryable {
				t.Fatalf("kind = %v, want errs.KindRetryable", kind)
			}
			status, _, _ := porthttp.NewRenderer().Render(context.Background(), port.FaultOf(refusal))
			if status != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", status)
			}
		})
	}

	// The control: a refusal of the *caller* must not have become retryable with
	// them, or this test would pass for a directory that made everything a 503.
	if kind := port.KindOf(tenancy.ErrNoScope); kind != errs.KindForbidden {
		t.Fatalf("a forbidden refusal became %v", kind)
	}
}

// A resolver's text names the tenant, the database and the credential, so it is
// collapsed. A caller that went away names none of them, and folding it into the
// unavailable answer puts every client disconnect on the graph that says the
// control plane is down.
func TestACallerGoingAwayIsNotAControlPlaneOutage(t *testing.T) {
	for name, err := range map[string]error{
		"cancelled": context.Canceled,
		"deadline":  context.DeadlineExceeded,
	} {
		t.Run(name, func(t *testing.T) {
			if got := tenancy.Classify(err); !errors.Is(got, err) {
				t.Fatalf("Classify(%v) = %v — the cause was dropped", err, got)
			}
			if errors.Is(tenancy.Classify(err), tenancy.ErrUnavailable) {
				t.Fatal("a caller that went away was reported as the capability being unavailable")
			}
		})
	}

	// The control: a resolver's own text still collapses, so the redaction that
	// this function exists for has not been traded away for the two cases above.
	leak := fmt.Errorf("tenant acme-7f3c on db_acme_7f3c: connection refused")
	if got := tenancy.Classify(leak); !errors.Is(got, tenancy.ErrUnavailable) || strings.Contains(got.Error(), "acme") {
		t.Fatalf("Classify leaked the resolver's text: %v", got)
	}
}
