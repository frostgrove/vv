package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
)

func TestDurabilityProfileNamesExactFailureModel(t *testing.T) {
	failures, err := Failures(FailureProcessCrash, FailureHostLoss)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := NewDurabilityProfile(AckLocalPersistence, AcknowledgedLossExcludedForDeclaredFailures, failures)
	if err != nil {
		t.Fatal(err)
	}
	if profile.AckMode() != AckLocalPersistence || profile.AcknowledgedLoss() != AcknowledgedLossExcludedForDeclaredFailures || !profile.FailureModel().Contains(FailureProcessCrash) || !slices.Equal(profile.FailureModel().Values(), []Failure{FailureProcessCrash, FailureHostLoss}) || !profile.valid() {
		t.Fatalf("profile = %+v", profile)
	}
	possible, err := NewDurabilityProfile(AckBeforePersistence, AcknowledgedLossPossible, FailureSet{})
	if err != nil || !possible.FailureModel().IsZero() {
		t.Fatalf("possible loss profile = %+v, %v", possible, err)
	}
	if _, err := NewDurabilityProfile(AckLocalPersistence, AcknowledgedLossPossible, failures); !errors.Is(err, ErrInvalid) {
		t.Fatalf("possible loss with guarantees = %v", err)
	}
	if _, err := NewDurabilityProfile(AckLocalPersistence, AcknowledgedLossExcludedForDeclaredFailures, FailureSet{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("excluded loss without model = %v", err)
	}
	if _, err := NewDurabilityProfile(AckBeforePersistence, AcknowledgedLossExcludedForDeclaredFailures, failures); !errors.Is(err, ErrInvalid) {
		t.Fatalf("pre-persistence durable ack = %v", err)
	}
	if _, err := Failures(FailureProcessCrash, FailureProcessCrash); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate failure = %v", err)
	}
}

func TestDurabilityRequirementUsesExactAckModesAndProtectedFailures(t *testing.T) {
	modes, err := AckModes(AckLocalPersistence, AckRemotePersistence)
	if err != nil {
		t.Fatal(err)
	}
	protected, err := Failures(FailureProcessCrash, FailureHostLoss)
	if err != nil {
		t.Fatal(err)
	}
	requirement, err := NewDurabilityRequirement(modes, protected)
	if err != nil {
		t.Fatal(err)
	}
	strong, err := NewDurabilityProfile(AckLocalPersistence, AcknowledgedLossExcludedForDeclaredFailures, protected)
	if err != nil {
		t.Fatal(err)
	}
	possible, _ := NewDurabilityProfile(AckLocalPersistence, AcknowledgedLossPossible, FailureSet{})
	wrongMode, _ := NewDurabilityProfile(AckRemoteWrite, AcknowledgedLossExcludedForDeclaredFailures, protected)
	processOnly, _ := Failures(FailureProcessCrash)
	weakFailures, _ := NewDurabilityProfile(AckLocalPersistence, AcknowledgedLossExcludedForDeclaredFailures, processOnly)
	if !requirement.accepts(strong) || requirement.accepts(possible) || requirement.accepts(wrongMode) || requirement.accepts(weakFailures) {
		t.Fatal("durability requirement accepted the wrong profile")
	}
	if !slices.Equal(modes.Values(), []AckMode{AckLocalPersistence, AckRemotePersistence}) || !strong.FailureModel().ContainsAll(protected) || requirement.IsZero() {
		t.Fatal("durability requirement lost its exact sets")
	}
	if unrestricted := (DurabilityRequirement{}); !unrestricted.accepts(possible) || !unrestricted.IsZero() {
		t.Fatal("zero durability requirement is not unrestricted")
	}
	if _, err := AckModes(AckLocalPersistence, AckLocalPersistence); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate ack mode = %v", err)
	}
	if _, err := AckModes(AckMode(255)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown ack mode = %v", err)
	}
	if fmt.Sprint(requirement) != "[job durability requirement]" {
		t.Fatal("durability requirement formatting changed")
	}
}

func TestDurabilityRequirementRejectsImpossibleProtectedAcknowledgement(t *testing.T) {
	beforeOnly, _ := AckModes(AckBeforePersistence)
	beforeAndLocal, _ := AckModes(AckBeforePersistence, AckLocalPersistence)
	protected, _ := Failures(FailureProcessCrash)
	if _, err := NewDurabilityRequirement(beforeOnly, protected); !errors.Is(err, ErrInvalid) {
		t.Fatalf("impossible requirement = %v", err)
	}
	if _, err := NewDurabilityRequirement(beforeAndLocal, protected); err != nil {
		t.Fatalf("satisfiable requirement = %v", err)
	}
	policy, err := Default.Build()
	if err != nil {
		t.Fatal(err)
	}
	policy.Durability = DurabilityRequirement{acceptedAckModes: beforeOnly, protectedFailures: protected}
	if err := validatePolicy(policy); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forged policy durability = %v", err)
	}
}

