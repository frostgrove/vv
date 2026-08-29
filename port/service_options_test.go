package port

import (
	"reflect"
	"testing"

	"github.com/frostgrove/vv/errs"
)

// WithPaths is the service half of the path chain ([[D-043]]): the model's
// field names to the ones its commands use. An option that were silently
// ignored would show up as a violation naming a field the client never sent —
// at request time, which is exactly what [[D-021]] says must not happen.
func TestWithPathsPutsTheServicesHopIntoTheChain(t *testing.T) {
	declared := Fields{"Name": At("title")}
	service := NewService[widget, int64, widgetUpdate](&fakeRepo{}, WithPaths(declared))

	if service.Paths() == nil {
		t.Fatal("a service built with WithPaths answers no hop at all")
	}

	hops := Hops[widget, int64, widgetUpdate, widget](service, Identity[widget]())
	if len(hops) != 1 {
		t.Fatalf("the declared hop reached the chain %d times, want once", len(hops))
	}
	got, ok := errs.Chain(hops...).Resolve(errs.Path{errs.Named("Name")})
	want := At("title")
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("a violation at the model's Name came out at %v, want the declared %v", got, want)
	}

	// The control. The same service without the option contributes no hop and
	// the path arrives unchanged — so the leg above measures the declaration
	// rather than a chain that rewrites Name whatever it was handed.
	bare := NewService[widget, int64, widgetUpdate](&fakeRepo{})
	if bare.Paths() != nil {
		t.Fatalf("a service with no WithPaths answers the hop %v", bare.Paths())
	}
	hops = Hops[widget, int64, widgetUpdate, widget](bare, Identity[widget]())
	if len(hops) != 0 {
		t.Fatalf("a service with no WithPaths contributed %d hops", len(hops))
	}
	got, ok = errs.Chain(hops...).Resolve(errs.Path{errs.Named("Name")})
	if !ok || !reflect.DeepEqual(got, errs.Path{errs.Named("Name")}) {
		t.Fatalf("with nothing declared the path came out at %v, %v, want it unchanged", got, ok)
	}
}
