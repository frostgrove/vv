package audit

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/frostgrove/vv/errs"
)

func TestPrimitivesEnforceSemanticNameGrammar(t *testing.T) {
	valid := []string{"a", "audit", "audit.record", "a0", "a_b.c9"}
	for _, value := range valid {
		if !validSemanticName(value) {
			t.Fatalf("%q is a legal semantic name", value)
		}
	}
	invalid := []string{"", ".audit", "audit.", "audit..record", "Audit", "9audit", "a.-b", "a+b", "é", strings.Repeat("a", MaxNameBytes+1)}
	for _, value := range invalid {
		if validSemanticName(value) {
			t.Fatalf("%q was accepted as a semantic name", value)
		}
	}
	if !validSemanticName(strings.Repeat("a", MaxNameBytes)) {
		t.Fatal("a semantic name at the byte bound was rejected")
	}
}

func TestPrimitivesEnforceOpaqueReferenceText(t *testing.T) {
	for _, value := range []string{"tenant:42", "東京", strings.Repeat("x", MaxReferenceBytes)} {
		if !validOpaqueReference(value, MaxReferenceBytes) {
			t.Fatalf("%q is legal opaque reference text", value)
		}
	}
	invalidUTF8 := string([]byte{0xff})
	if utf8.ValidString(invalidUTF8) {
		t.Fatal("the malformed UTF-8 control is not malformed")
	}
	for _, value := range []string{"", "a\x00b", "a\tb", "a\x1bb", "a\u0085b", invalidUTF8, strings.Repeat("x", MaxReferenceBytes+1)} {
		if validOpaqueReference(value, MaxReferenceBytes) {
			t.Fatalf("reference bytes %q were accepted", []byte(value))
		}
	}
	if !validBoundedText("", MaxNarrativeBytes, true) {
		t.Fatal("an explicitly optional bounded text rejected absence")
	}
}

func TestPrimitivesConstructCatalogChangeReference(t *testing.T) {
	ref, err := NewCatalogChangeRef("deploy.primary", "change/2026-09-09")
	if err != nil {
		t.Fatalf("a legal catalog change reference was refused: %v", err)
	}
	if !ref.valid() {
		t.Fatal("a constructed catalog change reference is not internally valid")
	}
	if got, want := ref.View(), (CatalogChangeRefView{Ledger: "deploy.primary", Change: "change/2026-09-09"}); got != want {
		t.Fatalf("catalog change view is %#v, want %#v", got, want)
	}
	if got := ref.String(); got != "[audit catalog change reference]" {
		t.Fatalf("catalog change reference renders as %q", got)
	}
	if strings.Contains(ref.String(), "change/2026") || strings.Contains(ref.String(), "deploy.primary") {
		t.Fatal("catalog change rendering disclosed deployment authority")
	}
}

func TestPrimitivesRejectMalformedCatalogChangeReferenceWithoutRenderingIt(t *testing.T) {
	secret := "credential\tvalue"
	for _, one := range []struct {
		ledger DeploymentLedger
		change DeploymentChange
		path   string
	}{
		{"Deploy", "change", "ledger"},
		{"deploy", DeploymentChange(secret), "change"},
		{"deploy", "", "change"},
	} {
		_, err := NewCatalogChangeRef(one.ledger, one.change)
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("malformed catalog change returned %v", err)
		}
		fault, ok := errs.AsFault(err)
		if !ok || len(fault.Violations) != 1 || fault.Violations[0].Path.String() != one.path {
			t.Fatalf("malformed catalog change fault is %#v", fault)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatal("catalog change error rendered the protected input")
		}
	}
}

func TestPrimitivesRecognizeZeroIdentityValues(t *testing.T) {
	if !isZeroValue(LogID{}) || !isZeroValue(OperationID{}) || !isZeroValue(EntityChainID{}) {
		t.Fatal("a zero identity was accepted as populated")
	}
	var log LogID
	log[0] = 1
	var operation OperationID
	operation[15] = 1
	var chain EntityChainID
	chain[31] = 1
	if isZeroValue(log) || isZeroValue(operation) || isZeroValue(chain) {
		t.Fatal("a populated identity was treated as zero")
	}
}