func TestDurabilityRequirementsComposeByIntersectionAndUnion(t *testing.T) {
	localAndRemote, _ := AckModes(AckLocalPersistence, AckRemotePersistence)
	localOnly, _ := AckModes(AckLocalPersistence)
	process, _ := Failures(FailureProcessCrash)
	host, _ := Failures(FailureHostLoss)
	left, _ := NewDurabilityRequirement(localAndRemote, process)
	right, _ := NewDurabilityRequirement(localOnly, host)
	combined, err := combineDurabilityRequirements(left, right)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(combined.AcceptedAckModes().Values(), []AckMode{AckLocalPersistence}) || !slices.Equal(combined.ProtectedFailures().Values(), []Failure{FailureProcessCrash, FailureHostLoss}) {
		t.Fatalf("combined = %+v", combined)
	}
	remoteOnly, _ := AckModes(AckRemotePersistence)
	conflicting, _ := NewDurabilityRequirement(remoteOnly, FailureSet{})
	if _, err := combineDurabilityRequirements(right, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("disjoint ack modes = %v", err)
	}
	beforeAndLocal, _ := AckModes(AckBeforePersistence, AckLocalPersistence)
	beforeAndRemote, _ := AckModes(AckBeforePersistence, AckRemotePersistence)
	left, _ = NewDurabilityRequirement(beforeAndLocal, FailureSet{})
	right, _ = NewDurabilityRequirement(beforeAndRemote, process)
	if _, err := combineDurabilityRequirements(left, right); !errors.Is(err, ErrConflict) {
		t.Fatalf("jointly impossible durability = %v", err)
	}
}

func TestTransactionContextIsBoundRedactedAndDurabilityExact(t *testing.T) {
	backend := queueTestBackendID(7)
	var raw [32]byte
	raw[0] = 9
	binding, err := TransactionBindingFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	context, err := NewTransactionContext(backend, binding, queueTestDurability())
	if err != nil {
		t.Fatal(err)
	}
	if context.Backend() != backend || context.Binding() != binding || context.Durability() != queueTestDurability() || context.IsZero() || !context.valid() {
		t.Fatalf("transaction = %+v", context)
	}
	for _, value := range []any{binding, context} {
		if encoded, err := json.Marshal(value); !errors.Is(err, ErrUnsupported) || len(encoded) != 0 {
			t.Fatalf("JSON %T = %q, %v", value, encoded, err)
		}
		if formatted := fmt.Sprintf("%+v", value); formatted == "" || formatted == fmt.Sprintf("%x", raw) {
			t.Fatalf("format %T = %q", value, formatted)
		}
	}
	if _, err := NewTransactionContext(BackendID{}, binding, queueTestDurability()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero backend = %v", err)
	}
	if _, err := NewTransactionContext(backend, TransactionBinding{}, queueTestDurability()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero binding = %v", err)
	}
	if _, err := NewTransactionContext(backend, binding, DurabilityProfile{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero durability = %v", err)
	}
}

func TestBackendDescriptionIsValidatedImmutableAndMachineReadable(t *testing.T) {
	id := queueTestBackendID(4)
	capabilities := Capabilities{Priority: true, Debounce: true, Scheduled: true, AttemptTrace: true}
	description, err := NewBackendDescription(id, queueTestDurability(), capabilities)
	if err != nil {
		t.Fatal(err)
	}
	copy := description.Capabilities()
	copy.Priority = false
	if description.ID() != id || description.Durability() != queueTestDurability() || description.Capabilities() != capabilities || !description.Capabilities().Priority || description.IsZero() || !description.valid() {
		t.Fatalf("description = %+v", description)
	}
	if fmt.Sprint(description) != "[job backend description]" || fmt.Sprint(capabilities) != "[job backend capabilities]" {
		t.Fatal("backend values are not safely formatted")
	}
	if _, err := NewBackendDescription(BackendID{}, queueTestDurability(), capabilities); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero ID = %v", err)
	}
	if _, err := NewBackendDescription(id, DurabilityProfile{}, capabilities); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero durability = %v", err)
	}
	forged := DurabilityProfile{ack: AckBeforePersistence, loss: AcknowledgedLossExcludedForDeclaredFailures, failures: queueTestDurability().FailureModel()}
	if _, err := NewBackendDescription(id, forged, capabilities); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forged backend durability = %v", err)
	}
	var raw [32]byte
	raw[0] = 1
	binding, _ := TransactionBindingFromBytes(raw)
	if _, err := NewTransactionContext(id, binding, forged); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forged transaction durability = %v", err)
	}
}
