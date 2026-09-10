package audit

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"slices"
)

type materializers struct {
	privacy   PrivacyAdmission
	protector Protector
	tokenizer Tokenizer
}

func (m materializers) value(ctx context.Context, input draftValue, aad []byte) (StoredValueView, error) {
	output := StoredValueView{
		Field: input.field, Codec: input.codec, Classification: input.classification,
		Mode: input.mode, State: input.state,
	}
	if input.state == ValueAbsent {
		return output, nil
	}
	if input.state == ValueRedacted || input.mode == AsRedacted {
		output.State = ValueRedacted
		output.Redacted = true
		return output, nil
	}
	switch input.mode {
	case AsPlaintext:
		if !m.privacy.allows(input.classification, input.mode) {
			return StoredValueView{}, auditErrorAt(ErrAdmission, "plaintext")
		}
		output.Plaintext = bytes.Clone(input.canonical)
	case AsToken:
		token, err := m.token(ctx, input.canonical, aad)
		if err != nil {
			return StoredValueView{}, err
		}
		output.Token = token
	case AsProtected:
		protected, err := m.protect(ctx, input.canonical, aad)
		if err != nil {
			return StoredValueView{}, err
		}
		output.Protected = protected
	case AsIndexedProtected:
		token, err := m.token(ctx, input.canonical, aad)
		if err != nil {
			return StoredValueView{}, err
		}
		protected, err := m.protect(ctx, input.canonical, aad)
		if err != nil {
			return StoredValueView{}, err
		}
		output.Token = token
		output.Protected = protected
	default:
		return StoredValueView{}, auditErrorAt(ErrInvalid, "storage_mode")
	}
	return output, nil
}

func (m materializers) token(ctx context.Context, plaintext, aad []byte) (Token, error) {
	if nilByReflection(m.tokenizer) {
		return Token{}, auditErrorAt(ErrMissingKey, "tokenizer")
	}
	request, err := newTokenizeRequest(plaintext, aad, m.tokenizer.ActiveDescription())
	if err != nil {
		return Token{}, err
	}
	token, err := m.tokenizer.Tokenize(ctx, request)
	if err != nil {
		return Token{}, cryptoRuntimeError(err)
	}
	description := request.Description()
	if token.Algorithm() != description.Algorithm || token.Profile() != description.Profile || token.KeyID() != description.KeyID || len(token.Bytes()) != 32 {
		return Token{}, auditErrorAt(ErrIntegrity, "token")
	}
	return token, nil
}

func (m materializers) protect(ctx context.Context, plaintext, aad []byte) (ProtectedValue, error) {
	if nilByReflection(m.protector) {
		return ProtectedValue{}, auditErrorAt(ErrMissingKey, "protector")
	}
	request, err := newProtectionRequest(plaintext, aad)
	if err != nil {
		return ProtectedValue{}, err
	}
	protected, err := m.protector.Protect(ctx, request)
	if err != nil {
		return ProtectedValue{}, cryptoRuntimeError(err)
	}
	description := m.protector.Description()
	if protected.Algorithm() != description.Algorithm || protected.Profile() != description.Profile || protected.KeyID() != description.KeyID || len(protected.Nonce()) == 0 || len(protected.Ciphertext()) == 0 {
		return ProtectedValue{}, auditErrorAt(ErrIntegrity, "protection")
	}
	return protected, nil
}

func materializationAAD(header RevisionHeaderView, ordinal uint16, field FieldName, role string) []byte {
	var output bytes.Buffer
	writeFrame(&output, []byte("frostgrove.audit/value-aad/v1"))
	writeFrame(&output, header.Log[:])
	writeCatalogRef(&output, header.Catalog)
	writeFrame(&output, header.OperationID[:])
	writeFrame(&output, header.RevisionID[:])
	writeUint32(&output, uint32(ordinal))
	writeFrame(&output, []byte(field))
	writeFrame(&output, []byte(role))
	return output.Bytes()
}

func materializeDraft(ctx context.Context, tools materializers, header RevisionHeaderView, ordinal uint16, input draft) (ItemWireView, error) {
	item := ItemWireView{
		Ordinal: ordinal, Kind: EventItem, Resource: input.descriptor.Resource,
		Action: input.descriptor.Action, OccurredAt: input.occurredAt,
		Outcome: input.outcome, Reason: input.reason,
	}
	if input.targetPresent {
		target := draftValue{
			codec: ReferenceText().Description(), classification: input.targetPolicy.Classification,
			mode: input.targetPolicy.Mode, state: ValuePresent, canonical: []byte(input.target),
		}
		value, err := tools.value(ctx, target, materializationAAD(header, ordinal, "", "target"))
		if err != nil {
			return ItemWireView{}, err
		}
		item.Target = value
	}
	item.Values = make([]StoredValueView, len(input.values))
	for index, value := range input.values {
		materialized, err := tools.value(ctx, value, materializationAAD(header, ordinal, value.field, "value"))
		if err != nil {
			return ItemWireView{}, err
		}
		item.Values[index] = materialized
	}
	return item, nil
}

