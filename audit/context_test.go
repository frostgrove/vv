package audit_test

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/audit"
)

func TestStaticContextCopiesAndReturnsIndependentActorChains(t *testing.T) {
	scope, err := audit.NewContextValue(
		audit.ScopedReference{Scope: "tenant", Reference: "42"},
		audit.Verified,
	)
	if err != nil {
		t.Fatal(err)
	}
	input := audit.Context{
		Actors: []audit.Actor{{Kind: audit.HumanActor, Reference: "user:7", Provenance: audit.Verified}},
		Scope:  scope,
	}
	resolver, err := audit.StaticContext(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Actors[0].Reference = "mutated"

	first, err := resolver.ResolveAuditContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Actors[0].Reference != "user:7" {
		t.Fatalf("resolver retained caller actor storage: %+v", first.Actors)
	}
	first.Actors[0].Reference = "again"
	second, err := resolver.ResolveAuditContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Actors[0].Reference != "user:7" {
		t.Fatalf("resolver returned aliased actor storage: %+v", second.Actors)
	}
	view := second.View()
	view.Actors[0].Reference = "view"
	if second.Actors[0].Reference != "user:7" {
		t.Fatal("Context.View aliases its actor input")
	}
}

func TestContextValuesRequireTypedCoordinatesAndProvenance(t *testing.T) {
	if _, err := audit.NewContextValue(audit.Reference("valid"), audit.UnstatedProvenance); !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("unstated provenance = %v", err)
	}
	if _, err := audit.NewContextValue(audit.Reference("bad\tvalue"), audit.Verified); !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("control-bearing reference = %v", err)
	}
	if _, err := audit.NewContextValue(audit.OperationID{}, audit.ServerDerived); !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("zero operation ID = %v", err)
	}
	if _, err := audit.NewContextValue(42, audit.Verified); !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("unsupported coordinate = %v", err)
	}

	value, err := audit.NewContextValue(audit.Reference("valid"), audit.Forwarded)
	if err != nil {
		t.Fatal(err)
	}
	got, present := value.Get()
	if !present || got != "valid" || value.Provenance() != audit.Forwarded {
		t.Fatalf("context value = (%q, %v, %v)", got, present, value.Provenance())
	}
}

func TestStaticContextRejectsMalformedActors(t *testing.T) {
	_, err := audit.StaticContext(audit.Context{Actors: []audit.Actor{{
		Kind:       audit.HumanActor,
		Reference:  "user\x1bsecret",
		Provenance: audit.Verified,
	}}})
	if !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("StaticContext = %v", err)
	}
}

func TestNilContextResolverFuncFailsClosed(t *testing.T) {
	var resolver audit.ContextResolverFunc
	_, err := resolver.ResolveAuditContext(context.Background())
	if !errors.Is(err, audit.ErrInvalid) {
		t.Fatalf("nil resolver = %v", err)
	}
}
