package tenancy_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/tenancy"
)

const secretReference = "acme-7f3c"

func reference(t *testing.T, raw string) tenancy.Reference {
	t.Helper()
	value, err := tenancy.ParseReference(raw)
	if err != nil {
		t.Fatalf("cannot parse the reference the test is built on: %v", err)
	}
	return value
}

func authorityFor(t *testing.T, raw string, state tenancy.Lifecycle, epoch uint64, admission tenancy.Admission) *tenancy.Authority {
	t.Helper()
	generation, err := tenancy.NewEpoch(epoch)
	if err != nil {
		t.Fatalf("cannot build the epoch the test is built on: %v", err)
	}
	authority, err := tenancy.New(tenancy.Spec{
		Resolver:  tenancy.Fixed{Reference: reference(t, raw), Lifecycle: state, Epoch: generation},
		Admission: admission,
	})
	if err != nil {
		t.Fatalf("cannot build the authority: %v", err)
	}
	return authority
}

func activeAuthority(t *testing.T, raw string) *tenancy.Authority {
	t.Helper()
	return authorityFor(t, raw, tenancy.Active, 1, tenancy.Admission{})
}

func TestAnAuthorityRefusesToExistWithoutAResolver(t *testing.T) {
	if _, err := tenancy.New(tenancy.Spec{}); err == nil {
		t.Fatal("an authority with no resolver was built — it would have to invent a tenant on the first request")
	}
}

func TestOnlyTheAuthorityThatMintedAScopeAcceptsIt(t *testing.T) {
	ctx := context.Background()
	mine := activeAuthority(t, secretReference)
	theirs := activeAuthority(t, secretReference)

	scope, err := mine.Verify(ctx, tenancy.ClassRead)
	if err != nil {
		t.Fatalf("the authority refused its own resolver: %v", err)
	}
	bound, err := mine.With(ctx, scope)
	if err != nil {
		t.Fatalf("the authority refused the scope it just minted: %v", err)
	}
	if got, err := mine.Scope(bound, tenancy.ClassRead); err != nil || got.Reference() != scope.Reference() {
		t.Fatalf("carrying a scope lost it: got %v err %v", got, err)
	}

	for name, candidate := range map[string]tenancy.Scope{
		"a zero value anyone can write":    {},
		"one minted by another deployment": mustVerify(t, theirs),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := mine.With(ctx, candidate); err == nil {
				t.Fatal("a scope this authority never produced was accepted — every other control rests on this one")
			}
		})
	}
}

func mustVerify(t *testing.T, authority *tenancy.Authority) tenancy.Scope {
	t.Helper()
	scope, err := authority.Verify(context.Background(), tenancy.ClassRead)
	if err != nil {
		t.Fatalf("cannot mint the scope the test compares against: %v", err)
	}
	return scope
}

func TestOnlyAnAdmittedLifecycleReachesWork(t *testing.T) {
	ctx := context.Background()
	states := []tenancy.Lifecycle{
		tenancy.Provisioning, tenancy.Active, tenancy.Suspended,
		tenancy.Migrating, tenancy.Deleting, tenancy.Deleted,
		tenancy.LifecycleUnknown, tenancy.Lifecycle(200),
	}

	for _, state := range states {
		for _, class := range tenancy.Classes() {
			authority := authorityFor(t, secretReference, state, 1, tenancy.Admission{})
			_, err := authority.Verify(ctx, class)
			if state == tenancy.Active {
				if err != nil {
					t.Fatalf("%s work for an active tenant was refused: %v", class, err)
				}
				continue
			}
			if err == nil {
				t.Fatalf("%s work was admitted for a tenant in state %q", class, state)
			}
		}
	}
}

func TestAdmissionIsDecidedPerOperationClass(t *testing.T) {
	ctx := context.Background()
	authority := authorityFor(t, secretReference, tenancy.Suspended, 1,
		tenancy.Admit(tenancy.ClassRead, tenancy.Active, tenancy.Suspended).
			Merge(tenancy.Admit(tenancy.ClassWrite, tenancy.Active)).
			Merge(tenancy.Admit(tenancy.ClassDurable, tenancy.Active)))

	if _, err := authority.Verify(ctx, tenancy.ClassRead); err != nil {
		t.Fatalf("a suspended tenant a deployment chose to keep readable was refused: %v", err)
	}
	for _, class := range []tenancy.Class{tenancy.ClassWrite, tenancy.ClassDurable} {
		if _, err := authority.Verify(ctx, class); !errors.Is(err, tenancy.ErrInactive) {
			t.Fatalf("%s work for a suspended tenant answered %v, want ErrInactive", class, err)
		}
	}
}

type failingResolver struct{ err error }

