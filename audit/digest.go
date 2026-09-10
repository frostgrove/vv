package audit

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"time"
)

func auditSHA256(domain string, frames ...[]byte) [sha256.Size]byte {
	digest := sha256.New()
	writeFrame(digest, []byte(domain))
	for _, frame := range frames {
		writeFrame(digest, frame)
	}
	var output [sha256.Size]byte
	copy(output[:], digest.Sum(nil))
	return output
}

func semanticDraftBytes(operation OperationName, operationID OperationID, context Context, policy ContextPolicy, values []draft) []byte {
	var output bytes.Buffer
	writeFrame(&output, []byte("frostgrove.audit/record-semantic/v1"))
	writeFrame(&output, []byte(operation))
	writeFrame(&output, operationID[:])
	writeSemanticContext(&output, context, policy)
	writeUint32(&output, uint32(len(values)))
	for _, value := range values {
		writeDraft(&output, value)
	}
	return output.Bytes()
}

func writeSemanticContext(output interface{ Write([]byte) (int, error) }, context Context, policy ContextPolicy) {
	writeUint32(output, uint32(len(context.Actors)))
	for _, actor := range context.Actors {
		writeUint32(output, uint32(actor.Kind))
		writeFrame(output, []byte(actor.Reference))
		writeUint32(output, uint32(actor.Provenance))
	}
	count := uint32(0)
	for _, fact := range policy.value.facts {
		if fact.kind == ActorChainContext {
			continue
		}
		if present, _, _ := contextFactValue(context, fact.kind); present {
			count++
		}
	}
	writeUint32(output, count)
	for _, fact := range policy.value.facts {
		if fact.kind == ActorChainContext {
			continue
		}
		present, provenance, raw := contextFactValue(context, fact.kind)
		if !present {
			continue
		}
		encoded, _ := contextValueBytes(raw)
		writeUint32(output, uint32(fact.kind))
		writeUint32(output, uint32(provenance))
		writeUint32(output, uint32(fact.classification))
		writeUint32(output, uint32(fact.mode))
		writeFrame(output, encoded)
	}
}

func writeDraft(output interface{ Write([]byte) (int, error) }, value draft) {
	writeFrame(output, []byte(value.descriptor.Resource))
	writeFrame(output, []byte(value.descriptor.Action))
	writeFrame(output, []byte(value.target))
	writeTime(output, value.occurredAt)
	writeFrame(output, []byte(value.outcome))
	writeFrame(output, []byte(value.reason))
	writeUint32(output, uint32(len(value.values)))
	for _, field := range value.values {
		writeDraftValue(output, field)
	}
}

func writeEntityDraft(output interface{ Write([]byte) (int, error) }, value entityDraft) {
	writeFrame(output, []byte(value.descriptor.Resource))
	writeFrame(output, []byte(value.action))
	writeUint32(output, uint32(value.state))
	writeFrame(output, []byte(value.subject))
	writeUint32(output, uint32(len(value.values)))
	for _, field := range value.values {
		writeDraftValue(output, field)
	}
	writeUint32(output, uint32(len(value.changes)))
	for _, change := range value.changes {
		writeFrame(output, []byte(change.field))
		writeDraftValue(output, change.before)
		writeDraftValue(output, change.after)
	}
}

func writeDraftValue(output interface{ Write([]byte) (int, error) }, value draftValue) {
	writeFrame(output, []byte(value.field))
	writeFrame(output, []byte(value.codec.Name))
	writeUint32(output, uint32(value.codec.WriteVersion))
	writeFrame(output, value.codec.Fingerprint[:])
	writeUint32(output, uint32(value.classification))
	writeUint32(output, uint32(value.mode))
	writeUint32(output, uint32(value.state))
	writeFrame(output, value.canonical)
}

