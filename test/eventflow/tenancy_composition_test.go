package eventflow_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/tenancy"
)

// Tenancy and the event extension compose in an application and nowhere else.
// Neither imports the other and neither ever will: the direction is held by
// TestNoBaseSubsystemDependsOnTheEventExtension and
// TestNoBaseSubsystemDependsOnTheOptionalExtension in `scripts`, which read the
// import graph of every published module. What no graph test can say is whether
// the composition a consumer has to write is one that keeps two tenants apart,
// and that is what this file is.
//
// The composition is the ordinary one: the authority verifies a scope, the
// verified scope selects the tenant's own store, and only then is there an event
// call at all. Written that way the refusal path reaches no store — there is
// nothing to leak from, rather than a leak that something noticed — and the
// control subtest below is what keeps that from being a vacuous claim.

type ledgerState struct{ Minor int64 }

type credited struct{ Minor int64 }

type accountID struct{ tenant, number string }

var (
	ledger = event.Define[ledgerState]("eventflow.ledger", func(id accountID) event.Key {
		return event.Compose(id.tenant, id.number)
	})
	ledgerCredited = event.Declare(ledger, "eventflow.ledger.credited", event.From(event.JSON[credited]()),
		func(state ledgerState, carried credited) ledgerState {
			state.Minor += carried.Minor
			return state
		})
)

type tenantKey struct{}

// The control plane, and the two things an operator does to a tenant between two
// requests: move its generation, and change what its lifecycle admits.
type controlPlane struct {
	tenants map[string]tenancy.Resolution
}

func (this *controlPlane) Resolve(ctx context.Context) (tenancy.Resolution, error) {
	named, _ := ctx.Value(tenantKey{}).(string)
	return this.Lookup(ctx, referenceOf(named))
}

func (this *controlPlane) Lookup(_ context.Context, reference tenancy.Reference) (tenancy.Resolution, error) {
	found, known := this.tenants[reference.Value()]
	if !known {
		return tenancy.Resolution{}, tenancy.ErrUnmapped
	}
	return found, nil
}

func referenceOf(named string) tenancy.Reference {
	reference, err := tenancy.ParseReference(named)
	if err != nil {
		return tenancy.Reference{}
	}
	return reference
}

// One store per tenant, which is this repository's tenancy model, and the store
// is reached through the scope rather than beside it. `entered` counts the times
// any event door was opened at all: a refusal that never resolves a store leaves
// it where it was, and that is the measurement the "zero leakage" clause needs.
type application struct {
	authority *tenancy.Authority
	stores    map[string]*event.Binding
	entered   atomic.Int64
}

func (this *application) repo(ctx context.Context, class tenancy.Class) (*event.Repo[ledgerState, accountID], error) {
	scope, err := this.authority.Scope(ctx, class)
	if err != nil {
		return nil, err
	}
	binding, mapped := this.stores[scope.Reference().Value()]
	if !mapped {
		return nil, tenancy.ErrUnmapped
	}
	this.entered.Add(1)
	return event.Bind(binding, ledger)
}

func newApplication(t *testing.T, plane *controlPlane, revalidate bool) *application {
	t.Helper()
	authority, err := tenancy.New(tenancy.Spec{
		Resolver:   plane,
		Admission:  tenancy.AdmitAll(tenancy.Active).Merge(tenancy.Admit(tenancy.ClassRead, tenancy.Active, tenancy.Suspended)),
		Origin:     "eventflow",
		Revalidate: revalidate,
	})
	if err != nil {
		t.Fatalf("the authority this composition is built on was refused: %v", err)
	}
	built := &application{authority: authority, stores: map[string]*event.Binding{}}
	for named := range plane.tenants {
		built.stores[named] = openedStore(t)
	}
	return built
}

