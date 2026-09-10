package audit

import (
	"bytes"
	"crypto/sha256"
	"slices"
)

type ManifestView struct {
	Ref          CatalogRef
	Previous     CatalogRef
	Owner        Owner
	Semantics    SemanticDigestDescription
	Identities   IdentityCommitmentDescription
	Protection   ProtectionDescription
	Tokens       TokenDescription
	Integrity    IntegrityPolicyView
	Retention    []RetentionRuleView
	Declarations []DeclarationDescription
	Codecs       []CodecDescription
	Contexts     []ContextPolicyDescription
	Control      ControlPolicyDescription
}

type manifest struct {
	view      ManifestView
	canonical []byte
}

type Manifest struct {
	value *manifest
}

func (m Manifest) Ref() CatalogRef {
	if m.value == nil {
		return CatalogRef{}
	}
	return m.value.view.Ref
}

func (m Manifest) Previous() CatalogRef {
	if m.value == nil {
		return CatalogRef{}
	}
	return m.value.view.Previous
}

func (m Manifest) Canonical() []byte {
	if m.value == nil {
		return nil
	}
	return bytes.Clone(m.value.canonical)
}

func (m Manifest) View() ManifestView {
	if m.value == nil {
		return ManifestView{}
	}
	return cloneManifestView(m.value.view)
}

func cloneManifest(value Manifest) Manifest {
	if value.value == nil {
		return Manifest{}
	}
	return Manifest{value: &manifest{view: cloneManifestView(value.value.view), canonical: bytes.Clone(value.value.canonical)}}
}

func cloneManifestView(input ManifestView) ManifestView {
	output := input
	output.Retention = slices.Clone(input.Retention)
	output.Declarations = make([]DeclarationDescription, len(input.Declarations))
	for index, declaration := range input.Declarations {
		output.Declarations[index] = cloneDeclarationDescription(declaration)
	}
	output.Codecs = make([]CodecDescription, len(input.Codecs))
	for index, codec := range input.Codecs {
		output.Codecs[index] = codec
		output.Codecs[index].ReadVersions = slices.Clone(codec.ReadVersions)
	}
	output.Contexts = make([]ContextPolicyDescription, len(input.Contexts))
	for index, context := range input.Contexts {
		output.Contexts[index] = cloneContextPolicyDescription(context)
	}
	output.Control = cloneControlPolicyDescription(input.Control)
	return output
}

func newManifest(view ManifestView) (Manifest, error) {
	view = cloneManifestView(view)
	view.Ref.Digest = CatalogDigest{}
	preimage := encodeManifestView(view)
	digest := sha256.New()
	writeFrame(digest, []byte("frostgrove.audit/catalog-manifest/v1"))
	writeFrame(digest, preimage)
	copy(view.Ref.Digest[:], digest.Sum(nil))
	canonical := encodeManifestView(view)
	if len(canonical) > MaxCatalogManifestBytes {
		return Manifest{}, auditTooLarge("manifest", MaxCatalogManifestBytes)
	}
	return Manifest{value: &manifest{view: view, canonical: canonical}}, nil
}

func validManifest(value Manifest) bool {
	if value.value == nil || !value.value.view.Ref.valid() || len(value.value.canonical) == 0 || len(value.value.canonical) > MaxCatalogManifestBytes {
		return false
	}
	if !bytes.Equal(value.value.canonical, encodeManifestView(value.value.view)) {
		return false
	}
	view := cloneManifestView(value.value.view)
	want := view.Ref.Digest
	view.Ref.Digest = CatalogDigest{}
	digest := sha256.New()
	writeFrame(digest, []byte("frostgrove.audit/catalog-manifest/v1"))
	writeFrame(digest, encodeManifestView(view))
	return bytes.Equal(want[:], digest.Sum(nil))
}

func policyFingerprint(description DeclarationDescription, metadata []byte) PolicyFingerprint {
	candidate := cloneDeclarationDescription(description)
	candidate.Semantics.Fingerprint = PolicyFingerprint{}
	digest := sha256.New()
	writeFrame(digest, []byte("frostgrove.audit/declaration-policy/v1"))
	writeDeclarationDescription(digest, candidate)
	writeFrame(digest, metadata)
	var output PolicyFingerprint
	wire := digest.Sum(nil)
	copy(output[:], wire)
	return output
}