func (this failingResolver) Resolve(context.Context) (tenancy.Resolution, error) {
	return tenancy.Resolution{}, this.err
}

func (this failingResolver) Lookup(context.Context, tenancy.Reference) (tenancy.Resolution, error) {
	return tenancy.Resolution{}, this.err
}

func TestAResolverFailureNeverTravelsBackAsText(t *testing.T) {
	leak := fmt.Errorf("dial tcp control-plane.internal:5432: tenant %s on db_acme_7f3c: password authentication failed", secretReference)

	authority, err := tenancy.New(tenancy.Spec{Resolver: failingResolver{err: leak}})
	if err != nil {
		t.Fatal(err)
	}
	_, refusal := authority.Verify(context.Background(), tenancy.ClassRead)
	if refusal == nil {
		t.Fatal("a resolver that could not answer produced a scope")
	}
	for _, sentinel := range []string{secretReference, "db_acme_7f3c", "password", "control-plane.internal"} {
		if strings.Contains(refusal.Error(), sentinel) {
			t.Fatalf("the refusal carries %q from the resolver: %v", sentinel, refusal)
		}
	}
	if !errors.Is(refusal, tenancy.ErrUnavailable) {
		t.Fatalf("refusal = %v, want ErrUnavailable", refusal)
	}

	t.Run("a sentinel the resolver chose deliberately survives", func(t *testing.T) {
		authority, err := tenancy.New(tenancy.Spec{
			Resolver: failingResolver{err: fmt.Errorf("no membership: %w", tenancy.ErrNoScope)},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, refusal := authority.Verify(context.Background(), tenancy.ClassRead); !errors.Is(refusal, tenancy.ErrNoScope) {
			t.Fatalf("refusal = %v, want ErrNoScope", refusal)
		}
	})
}

func TestEveryRefusalIsForbiddenToATransportThatKnowsNothingAboutTenants(t *testing.T) {
	for _, refusal := range []error{
		tenancy.ErrNoScope, tenancy.ErrUntrusted, tenancy.ErrInactive,
		tenancy.ErrStale, tenancy.ErrIncompatible, tenancy.ErrUnmapped,
		tenancy.ErrGrantRequired,
	} {
		if !errors.Is(refusal, crud.ErrForbidden) {
			t.Fatalf("%v does not wrap crud.ErrForbidden, so the transport cannot answer 403 without importing tenancy", refusal)
		}
	}
	if !errors.Is(tenancy.ErrPinned, crud.ErrConflict) {
		t.Fatalf("%v is not a conflict", tenancy.ErrPinned)
	}
	for _, capacity := range []error{tenancy.ErrCapacity, tenancy.ErrUnavailable} {
		if errors.Is(capacity, crud.ErrForbidden) {
			t.Fatalf("%v reads as an authorisation failure, so a full pool would page the security team", capacity)
		}
	}
}

func TestNothingCarryingATenantRendersIt(t *testing.T) {
	authority := activeAuthority(t, secretReference)
	scope := mustVerify(t, authority)

	for name, value := range map[string]any{
		"the scope":     scope,
		"the reference": scope.Reference(),
	} {
		t.Run(name, func(t *testing.T) {
			for _, rendered := range []string{
				fmt.Sprint(value), fmt.Sprintf("%v", value), fmt.Sprintf("%s", value), fmt.Sprintf("%+v", value),
			} {
				if strings.Contains(rendered, secretReference) {
					t.Fatalf("%q renders the tenant it names", rendered)
				}
			}
			if _, err := json.Marshal(value); err == nil {
				t.Fatal("it serialises, so one log line or one API response leaks it")
			}
		})
	}

	if scope.Reference().Value() != secretReference {
		t.Fatal("the reference cannot be read at all, so no policy could build a predicate from it")
	}
}

type switchable struct{ current *tenancy.Resolution }

func (this switchable) Resolve(context.Context) (tenancy.Resolution, error) {
	return *this.current, nil
}

func (this switchable) Lookup(_ context.Context, reference tenancy.Reference) (tenancy.Resolution, error) {
	if reference != this.current.Reference {
		return tenancy.Resolution{}, tenancy.ErrUnmapped
	}
	return *this.current, nil
}

func TestBindingASecondTenantInsideBoundWorkRefuses(t *testing.T) {
	ctx := context.Background()
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	current := &tenancy.Resolution{Reference: reference(t, secretReference), Lifecycle: tenancy.Active, Epoch: epoch}
	authority, err := tenancy.New(tenancy.Spec{Resolver: switchable{current: current}})
	if err != nil {
		t.Fatal(err)
	}

	bound, err := authority.Bind(ctx, tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authority.Bind(bound, tenancy.ClassWrite); err != nil {
		t.Fatalf("re-binding the same tenant inside its own work was refused: %v", err)
	}

	current.Reference = reference(t, "globex-1a2b")
	if _, err := authority.Bind(bound, tenancy.ClassWrite); !errors.Is(err, tenancy.ErrPinned) {
		t.Fatalf("err = %v, want ErrPinned — a unit of work whose tenant can move has no atomicity to speak of", err)
	}
}

func TestWorkWithNoScopeInContextRefuses(t *testing.T) {
	authority := activeAuthority(t, secretReference)

	for _, class := range tenancy.Classes() {
		if _, err := authority.Scope(context.Background(), class); !errors.Is(err, tenancy.ErrNoScope) {
			t.Fatalf("%s answered %v for a context carrying nothing, want ErrNoScope", class, err)
		}
	}

	t.Run("and still refuses right after a successful operation for another tenant", func(t *testing.T) {
		if _, err := authority.Bind(context.Background(), tenancy.ClassRead); err != nil {
			t.Fatal(err)
		}
		if _, err := authority.Scope(context.Background(), tenancy.ClassRead); !errors.Is(err, tenancy.ErrNoScope) {
			t.Fatalf("err = %v — a tenant leaked out of one call into the next", err)
		}
	})
}

// A scope never changes once produced. Sixty-four goroutines
// reading copies of an immutable struct cannot falsify that — the property is of
// the type, so the type is what this asserts: no exported field, no setter, and
// nothing reachable from it that a second holder could write through.
func TestAScopeHasNothingAnybodyCanWriteThrough(t *testing.T) {
	scope := mustVerify(t, activeAuthority(t, secretReference))
	value := reflect.ValueOf(scope)

	if value.Kind() != reflect.Struct {
		t.Fatalf("a scope is a %v, and this test only reasons about structs", value.Kind())
	}
	for i := range value.NumField() {
		field := value.Type().Field(i)
		if field.IsExported() {
			t.Fatalf("field %s is exported, so a holder can retarget the scope in place", field.Name)
		}
		switch field.Type.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func, reflect.Interface:
			t.Fatalf("field %s is a %v, so two holders share what it points at and one can write it", field.Name, field.Type.Kind())
		}
	}

	for _, method := range []string{"Set", "SetReference", "SetLifecycle", "SetEpoch", "Widen", "Retarget"} {
		if _, found := reflect.TypeOf(scope).MethodByName(method); found {
			t.Fatalf("Scope has a %s method", method)
		}
		if _, found := reflect.TypeOf(&scope).MethodByName(method); found {
			t.Fatalf("*Scope has a %s method", method)
		}
	}

	copied := scope
	if copied.Reference() != scope.Reference() || copied.Epoch() != scope.Epoch() || copied.Lifecycle() != scope.Lifecycle() {
		t.Fatal("copying a scope changed it")
	}
}

// Unbound exists so a durable record naming no tenant does not inherit whatever
// the worker's base context happened to hold. It is not a way out of the pin: if
// it cleared that too, then bind-A, Unbound, bind-B would be a supported,
// capability-free re-target of work that has already chosen its narrowing or its
// datasource, and ErrPinned would be advice rather than a refusal.
func TestUnbindingIsNotAWayToRetargetBoundWork(t *testing.T) {
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	current := &tenancy.Resolution{Reference: reference(t, secretReference), Lifecycle: tenancy.Active, Epoch: epoch}
	authority, err := tenancy.New(tenancy.Spec{Resolver: switchable{current: current}})
	if err != nil {
		t.Fatal(err)
	}

	bound, err := authority.Bind(context.Background(), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	released := tenancy.Unbound(bound)

	// The control, and the whole point of Unbound: the scope really is gone, so
	// the refusal below is the pin rather than a scope that never left.
	if _, still := tenancy.From(released); still {
		t.Fatal("Unbound left the scope in the context, so nothing here is tested")
	}
	if _, err := authority.Scope(released, tenancy.ClassRead); !errors.Is(err, tenancy.ErrNoScope) {
		t.Fatalf("err = %v, want ErrNoScope — unbound work reached a tenant", err)
	}

	current.Reference = reference(t, "globex-1a2b")
	if _, err := authority.Bind(released, tenancy.ClassWrite); !errors.Is(err, tenancy.ErrPinned) {
		t.Fatalf("err = %v, want ErrPinned — bind, unbind, bind re-targeted a unit of work at another tenant", err)
	}

	// And the tenant it was bound to can still be re-entered, so the pin is a pin
	// and not a one-shot poison of the context.
	current.Reference = reference(t, secretReference)
	if _, err := authority.Bind(released, tenancy.ClassWrite); err != nil {
		t.Fatalf("the tenant this work was bound to could not re-enter it: %v", err)
	}
}
