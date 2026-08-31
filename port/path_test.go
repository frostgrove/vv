package port

import (
	"reflect"
	"testing"

	"github.com/frostgrove/vv/errs"
)

func TestADeclaredHeadIsRewrittenAndTheTailSurvives(t *testing.T) {
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

	first, _ := f.Resolve(errs.Path{errs.Named("Line1"), errs.Named("Zip")})
	second, _ := f.Resolve(errs.Path{errs.Named("Line1"), errs.Named("City")})
	if first[2].Name != "Zip" || second[2].Name != "City" {
		t.Fatalf("two resolutions share a backing array: they answered %v and %v", first, second)
	}
}

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

func TestAnEmptyFieldsMapIsTheIdentity(t *testing.T) {
	in := errs.Path{errs.Named("Email")}
	for name, f := range map[string]Fields{"nil": nil, "empty": {}} {
		got, ok := f.Resolve(in)
		if !ok || !reflect.DeepEqual(got, in) {
			t.Fatalf("a %s map answered %v, %v, want the path unchanged", name, got, ok)
		}
	}

	f := Fields{"3": errs.Path{errs.Named("wrong")}}
	in = errs.Path{errs.Indexed(3), errs.Named("Email")}
	if got, ok := f.Resolve(in); !ok || !reflect.DeepEqual(got, in) {
		t.Fatalf("a leading index resolved to %v, want the path unchanged", got)
	}
}

func TestHopsCollectsTheServiceAndTheMapperInThatOrder(t *testing.T) {
	service := &fakeService{paths: Fields{"Name": errs.Path{errs.Named("title")}}}

	if got := Hops[widget, int64, widgetUpdate](service, Identity[widget]()); len(got) != 1 {
		t.Fatalf("a mapper that declares no hop contributed %d, want only the service's", len(got))
	}

	bare := &fakeService{}
	if got := Hops[widget, int64, widgetUpdate](bare, mappingIn{}); len(got) != 1 {
		t.Fatalf("a service with no hop contributed %d hops, want only the mapper's", len(got))
	}

	hops := errs.Chain(Hops[widget, int64, widgetUpdate](service, mappingIn{})...)
	got, ok := hops.Resolve(errs.Path{errs.Named("Name")})
	want := errs.Path{errs.Named("payload"), errs.Named("title")}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("the chain answered %v, %v, want the service's hop then the mapper's %v", got, ok, want)
	}
}
