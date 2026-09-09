package faults_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/decorators/faults"
)

type sealedMutationBoundary struct {
	crud.Core[Doc, int64]
	markerCalls int
	metaCalls   int
}

func (this *sealedMutationBoundary) MutationBoundarySealed() { this.markerCalls++ }

func (this *sealedMutationBoundary) Meta() *crud.Meta {
	this.metaCalls++
	return this.Core.Meta()
}

type opaqueMutationBoundary struct{ crud.Core[Doc, int64] }

func (this opaqueMutationBoundary) Next() crud.Core[Doc, int64] { return this.Core }

func TestEnrichRejectsADirectSealedMutationBoundaryBeforeInspectingIt(t *testing.T) {
	base := Docs.Bind(crudtest.Postgres()).Unwrap()
	sealed := &sealedMutationBoundary{Core: base}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = faults.Enrich[Doc, int64]()(sealed)
	}()
	if recovered == nil || !strings.Contains(fmt.Sprint(recovered), "sealed mutation boundary") {
		t.Fatalf("Enrich panic = %v, want a sealed-boundary wiring refusal", recovered)
	}
	if sealed.markerCalls != 0 || sealed.metaCalls != 0 {
		t.Fatalf("Enrich called marker=%d Meta=%d before refusing the direct boundary", sealed.markerCalls, sealed.metaCalls)
	}
}

func TestEnrichChecksOnlyItsExactDirectInner(t *testing.T) {
	base := Docs.Bind(crudtest.Postgres()).Unwrap()
	sealed := &sealedMutationBoundary{Core: base}
	opaque := opaqueMutationBoundary{Core: sealed}

	decorated := faults.Enrich[Doc, int64]()(opaque)
	if decorated == nil {
		t.Fatal("Enrich discarded an ordinary direct core")
	}
	if sealed.markerCalls != 0 || sealed.metaCalls != 1 {
		t.Fatalf("exact-direct check called marker=%d Meta=%d, want no marker call and ordinary Meta forwarding", sealed.markerCalls, sealed.metaCalls)
	}
}