func fixtureFingerprint(kind PolicyFixtureKind, name FixtureName, version PolicyVersion, resource Resource, action Action, payload []byte) PolicyFixtureFingerprint {
	digest := sha256.New()
	writeFrame(digest, []byte("frostgrove.audit/policy-fixture/v1"))
	writeUint32(digest, uint32(kind))
	writeFrame(digest, []byte(name))
	writeUint32(digest, uint32(version))
	writeFrame(digest, []byte(resource))
	writeFrame(digest, []byte(action))
	writeFrame(digest, payload)
	var output PolicyFixtureFingerprint
	copy(output[:], digest.Sum(nil))
	return output
}

func encodeManifestView(view ManifestView) []byte {
	var output bytes.Buffer
	writeFrame(&output, []byte("frostgrove.audit/manifest-wire/v1"))
	writeCatalogRef(&output, view.Ref)
	writeCatalogRef(&output, view.Previous)
	writeFrame(&output, []byte(view.Owner))
	writeProvider(&output, view.Semantics.Algorithm, view.Semantics.Profile, view.Semantics.KeyID)
	writeProvider(&output, view.Identities.Algorithm, view.Identities.Profile, view.Identities.KeyID)
	writeProvider(&output, view.Protection.Algorithm, view.Protection.Profile, view.Protection.KeyID)
	writeProvider(&output, view.Tokens.Algorithm, view.Tokens.Profile, view.Tokens.KeyID)
	writeBool(&output, view.Integrity.RequiresSignature)
	writeProvider(&output, view.Integrity.Signature.Algorithm, view.Integrity.Signature.Profile, view.Integrity.Signature.KeyID)
	writeUint32(&output, uint32(len(view.Retention)))
	for _, rule := range view.Retention {
		writeFrame(&output, []byte(rule.Class))
		writeBool(&output, rule.Forever)
		writeUint32(&output, uint32(rule.Period.Years))
		writeUint32(&output, uint32(rule.Period.Months))
		writeUint32(&output, uint32(rule.Period.Days))
	}
	writeUint32(&output, uint32(len(view.Declarations)))
	for _, declaration := range view.Declarations {
		writeDeclarationDescription(&output, declaration)
	}
	writeUint32(&output, uint32(len(view.Codecs)))
	for _, codec := range view.Codecs {
		writeCodecDescription(&output, codec)
	}
	writeUint32(&output, uint32(len(view.Contexts)))
	for _, context := range view.Contexts {
		writeContextDescription(&output, context)
	}
	writeControlDescription(&output, view.Control)
	return output.Bytes()
}

func writeDeclarationDescription(output interface{ Write([]byte) (int, error) }, value DeclarationDescription) {
	writeUint32(output, uint32(value.Kind))
	writeFrame(output, []byte(value.Resource))
	writeFrame(output, []byte(value.Action))
	writeFrame(output, []byte(value.Operation))
	writePolicySemanticsDescription(output, value.Semantics)
	writeFrame(output, []byte(value.Owner))
	writeFrame(output, []byte(value.Purpose))
	writeFrame(output, []byte(value.Retention))
	writeUint32(output, uint32(value.Consequence))
	writeSubjectDescription(output, value.Subject)
	writeBool(output, value.TargetPresent)
	writeSubjectDescription(output, value.Target)
	writeUint32(output, uint32(len(value.Actions)))
	for _, action := range value.Actions {
		writeFrame(output, []byte(action))
	}
	writeUint32(output, uint32(len(value.Outcomes)))
	for _, outcome := range value.Outcomes {
		writeFrame(output, []byte(outcome))
	}
	writeUint32(output, uint32(len(value.Reasons)))
	for _, reason := range value.Reasons {
		writeFrame(output, []byte(reason))
	}
	writeBool(output, value.ReasonOptional)
	writeFieldDescriptions(output, value.Fields)
	writeContextDescription(output, value.Context)
	writeUint32(output, uint32(len(value.Members)))
	for _, member := range value.Members {
		writeFrame(output, []byte(member))
	}
	writeAttemptDescription(output, value.Attempt)
}

