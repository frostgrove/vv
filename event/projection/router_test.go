package projection_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
)

// The whole of the routing rule over a log that carries three aggregates: each
// event reaches the applier its own declaration produces, the ignored type
// reaches none, and the third aggregate's events are skipped and counted.
//
// The two controls are what make it discriminating rather than either a
// universal refusal or a universal silence: the same router with one On removed
// INSIDE a covered family halts on the first such event, and the same removal
// OUTSIDE every covered family is skipped and counted. A router that refused
// both, or skipped both, fails exactly one of them.
func TestARouterRoutesDeclaresAndRefusesTheUnclaimed(t *testing.T) {
	t.Run("every declared route runs and the foreign family is counted", func(t *testing.T) {
		stand, written := routedStand(t)
		router := completeRouter(t, stand.model, routed{placed: true, paid: true, authorised: true, captured: true})

		spec := stand.spec("orders.read-model", router)
		running(t, newProjection(t, spec))
		stand.observer.await(t, "the whole log was applied", func(state projection.State) bool {
			return state.Progress.Highest >= written && state.Phase == projection.PhaseFollowing
		})

		rows := stand.model.rows()
		want := []string{"placed:chair x1", "placed:desk x2", "paid:900", "authorised:900", "captured:900"}
		if !same(rows, want) {
			t.Fatalf("the four appliers wrote %v where their declarations produce %v", rows, want)
		}
		if skipped := router.Skipped(); skipped != 2 {
			t.Fatalf("two events of a family this router routes nothing of were skipped and %d were counted", skipped)
		}
		if state := stand.observer.await(t, "it is following", func(state projection.State) bool {
			return state.Phase == projection.PhaseFollowing
		}); state.Progress.Quarantined != 0 {
			t.Fatalf("a run in which nothing failed quarantined %d envelopes", state.Progress.Quarantined)
		}
	})

	t.Run("the control: one On removed inside a covered family halts", func(t *testing.T) {
		stand, _ := routedStand(t)
		router := completeRouter(t, stand.model, routed{placed: true, authorised: true, captured: true})

		running(t, newProjection(t, stand.spec("orders.read-model", router)))
		halted := stand.observer.await(t, "the projection halted", func(state projection.State) bool {
			return state.Phase == projection.PhaseHalted
		})
		if !errors.Is(halted.Err, projection.ErrUnrouted) {
			t.Fatalf("a type of a routed family that no route claims halted with %v, where a forgotten registration is ErrUnrouted", halted.Err)
		}
		if !strings.Contains(halted.Err.Error(), "orders.paid") {
			t.Fatalf("the refusal is %q and does not name the type nobody routed", halted.Err)
		}
		if row := stand.row(t, "orders.read-model"); !row.Fresh() {
			t.Fatalf("the checkpoint advanced to %d over a page that halted, so a whole event type is dropped for the life of the deployment", row.Advance)
		}
		if router.Skipped() != 0 {
			t.Fatal("the unclaimed type of a covered family was counted as a skip, which is the silence this refusal exists to replace")
		}
	})

	t.Run("the control: the same removal outside every covered family is skipped", func(t *testing.T) {
		stand, written := routedStand(t)
		router := completeRouter(t, stand.model, routed{placed: true, paid: true, authorised: true, captured: true})

		running(t, newProjection(t, stand.spec("orders.read-model", router)))
		stand.observer.await(t, "the whole log was applied", func(state projection.State) bool {
			return state.Progress.Highest >= written && state.Phase == projection.PhaseFollowing
		})
		if router.Skipped() != 2 {
			t.Fatalf("the shipping events are of a family no route claims and %d were skipped", router.Skipped())
		}
		if state := stand.observer.await(t, "it is following", func(state projection.State) bool {
			return state.Phase == projection.PhaseFollowing
		}); state.Err != nil {
			t.Fatalf("a family this router routes nothing of refused the page: %v", state.Err)
		}
	})

	t.Run("RefuseForeign halts on the first foreign event with the same refusal", func(t *testing.T) {
		stand, _ := routedStand(t)
		router := projection.NewRouter(projection.RefuseForeign)
		declareRoutes(t, router, stand.model, routed{placed: true, paid: true, authorised: true, captured: true})

		running(t, newProjection(t, stand.spec("orders.read-model", router)))
		halted := stand.observer.await(t, "the projection halted", func(state projection.State) bool {
			return state.Phase == projection.PhaseHalted
		})
		if !errors.Is(halted.Err, projection.ErrUnrouted) {
			t.Fatalf("RefuseForeign halted with %v, where a consumer reads one refusal rather than two", halted.Err)
		}
		if projection.Classify(halted.Err) != projection.Permanent {
			t.Fatal("the default classifier calls a foreign refusal retryable, so a router that must see every fact spins instead of stopping")
		}
	})

	t.Run("every registration refusal is a declaration refusal", func(t *testing.T) {
		facts := declareOrders(t)
		apply := func(context.Context, placed, event.Envelope) error { return nil }

		for _, refused := range []struct {
			what string
			run  func() error
		}{
			{"a nil router", func() error { return projection.TryOn(nil, facts.placed, apply) }},
			{"a nil fact", func() error {
				return projection.TryOn[order, string, placed](projection.NewRouter(projection.SkipForeign), nil, apply)
			}},
			{"a nil applier", func() error {
				return projection.TryOn[order, string, placed](projection.NewRouter(projection.SkipForeign), facts.placed, nil)
			}},
			{"a nil router at Ignore", func() error { return projection.TryIgnore(nil, "orders", "orders.archived") }},
			{"a family the identifier rule refuses", func() error {
				return projection.TryIgnore(projection.NewRouter(projection.SkipForeign), "orders\x00", "orders.archived")
			}},
			{"a type name the identifier rule refuses", func() error {
				return projection.TryIgnore(projection.NewRouter(projection.SkipForeign), "orders", "orders.[archived]")
			}},
			{"the same route twice", func() error {
				router := projection.NewRouter(projection.SkipForeign)
				projection.On(router, facts.placed, apply)
				return projection.TryOn(router, facts.placed, apply)
			}},
			{"a type both routed and ignored", func() error {
				router := projection.NewRouter(projection.SkipForeign)
				projection.On(router, facts.placed, apply)
				return projection.TryIgnore(router, "orders", "orders.placed")
			}},
			{"a registration after the first page", func() error {
				router := projection.NewRouter(projection.SkipForeign)
				if err := router.Apply(context.Background(), projection.Batch{}); err != nil {
					return err
				}
				return projection.TryOn(router, facts.placed, apply)
			}},
			{"an ignore after the first page", func() error {
				router := projection.NewRouter(projection.SkipForeign)
				if err := router.Apply(context.Background(), projection.Batch{}); err != nil {
					return err
				}
				return projection.TryIgnore(router, "orders", "orders.archived")
			}},
		} {
			t.Run(refused.what, func(t *testing.T) {
				err := refused.run()
				if err == nil {
					t.Fatalf("%s was accepted", refused.what)
				}
				if !errors.Is(err, event.ErrDeclaration) {
					t.Fatalf("%s was refused as %v, where every registration refusal is a declaration refusal", refused.what, err)
				}
			})
		}

		t.Run("the control: the legal declarations every case above breaks", func(t *testing.T) {
			router := projection.NewRouter(projection.SkipForeign)
			if err := projection.TryOn(router, facts.placed, apply); err != nil {
				t.Fatalf("a legal route was refused: %v", err)
			}
			if err := projection.TryIgnore(router, "orders", "orders.archived"); err != nil {
				t.Fatalf("a legal ignore was refused: %v", err)
			}
		})

		t.Run("On panics where TryOn returns", func(t *testing.T) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					t.Fatal("On accepted a nil applier, where a declaration mistake is a panic")
				}
				if err, is := recovered.(error); !is || !errors.Is(err, event.ErrDeclaration) {
					t.Fatalf("On panicked with %v rather than with the refusal TryOn returns", recovered)
				}
			}()
			projection.On[order, string, placed](projection.NewRouter(projection.SkipForeign), facts.placed, nil)
		})
	})

	// The rule the router applies to a name it is given is the kernel's, and this
	// is what says so rather than a comment: the same names go through the
	// kernel's own family door and the two must agree, so a rule that drifts is a
	// red line here rather than a router that accepts a name no envelope can
	// carry.
	t.Run("the identifier rule is the kernel's", func(t *testing.T) {
		for _, name := range []string{
			"orders.placed", "", "orders\x00v1", "orders[1]", "orders]", "\xff\xfe", "ok.name",
			strings.Repeat("o", event.MaxNameBytes), strings.Repeat("o", event.MaxNameBytes+1),
		} {
			_, kernel := event.TryDefine[order, string](name, func(id string) event.Key { return event.Key(id) })
			router := projection.TryIgnore(projection.NewRouter(projection.SkipForeign), name, "a.type")
			if (kernel == nil) != (router == nil) {
				t.Fatalf("the kernel answers %v for a family of %d bytes and this router answers %v, so the two rules have drifted", kernel, len(name), router)
			}
		}
	})
}

