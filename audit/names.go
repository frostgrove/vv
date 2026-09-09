package audit

import (
	"unicode"
	"unicode/utf8"
)

type CatalogID string
type CatalogGeneration uint64
type CatalogDigest [32]byte
type CatalogSetDigest [32]byte
type LogID [16]byte
type BackingID [16]byte
type OperationName string
type OperationID [16]byte
type RevisionID [16]byte
type AttemptChainID [32]byte
type AttemptPolicyFingerprint [32]byte
type AttemptReplayFingerprint [32]byte
type CatalogActivationGateDigest [32]byte
type AttemptCheckpointCode string
type RetentionCohortID [32]byte
type HoldID [16]byte
type IdempotencyKey string
type IdempotencyToken [32]byte
type EntityChainKey [32]byte
type EntityChainID [32]byte
type HoldIdentity [32]byte
type HoldIDCommitment [32]byte
type HoldMatterCommitment [32]byte
type RequesterCommitment [32]byte
type AttemptOwnerCommitment [32]byte
type AttemptTargetCommitment [32]byte
type EvidenceScopeCommitment [32]byte
type LeafDigest [32]byte
type SelectionDigest [32]byte
type FenceDigest [32]byte
type AppendIntentDigest [32]byte
type CorrectionProposalDigest [32]byte
type DenialRequestDigest [32]byte
type HoldRequestDigest [32]byte
type RetentionBasisDigest [32]byte
type HoldSetDigest [32]byte
type AccessRequestDigest [32]byte
type AccessGrantDigest [32]byte
type AccessResultDigest [32]byte
type AccessContinuationDigest [32]byte
type ControlRequestDigest [32]byte
type ControlGrantDigest [32]byte
type ControlResultDigest [32]byte
type ControlContinuationDigest [32]byte
type Resource string
type Action string
type FieldName string
type Purpose string
type Owner string
type RetentionClass string
type Reason string
type Reference string
type Outcome string
type Source string
type FixtureName string
type PolicyVersion uint32
type PolicyFingerprint [32]byte
type PolicyFixtureFingerprint [32]byte
type CodecSemanticFingerprint [32]byte
type PrivacyReason string
type DeploymentLedger string
type DeploymentChange string
type SemanticDigest [32]byte
type EnvelopeDigest [32]byte
type IntegrityDigest [32]byte

type ScopedReference struct {
	Scope     Reference
	Reference Reference
}

type catalogChangeRef struct {
	ledger DeploymentLedger
	change DeploymentChange
}

type CatalogChangeRef struct{ value catalogChangeRef }

type CatalogChangeRefView struct {
	Ledger DeploymentLedger
	Change DeploymentChange
}

func NewCatalogChangeRef(ledger DeploymentLedger, change DeploymentChange) (CatalogChangeRef, error) {
	if !validSemanticName(string(ledger)) {
		return CatalogChangeRef{}, auditErrorAt(ErrInvalid, "ledger")
	}
	if !validOpaqueReference(string(change), MaxReferenceBytes) {
		return CatalogChangeRef{}, auditErrorAt(ErrInvalid, "change")
	}
	return CatalogChangeRef{value: catalogChangeRef{ledger: ledger, change: change}}, nil
}

func (r CatalogChangeRef) View() CatalogChangeRefView {
	return CatalogChangeRefView{Ledger: r.value.ledger, Change: r.value.change}
}

func (CatalogChangeRef) String() string { return "[audit catalog change reference]" }

func (r CatalogChangeRef) valid() bool {
	return validSemanticName(string(r.value.ledger)) && validOpaqueReference(string(r.value.change), MaxReferenceBytes)
}

func validSemanticName(value string) bool {
	if value == "" || len(value) > MaxNameBytes {
		return false
	}
	segmentStart := true
	for _, r := range value {
		if segmentStart {
			if r < 'a' || r > 'z' {
				return false
			}
			segmentStart = false
			continue
		}
		switch {
		case r == '.':
			segmentStart = true
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}
	return !segmentStart
}

func validOpaqueReference(value string, bound int) bool {
	return validBoundedText(value, bound, false)
}

func validBoundedText(value string, bound int, empty bool) bool {
	if bound < 0 || len(value) > bound || !utf8.ValidString(value) || value == "" && !empty {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func isZeroValue[T comparable](value T) bool {
	var zero T
	return value == zero
}
