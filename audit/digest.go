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

func semanticDraftBytes(operation OperationName, operationID OperationID, values []draft) []byte {
	var output bytes.Buffer
	writeFrame(&output, []byte("frostgrove.audit/record-semantic/v1"))
	writeFrame(&output, []byte(operation))
	writeFrame(&output, operationID[:])
	writeUint32(&output, uint32(len(values)))
	for _, value := range values {
		writeDraft(&output, value)
	}
	return output.Bytes()
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
	writeFrame(output, header.Semantic[:])
	if includeEnvelope {
		writeFrame(output, header.Envelope[:])
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
