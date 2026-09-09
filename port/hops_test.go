package port

import (
	"context"
	"reflect"
	"testing"

	"github.com/frostgrove/vv/errs"
)

func TestHopsAreClonedAtBothContextBoundaries(t *testing.T) {
	first := renamer{"Name": "label"}
	second := renamer{"Name": "wrong"}
	hops := []errs.Resolver{first}
	ctx := WithHops(context.Background(), hops)
	hops[0] = second

	read := HopsFrom(ctx)
	if len(read) != 1 {
		t.Fatalf("one declared hop came back as %d", len(read))
	}
	read[0] = second

	got, ok := errs.Chain(HopsFrom(ctx)...).Resolve(errs.Path{errs.Named("Name")})
	want := errs.Path{errs.Named("label")}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("mutating the caller and returned slices changed the stored hop to %v, %v", got, ok)
	}
}

func TestHopsAreNilSafe(t *testing.T) {
	if got := HopsFrom(nil); got != nil {
		t.Fatalf("a nil context carries %d hops", len(got))
	}
	if got := HopsFrom(WithHops(nil, nil)); got != nil {
		t.Fatalf("binding no hops to a nil context carries %d", len(got))
	}

	ctx := WithHops(nil, []errs.Resolver{renamer{"Name": "label"}})
	got, ok := errs.Chain(HopsFrom(ctx)...).Resolve(errs.Path{errs.Named("Name")})
	if !ok || got.String() != "label" {
		t.Fatalf("a hop bound without a parent context resolved to %q, %v", got, ok)
	}
}

func TestRequestHopsRunBeforeRendererResolvers(t *testing.T) {
	f := errs.Validation().Field("Name").Code(errs.CodeRequired).Fault()
	ctx := WithHops(context.Background(), []errs.Resolver{renamer{"Name": "label"}})
	vs := pipelineCtx(t, ctx, f, ViolationOptions{
		Resolvers: []errs.Resolver{renamer{"label": "payload.label"}},
	})
	if got := vs[0].Path.String(); got != "payload.label" {
		t.Fatalf("the request hop and renderer hop resolved to %q, want payload.label in that order", got)
	}

	withoutRequest := pipeline(t, f, ViolationOptions{
		Resolvers: []errs.Resolver{renamer{"label": "payload.label"}},
	})
	if got := withoutRequest[0].Path.String(); got != "Name" || !withoutRequest[0].Approximate {
		t.Fatalf("without the request hop the renderer resolved to %q (approximate %v)", got, withoutRequest[0].Approximate)
	}
}
