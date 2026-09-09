package projection_test

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
	"github.com/frostgrove/vv/runtime"
)

// A halt is terminal for this value's life and it is not a retry loop with a
// longer period: over a window after it, this projection issues nothing at all.
// The two implementations that pass every other test are the one that re-applies
// the page its classifier called permanent and the one that keeps a dead
// projection's poll on the database.
func TestAHaltedProjectionIssuesNothingAndReportsThroughReady(t *testing.T) {
	ctx := context.Background()
	stand := newStand(t, eventmemory.Spec{})
	stand.append(t, "a", "one")

	spec := stand.spec("orders", projection.HandlerFunc(func(context.Context, projection.Batch) error {
		return fmt.Errorf("this build cannot read the payload: %w", event.ErrPayload)
	}))
	held := newProjection(t, spec)
	stop, returned := running(t, held)

	halted := stand.observer.await(t, "the projection halted", func(state projection.State) bool {
		return state.Phase == projection.PhaseHalted
	})
	stand.ticks.expect(t, time.Second, "the poll the loop opens with")

	reads, saves, published := stand.read.reads.Load(), stand.points.saves.Load(), stand.observer.published()
	stand.ticks.quiet(t, 100*time.Millisecond)
	if now := stand.read.reads.Load(); now != reads {
		t.Fatalf("%d reads were issued after the halt, and a halted projection reads nothing", now-reads)
	}
	if now := stand.points.saves.Load(); now != saves {
		t.Fatalf("%d saves were issued after the halt", now-saves)
	}
	if now := stand.observer.published(); now != published {
		t.Fatalf("%d states were published after the halt, and a halt is published once", now-published)
	}
	if count := stand.observer.counted(projection.PhaseHalted); count != 1 {
		t.Fatalf("the halt was published %d times", count)
	}

	drained := make(chan error, 1)
	go func() { drained <- held.Drain(ctx) }()
	select {
	case err := <-drained:
		if err != nil {
			t.Fatalf("draining a halted projection answered %v", err)
		}
	case <-time.After(settle):
		t.Fatal("draining a halted projection waited, and it holds no pass to finish")
	}

	if state := held.State(); state.Phase != projection.PhaseHalted || state.Err == nil {
		t.Fatalf("a halted projection answers %+v, and it keeps answering", state)
	}
	if err := held.Ready(ctx); !errors.Is(err, event.ErrPayload) {
		t.Fatalf("a halted projection's readiness is %v, where it reports what halted it", err)
	}
	if !errors.Is(halted.Err, projection.ErrHalted) {
		t.Fatalf("the halt was published as %v", halted.Err)
	}

	stop()
	if err := returns(t, returned); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run answered %v, where a halted projection returns its context's error and never ErrHalted", err)
	}
}

// The drain exists to close the window between a handler's write and the advance
// that accounts for it, so the control is the same shutdown without one: the page
// is unsaved and arrives again on the next start.
func TestADrainFinishesThePassInFlightAndAHaltedOneReturnsAtOnce(t *testing.T) {
	t.Run("the pass in flight finishes, handler and advance both", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "one")

		inside, release := make(chan struct{}, 1), make(chan struct{})
		spec := stand.spec("orders", projection.HandlerFunc(func(_ context.Context, batch projection.Batch) error {
			inside <- struct{}{}
			<-release
			stand.model.write(payloadsOf(batch.Envelopes)...)
			return nil
		}))
		held := newProjection(t, spec)
		stop, returned := running(t, held)
		<-inside

		drained := make(chan error, 1)
		go func() { drained <- held.Drain(context.Background()) }()
		close(release)
		select {
		case err := <-drained:
			if err != nil {
				t.Fatalf("the drain answered %v", err)
			}
		case <-time.After(settle):
			t.Fatal("the drain never returned after the pass it was waiting for finished")
		}

		if rows := stand.model.rows(); !same(rows, []string{"one"}) {
			t.Fatalf("the drained pass left %v in the read model", rows)
		}
		if row := stand.row(t, "orders"); row.Advance != 1 {
			t.Fatalf("the drain returned with the row at advance %d, so a shutdown landed between the handler and the advance", row.Advance)
		}
		stop()
		if err := returns(t, returned); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run answered %v after the drain", err)
		}
	})

	t.Run("the control: a cancellation with no drain leaves the page unsaved", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		stand.append(t, "a", "one")

		inside := make(chan struct{}, 1)
		spec := stand.spec("orders", projection.HandlerFunc(func(ctx context.Context, _ projection.Batch) error {
			inside <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		}))
		stop, returned := running(t, newProjection(t, spec))
		<-inside
		stop()
		if err := returns(t, returned); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run answered %v", err)
		}
		if row := stand.row(t, "orders"); !row.Fresh() {
			t.Fatalf("the row is at advance %d after a cancellation inside the handler", row.Advance)
		}

		resumed := stand.spec("orders", applies(stand.model))
		running(t, newProjection(t, resumed))
		stand.observer.await(t, "the page arrived again", func(state projection.State) bool {
			return state.Progress.Applied == 1
		})
		if rows := stand.model.rows(); !same(rows, []string{"one"}) {
			t.Fatalf("the next start applied %v, where the page the cancelled pass abandoned is redelivered", rows)
		}
	})
}

