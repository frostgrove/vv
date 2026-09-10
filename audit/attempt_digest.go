package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"slices"
)

type AttemptOwnerIdentityComponent struct {
	Fact      AttemptOwnerFact
	ActorKind ActorKind
	Reference Reference
}

type AttemptOwnerIdentityInput struct {
	Catalog    CatalogID
	Operation  OperationName
	Components []AttemptOwnerIdentityComponent
}

type AttemptSemanticTargetView struct {
	Present        bool
	Classification Classification
	Mode           StorageMode
	Canonical      []byte
}

type AttemptSemanticValueView struct {
	Field          FieldName
	Codec          CodecDescription
	Classification Classification
	Mode           StorageMode
	State          ValueState
	Canonical      []byte
}

type AttemptSemanticDigestInput struct {
	Catalog      CatalogID
	Policy       AttemptPolicyFingerprint
	Replay       AttemptReplayFingerprint
	Resource     Resource
	Operation    OperationName
	OperationID  OperationID
	Chain        AttemptChainID
	ScopePresent bool
	Scope        EvidenceScopeCommitment
	Owner        AttemptOwnerCommitment
	Transition   AttemptTransitionKind
	Checkpoint   AttemptCheckpointCode
	Reason       Reason
	Target       AttemptSemanticTargetView
	Values       []AttemptSemanticValueView
}

func AttemptChainCandidate(log LogID, catalog CatalogID, operation OperationID) (AttemptChainID, error) {
	if log == (LogID{}) || !validSemanticName(string(catalog)) || operation == (OperationID{}) {
		return AttemptChainID{}, auditErrorAt(ErrInvalid, "attempt.chain")
	}
	value := auditSHA256("frostgrove.audit/attempt-chain/v1", log[:], []byte(catalog), operation[:])
	return AttemptChainID(value), nil
}

func AttemptReplayFingerprintOf(policy AttemptPolicyFingerprint, description SemanticDigestDescription) (AttemptReplayFingerprint, error) {
	if policy == (AttemptPolicyFingerprint{}) {
		return AttemptReplayFingerprint{}, auditErrorAt(ErrInvalid, "attempt.replay")
	}
	if err := validateProviderDescription(description.Algorithm, description.Profile, description.KeyID); err != nil {
		return AttemptReplayFingerprint{}, auditErrorAt(ErrInvalid, "attempt.replay")
	}
	var encoded bytes.Buffer
	writeProvider(&encoded, description.Algorithm, description.Profile, description.KeyID)
	value := auditSHA256("frostgrove.audit/attempt-replay-policy/v1", policy[:], encoded.Bytes())
	return AttemptReplayFingerprint(value), nil
}

func AttemptSemanticDigestOf(ctx context.Context, digester SemanticDigester, input AttemptSemanticDigestInput) (SemanticDigest, error) {
	if ctx == nil || nilByReflection(digester) {
		return SemanticDigest{}, auditErrorAt(ErrInvalid, "attempt.semantic")
	}
	copy := cloneAttemptSemanticInput(input)
	if err := validateAttemptSemanticInput(copy); err != nil {
		return SemanticDigest{}, err
	}
	slices.SortFunc(copy.Values, func(left, right AttemptSemanticValueView) int {
		return bytes.Compare([]byte(left.Field), []byte(right.Field))
	})
	var encoded bytes.Buffer
	writeFrame(&encoded, []byte("frostgrove.audit/attempt-semantic/v1"))
	writeFrame(&encoded, []byte(copy.Catalog))
	writeFrame(&encoded, copy.Policy[:])
	writeFrame(&encoded, copy.Replay[:])
	writeFrame(&encoded, []byte(copy.Resource))
	writeFrame(&encoded, []byte(copy.Operation))
	writeFrame(&encoded, copy.OperationID[:])
	writeFrame(&encoded, copy.Chain[:])
	writeBool(&encoded, copy.ScopePresent)
	writeFrame(&encoded, copy.Scope[:])
	writeFrame(&encoded, copy.Owner[:])
	writeUint32(&encoded, uint32(copy.Transition))
	writeFrame(&encoded, []byte(copy.Checkpoint))
	writeFrame(&encoded, []byte(copy.Reason))
	writeBool(&encoded, copy.Target.Present)
	writeUint32(&encoded, uint32(copy.Target.Classification))
	writeUint32(&encoded, uint32(copy.Target.Mode))
	writeFrame(&encoded, copy.Target.Canonical)
	writeUint32(&encoded, uint32(len(copy.Values)))
	for _, value := range copy.Values {
		writeFrame(&encoded, []byte(value.Field))
		writeCodecDescription(&encoded, value.Codec)
		writeUint32(&encoded, uint32(value.Classification))
		writeUint32(&encoded, uint32(value.Mode))
		writeUint32(&encoded, uint32(value.State))
		writeFrame(&encoded, value.Canonical)
	}
	digest, err := digester.Digest(ctx, encoded.Bytes())
	if err != nil || digest == (SemanticDigest{}) {
		return SemanticDigest{}, cryptoRuntimeError(err)
	}
	return digest, nil
}

