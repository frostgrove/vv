package crudsqlfx

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	"go.uber.org/fx"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/vvdb"
)

type trace struct{ calls []string }

type bareSource struct{ trace *trace }

func (bareSource) Exec(context.Context, string, ...any) (crud.Result, error) {
	return crud.Result{}, nil
}

func (this bareSource) Query(context.Context, string, ...any) (crud.Rows, error) {
	this.trace.calls = append(this.trace.calls, "source")
	return nil, nil
}

func (bareSource) Dialect() crud.Dialect { return crud.SQLite{} }

type layer struct {
	crud.Source
	name  string
	trace *trace
}

func (this layer) Query(ctx context.Context, query string, args ...any) (crud.Rows, error) {
	this.trace.calls = append(this.trace.calls, this.name)
	return this.Source.Query(ctx, query, args...)
}

func layering(recorded *trace, name string) Wrapping {
	return Wrapping{
		Name: name,
		Wrap: func(next crud.Source) crud.Source { return layer{Source: next, name: name, trace: recorded} },
	}
}

func asked(t *testing.T, source crud.Source) {
	t.Helper()
	if _, err := source.Query(context.Background(), "SELECT 1"); err != nil {
		t.Fatalf("asking the source: %v", err)
	}
}

func TestTheChainIsTheOneTheDeploymentWroteDown(t *testing.T) {
	recorded := &trace{}

	source, err := chain(bareSource{trace: recorded},
		[]string{"telemetry", "slow-query", "replica"},
		[]Wrapping{layering(recorded, "replica"), layering(recorded, "telemetry"), layering(recorded, "slow-query")},
	)
	if err != nil {
		t.Fatalf("the declared chain was refused: %v", err)
	}
	asked(t, source)

	want := "telemetry slow-query replica source"
	if got := strings.Join(recorded.calls, " "); got != want {
		t.Fatalf("the call went through %q, and the chain this deployment declared is %q", got, want)
	}
}