func materializeEntityDraft(ctx context.Context, tools materializers, header RevisionHeaderView, ordinal uint16, input entityDraft, head EntityHeadResult) (ItemWireView, error) {
	item := ItemWireView{
		Ordinal: ordinal, Kind: EntityItem, Resource: input.descriptor.Resource,
		Action: Action(input.action), EntityState: input.state, Chain: head.ChainID(), Previous: head.Previous(),
	}
	subject := draftValue{
		codec: ReferenceText().Description(), classification: input.subjectInfo.Classification,
		mode: input.subjectInfo.Mode, state: ValuePresent, canonical: []byte(input.subject),
	}
	materializedSubject, err := tools.value(ctx, subject, materializationAAD(header, ordinal, "", "subject"))
	if err != nil {
		return ItemWireView{}, err
	}
	item.Subject = materializedSubject
	item.Values = make([]StoredValueView, len(input.values))
	for index, value := range input.values {
		materialized, err := tools.value(ctx, value, materializationAAD(header, ordinal, value.field, "value"))
		if err != nil {
			return ItemWireView{}, err
		}
		item.Values[index] = materialized
	}
	item.Changes = make([]StoredChangeView, len(input.changes))
	for index, change := range input.changes {
		before, err := tools.value(ctx, change.before, materializationAAD(header, ordinal, change.field, "before"))
		if err != nil {
			return ItemWireView{}, err
		}
		after, err := tools.value(ctx, change.after, materializationAAD(header, ordinal, change.field, "after"))
		if err != nil {
			return ItemWireView{}, err
		}
		item.Changes[index] = StoredChangeView{Field: change.field, Before: before, After: after}
	}
	return item, nil
}

func identitySubjectRequest(catalog CatalogRef, resource Resource, scope EvidenceScopeCommitment, scopePresent bool, subject Reference, required []IdentityCommitmentDescription) (IdentityCommitmentRequest, error) {
	var aad bytes.Buffer
	writeFrame(&aad, []byte("frostgrove.audit/entity-subject/v2"))
	writeFrame(&aad, []byte(catalog.ID))
	writeFrame(&aad, []byte(resource))
	if scopePresent {
		aad.WriteByte(1)
	} else {
		aad.WriteByte(0)
	}
	writeFrame(&aad, scope[:])
	return newIdentityCommitmentRequest(CommitEntitySubject, []byte(subject), aad.Bytes(), required)
}

func scopeCommitment(ctx context.Context, keyring IdentityKeyring, catalog CatalogRef, resolved Context, required []IdentityCommitmentDescription) (EvidenceScopeCommitment, bool, error) {
	scope, present := resolved.Scope.Get()
	if !present {
		return EvidenceScopeCommitment{}, false, nil
	}
	plaintext, err := contextValueBytes(scope)
	if err != nil {
		return EvidenceScopeCommitment{}, false, err
	}
	var aad bytes.Buffer
	writeFrame(&aad, []byte("frostgrove.audit/evidence-scope/v2"))
	writeFrame(&aad, []byte(catalog.ID))
	request, err := newIdentityCommitmentRequest(CommitEvidenceScope, plaintext, aad.Bytes(), required)
	if err != nil {
		return EvidenceScopeCommitment{}, false, err
	}
	set, err := keyring.CommitIdentities(ctx, request)
	if err != nil {
		return EvidenceScopeCommitment{}, false, cryptoRuntimeError(err)
	}
	value := set.Active().Bytes()
	if len(value) != 32 {
		return EvidenceScopeCommitment{}, false, auditErrorAt(ErrIntegrity, "scope")
	}
	var commitment EvidenceScopeCommitment
	copy(commitment[:], value)
	return commitment, true, nil
}

func idempotencyRequest(catalog CatalogRef, operation OperationName, operationID OperationID, key IdempotencyKey, required []IdentityCommitmentDescription) (IdentityCommitmentRequest, error) {
	var aad bytes.Buffer
	writeFrame(&aad, []byte("frostgrove.audit/idempotency/v1"))
	writeCatalogRef(&aad, catalog)
	writeFrame(&aad, []byte(operation))
	writeFrame(&aad, operationID[:])
	return newIdentityCommitmentRequest(CommitIdempotency, []byte(key), aad.Bytes(), required)
}

func idempotencyTokenOf(set IdentityCommitmentSet) (IdempotencyToken, error) {
	if set.Domain() != CommitIdempotency {
		return IdempotencyToken{}, auditErrorAt(ErrInvalid, "idempotency")
	}
	value := set.Active().Bytes()
	if len(value) != 32 {
		return IdempotencyToken{}, auditErrorAt(ErrIntegrity, "idempotency")
	}
	var output IdempotencyToken
	copy(output[:], value)
	return output, nil
}

func contextPolicyFact(policy ContextPolicy, kind ContextFactKind) (contextFactPolicy, bool) {
	for _, fact := range policy.value.facts {
		if fact.kind == kind {
			fact.allowed = slices.Clone(fact.allowed)
			return fact, true
		}
	}
	return contextFactPolicy{}, false
}

func cryptoRuntimeError(err error) error {
	if err == nil {
		return nil
	}
	if _, classified := CryptoOutcomeOf(err); classified {
		return err
	}
	return CryptoFailure(CryptoUnclassified, err)
}

func contextValueBytes(value any) ([]byte, error) {
	switch typed := value.(type) {
	case Reference:
		return []byte(typed), nil
	case ScopedReference:
		return []byte(string(typed.Scope) + "\x00" + string(typed.Reference)), nil
	case OperationID:
		return []byte(hex.EncodeToString(typed[:])), nil
	case Source:
		return []byte(typed), nil
	default:
		return nil, fmt.Errorf("%w: unsupported context coordinate", ErrInvalid)
	}
}