func cloneAttemptSemanticInput(input AttemptSemanticDigestInput) AttemptSemanticDigestInput {
	input.Target.Canonical = bytes.Clone(input.Target.Canonical)
	input.Values = slices.Clone(input.Values)
	for index := range input.Values {
		input.Values[index].Codec.ReadVersions = slices.Clone(input.Values[index].Codec.ReadVersions)
		input.Values[index].Canonical = bytes.Clone(input.Values[index].Canonical)
	}
	return input
}

func validateAttemptSemanticInput(input AttemptSemanticDigestInput) error {
	if !validSemanticName(string(input.Catalog)) || input.Policy == (AttemptPolicyFingerprint{}) || input.Replay == (AttemptReplayFingerprint{}) || !validSemanticName(string(input.Resource)) || !validSemanticName(string(input.Operation)) || input.OperationID == (OperationID{}) || input.Chain == (AttemptChainID{}) || input.Owner == (AttemptOwnerCommitment{}) || input.ScopePresent != (input.Scope != (EvidenceScopeCommitment{})) {
		return auditErrorAt(ErrInvalid, "attempt.semantic")
	}
	if input.Target.Present {
		if !input.Target.Classification.Valid() || !input.Target.Mode.Valid() || len(input.Target.Canonical) == 0 || len(input.Target.Canonical) > MaxReferenceBytes {
			return auditErrorAt(ErrInvalid, "attempt.semantic.target")
		}
	} else if input.Target.Classification != 0 || input.Target.Mode != 0 || len(input.Target.Canonical) != 0 {
		return auditErrorAt(ErrInvalid, "attempt.semantic.target")
	}
	switch input.Transition {
	case AttemptStartedTransition:
		if input.Checkpoint != "" || input.Reason != "" {
			return auditErrorAt(ErrInvalid, "attempt.semantic.phase")
		}
	case AttemptCheckpointTransition:
		if !validSemanticName(string(input.Checkpoint)) || input.Reason != "" || input.Target.Present {
			return auditErrorAt(ErrInvalid, "attempt.semantic.phase")
		}
	case AttemptSucceededTransition:
		if input.Checkpoint != "" || input.Reason != "" || input.Target.Present {
			return auditErrorAt(ErrInvalid, "attempt.semantic.phase")
		}
	case AttemptFailedTransition, AttemptCancelledTransition, AttemptOutcomeUnknownTransition:
		if input.Checkpoint != "" || !validSemanticName(string(input.Reason)) || input.Target.Present {
			return auditErrorAt(ErrInvalid, "attempt.semantic.phase")
		}
	case AttemptAbandonedTransition:
		if input.Checkpoint != "" || input.Reason != "" || input.Target.Present || len(input.Values) != 0 {
			return auditErrorAt(ErrInvalid, "attempt.semantic.phase")
		}
	default:
		return auditErrorAt(ErrInvalid, "attempt.semantic.phase")
	}
	if len(input.Values) > MaxFieldsPerItem {
		return auditTooLarge("attempt.semantic.values", MaxFieldsPerItem)
	}
	seen := make(map[FieldName]struct{}, len(input.Values))
	for _, value := range input.Values {
		if !validSemanticName(string(value.Field)) || value.Codec.Name == "" || !value.Classification.Valid() || !value.Mode.Valid() || value.State != ValuePresent && value.State != ValueRedacted && value.State != ValueAbsent || len(value.Canonical) > MaxValueBytes {
			return auditErrorAt(ErrInvalid, "attempt.semantic.values")
		}
		if _, duplicate := seen[value.Field]; duplicate {
			return auditErrorAt(ErrInvalid, "attempt.semantic.values")
		}
		seen[value.Field] = struct{}{}
	}
	return nil
}