func envelopeDigestOf(view RevisionWireView) EnvelopeDigest {
	digest := sha256.New()
	writeFrame(digest, []byte("frostgrove.audit/revision-envelope/v1"))
	writeRevisionHeader(digest, view.Header, false)
	writeUint32(digest, uint32(len(view.Actors)))
	for _, actor := range view.Actors {
		writeUint32(digest, uint32(actor.Ordinal))
		writeUint32(digest, uint32(actor.Kind))
		writeUint32(digest, uint32(actor.Provenance))
		writeStoredValue(digest, actor.Reference)
	}
	writeUint32(digest, uint32(len(view.Context)))
	for _, fact := range view.Context {
		writeUint32(digest, uint32(fact.Kind))
		writeUint32(digest, uint32(fact.Provenance))
		writeUint32(digest, uint32(fact.Classification))
		writeUint32(digest, uint32(fact.Mode))
		writeFrame(digest, fact.Plaintext)
		writeBool(digest, fact.Redacted)
		writeToken(digest, fact.Token)
		writeProtected(digest, fact.Protected)
	}
	writeUint32(digest, uint32(len(view.Items)))
	for _, item := range view.Items {
		writeItem(digest, item, true)
	}
	var output EnvelopeDigest
	copy(output[:], digest.Sum(nil))
	return output
}

func leafDigestOf(log LogID, catalog CatalogRef, revision RevisionID, item ItemWireView) LeafDigest {
	digest := sha256.New()
	writeFrame(digest, []byte("frostgrove.audit/item-leaf/v1"))
	writeFrame(digest, log[:])
	writeCatalogRef(digest, catalog)
	writeFrame(digest, revision[:])
	writeItem(digest, item, false)
	var output LeafDigest
	copy(output[:], digest.Sum(nil))
	return output
}

func integrityDigestOf(header RevisionHeaderView) IntegrityDigest {
	digest := sha256.New()
	writeFrame(digest, []byte("frostgrove.audit/revision-integrity/v1"))
	writeCatalogRef(digest, header.Catalog)
	writeFrame(digest, header.RevisionID[:])
	writeTime(digest, header.ObservedAt)
	writeFrame(digest, header.Semantic[:])
	writeFrame(digest, header.Envelope[:])
	var output IntegrityDigest
	copy(output[:], digest.Sum(nil))
	return output
}

func appendIntentDigestOf(view RevisionWireView) AppendIntentDigest {
	digest := sha256.New()
	writeFrame(digest, []byte("frostgrove.audit/append-intent/v1"))
	writeRevisionHeader(digest, view.Header, true)
	writeFrame(digest, view.Header.Integrity[:])
	writeSeal(digest, view.Header.Seal)
	envelope := envelopeDigestOf(view)
	writeFrame(digest, envelope[:])
	var output AppendIntentDigest
	copy(output[:], digest.Sum(nil))
	return output
}

func attemptAppendIntentDigestOf(view RevisionWireView, attempt AttemptConditionalAppendView) AppendIntentDigest {
	digest := sha256.New()
	writeFrame(digest, []byte("frostgrove.audit/attempt-append-intent/v1"))
	writeRevisionHeader(digest, view.Header, true)
	writeFrame(digest, view.Header.Integrity[:])
	writeSeal(digest, view.Header.Seal)
	envelope := envelopeDigestOf(view)
	writeFrame(digest, envelope[:])
	writeFrame(digest, attempt.Chain[:])
	writeAttemptProjectionState(digest, attempt.Expected)
	writeAttemptTypeProjectionState(digest, attempt.TypeExpected)
	writeFrame(digest, attempt.ResumeAuthorization[:])
	writeAttemptProjectionState(digest, attempt.Candidate.Result)
	writeAttemptTypeProjectionState(digest, attempt.Candidate.TypeResult)
	var output AppendIntentDigest
	copy(output[:], digest.Sum(nil))
	return output
}

func writeRevisionHeader(output interface{ Write([]byte) (int, error) }, header RevisionHeaderView, includeEnvelope bool) {
	writeUint32(output, uint32(header.Format))
	writeFrame(output, header.Log[:])
	writeCatalogRef(output, header.Catalog)
	writeFrame(output, header.CatalogSet[:])
	writeFrame(output, header.Deployment[:])
	writeFrame(output, []byte(header.Operation))
	writeFrame(output, header.OperationID[:])
	writeFrame(output, header.RevisionID[:])
	writeBool(output, header.HasIdempotency)
	writeFrame(output, header.Idempotency[:])
	writeTime(output, header.ObservedAt)
	writeFrame(output, []byte(header.Retention))
	writeUint32(output, uint32(header.Consequence))
	writeFrame(output, header.RetentionBasis[:])
	writeAuthorizationSummary(output, header.Authorization)
	writeFrame(output, header.Semantic[:])
	if includeEnvelope {
		writeFrame(output, header.Envelope[:])
	}
}