// The host owns the loop: New starts nothing, the supervisor starts it, and what
// an operator reads about it comes back through the supervisor's own two answers.
func TestTheSupervisorHoldsAProjectionAndNewStartsNothing(t *testing.T) {
	ctx := context.Background()
	stand := newStand(t, eventmemory.Spec{})
	stand.append(t, "a", "one")

	held := newProjection(t, stand.spec("orders", applies(stand.model)))
	time.Sleep(50 * time.Millisecond)
	if reads, saves := stand.read.reads.Load(), stand.points.saves.Load(); reads != 0 || saves != 0 {
		t.Fatalf("a projection nobody started issued %d reads and %d saves", reads, saves)
	}
	if state := held.State(); state.Phase != projection.PhaseStarting || state.Projection != "orders" {
		t.Fatalf("a projection nobody started answers %+v", state)
	}
	if declared := held.Declaration(); declared.Placement != runtime.Singleton || declared.Durability != runtime.Durable {
		t.Fatalf("a projection declares %+v, where one checkpoint row is one writer and its progress outlives the process", declared)
	}
	if name := held.Name(); name != "vv.event.projection.orders" {
		t.Fatalf("a projection is supervised as %q, and an operator reads the name they chose", name)
	}

	supervisor, err := runtime.NewSupervisor(runtime.Spec{
		Runners: []runtime.Runner{held},
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("a supervisor over one projection was refused: %v", err)
	}
	if err := supervisor.Start(ctx); err != nil {
		t.Fatalf("starting the supervisor answered %v", err)
	}
	stand.observer.await(t, "the page applied", func(state projection.State) bool {
		return state.Progress.Applied == 1
	})
	if err := supervisor.Ready(ctx); err != nil {
		t.Fatalf("a supervisor holding a healthy projection answered %v", err)
	}

	states := supervisor.States()
	if len(states) != 1 || states[0].Name != held.Name() {
		t.Fatalf("the supervisor holds %+v, where the projection is visible under its own name", states)
	}
	if err := supervisor.Stop(ctx); err != nil {
		t.Fatalf("stopping the supervisor answered %v", err)
	}
	if rows := stand.model.rows(); !same(rows, []string{"one"}) {
		t.Fatalf("the supervised projection applied %v", rows)
	}
}

// The four shapes a package the host supervises may not carry, read out of its
// own source. A walk that found nothing because it looked in the wrong place is
// what the fixture below refuses.
func TestTheProjectionStartsNothingAndReadsNoEnvironment(t *testing.T) {
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("reading this package's own files answered %v", err)
	}
	walked := 0
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		walked++
		held, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("reading %s answered %v", source, err)
		}
		for _, complaint := range startsOrReadsSomething(t, source, string(held)) {
			t.Error(complaint)
		}
	}
	if walked < 9 {
		t.Fatalf("%d files of this package were walked, so this read the wrong directory", walked)
	}

	t.Run("the control: every shape is reported when it is there", func(t *testing.T) {
		reported := strings.Join(startsOrReadsSomething(t, "fixture.go", forbiddenShapes), "\n")
		for _, shape := range []string{"goroutine", "init", "log.Printf", "fmt.Println", "os.Getenv", "counted"} {
			if !strings.Contains(reported, shape) {
				t.Fatalf("the fixture's %s was not reported, so the arm that would have found it proves nothing:\n%s", shape, reported)
			}
		}
	})
}

const forbiddenShapes = `package fixture

import (
	"fmt"
	"log"
	"os"
)

var counted int

func init() { counted = 1 }

func startsOne() {
	go func() { counted++ }()
	log.Printf("started")
	fmt.Println(os.Getenv("HOME"))
}
`

var forbiddenCalls = map[string]bool{
	"log.Printf": true, "log.Print": true, "log.Println": true, "log.Fatal": true, "log.Fatalf": true,
	"fmt.Print": true, "fmt.Printf": true, "fmt.Println": true,
	"os.Getenv": true, "os.LookupEnv": true, "os.Environ": true,
}

// Package-level names are collected first and then read for a write, because a
// var this package assigns to is state every value of it shares whether or not
// the assignment is in the same file as the declaration.
func startsOrReadsSomething(t *testing.T, name, source string) []string {
	t.Helper()
	fileSet := token.NewFileSet()
	parsed, err := parser.ParseFile(fileSet, name, source, 0)
	if err != nil {
		t.Fatalf("parsing %s answered %v", name, err)
	}
	var complaints []string
	at := func(node ast.Node, what string) {
		complaints = append(complaints, fmt.Sprintf("%s: %s", fileSet.Position(node.Pos()), what))
	}

	declared := map[string]bool{}
	for _, declaration := range parsed.Decls {
		general, isGeneral := declaration.(*ast.GenDecl)
		if !isGeneral || general.Tok != token.VAR {
			continue
		}
		for _, spec := range general.Specs {
			for _, held := range spec.(*ast.ValueSpec).Names {
				declared[held.Name] = held.Name != "_"
			}
		}
	}

	ast.Inspect(parsed, func(node ast.Node) bool {
		switch found := node.(type) {
		case *ast.GoStmt:
			at(found, "a goroutine is started here, and this package's one loop runs on the caller's own")
		case *ast.FuncDecl:
			if found.Recv == nil && found.Name.Name == "init" {
				at(found, "an init function runs before a composition root chose anything")
			}
		case *ast.CallExpr:
			if selector, isSelector := found.Fun.(*ast.SelectorExpr); isSelector {
				if pkg, isName := selector.X.(*ast.Ident); isName && forbiddenCalls[pkg.Name+"."+selector.Sel.Name] {
					at(found, pkg.Name+"."+selector.Sel.Name+" writes a line or reads an environment this package was not given")
				}
			}
		case *ast.AssignStmt:
			for _, target := range found.Lhs {
				if held, isName := target.(*ast.Ident); isName && declared[held.Name] {
					at(found, "the package-level "+held.Name+" is written here")
				}
			}
		case *ast.IncDecStmt:
			if held, isName := found.X.(*ast.Ident); isName && declared[held.Name] {
				at(found, "the package-level "+held.Name+" is written here")
			}
		}
		return true
	})
	return complaints
}