func writePolicySemanticsDescription(output interface{ Write([]byte) (int, error) }, value PolicySemanticsDescription) {
	writeUint32(output, uint32(value.Version))
	writeFrame(output, value.Fingerprint[:])
	writeUint32(output, uint32(len(value.Fixtures)))
	for _, fixture := range value.Fixtures {
		writeFrame(output, []byte(fixture.Name))
		writeFrame(output, fixture.Fingerprint[:])
		writeUint32(output, uint32(fixture.Kind))
		writeUint32(output, uint32(fixture.Transition))
		writeFrame(output, []byte(fixture.Checkpoint))
		writeFrame(output, []byte(fixture.Reason))
	}
}

func writeSubjectDescription(output interface{ Write([]byte) (int, error) }, value SubjectDescription) {
	writeUint32(output, uint32(value.Classification))
	writeUint32(output, uint32(value.Mode))
}

func writeFieldDescriptions(output interface{ Write([]byte) (int, error) }, values []FieldDescription) {
	writeUint32(output, uint32(len(values)))
	for _, value := range values {
		writeFrame(output, []byte(value.Source))
		writeFrame(output, []byte(value.Name))
		writeCodecDescription(output, value.Codec)
		writeUint32(output, uint32(value.Classification))
		writeUint32(output, uint32(value.Mode))
		writeBool(output, value.Reconstruct)
		writeBool(output, value.HistoricalOnly)
		writeBool(output, value.QueryIndex)
	}
}

func writeCodecDescription(output interface{ Write([]byte) (int, error) }, value CodecDescription) {
	writeFrame(output, []byte(value.Name))
	writeUint32(output, uint32(value.WriteVersion))
	writeUint32(output, uint32(len(value.ReadVersions)))
	for _, version := range value.ReadVersions {
		writeUint32(output, uint32(version))
	}
	writeFrame(output, value.Fingerprint[:])
}

func writeContextDescription(output interface{ Write([]byte) (int, error) }, value ContextPolicyDescription) {
	writeUint32(output, uint32(len(value.Facts)))
	for _, fact := range value.Facts {
		writeUint32(output, uint32(fact.Kind))
		writeUint32(output, uint32(fact.Presence))
		writeUint32(output, uint32(len(fact.Allowed)))
		for _, provenance := range fact.Allowed {
			writeUint32(output, uint32(provenance))
		}
		writeUint32(output, uint32(fact.Classification))
		writeUint32(output, uint32(fact.Mode))
	}
}

func writeAttemptDescription(output interface{ Write([]byte) (int, error) }, value AttemptDescription) {
	writeFrame(output, []byte(value.Operation))
	writeFrame(output, value.Fingerprint[:])
	writeUint64(output, uint64(value.MaxOpen))
	writeUint32(output, uint32(value.MaxCheckpoints))
	writeUint64(output, value.MaxStateBytes)
	writeUint64(output, value.OpenReserveBytes)
	writeUint64(output, value.UncertainReserveBytes)
	writeUint32(output, uint32(len(value.Continuity)))
	for _, fact := range value.Continuity {
		writeUint32(output, uint32(fact))
	}
	writeAttemptPhaseDescription(output, value.Start)
	writeAttemptPhaseDescription(output, value.Checkpoint)
	writeUint32(output, uint32(len(value.CheckpointCodes)))
	for _, code := range value.CheckpointCodes {
		writeFrame(output, []byte(code))
	}
	writeUint32(output, uint32(len(value.Finish)))
	for _, phase := range value.Finish {
		writeUint32(output, uint32(phase.Transition))
		writeFieldDescriptions(output, phase.Fields)
	}
	writeUint32(output, uint32(len(value.Reasons)))
	for _, reason := range value.Reasons {
		writeUint32(output, uint32(reason.Transition))
		writeUint32(output, uint32(len(reason.Codes)))
		for _, code := range reason.Codes {
			writeFrame(output, []byte(code))
		}
	}
}

func writeAttemptPhaseDescription(output interface{ Write([]byte) (int, error) }, value AttemptPhaseDescription) {
	writeBool(output, value.TargetPresent)
	writeSubjectDescription(output, value.Target)
	writeFieldDescriptions(output, value.Fields)
}

func writeProvider(output interface{ Write([]byte) (int, error) }, algorithm, profile, keyID string) {
	writeFrame(output, []byte(algorithm))
	writeFrame(output, []byte(profile))
	writeFrame(output, []byte(keyID))
}