type routed struct{ placed, paid, authorised, captured bool }

func declareRoutes(t *testing.T, router *projection.Router, model *model, which routed) {
	t.Helper()
	orders, payments := declareOrders(t), declarePayments(t)
	if which.placed {
		projection.On(router, orders.placed, func(_ context.Context, carried placed, _ event.Envelope) error {
			model.write("placed:" + carried.Item + " x" + strconv.Itoa(carried.Quantity))
			return nil
		})
	}
	if which.paid {
		projection.On(router, orders.paid, func(_ context.Context, carried paid, _ event.Envelope) error {
			model.write("paid:" + strconv.FormatInt(carried.Amount, 10))
			return nil
		})
	}
	projection.Ignore(router, "orders", orders.archived.Name())
	if which.authorised {
		projection.On(router, payments.authorised, func(_ context.Context, carried authorised, _ event.Envelope) error {
			model.write("authorised:" + strconv.FormatInt(carried.Amount, 10))
			return nil
		})
	}
	if which.captured {
		projection.On(router, payments.captured, func(_ context.Context, carried captured, _ event.Envelope) error {
			model.write("captured:" + strconv.FormatInt(carried.Amount, 10))
			return nil
		})
	}
}

func completeRouter(t *testing.T, model *model, which routed) *projection.Router {
	t.Helper()
	router := projection.NewRouter(projection.SkipForeign)
	declareRoutes(t, router, model, which)
	return router
}

