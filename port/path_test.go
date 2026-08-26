package port

import (
	"reflect"
	"testing"

	"github.com/frostgrove/vv/errs"
)

// A declared head is replaced and the rest of the path rides along, which is
// what makes one entry enough for every violation under it.
func TestADeclaredHeadIsRewrittenAndTheTailSurvives(t *testing.T) {
	// Built with spare capacity on purpose. A composite literal has none, so a
	// resolver that appended the tail onto the map's own slice would silently
	// reallocate and the write-through check at the bottom would prove nothing.
	to := make(errs.Path, 0, 8)
	to = append(to, errs.Named("shipping"), errs.Named("line1"))
	f := Fields{"Line1": to}

	got, ok := f.Resolve(errs.Path{errs.Named("Line1")})
	if !ok {
		t.Fatal("a declared head declined; a hop that owns the mapping must never decline it")
	}
	if want := (errs.Path{errs.Named("shipping"), errs.Named("line1")}); !reflect.DeepEqual(got, want) {
		t.Fatalf("a declared head resolved to %v, want %v", got, want)
	}

	got, ok = f.Resolve(errs.Path{errs.Named("Line1"), errs.Indexed(2), errs.Named("Zip")})
	want := errs.Path{errs.Named("shipping"), errs.Named("line1"), errs.Indexed(2), errs.Named("Zip")}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("the tail was lost: %v, %v, want %v", got, ok, want)
	}

	// The declared path is shared by every request that hits this field, so two
	// resolutions must not be able to reach each other. A hop that appended the
	// tail onto the map's own slice writes into the spare capacity above it, and
	// the second request then rewrites the first one's last step in place — a
	// corrupted field path under load, which is the worst way for this to fail.
	first, _ := f.Resolve(errs.Path{errs.Named("Line1"), errs.Named("Zip")})
	second, _ := f.Resolve(errs.Path{errs.Named("Line1"), errs.Named("City")})
	if first[2].Name != "Zip" || second[2].Name != "City" {
		t.Fatalf("two resolutions share a backing array: they answered %v and %v", first, second)
	}
}

// An undeclared head passes through and reports true. Declining would poison
// errs.Chain: everything after this hop is dropped and the violation is marked
// approximate, which would take a path the raw-body index resolves today and
// make it worse.
func TestFieldsPassAnUndeclaredHeadThrough(t *testing.T) {
	f := Fields{"Line1": errs.Path{errs.Named("shipping")}}

	in := errs.Path{errs.Named("Email")}
	got, ok := f.Resolve(in)
	if !ok {
		t.Fatal("an undeclared head declined; the hops behind it would be dropped and the violation marked approximate")
	}
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("an undeclared head resolved to %v, want the path unchanged", got)
	}

	// The control: the hop after it still runs, which is the whole reason
	// passing through is not the same as declining.
	after := &recordingHop{prefix: errs.Named("body")}
	out, ok := errs.Chain(f, after).Resolve(in)
	seen := after.ran
	if !seen {
		t.Fatal("the hop behind an undeclared head never ran")
	}
	if want := (errs.Path{errs.Named("body"), errs.Named("Email")}); !ok || !reflect.DeepEqual(out, want) {
		t.Fatalf("the chain answered %v, %v, want %v", out, ok, want)
	}
}

// Nothing declared, nothing to do: an empty map and an empty path are the
// identity rather than a special case somebody has to remember.
func TestAnEmptyFieldsMapIsTheIdentity(t *testing.T) {
	in := errs.Path{errs.Named("Email")}
	for name, f := range map[string]Fields{"nil": nil, "empty": {}} {
		got, ok := f.Resolve(in)
		if !ok || !reflect.DeepEqual(got, in) {
			t.Fatalf("a %s map answered %v, %v, want the path unchanged", name, got, ok)
		}
	}

	// A leading index is a position and not a field name, so it is never looked
	// up: a bulk write's ["3","Email"] must keep its row number.
	f := Fields{"3": errs.Path{errs.Named("wrong")}}
	in = errs.Path{errs.Indexed(3), errs.Named("Email")}
	if got, ok := f.Resolve(in); !ok || !reflect.DeepEqual(got, in) {
		t.Fatalf("a leading index resolved to %v, want the path unchanged", got)
	}
}

// Hops is what a binding wires ahead of the raw-body fallback: the service's
// own translation, then the mapper's when it declares one.
func TestHopsCollectsTheServiceAndTheMapperInThatOrder(t *testing.T) {
	svc := &fakeService{paths: Fields{"Name": errs.Path{errs.Named("title")}}}

	// A mapper that is not a resolver contributes nothing, which is what keeps
	// a hand-written one from having to write a path map it has no use for.
	if got := Hops[widget, int64, widgetUpdate](svc, Identity[widget]()); len(got) != 1 {
		t.Fatalf("a mapper that declares no hop contributed %d, want only the service's", len(got))
	}

	// And a service with no hop leaves only the mapper's, so neither position
	// is filled in by accident.
	bare := &fakeService{}
	if got := Hops[widget, int64, widgetUpdate](bare, mappingIn{}); len(got) != 1 {
		t.Fatalf("a service with no hop contributed %d hops, want only the mapper's", len(got))
	}

	hops := errs.Chain(Hops[widget, int64, widgetUpdate](svc, mappingIn{})...)
	got, ok := hops.Resolve(errs.Path{errs.Named("Name")})
	want := errs.Path{errs.Named("payload"), errs.Named("title")}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("the chain answered %v, %v, want the service's hop then the mapper's %v", got, ok, want)
	}
}
