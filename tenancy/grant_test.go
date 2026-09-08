package tenancy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/tenancy"
)

func cohortAuthority(t *testing.T, clock *time.Time, raws ...string) (*tenancy.Authority, []tenancy.Reference) {
	t.Helper()
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	known := map[tenancy.Reference]tenancy.Resolution{}
	cohort := make([]tenancy.Reference, 0, len(raws))
	for _, raw := range raws {
		ref := reference(t, raw)
		known[ref] = tenancy.Resolution{Reference: ref, Lifecycle: tenancy.Active, Epoch: epoch}
		cohort = append(cohort, ref)
	}
	authority, err := tenancy.New(tenancy.Spec{
		Resolver: directory{byReference: known},
		Now:      func() time.Time { return *clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	return authority, cohort
}

func purpose(t *testing.T, raw string) tenancy.Purpose {
	t.Helper()
	value, err := tenancy.ParsePurpose(raw)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestCrossTenantWorkEntersOneTenantAtATime(t *testing.T) {
	clock := time.Now()
	authority, cohort := cohortAuthority(t, &clock, "acme", "globex", "initech")

	grant, err := authority.Accept(purpose(t, "monthly-billing"), cohort, clock.Add(time.Hour), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}

	var visited []string
	outcomes, err := authority.Each(context.Background(), grant, tenancy.ClassWrite, func(ctx context.Context) error {
		scope, ok := tenancy.From(ctx)
		if !ok {
			t.Fatal("cohort work ran with no tenant bound")
		}
		visited = append(visited, scope.Reference().Value())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(visited) != 3 || len(outcomes) != 3 {
		t.Fatalf("visited %v with %d outcomes", visited, len(outcomes))
	}
	for _, member := range outcomes {
		if member.Outcome != tenancy.OutcomeOk {
			t.Fatalf("%v reported %q", member.Reference, member.Outcome)
		}
	}
}

func TestCrossTenantWorkWithNoGrantIsRefused(t *testing.T) {
	clock := time.Now()
	authority, _ := cohortAuthority(t, &clock, "acme")

	if _, err := authority.Each(context.Background(), nil, tenancy.ClassWrite, func(context.Context) error {
		t.Fatal("work ran with no grant at all")
		return nil
	}); !errors.Is(err, tenancy.ErrGrantRequired) {
		t.Fatalf("err = %v, want ErrGrantRequired", err)
	}
}

func TestAGrantNamesTenantsRatherThanEverybody(t *testing.T) {
	clock := time.Now()
	authority, cohort := cohortAuthority(t, &clock, "acme")

	if _, err := authority.Accept(purpose(t, "monthly-billing"), nil, clock.Add(time.Hour), tenancy.ClassWrite); err == nil {
		t.Fatal("a grant naming nobody was issued — that is a second way to disable narrowing")
	}
	if _, err := authority.Accept(tenancy.Purpose{}, cohort, clock.Add(time.Hour), tenancy.ClassWrite); err == nil {
		t.Fatal("a grant with no purpose was issued")
	}
	if _, err := authority.Accept(purpose(t, "monthly-billing"), cohort, clock.Add(-time.Hour), tenancy.ClassWrite); !errors.Is(err, tenancy.ErrStale) {
		t.Fatal("a grant that had already expired was issued")
	}
}

func TestAGrantFromAnotherAuthorityIsNotHeldHere(t *testing.T) {
	clock := time.Now()
	mine, cohort := cohortAuthority(t, &clock, "acme")
	theirs, _ := cohortAuthority(t, &clock, "acme")

	grant, err := theirs.Accept(purpose(t, "monthly-billing"), cohort, clock.Add(time.Hour), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mine.Each(context.Background(), grant, tenancy.ClassWrite, func(context.Context) error {
		t.Fatal("a grant this authority never accepted was honoured")
		return nil
	}); !errors.Is(err, tenancy.ErrUntrusted) {
		t.Fatalf("err = %v, want ErrUntrusted", err)
	}
}

func TestAGrantThatExpiresMidRunStopsAndReportsWhatFinished(t *testing.T) {
	clock := time.Now()
	authority, cohort := cohortAuthority(t, &clock, "acme", "globex", "initech")

	grant, err := authority.Accept(purpose(t, "monthly-billing"), cohort, clock.Add(time.Minute), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}

	done := 0
	outcomes, err := authority.Each(context.Background(), grant, tenancy.ClassWrite, func(context.Context) error {
		done++
		if done == 2 {
			clock = clock.Add(time.Hour)
		}
		return nil
	})
	if !errors.Is(err, tenancy.ErrStale) {
		t.Fatalf("err = %v, want ErrStale", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("the run reported %d finished members, so a resume would repeat or skip work", len(outcomes))
	}
}

func TestOneCohortMemberFailingLeavesTheRestResumable(t *testing.T) {
	clock := time.Now()
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	acme, globex, initech := reference(t, "acme"), reference(t, "globex"), reference(t, "initech")
	authority, err := tenancy.New(tenancy.Spec{
		Now: func() time.Time { return clock },
		Resolver: directory{byReference: map[tenancy.Reference]tenancy.Resolution{
			acme:    {Reference: acme, Lifecycle: tenancy.Active, Epoch: epoch},
			globex:  {Reference: globex, Lifecycle: tenancy.Suspended, Epoch: epoch},
			initech: {Reference: initech, Lifecycle: tenancy.Active, Epoch: epoch},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}

	grant, err := authority.Accept(purpose(t, "monthly-billing"), []tenancy.Reference{acme, globex, initech}, clock.Add(time.Hour), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}

	var ran []string
	outcomes, err := authority.Each(context.Background(), grant, tenancy.ClassWrite, func(ctx context.Context) error {
		scope, _ := tenancy.From(ctx)
		ran = append(ran, scope.Reference().Value())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ran) != 2 || ran[0] != "acme" || ran[1] != "initech" {
		t.Fatalf("the run entered %v — a member that became non-active took the others with it", ran)
	}
	if outcomes[1].Outcome != tenancy.OutcomeInactive {
		t.Fatalf("the skipped member reported %q, so a resume cannot tell it from a success", outcomes[1].Outcome)
	}
}

func TestAContextCarriedOutOfAnExpiredGrantStopsWorking(t *testing.T) {
	clock := time.Now()
	authority, cohort := cohortAuthority(t, &clock, "acme")

	grant, err := authority.Accept(purpose(t, "monthly-billing"), cohort, clock.Add(time.Minute), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}

	var escaped context.Context
	if _, err := authority.Each(context.Background(), grant, tenancy.ClassWrite, func(ctx context.Context) error {
		escaped = ctx
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := authority.Scope(escaped, tenancy.ClassWrite); err != nil {
		t.Fatalf("the control refuses too, so the assertion below proves nothing: %v", err)
	}

	clock = clock.Add(time.Hour)
	if _, err := authority.Scope(escaped, tenancy.ClassWrite); !errors.Is(err, tenancy.ErrStale) {
		t.Fatalf("err = %v, want ErrStale — a context kept from a finished cohort run outlived its grant", err)
	}
}

func TestACohortRunFromInsideABoundRequestIsRefusedRatherThanEmpty(t *testing.T) {
	clock := time.Now()
	authority, cohort := cohortAuthority(t, &clock, "acme", "globex")

	grant, err := authority.Accept(purpose(t, "monthly-billing"), cohort, clock.Add(time.Hour), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}

	operator, err := authority.Lookup(context.Background(), reference(t, "acme"), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	inRequest, err := authority.With(context.Background(), operator)
	if err != nil {
		t.Fatal(err)
	}

	entered := 0
	outcomes, err := authority.Each(inRequest, grant, tenancy.ClassWrite, func(context.Context) error {
		entered++
		return nil
	})
	if !errors.Is(err, tenancy.ErrPinned) {
		t.Fatalf("err = %v, want ErrPinned — the idiomatic call site sees success while the run bills nobody", err)
	}
	if entered != 0 || len(outcomes) != 0 {
		t.Fatalf("entered %d members and reported %d outcomes for a run that was refused", entered, len(outcomes))
	}

	t.Run("and the same grant runs from an unbound context", func(t *testing.T) {
		entered := 0
		if _, err := authority.Each(context.Background(), grant, tenancy.ClassWrite, func(context.Context) error {
			entered++
			return nil
		}); err != nil {
			t.Fatalf("the control refuses too, so the assertion above proves nothing: %v", err)
		}
		if entered != 2 {
			t.Fatalf("the control entered %d of two members", entered)
		}
	})
}

// One member failing is deliberately not a failed run — that is the resumability
// the loop exists for, and TestOneCohortMemberFailingLeavesTheRestResumable pins
// it. A run where *nobody* completed is a different thing, and answering nil
// there means the ordinary wrapper — check err, log the slice — records a total
// failure as a finished run and never pages anyone.
func TestACohortRunWhereNobodyCompletedIsNotAFinishedRun(t *testing.T) {
	clock := time.Now()
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	acme, globex := reference(t, "acme"), reference(t, "globex")
	authority, err := tenancy.New(tenancy.Spec{
		Now: func() time.Time { return clock },
		Resolver: directory{byReference: map[tenancy.Reference]tenancy.Resolution{
			acme:   {Reference: acme, Lifecycle: tenancy.Active, Epoch: epoch},
			globex: {Reference: globex, Lifecycle: tenancy.Active, Epoch: epoch},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	grant, err := authority.Accept(purpose(t, "monthly-billing"), []tenancy.Reference{acme, globex}, clock.Add(time.Hour), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}

	everyoneFails := errors.New("the downstream this job needs is down")
	outcomes, err := authority.Each(context.Background(), grant, tenancy.ClassWrite, func(context.Context) error {
		return everyoneFails
	})
	if !errors.Is(err, tenancy.ErrCohortFailed) {
		t.Fatalf("err = %v, want tenancy.ErrCohortFailed", err)
	}
	if len(outcomes) != 2 || tenancy.Failures(outcomes) != 2 {
		t.Fatalf("outcomes = %v — the per-member record must survive the run-level error, or the run cannot be resumed", outcomes)
	}

	// The control: one member completing is still a nil error, so the assertion
	// above is about nobody completing rather than about anybody failing.
	var attempts int
	partial, err := authority.Each(context.Background(), grant, tenancy.ClassWrite, func(context.Context) error {
		attempts++
		if attempts == 1 {
			return everyoneFails
		}
		return nil
	})
	if err != nil {
		t.Fatalf("a run with one success answered %v", err)
	}
	if tenancy.Failures(partial) != 1 {
		t.Fatalf("Failures = %d, want 1", tenancy.Failures(partial))
	}
}

// A grant names the classes the cohort run may spend, and every verb asks. Sealing
// used to be the one that did not: it took a bare scope and no context, so a run
// under a read-only grant could still write a durable record for every tenant in
// the cohort — work that executes later, outside the grant's deadline, with a
// class the grant never named.
func TestAReadOnlyGrantSealsNoDurableRecord(t *testing.T) {
	clock := time.Now()
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	acme := reference(t, "acme")
	authority, err := tenancy.New(tenancy.Spec{
		Now:        func() time.Time { return clock },
		DurableKey: []byte("a-durable-key-of-at-least-32-bytes!!"),
		Resolver: directory{byReference: map[tenancy.Reference]tenancy.Resolution{
			acme: {Reference: acme, Lifecycle: tenancy.Active, Epoch: epoch},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := authority.Sealer()
	if err != nil {
		t.Fatal(err)
	}
	binding := [][]byte{[]byte("billing"), []byte("send-invoice")}

	readOnly, err := authority.Accept(purpose(t, "monthly-report"), []tenancy.Reference{acme}, clock.Add(time.Hour), tenancy.ClassRead)
	if err != nil {
		t.Fatal(err)
	}
	var refusal error
	if _, err := authority.Each(context.Background(), readOnly, tenancy.ClassRead, func(ctx context.Context) error {
		scope, _ := tenancy.From(ctx)
		_, refusal = sealer.Seal(ctx, scope, binding...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(refusal, tenancy.ErrGrantRequired) {
		t.Fatalf("err = %v, want ErrGrantRequired — a read-only cohort run wrote durable work", refusal)
	}

	// The control: a grant that does name the durable class seals, so the refusal
	// above is the grant's classes rather than sealing being broken under Each.
	durable, err := authority.Accept(purpose(t, "monthly-billing"), []tenancy.Reference{acme}, clock.Add(time.Hour), tenancy.ClassRead, tenancy.ClassDurable)
	if err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	var sealErr error
	if _, err := authority.Each(context.Background(), durable, tenancy.ClassRead, func(ctx context.Context) error {
		scope, _ := tenancy.From(ctx)
		sealed, sealErr = sealer.Seal(ctx, scope, binding...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if sealErr != nil || len(sealed) == 0 {
		t.Fatalf("a grant that names the durable class sealed nothing: %v", sealErr)
	}
}