// A log carrying two routed families and one foreign one, with the first
// orders.placed written by a build that declared one revision — so the upcaster
// runs over a payload that really is revision 1 rather than over a value this
// test invented.
func routedStand(t *testing.T) (*stand, event.Position) {
	t.Helper()
	stand := newStand(t, eventmemory.Spec{})

	first := declareOrdersV1(t)
	wrote(t, writes(t, stand.store, first.aggregate), "a", first.placed.New("a", placedV1{Item: "chair"}))

	current := declareOrders(t)
	orders := writes(t, stand.store, current.aggregate)
	wrote(t, orders, "a",
		current.placed.New("a", placed{Item: "desk", Quantity: 2}),
		current.paid.New("a", paid{Amount: 900}),
		current.archived.New("a", archived{Reason: "done"}))

	pay := declarePayments(t)
	payments := writes(t, stand.store, pay.aggregate)
	wrote(t, payments, "p", pay.authorised.New("p", authorised{Amount: 900}), pay.captured.New("p", captured{Amount: 900}))

	ship := declareShipping(t)
	shipments := writes(t, stand.store, ship.aggregate)
	wrote(t, shipments, "s", ship.dispatched.New("s", dispatched{Carrier: "dhl"}))
	wrote(t, shipments, "s", ship.dispatched.New("s", dispatched{Carrier: "ups"}))

	page, _, err := stand.store.ReadAll(context.Background(), "")
	if err != nil {
		t.Fatalf("reading the log this stand wrote answered %v", err)
	}
	return stand, page[len(page)-1].Position
}
