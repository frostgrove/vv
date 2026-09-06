package tenancy_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

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