func writeAuthorizationSummary(output interface{ Write([]byte) (int, error) }, summary RevisionAuthorizationSummaryView) {
	writeUint32(output, uint32(len(summary.Resources)))
	for _, resource := range summary.Resources {
		writeFrame(output, []byte(resource))
	}
	writeUint32(output, uint32(len(summary.Actions)))
	for _, action := range summary.Actions {
		writeFrame(output, []byte(action))
	}
	writeUint32(output, uint32(len(summary.Classifications)))
	for _, classification := range summary.Classifications {
		writeUint32(output, uint32(classification))
	}
}

func writeItem(output interface{ Write([]byte) (int, error) }, item ItemWireView, includeLeaf bool) {
	writeUint32(output, uint32(item.Ordinal))
	writeUint32(output, uint32(item.Kind))
	writeFrame(output, []byte(item.Resource))
	writeFrame(output, []byte(item.Action))
	writeUint32(output, uint32(item.EntityState))
	writeFrame(output, item.Chain[:])
	writeStoredValue(output, item.Subject)
	writeStoredValue(output, item.Target)
	writeTime(output, item.OccurredAt)
	writeFrame(output, []byte(item.Outcome))
	writeFrame(output, []byte(item.Reason))
	writeFrame(output, item.Previous[:])
	if includeLeaf {
		writeFrame(output, item.Leaf[:])
	}
	writeUint32(output, uint32(len(item.Values)))
	for _, value := range item.Values {
		writeStoredValue(output, value)
	}
	writeUint32(output, uint32(len(item.Changes)))
	for _, change := range item.Changes {
		writeFrame(output, []byte(change.Field))
		writeStoredValue(output, change.Before)
		writeStoredValue(output, change.After)
	}
	if item.Kind == AttemptItem {
		writeAttemptTransition(output, item.Attempt)
	}
}

func writeAttemptTransition(output interface{ Write([]byte) (int, error) }, value AttemptTransitionWireView) {
	writeFrame(output, value.Chain[:])
	writeFrame(output, value.Policy[:])
	writeFrame(output, value.Replay[:])
	writeFrame(output, []byte(value.Operation))
	writeFrame(output, value.OperationID[:])
	writeBool(output, value.ScopePresent)
	writeFrame(output, value.Scope[:])
	writeItemRef(output, value.Start)
	writeUint32(output, uint32(value.Sequence))
	writeUint32(output, uint32(value.CheckpointCount))
	writeUint32(output, uint32(value.Kind))
	writeFrame(output, []byte(value.Checkpoint))
	writeTime(output, value.ExpiresAt)
	writeAttemptProjectionState(output, value.Expected)
	writeAttemptProjectionNext(output, value.Result)
	writeAttemptTypeProjectionState(output, value.TypeExpected)
	writeAttemptTypeProjectionNext(output, value.TypeResult)
	writeFrame(output, value.ResumeAuthorization[:])
}

func writeAttemptProjectionState(output interface{ Write([]byte) (int, error) }, value AttemptProjectionStateView) {
	writeBool(output, value.Present)
	writeFrame(output, value.Chain[:])
	writeFrame(output, value.Policy[:])
	writeFrame(output, value.Replay[:])
	writeFrame(output, []byte(value.Operation))
	writeFrame(output, value.OperationID[:])
	writeBool(output, value.TargetPresent)
	writeFrame(output, value.Target[:])
	writeBool(output, value.ScopePresent)
	writeFrame(output, value.Scope[:])
	writeFrame(output, value.Owner[:])
	writeUint32(output, uint32(value.State))
	writeUint32(output, uint32(value.Sequence))
	writeUint32(output, uint32(value.CheckpointCount))
	writeUint64(output, value.TransitionBytes)
	writeItemRef(output, value.Start)
	writeItemRef(output, value.Head)
	writeFrame(output, value.Leaf[:])
	writeTime(output, value.ExpiresAt)
}