func TestTheChainDoesNotDependOnTheOrderTheGroupArrivedIn(t *testing.T) {
	forwards, backwards := &trace{}, &trace{}
	declared := []string{"telemetry", "replica"}

	first, err := chain(bareSource{trace: forwards}, declared,
		[]Wrapping{layering(forwards, "telemetry"), layering(forwards, "replica")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := chain(bareSource{trace: backwards}, declared,
		[]Wrapping{layering(backwards, "replica"), layering(backwards, "telemetry")})
	if err != nil {
		t.Fatal(err)
	}
	asked(t, first)
	asked(t, second)

	if strings.Join(forwards.calls, " ") != strings.Join(backwards.calls, " ") {
		t.Fatalf("one group in two arrival orders built two different sources: %q and %q",
			forwards.calls, backwards.calls)
	}
}

func TestASourceNobodyDeclaredALayerAroundIsTheSourceItself(t *testing.T) {
	recorded := &trace{}
	base := bareSource{trace: recorded}

	built, err := chain(base, nil, nil)
	if err != nil {
		t.Fatalf("a deployment that declared no layer was refused: %v", err)
	}
	if built != crud.Source(base) {
		t.Fatalf("an empty declaration still changed the source: %#v", built)
	}
}

func TestARefusedChainIsPutAroundNothingAtAll(t *testing.T) {
	recorded := &trace{}
	applied := false
	watched := layering(recorded, "telemetry")
	wrap := watched.Wrap
	watched.Wrap = func(next crud.Source) crud.Source {
		applied = true
		return wrap(next)
	}

	_, err := chain(bareSource{trace: recorded},
		[]string{"telemetry", "slow-query"},
		[]Wrapping{watched},
	)

	if !errors.Is(err, ErrWrappingMissing) {
		t.Fatalf("a declared layer nobody contributed was accepted: %v", err)
	}
	if applied {
		t.Fatal("a refused chain was put around the source before it was refused, so a refusal leaves a half-wrapped source behind")
	}
}

func TestWhatIsMissingAndWhatWasNeverDeclaredAreBothNamed(t *testing.T) {
	recorded := &trace{}

	_, missing := chain(bareSource{trace: recorded}, []string{"telemetry", "replica"}, nil)
	if !errors.Is(missing, ErrWrappingMissing) {
		t.Fatalf("a chain nothing contributed to was accepted: %v", missing)
	}
	for _, name := range []string{"telemetry", "replica"} {
		if !strings.Contains(missing.Error(), name) {
			t.Fatalf("the refusal does not say %s is the one nobody contributed: %v", name, missing)
		}
	}

	_, undeclared := chain(bareSource{trace: recorded}, nil,
		[]Wrapping{layering(recorded, "replica"), layering(recorded, "telemetry")})
	if !errors.Is(undeclared, ErrWrappingUndeclared) {
		t.Fatalf("a layer this deployment never declared was put on anyway: %v", undeclared)
	}
	if got := undeclared.Error(); !strings.Contains(got, "replica, telemetry") {
		t.Fatalf("the refusal names the undeclared layers in the order fx happened to hand them over: %v", got)
	}
}

func TestTheRefusals(t *testing.T) {
	recorded := &trace{}
	base := bareSource{trace: recorded}
	identity := func(next crud.Source) crud.Source { return next }

	for _, testCase := range []struct {
		name        string
		declared    []string
		contributed []Wrapping
		want        error
	}{
		{
			name:        "a layer with no name",
			declared:    []string{"telemetry"},
			contributed: []Wrapping{{Wrap: identity}},
			want:        ErrWrappingUnnamed,
		},
		{
			name:        "a layer with no function",
			declared:    []string{"telemetry"},
			contributed: []Wrapping{{Name: "telemetry"}},
			want:        ErrWrappingEmpty,
		},
		{
			name:        "one name contributed twice",
			declared:    []string{"telemetry"},
			contributed: []Wrapping{{Name: "telemetry", Wrap: identity}, {Name: "telemetry", Wrap: identity}},
			want:        ErrWrappingTwice,
		},
		{
			name:        "one name declared twice",
			declared:    []string{"telemetry", "telemetry"},
			contributed: []Wrapping{{Name: "telemetry", Wrap: identity}},
			want:        ErrWrappingTwice,
		},
		{
			name:        "a layer nobody declared",
			declared:    nil,
			contributed: []Wrapping{{Name: "telemetry", Wrap: identity}},
			want:        ErrWrappingUndeclared,
		},
		{
			name:        "a declared layer nobody contributed",
			declared:    []string{"telemetry"},
			contributed: nil,
			want:        ErrWrappingMissing,
		},
		{
			name:        "a layer that answers with no source",
			declared:    []string{"telemetry"},
			contributed: []Wrapping{{Name: "telemetry", Wrap: func(crud.Source) crud.Source { return nil }}},
			want:        ErrWrappingDropped,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			built, err := chain(base, testCase.declared, testCase.contributed)

			if !errors.Is(err, testCase.want) {
				t.Fatalf("got %v, and what this refuses is %v", err, testCase.want)
			}
			if built != nil {
				t.Fatalf("a refusal still answered with a source: %#v", built)
			}
		})
	}
}

// answersNothing is a pool that accepts every connection and answers every query
// with no rows. It exists because what these tests are about is the source the
// graph hands out, and reaching that source means letting the schema read that
// builds it succeed without a database.
type answersNothing struct{}

func (answersNothing) Open(string) (driver.Conn, error) { return answersNothingConn{}, nil }

type answersNothingConn struct{}

func (answersNothingConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("ask with a context")
}
func (answersNothingConn) Close() error               { return nil }
func (answersNothingConn) Begin() (driver.Tx, error)  { return nil, errors.New("ask with a context") }
func (answersNothingConn) Ping(context.Context) error { return nil }

func (answersNothingConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return noRows{}, nil
}

type noRows struct{}

func (noRows) Columns() []string         { return nil }
func (noRows) Close() error              { return nil }
func (noRows) Next([]driver.Value) error { return io.EOF }

func init() { sql.Register("crudsqlfx-answers-nothing", answersNothing{}) }

func graphOver(t *testing.T, resolved *crud.Source, declared []string, options ...fx.Option) error {
	t.Helper()
	configuration := &vvdb.Config{Engine: vvdb.SQLite, Driver: "crudsqlfx-answers-nothing", Path: ":memory:"}
	return fx.New(append([]fx.Option{
		fx.NopLogger,
		Module(configuration, Layers(declared...)),
		fx.Invoke(func(source crud.Source) { *resolved = source }),
	}, options...)...).Err()
}

func TestTheSourceTheGraphHandsOutCarriesTheDeclaredChain(t *testing.T) {
	recorded := &trace{}
	var resolved crud.Source

	err := graphOver(t, &resolved, []string{"telemetry", "replica"},
		fx.Provide(AsWrapping(func() Wrapping { return layering(recorded, "replica") })),
		fx.Provide(AsWrapping(func() Wrapping { return layering(recorded, "telemetry") })),
	)
	if err != nil {
		t.Fatalf("the graph does not build over a declared chain: %v", err)
	}
	asked(t, resolved)

	want := "telemetry replica"
	if got := strings.Join(recorded.calls, " "); got != want {
		t.Fatalf("the query went through %q, and the chain this graph declared is %q", got, want)
	}
}

// The control for the test above: a graph that declares nothing gets the source
// this module wired, so a layer seen there was put on by the declaration and not
// by something this module does to every source.
func TestASourceNoDeploymentWrappedIsTheSourceTheModuleWired(t *testing.T) {
	var resolved crud.Source

	if err := graphOver(t, &resolved, nil); err != nil {
		t.Fatalf("the graph does not build: %v", err)
	}

	if _, wrapped := resolved.(layer); wrapped {
		t.Fatalf("a graph that declared no layer still got a wrapped source: %#v", resolved)
	}
}

// A contribution that forgets AsWrapping is provided and consumed by nobody. It
// is the quietest way to lose a layer, and the declaration is what turns it into
// a start that stops.
func TestAContributionThatNeverReachedTheGroupStopsTheStart(t *testing.T) {
	recorded := &trace{}
	var resolved crud.Source

	err := graphOver(t, &resolved, []string{"telemetry"},
		fx.Provide(func() Wrapping { return layering(recorded, "telemetry") }),
	)

	if !errors.Is(err, ErrWrappingMissing) {
		t.Fatalf("a layer that never reached the group left a graph that builds and measures nothing: %v", err)
	}
}

func TestALayerNoDeploymentDeclaredStopsTheStart(t *testing.T) {
	recorded := &trace{}
	var resolved crud.Source

	err := graphOver(t, &resolved, nil,
		fx.Provide(AsWrapping(func() Wrapping { return layering(recorded, "telemetry") })),
	)

	if !errors.Is(err, ErrWrappingUndeclared) {
		t.Fatalf("a layer nobody wrote down was put on the source anyway: %v", err)
	}
}

// A harness that cannot reach a database replaces the base, and the chain the
// deployment declared is still what every repository in that graph reads
// through. Replacing the source itself is the other gesture and means the other
// thing: this one keeps the layers.
func TestAReplacedBaseStillCarriesTheDeclaredChain(t *testing.T) {
	recorded := &trace{}
	var resolved crud.Source

	err := graphOver(t, &resolved, []string{"telemetry"},
		fx.Provide(AsWrapping(func() Wrapping { return layering(recorded, "telemetry") })),
		Base(bareSource{trace: recorded}),
	)
	if err != nil {
		t.Fatalf("the graph does not build over a supplied base: %v", err)
	}
	asked(t, resolved)

	want := "telemetry source"
	if got := strings.Join(recorded.calls, " "); got != want {
		t.Fatalf("the query went through %q, and the base this graph supplied under the declared chain is %q", got, want)
	}
}
