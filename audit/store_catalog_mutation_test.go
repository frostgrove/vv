package audit

import (
	"errors"
	"strings"
	"testing"
)

type catalogMutationStoreInfo struct {
	identity Backing
	backing  BackingID
	log      LogID
}

func (s catalogMutationStoreInfo) Capabilities() Capabilities  { return Capabilities{} }
func (s catalogMutationStoreInfo) Limits() Limits              { return Limits{} }
func (s catalogMutationStoreInfo) Backing() Backing            { return s.identity }
func (s catalogMutationStoreInfo) BackingID() BackingID        { return s.backing }
func (s catalogMutationStoreInfo) LogID() LogID                { return s.log }
func (s catalogMutationStoreInfo) Catalogs() StoreCatalogState { return StoreCatalogState{} }

func TestCatalogMutationLogIsOrderedImmutableAndOriginBound(t *testing.T) {
	backing, log := BackingID{1}, LogID{2}
	first, second := catalogMutationRef(1), catalogMutationRef(2)
	change := catalogMutationChange(t, "release-1")
	firstProof, err := NoAttemptCatalogActivation(CatalogRef{}, first)
	if err != nil {
		t.Fatal(err)
	}
	secondProof, err := NoAttemptCatalogActivation(first, second)
	if err != nil {
		t.Fatal(err)
	}
	want := []CatalogMutationView{
		CatalogInstalled(first, change),
		CatalogInstalled(second, change),
		CatalogActivated(CatalogRef{}, first, change, firstProof),
		CatalogActivated(first, second, change, secondProof),
	}
	mutations, err := NewCatalogMutationLog(backing, log, want)
	if err != nil {
		t.Fatal(err)
	}
	want[0] = CatalogMutationView{}
	read := mutations.Mutations()
	if len(read) != 4 || read[0].Catalog != first || mutations.BackingID() != backing || mutations.LogID() != log {
		t.Fatalf("mutation log = (%+v, %x, %x)", read, mutations.BackingID(), mutations.LogID())
	}
	read[0] = CatalogMutationView{}
	if mutations.Mutations()[0].Catalog != first {
		t.Fatal("returned mutation slice aliases the log")
	}
	expected := []CatalogMutationView{
		CatalogInstalled(first, change),
		CatalogInstalled(second, change),
		CatalogActivated(CatalogRef{}, first, change, firstProof),
		CatalogActivated(first, second, change, secondProof),
	}
	identity, err := BackingFor(&struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	origin := catalogMutationStoreInfo{identity: identity, backing: backing, log: log}
	if err := VerifyCatalogMutations(origin, expected, mutations); err != nil {
		t.Fatalf("verify exact mutations: %v", err)
	}
	foreign := catalogMutationStoreInfo{identity: identity, backing: BackingID{9}, log: log}
	if err := VerifyCatalogMutations(foreign, expected, mutations); !errors.Is(err, ErrWrongStore) {
		t.Fatalf("foreign origin = %v", err)
	}
	mutations.value.canonical[0] ^= 1
	if err := VerifyCatalogMutations(origin, expected, mutations); !errors.Is(err, ErrMalformedEvidence) {
		t.Fatalf("tampered log = %v", err)
	}
}

func TestCatalogMutationLogRejectsZeroMixedDuplicateAndReorderedRecords(t *testing.T) {
	backing, log := BackingID{1}, LogID{2}
	first, second := catalogMutationRef(1), catalogMutationRef(2)
	change := catalogMutationChange(t, "release-1")
	firstProof, err := NoAttemptCatalogActivation(CatalogRef{}, first)
	if err != nil {
		t.Fatal(err)
	}
	invalidProof, err := NoAttemptCatalogActivation(first, second)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		backing   BackingID
		log       LogID
		mutations []CatalogMutationView
	}{
		{name: "backing", log: log},
		{name: "log", backing: backing},
		{name: "change", backing: backing, log: log, mutations: []CatalogMutationView{CatalogInstalled(first, CatalogChangeRef{})}},
		{name: "kind", backing: backing, log: log, mutations: []CatalogMutationView{{Kind: 99, Change: change}}},
		{name: "mixed install", backing: backing, log: log, mutations: []CatalogMutationView{{Kind: CatalogInstallMutation, Catalog: first, Active: first, Change: change}}},
		{name: "duplicate", backing: backing, log: log, mutations: []CatalogMutationView{CatalogInstalled(first, change), CatalogInstalled(first, change)}},
		{name: "reordered install", backing: backing, log: log, mutations: []CatalogMutationView{CatalogInstalled(second, change)}},
		{name: "activation before install", backing: backing, log: log, mutations: []CatalogMutationView{CatalogActivated(CatalogRef{}, first, change, firstProof)}},
		{name: "wrong activation proof", backing: backing, log: log, mutations: []CatalogMutationView{CatalogInstalled(first, change), CatalogActivated(CatalogRef{}, first, change, invalidProof)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewCatalogMutationLog(test.backing, test.log, test.mutations); err == nil {
				t.Fatal("invalid mutation log accepted")
			}
		})
	}
}

func TestCatalogMutationLogEnforcesCountAndCanonicalByteBounds(t *testing.T) {
	overCount := make([]CatalogMutationView, MaxCatalogMutations+1)
	if _, err := NewCatalogMutationLog(BackingID{1}, LogID{2}, overCount); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over count = %v", err)
	}
	short := catalogMutationSequence(t, "release")
	if len(short) != MaxCatalogMutations {
		t.Fatalf("boundary mutation count = %d", len(short))
	}
	if _, err := NewCatalogMutationLog(BackingID{1}, LogID{2}, short); err != nil {
		t.Fatalf("count boundary: %v", err)
	}
	long := catalogMutationSequence(t, strings.Repeat("x", MaxReferenceBytes))
	if _, err := NewCatalogMutationLog(BackingID{1}, LogID{2}, long); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over byte bound = %v", err)
	}
}

func catalogMutationSequence(t *testing.T, changeName string) []CatalogMutationView {
	t.Helper()
	change := catalogMutationChange(t, changeName)
	count := MaxCatalogMutations / 2
	references := make([]CatalogRef, count)
	result := make([]CatalogMutationView, 0, MaxCatalogMutations)
	for index := range count {
		references[index] = catalogMutationRef(CatalogGeneration(index + 1))
		result = append(result, CatalogInstalled(references[index], change))
	}
	var expected CatalogRef
	for _, reference := range references {
		proof, err := NoAttemptCatalogActivation(expected, reference)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, CatalogActivated(expected, reference, change, proof))
		expected = reference
	}
	return result
}

func catalogMutationRef(generation CatalogGeneration) CatalogRef {
	var digest CatalogDigest
	digest[0] = byte(generation>>8) + 1
	digest[1] = byte(generation)
	return CatalogRef{ID: "application.audit", Generation: generation, Digest: digest}
}

func catalogMutationChange(t *testing.T, name string) CatalogChangeRef {
	t.Helper()
	change, err := NewCatalogChangeRef("application.deploy", DeploymentChange(name))
	if err != nil {
		t.Fatal(err)
	}
	return change
}