func writeAttemptProjectionNext(output interface{ Write([]byte) (int, error) }, value AttemptProjectionNextView) {
	writeBool(output, value.Present)
	writeFrame(output, value.Chain[:])
	writeFrame(output, value.Policy[:])
	writeFrame(output, value.Replay[:])
	writeFrame(output, []byte(value.Operation))
	writeFrame(output, value.OperationID[:])
	writeBool(output, value.TargetPresent)
	writeFrame(output, value.Target[:])
	writeBool(output, value.ScopePresent)
	writeFrame(output, value.Scope[:])
	writeFrame(output, value.Owner[:])
	writeUint32(output, uint32(value.State))
	writeUint32(output, uint32(value.Sequence))
	writeUint32(output, uint32(value.CheckpointCount))
	writeUint64(output, value.TransitionBytes)
	writeItemRef(output, value.Start)
	writeItemRef(output, value.Head)
	writeTime(output, value.ExpiresAt)
}

func writeAttemptTypeProjectionState(output interface{ Write([]byte) (int, error) }, value AttemptTypeProjectionStateView) {
	writeFrame(output, []byte(value.Catalog))
	writeFrame(output, []byte(value.Operation))
	writeFrame(output, value.Policy[:])
	writeFrame(output, value.Replay[:])
	writeFrame(output, value.Anchor[:])
	writeUint64(output, value.Unsettled)
	writeItemRef(output, value.Head)
	writeFrame(output, value.Leaf[:])
}

func writeAttemptTypeProjectionNext(output interface{ Write([]byte) (int, error) }, value AttemptTypeProjectionNextView) {
	writeFrame(output, []byte(value.Catalog))
	writeFrame(output, []byte(value.Operation))
	writeFrame(output, value.Policy[:])
	writeFrame(output, value.Replay[:])
	writeFrame(output, value.Anchor[:])
	writeUint64(output, value.Unsettled)
	writeItemRef(output, value.Head)
}

func writeItemRef(output interface{ Write([]byte) (int, error) }, value ItemRef) {
	writeCatalogRef(output, value.Revision.Catalog)
	writeFrame(output, value.Revision.Revision[:])
	writeUint32(output, uint32(value.Ordinal))
}

func writeStoredValue(output interface{ Write([]byte) (int, error) }, value StoredValueView) {
	writeFrame(output, []byte(value.Field))
	writeFrame(output, []byte(value.Codec.Name))
	writeUint32(output, uint32(value.Codec.WriteVersion))
	writeFrame(output, value.Codec.Fingerprint[:])
	writeUint32(output, uint32(value.Classification))
	writeUint32(output, uint32(value.Mode))
	writeUint32(output, uint32(value.State))
	writeFrame(output, value.Plaintext)
	writeBool(output, value.Redacted)
	writeToken(output, value.Token)
	writeProtected(output, value.Protected)
}

func writeToken(output interface{ Write([]byte) (int, error) }, value Token) {
	writeFrame(output, []byte(value.Algorithm()))
	writeFrame(output, []byte(value.Profile()))
	writeFrame(output, []byte(value.KeyID()))
	writeFrame(output, value.Bytes())
}

func writeProtected(output interface{ Write([]byte) (int, error) }, value ProtectedValue) {
	writeFrame(output, []byte(value.Algorithm()))
	writeFrame(output, []byte(value.Profile()))
	writeFrame(output, []byte(value.KeyID()))
	writeFrame(output, value.Nonce())
	writeFrame(output, value.Ciphertext())
}

func writeSeal(output interface{ Write([]byte) (int, error) }, value Seal) {
	writeFrame(output, []byte(value.Algorithm()))
	writeFrame(output, []byte(value.Profile()))
	writeFrame(output, []byte(value.KeyID()))
	writeFrame(output, value.Bytes())
}

func writeCatalogRef(output interface{ Write([]byte) (int, error) }, catalog CatalogRef) {
	writeFrame(output, []byte(catalog.ID))
	writeUint64(output, uint64(catalog.Generation))
	writeFrame(output, catalog.Digest[:])
}

func writeTime(output interface{ Write([]byte) (int, error) }, value time.Time) {
	writeFrame(output, []byte(value.UTC().Format(time.RFC3339Nano)))
}

func writeBool(output interface{ Write([]byte) (int, error) }, value bool) {
	if value {
		writeUint32(output, 1)
		return
	}
	writeUint32(output, 0)
}

func writeUint64(target interface{ Write([]byte) (int, error) }, value uint64) {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], value)
	_, _ = target.Write(raw[:])
}