func openedStore(t *testing.T) *event.Binding {
	t.Helper()
	log, err := eventmemory.NewLog(eventmemory.LogSpec{})
	if err != nil {
		t.Fatalf("a tenant's log was refused: %v", err)
	}
	store, err := eventmemory.New(eventmemory.Spec{Log: log})
	if err != nil {
		t.Fatalf("a tenant's store was refused: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return event.Open(store)
}

func bound(t *testing.T, built *application, named string, class tenancy.Class) context.Context {
	t.Helper()
	ctx := context.WithValue(t.Context(), tenantKey{}, named)
	carried, err := built.authority.Bind(ctx, class)
	if err != nil {
		t.Fatalf("binding %q for %s answered %v", named, class, err)
	}
	return carried
}

func credit(t *testing.T, built *application, ctx context.Context, id accountID, minor int64) error {
	t.Helper()
	repo, err := built.repo(ctx, tenancy.ClassWrite)
	if err != nil {
		return err
	}
	_, at, err := repo.Load(ctx, id)
	if err != nil {
		return err
	}
	_, _, err = repo.Append(ctx, at, ledgerCredited.New(id, credited{Minor: minor}))
	return err
}

func balance(t *testing.T, built *application, ctx context.Context, id accountID) int64 {
	t.Helper()
	repo, err := built.repo(ctx, tenancy.ClassRead)
	if err != nil {
		t.Fatalf("reading %v answered %v", id, err)
	}
	state, _, err := repo.Load(ctx, id)
	if err != nil {
		t.Fatalf("loading %v answered %v", id, err)
	}
	return state.Minor
}

func twoTenants() *controlPlane {
	return &controlPlane{tenants: map[string]tenancy.Resolution{
		"acme":   {Reference: referenceOf("acme"), Lifecycle: tenancy.Active, Epoch: 1},
		"globex": {Reference: referenceOf("globex"), Lifecycle: tenancy.Active, Epoch: 1},
	}}
}

func TestAVerifiedScopeIsResolvedBeforeAnyEventCallAndARefusedOneReachesNoStore(t *testing.T) {
	t.Run("a bound tenant writes and reads its own history", func(t *testing.T) {
		built := newApplication(t, twoTenants(), false)
		acme := bound(t, built, "acme", tenancy.ClassWrite)
		globex := bound(t, built, "globex", tenancy.ClassWrite)
		id := accountID{tenant: "acme", number: "7"}

		if err := credit(t, built, acme, id, 500); err != nil {
			t.Fatalf("a bound write answered %v", err)
		}
		if held := balance(t, built, acme, id); held != 500 {
			t.Fatalf("the tenant that wrote 500 reads %d", held)
		}
		if held := balance(t, built, globex, accountID{tenant: "globex", number: "7"}); held != 0 {
			t.Fatalf("the other tenant reads %d for an account of its own that nobody credited", held)
		}
	})

	t.Run("an unbound context reaches no store at all", func(t *testing.T) {
		built := newApplication(t, twoTenants(), false)
		acme := bound(t, built, "acme", tenancy.ClassWrite)
		if err := credit(t, built, acme, accountID{tenant: "acme", number: "7"}, 500); err != nil {
			t.Fatalf("the write this case measures against answered %v", err)
		}
		entered := built.entered.Load()

		err := credit(t, built, t.Context(), accountID{tenant: "acme", number: "7"}, 900)
		if !errors.Is(err, tenancy.ErrNoScope) {
			t.Fatalf("work naming no tenant answered %v, and a context that carries no scope must not inherit one", err)
		}
		if after := built.entered.Load(); after != entered {
			t.Fatalf("a refused scope opened %d event doors, and it is supposed to be refused before there is a store to open", after-entered)
		}
		if held := balance(t, built, acme, accountID{tenant: "acme", number: "7"}); held != 500 {
			t.Fatalf("the tenant's balance is %d after a refused write of 900", held)
		}
	})

	t.Run("a scope another authority minted reaches no store at all", func(t *testing.T) {
		plane := twoTenants()
		built := newApplication(t, plane, false)
		foreign := newApplication(t, plane, false)
		entered := built.entered.Load()

		carried := bound(t, foreign, "acme", tenancy.ClassWrite)
		err := credit(t, built, carried, accountID{tenant: "acme", number: "7"}, 900)
		if !errors.Is(err, tenancy.ErrUntrusted) {
			t.Fatalf("a scope minted by a second authority answered %v", err)
		}
		if after := built.entered.Load(); after != entered {
			t.Fatalf("a scope this authority never minted opened %d event doors", after-entered)
		}
	})

	t.Run("a scope whose generation has moved reaches no store at all", func(t *testing.T) {
		plane := twoTenants()
		built := newApplication(t, plane, true)
		acme := bound(t, built, "acme", tenancy.ClassWrite)
		if err := credit(t, built, acme, accountID{tenant: "acme", number: "7"}, 500); err != nil {
			t.Fatalf("the write this case measures against answered %v", err)
		}
		entered := built.entered.Load()

		plane.tenants["acme"] = tenancy.Resolution{Reference: referenceOf("acme"), Lifecycle: tenancy.Active, Epoch: 2}
		err := credit(t, built, acme, accountID{tenant: "acme", number: "7"}, 900)
		if !errors.Is(err, tenancy.ErrStale) {
			t.Fatalf("a scope carried across a restore answered %v, and the generation it names is not the one the control plane serves", err)
		}
		if after := built.entered.Load(); after != entered {
			t.Fatalf("an out-of-generation scope opened %d event doors", after-entered)
		}
	})

	t.Run("a lifecycle that admits reads and not writes reaches a store for one and not the other", func(t *testing.T) {
		plane := twoTenants()
		built := newApplication(t, plane, true)
		acme := bound(t, built, "acme", tenancy.ClassWrite)
		if err := credit(t, built, acme, accountID{tenant: "acme", number: "7"}, 500); err != nil {
			t.Fatalf("the write this case measures against answered %v", err)
		}

		plane.tenants["acme"] = tenancy.Resolution{Reference: referenceOf("acme"), Lifecycle: tenancy.Suspended, Epoch: 1}
		entered := built.entered.Load()
		if err := credit(t, built, acme, accountID{tenant: "acme", number: "7"}, 900); !errors.Is(err, tenancy.ErrInactive) {
			t.Fatalf("a write for a suspended tenant answered %v", err)
		}
		if after := built.entered.Load(); after != entered {
			t.Fatalf("a suspended tenant's write opened %d event doors", after-entered)
		}
		if held := balance(t, built, acme, accountID{tenant: "acme", number: "7"}); held != 500 {
			t.Fatalf("a suspended tenant reads %d, and this policy admits its reads", held)
		}
	})

	// The control. Every assertion above is about a composition that routes
	// through the verified scope; none of them would notice if the event
	// vocabulary quietly kept tenants apart on its own. It does not, and this is
	// where that is written down: one store shared by two tenants, with the
	// tenant left out of the key, and the second tenant reads the first one's
	// money. If this subtest ever stops leaking, the subtests above have stopped
	// proving anything and should be read again rather than trusted.
	t.Run("the control: one store and a key without the tenant leaks", func(t *testing.T) {
		shared := openedStore(t)
		untenanted := event.Define[ledgerState]("eventflow.leak", func(id accountID) event.Key {
			return event.Key(id.number)
		})
		untenantedCredited := event.Declare(untenanted, "eventflow.leak.credited", event.From(event.JSON[credited]()),
			func(state ledgerState, carried credited) ledgerState {
				state.Minor += carried.Minor
				return state
			})
		repo, err := event.Bind(shared, untenanted)
		if err != nil {
			t.Fatalf("binding the untenanted aggregate answered %v", err)
		}

		ctx := t.Context()
		_, at, err := repo.Load(ctx, accountID{tenant: "acme", number: "7"})
		if err != nil {
			t.Fatalf("loading a fresh stream answered %v", err)
		}
		if _, _, err := repo.Append(ctx, at, untenantedCredited.New(accountID{tenant: "acme", number: "7"}, credited{Minor: 500})); err != nil {
			t.Fatalf("appending one tenant's credit answered %v", err)
		}

		other, _, err := repo.Load(ctx, accountID{tenant: "globex", number: "7"})
		if err != nil {
			t.Fatalf("loading the other tenant's account answered %v", err)
		}
		if other.Minor != 500 {
			t.Fatalf("a shared store keyed without the tenant answered %d for the other tenant, so the separation above is not the composition's and the cases above prove nothing", other.Minor)
		}
	})
}
