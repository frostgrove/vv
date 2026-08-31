package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestNamespaceSeparatesApplicationAndEnvironment(t *testing.T) {
	production, err := NamespaceOf("lease", "production")
	if err != nil {
		t.Fatal(err)
	}
	test, err := NamespaceOf("lease", "test")
	if err != nil {
		t.Fatal(err)
	}
	other, err := NamespaceOf("other", "production")
	if err != nil {
		t.Fatal(err)
	}
	if production == test || production == other || !production.valid() || production.Application().Value() != "lease" || production.Environment().Value() != "production" {
		t.Fatal("namespace scoping failed")
	}
	if fmt.Sprintf("%+v", production) != "[job namespace]" {
		t.Fatal("namespace formatting exposed components")
	}
	if _, err := json.Marshal(production); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("namespace JSON error = %v", err)
	}
	rebuilt, err := NewNamespace(production.Application(), production.Environment())
	if err != nil || rebuilt != production {
		t.Fatalf("rebuilt namespace = %+v, %v", rebuilt, err)
	}
	if _, err := NamespaceOf(strings.Repeat("x", MaxNameBytes+1), "test"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized application error = %v", err)
	}
}

func TestProducerPartitionIsBoundedRedactedAndFailClosed(t *testing.T) {
	partition, err := ParsePartition("tenant:private-42")
	if err != nil {
		t.Fatal(err)
	}
	for _, formatted := range []string{fmt.Sprint(partition), fmt.Sprintf("%+v", partition), fmt.Sprintf("%#v", partition), partition.LogValue().String()} {
		if strings.Contains(formatted, "private") || !strings.Contains(formatted, "producer partition") {
			t.Fatalf("format = %q", formatted)
		}
	}
	if _, err := json.Marshal(partition); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("JSON error = %v", err)
	}
	oversized := Partition(strings.Repeat("x", MaxPartitionBytes+1))
	if oversized.valid() || !oversized.rejected {
		t.Fatal("oversized magic partition was accepted")
	}
	for _, raw := range []string{"", " tenant", "tenant\nother", "tenant\u2028other"} {
		if _, err := ParsePartition(raw); !errors.Is(err, ErrInvalid) {
			t.Fatalf("partition %q error = %v", raw, err)
		}
	}
	var _ slog.LogValuer = partition
}

func TestPartitionKeySeparatesNamespacePartitionAndGlobal(t *testing.T) {
	production, _ := NamespaceOf("lease", "production")
	test, _ := NamespaceOf("lease", "test")
	left := partitionKey(production, Partition("tenant-a"))
	right := partitionKey(production, Partition("tenant-b"))
	otherEnvironment := partitionKey(test, Partition("tenant-a"))
	global := partitionKey(production, ProducerPartition{})
	if !left.valid() || left.Global() || !global.Global() || left == right || left == otherEnvironment || left == global {
		t.Fatal("partition key scoping failed")
	}
	restored, err := RestorePartitionKey(production, left.NamespaceBinding(), left.Revision(), left.Digest(), left.Global())
	if err != nil || restored != left {
		t.Fatalf("restored = %+v, %v", restored, err)
	}
	if _, err := RestorePartitionKey(production, left.NamespaceBinding(), 0, left.Digest(), false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("revision error = %v", err)
	}
	if _, err := RestorePartitionKey(test, left.NamespaceBinding(), left.Revision(), left.Digest(), left.Global()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("persisted namespace swap error = %v", err)
	}
	restoredBinding, err := PartitionNamespaceBindingFromBytes(left.NamespaceBinding().Bytes())
	if err != nil || restoredBinding != left.NamespaceBinding() {
		t.Fatalf("restored binding = %+v, %v", restoredBinding, err)
	}
	if !left.validFor(production) || left.validFor(test) {
		t.Fatal("partition namespace binding was not enforced")
	}
	if fmt.Sprintf("%x", left) != "[job partition key]" || fmt.Sprintf("%x", left.Digest()) != "[job partition digest]" {
		t.Fatal("partition key formatting exposed digest")
	}
	if _, err := json.Marshal(left); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("partition key JSON error = %v", err)
	}
	if _, err := json.Marshal(left.NamespaceBinding()); !errors.Is(err, ErrUnsupported) || fmt.Sprintf("%x", left.NamespaceBinding()) != "[job partition namespace binding]" {
		t.Fatalf("partition namespace binding redaction = %v", err)
	}
}