func AttemptOwnerIdentityRequest(input AttemptOwnerIdentityInput) (IdentityCommitmentRequest, error) {
	if !validSemanticName(string(input.Catalog)) || !validSemanticName(string(input.Operation)) || len(input.Components) == 0 || len(input.Components) > 3 {
		return IdentityCommitmentRequest{}, auditErrorAt(ErrInvalid, "attempt.owner")
	}
	components := slices.Clone(input.Components)
	slices.SortFunc(components, func(left, right AttemptOwnerIdentityComponent) int { return int(left.Fact) - int(right.Fact) })
	var plaintext bytes.Buffer
	seen := make(map[AttemptOwnerFact]struct{}, len(components))
	for _, component := range components {
		if component.Fact < AttemptEffectiveActorOwner || component.Fact > AttemptServiceOwner || !component.ActorKind.Valid() || !validReferenceText(string(component.Reference)) {
			return IdentityCommitmentRequest{}, auditErrorAt(ErrInvalid, "attempt.owner")
		}
		if _, duplicate := seen[component.Fact]; duplicate {
			return IdentityCommitmentRequest{}, auditErrorAt(ErrInvalid, "attempt.owner")
		}
		seen[component.Fact] = struct{}{}
		writeUint32(&plaintext, uint32(component.Fact))
		writeUint32(&plaintext, uint32(component.ActorKind))
		writeFrame(&plaintext, []byte(component.Reference))
	}
	var aad bytes.Buffer
	writeFrame(&aad, []byte("frostgrove.audit/attempt-owner/v1"))
	writeFrame(&aad, []byte(input.Catalog))
	writeFrame(&aad, []byte(input.Operation))
	return newIdentityCommitmentRequest(CommitAttemptOwner, plaintext.Bytes(), aad.Bytes(), nil)
}

func AttemptOwnerCommitmentOf(set IdentityCommitmentSet) (AttemptOwnerCommitment, error) {
	if set.Domain() != CommitAttemptOwner || len(set.Aliases()) == 0 {
		return AttemptOwnerCommitment{}, auditErrorAt(ErrInvalid, "attempt.owner")
	}
	bytes := set.Active().Bytes()
	if len(bytes) != sha256.Size {
		return AttemptOwnerCommitment{}, auditErrorAt(ErrIntegrity, "attempt.owner")
	}
	var result AttemptOwnerCommitment
	copy(result[:], bytes)
	return result, nil
}

func AttemptTargetIdentityRequest(catalog CatalogID, operation OperationName, target Reference) (IdentityCommitmentRequest, error) {
	if !validSemanticName(string(catalog)) || !validSemanticName(string(operation)) || !validReferenceText(string(target)) {
		return IdentityCommitmentRequest{}, auditErrorAt(ErrInvalid, "attempt.target")
	}
	var aad bytes.Buffer
	writeFrame(&aad, []byte("frostgrove.audit/attempt-target/v1"))
	writeFrame(&aad, []byte(catalog))
	writeFrame(&aad, []byte(operation))
	return newIdentityCommitmentRequest(CommitAttemptTarget, []byte(target), aad.Bytes(), nil)
}

func AttemptTargetCommitmentOf(set IdentityCommitmentSet) (AttemptTargetCommitment, error) {
	if set.Domain() != CommitAttemptTarget || len(set.Aliases()) == 0 {
		return AttemptTargetCommitment{}, auditErrorAt(ErrInvalid, "attempt.target")
	}
	bytes := set.Active().Bytes()
	if len(bytes) != sha256.Size {
		return AttemptTargetCommitment{}, auditErrorAt(ErrIntegrity, "attempt.target")
	}
	var result AttemptTargetCommitment
	copy(result[:], bytes)
	return result, nil
}

func attemptIdempotencyRequest(kind IdempotencyDomainKind, catalog CatalogID, chain AttemptChainID, key IdempotencyKey, required []IdentityCommitmentDescription) (IdentityCommitmentRequest, error) {
	if kind != AttemptStartIdempotencyDomain && kind != AttemptFinishIdempotencyDomain && kind != AttemptCheckpointIdempotencyDomain || !validSemanticName(string(catalog)) || !validOpaqueReference(string(key), MaxIdempotencyKeyBytes) {
		return IdentityCommitmentRequest{}, auditErrorAt(ErrInvalid, "attempt.idempotency")
	}
	if kind == AttemptStartIdempotencyDomain != (chain == (AttemptChainID{})) {
		return IdentityCommitmentRequest{}, auditErrorAt(ErrInvalid, "attempt.idempotency")
	}
	var aad bytes.Buffer
	writeFrame(&aad, []byte("frostgrove.audit/attempt-idempotency/v1"))
	writeUint32(&aad, uint32(kind))
	writeFrame(&aad, []byte(catalog))
	writeFrame(&aad, chain[:])
	return newIdentityCommitmentRequest(CommitIdempotency, []byte(key), aad.Bytes(), required)
}