func TestIntentDigestPlanAndAliasesAreBoundedAndPurposeSeparated(t *testing.T) {
	namespace, _ := NamespaceOf("tests", "test")
	partition := partitionKey(namespace, ProducerPartition{})
	definition := queueMustName("tests.intent")
	scope := intentScopeBinding(namespace, partition, definition, IntentOnce)
	plan, err := NewIntentDigestPlan(DigestRevision2, DigestRevision1)
	if err != nil || plan.Current() != DigestRevision2 || len(plan.Revisions()) != MaxIntentDigestKeys {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	if _, err := NewIntentDigestPlan(DigestRevision1, DigestRevision1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate revision error = %v", err)
	}
	legacyPlan, err := WithLegacyIntentCompatibility(plan)
	if err != nil || !legacyPlan.LegacyCompatibility() || plan.LegacyCompatibility() {
		t.Fatalf("legacy plan = %+v, %v", legacyPlan, err)
	}
	if _, err := WithLegacyIntentCompatibility(legacyPlan); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate legacy compatibility = %v", err)
	}
	var firstRaw [IntentDigestBytes]byte
	firstRaw[0] = 1
	var secondRaw [IntentDigestBytes]byte
	secondRaw[0] = 2
	firstDigest, _ := IntentDigestFromBytes(firstRaw)
	secondDigest, _ := IntentDigestFromBytes(secondRaw)
	first, _ := NewIntentKey(scope, DigestRevision2, IntentOnce, firstDigest)
	second, _ := NewIntentKey(scope, DigestRevision1, IntentOnce, secondDigest)
	aliases, err := NewIntentDigests(first, second)
	if err != nil || len(aliases.ReadCandidates()) != 2 || len(aliases.ReservationKeys()) != 2 || aliases.Current() != first || !aliases.validFor(namespace, partition, definition) {
		t.Fatalf("aliases = %+v, %v", aliases, err)
	}
	wrongScope := intentScopeBinding(namespace, partition, definition, IntentCollapse)
	wrongPurpose, _ := NewIntentKey(wrongScope, DigestRevision1, IntentCollapse, secondDigest)
	if _, err := NewIntentDigests(first, wrongPurpose); !errors.Is(err, ErrInvalid) {
		t.Fatalf("purpose error = %v", err)
	}
	otherNamespace, _ := NamespaceOf("tests", "other")
	otherPartition := partitionKey(otherNamespace, ProducerPartition{})
	otherScope := intentScopeBinding(otherNamespace, otherPartition, definition, IntentOnce)
	wrongAlias, _ := NewIntentKey(otherScope, DigestRevision1, IntentOnce, secondDigest)
	if _, err := NewIntentDigests(first, wrongAlias); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mixed scope error = %v", err)
	}
	restoredScope, err := IntentScopeBindingFromBytes(scope.Bytes())
	if err != nil || restoredScope != scope || !first.validFor(namespace, partition, definition) || first.validFor(otherNamespace, otherPartition, definition) {
		t.Fatalf("scope restore = %+v, %v", restoredScope, err)
	}
	if _, err := json.Marshal(scope); !errors.Is(err, ErrUnsupported) || fmt.Sprintf("%x", scope) != "[job intent scope binding]" {
		t.Fatalf("scope redaction = %v", err)
	}
	if strings.Contains(fmt.Sprintf("%#v", aliases), "01") {
		t.Fatal("intent aliases exposed digest")
	}
}

func TestBackendIDIsValidatedAndRedacted(t *testing.T) {
	if _, err := BackendIDFromBytes([BackendIDBytes]byte{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero backend error = %v", err)
	}
	var raw [BackendIDBytes]byte
	raw[0] = 0xab
	id, err := BackendIDFromBytes(raw)
	if err != nil || id.Bytes() != raw || id.IsZero() {
		t.Fatalf("backend id = %+v, %v", id, err)
	}
	if formatted := fmt.Sprintf("%x", id); formatted != "[job backend id]" {
		t.Fatalf("backend format = %q", formatted)
	}
}
