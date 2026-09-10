package audit

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
)

type ControlAction Action

const (
	HistoryRead                   ControlAction = "audit.history.read"
	HistoryDenied                 ControlAction = "audit.history.denied"
	ControlDenied                 ControlAction = "audit.control.denied"
	CorrectionAppended            ControlAction = "audit.correction.appended"
	DisputeAppended               ControlAction = "audit.dispute.appended"
	IntegrityVerified             ControlAction = "audit.integrity.verified"
	InventoryRead                 ControlAction = "audit.inventory.read"
	HoldPlaced                    ControlAction = "audit.hold.placed"
	HoldReleased                  ControlAction = "audit.hold.released"
	PurgePlanned                  ControlAction = "audit.purge.planned"
	AttemptContinuationAuthorized ControlAction = "audit.attempt.continuation_authorized"
	AttemptAccessDenied           ControlAction = "audit.attempt.access_denied"
)

type controlReasonPolicy struct {
	action ControlAction
	codes  []Reason
}

type ControlReasonPolicy struct {
	value controlReasonPolicy
}

type ControlReasonDescription struct {
	Action ControlAction
	Codes  []Reason
}

type ControlOutcomeDescription struct {
	Action ControlAction
	Codes  []Outcome
}

type holdMatterPolicy struct {
	codec          Codec[Reference]
	classification Classification
	mode           StorageMode
}

type HoldMatterPolicy struct {
	value holdMatterPolicy
}

type HoldMatterDescription struct {
	Codec          CodecDescription
	Classification Classification
	Mode           StorageMode
}

type ControlPolicy struct {
	Resource    Resource
	Semantics   PolicySemantics
	Purpose     Purpose
	Retention   RetentionClass
	Consequence Consequence
	Context     ContextPolicy
	Matter      HoldMatterPolicy
	Actions     []ControlAction
	Reasons     []ControlReasonPolicy
}

type ControlPolicyDescription struct {
	Resource    Resource
	Semantics   PolicySemanticsDescription
	Purpose     Purpose
	Retention   RetentionClass
	Consequence Consequence
	Context     ContextPolicyDescription
	Matter      HoldMatterDescription
	Actions     []ControlAction
	Reasons     []ControlReasonDescription
	Outcomes    []ControlOutcomeDescription
}

type CatalogSpec struct {
	ID         CatalogID
	Owner      Owner
	Generation CatalogGeneration
	Previous   CatalogRef
	Retention  []RetentionRule
	Semantics  SemanticDigestDescription
	Identities IdentityCommitmentDescription
	Protection ProtectionDescription
	Tokens     TokenDescription
	Integrity  IntegrityPolicy
	Control    ControlPolicy
}

type catalog struct {
	manifest     Manifest
	declarations map[Declaration]PolicyFingerprint
}

type Catalog struct {
	value *catalog
}

type historicalCatalog struct {
	manifest Manifest
}

type HistoricalCatalog struct {
	value *historicalCatalog
}

type catalogSet struct {
	active       CatalogRef
	digest       CatalogSetDigest
	manifests    []Manifest
	byRef        map[CatalogRef]int
	declarations map[Declaration]PolicyFingerprint
}

type CatalogSet struct {
	value *catalogSet
}

func ControlActions(actions ...ControlAction) []ControlAction {
	return slices.Clone(actions)
}

func ReasonsFor(action ControlAction, codes ReasonCodes) ControlReasonPolicy {
	policy, err := TryReasonsFor(action, codes)
	if err != nil {
		panic(err)
	}
	return policy
}

func TryReasonsFor(action ControlAction, codes ReasonCodes) (ControlReasonPolicy, error) {
	if !controlReasonAction(action) || len(codes.value.values) == 0 {
		return ControlReasonPolicy{}, auditErrorAt(ErrDeclaration, "control.reasons")
	}
	return ControlReasonPolicy{value: controlReasonPolicy{action: action, codes: slices.Clone(codes.value.values)}}, nil
}

func ControlReasons(reasons ...ControlReasonPolicy) []ControlReasonPolicy {
	return slices.Clone(reasons)
}

func HoldMatter(codec Codec[Reference], classification Classification, mode StorageMode) HoldMatterPolicy {
	policy, err := TryHoldMatter(codec, classification, mode)
	if err != nil {
		panic(err)
	}
	return policy
}

func TryHoldMatter(codec Codec[Reference], classification Classification, mode StorageMode) (HoldMatterPolicy, error) {
	if codec.value == nil || !classification.Valid() || mode != AsProtected && mode != AsIndexedProtected {
		return HoldMatterPolicy{}, auditErrorAt(ErrDeclaration, "control.matter")
	}
	return HoldMatterPolicy{value: holdMatterPolicy{codec: codec, classification: classification, mode: mode}}, nil
}

func Compile(spec CatalogSpec, declarations ...Declaration) (*Catalog, error) {
	if err := validateCatalogSpec(spec); err != nil {
		return nil, err
	}
	if len(declarations) == 0 {
		return nil, auditErrorAt(ErrDeclaration, "declarations")
	}
	if len(declarations) > MaxCatalogDeclarations {
		return nil, auditTooLarge("declarations", MaxCatalogDeclarations)
	}
	retention, retentionByClass, err := canonicalRetention(spec.Retention)
	if err != nil {
		return nil, err
	}
	control, err := compileControlPolicy(spec.Control, retentionByClass)
	if err != nil {
		return nil, err
	}
	descriptions := make([]DeclarationDescription, len(declarations))
	declarationSet := make(map[Declaration]PolicyFingerprint, len(declarations))
	itemIdentities := make(map[string]struct{})
	resourceIdentities := make(map[Resource]struct{})
	operationIdentities := make(map[OperationName]struct{})
	seals := make([]*declarationSeal, len(declarations))
	for index, declaration := range declarations {
		if nilByReflection(declaration) {
			return nil, auditErrorAt(ErrDeclaration, "declarations")
		}
		compiled, ok := declaration.(compiledDeclaration)
		if !ok {
			return nil, auditErrorAt(ErrDeclaration, "declarations")
		}
		seal := compiled.sealedDeclaration()
		if seal == nil || seal.description.Semantics.Fingerprint == (PolicyFingerprint{}) {
			return nil, auditErrorAt(ErrDeclaration, "declarations")
		}
		if _, duplicate := declarationSet[declaration]; duplicate {
			return nil, auditErrorAt(ErrDeclaration, "declarations")
		}
		if _, ok := retentionByClass[seal.description.Retention]; !ok {
			return nil, auditErrorAt(ErrDeclaration, "declarations.retention")
		}
		if seal.description.Kind == ResourceDeclaration {
			if _, duplicate := resourceIdentities[seal.description.Resource]; duplicate {
				return nil, auditErrorAt(ErrDeclaration, "declarations.resource")
			}
			resourceIdentities[seal.description.Resource] = struct{}{}
		}
		if seal.description.Kind == OperationDeclaration {
			if _, duplicate := operationIdentities[seal.description.Operation]; duplicate {
				return nil, auditErrorAt(ErrDeclaration, "declarations.operation")
			}
			operationIdentities[seal.description.Operation] = struct{}{}
		}
		for _, member := range seal.members {
			if member.resource == "" {
				continue
			}
			if _, duplicate := itemIdentities[member.key]; duplicate {
				return nil, auditErrorAt(ErrDeclaration, "declarations.identity")
			}
			itemIdentities[member.key] = struct{}{}
		}
		fingerprint := seal.description.Semantics.Fingerprint
		declarationSet[declaration] = fingerprint
		descriptions[index] = cloneDeclarationDescription(seal.description)
		seals[index] = seal
	}
	if err := validateOperationMembership(seals, declarationSet); err != nil {
		return nil, err
	}
	if spec.Generation == 1 {
		for _, description := range descriptions {
			for _, field := range description.Fields {
				if field.HistoricalOnly {
					return nil, auditErrorAt(ErrDeclaration, "declarations.historical_field")
				}
			}
		}
	}
	if control.Resource != "" {
		for _, action := range control.Actions {
			key := declarationMemberKey(control.Resource, Action(action))
			if _, duplicate := itemIdentities[key]; duplicate {
				return nil, auditErrorAt(ErrDeclaration, "control.resource")
			}
			itemIdentities[key] = struct{}{}
		}
	}
	slices.SortFunc(descriptions, compareDeclarationDescription)
	codecs, err := collectCodecDescriptions(descriptions, control)
	if err != nil {
		return nil, err
	}
	contexts := collectContextDescriptions(descriptions, control)
	view := ManifestView{
		Ref: CatalogRef{ID: spec.ID, Generation: spec.Generation}, Previous: spec.Previous,
		Owner: spec.Owner, Semantics: spec.Semantics, Identities: spec.Identities,
		Protection: spec.Protection, Tokens: spec.Tokens, Integrity: spec.Integrity.view(),
		Retention: retention, Declarations: descriptions, Codecs: codecs, Contexts: contexts,
		Control: control,
	}
	if err := validateManifestProviders(view); err != nil {
		return nil, err
	}
	manifest, err := newManifest(view)
	if err != nil {
		return nil, err
	}
	return &Catalog{value: &catalog{manifest: manifest, declarations: declarationSet}}, nil
}

func validateCatalogSpec(spec CatalogSpec) error {
	if !validSemanticName(string(spec.ID)) || !validSemanticName(string(spec.Owner)) || spec.Generation == 0 {
		return auditErrorAt(ErrDeclaration, "catalog")
	}
	if spec.Generation == 1 {
		if spec.Previous != (CatalogRef{}) {
			return auditErrorAt(ErrDeclaration, "catalog.previous")
		}
	} else if !spec.Previous.valid() || spec.Previous.ID != spec.ID || spec.Previous.Generation+1 != spec.Generation {
		return auditErrorAt(ErrDeclaration, "catalog.previous")
	}
	if err := validateProviderDescription(spec.Semantics.Algorithm, spec.Semantics.Profile, spec.Semantics.KeyID); err != nil {
		return err
	}
	if err := validateProviderDescription(spec.Identities.Algorithm, spec.Identities.Profile, spec.Identities.KeyID); err != nil {
		return err
	}
	if err := validateOptionalProvider(spec.Protection.Algorithm, spec.Protection.Profile, spec.Protection.KeyID); err != nil {
		return err
	}
	if err := validateOptionalProvider(spec.Tokens.Algorithm, spec.Tokens.Profile, spec.Tokens.KeyID); err != nil {
		return err
	}
	if spec.Integrity.value.requiresSignature {
		if err := validateProviderDescription(spec.Integrity.value.signature.Algorithm, spec.Integrity.value.signature.Profile, spec.Integrity.value.signature.KeyID); err != nil {
			return err
		}
	}
	return nil
}

func validateOptionalProvider(algorithm, profile, keyID string) error {
	if algorithm == "" && profile == "" && keyID == "" {
		return nil
	}
	return validateProviderDescription(algorithm, profile, keyID)
}

func canonicalRetention(input []RetentionRule) ([]RetentionRuleView, map[RetentionClass]RetentionRuleView, error) {
	if len(input) == 0 || len(input) > MaxCatalogDeclarations {
		return nil, nil, auditErrorAt(ErrDeclaration, "retention")
	}
	views := make([]RetentionRuleView, len(input))
	byClass := make(map[RetentionClass]RetentionRuleView, len(input))
	for index, rule := range input {
		view := rule.view()
		if !validSemanticName(string(view.Class)) || view.Forever == (view.Period != (CalendarPeriod{})) || !view.Forever && !validCalendarPeriod(view.Period) {
			return nil, nil, auditErrorAt(ErrDeclaration, "retention")
		}
		if _, duplicate := byClass[view.Class]; duplicate {
			return nil, nil, auditErrorAt(ErrDeclaration, "retention")
		}
		byClass[view.Class] = view
		views[index] = view
	}
	slices.SortFunc(views, func(left, right RetentionRuleView) int {
		return strings.Compare(string(left.Class), string(right.Class))
	})
	return views, byClass, nil
}

func compileControlPolicy(policy ControlPolicy, retention map[RetentionClass]RetentionRuleView) (ControlPolicyDescription, error) {
	if controlPolicyZero(policy) {
		return ControlPolicyDescription{}, nil
	}
	if !validSemanticName(string(policy.Resource)) || !validSemanticName(string(policy.Purpose)) || !validSemanticName(string(policy.Retention)) {
		return ControlPolicyDescription{}, auditErrorAt(ErrDeclaration, "control")
	}
	if _, ok := retention[policy.Retention]; !ok || policy.Consequence != Required {
		return ControlPolicyDescription{}, auditErrorAt(ErrDeclaration, "control.retention")
	}
	if err := validatePolicySemantics(policy.Semantics, false); err != nil {
		return ControlPolicyDescription{}, err
	}
	if err := validateContextPolicy(policy.Context); err != nil {
		return ControlPolicyDescription{}, err
	}
	actions, err := canonicalControlActions(policy.Actions)
	if err != nil {
		return ControlPolicyDescription{}, err
	}
	reasons, err := canonicalControlReasons(policy.Reasons, actions)
	if err != nil {
		return ControlPolicyDescription{}, err
	}
	if controlNeedsScope(actions) && !requiredSearchableScope(policy.Context) {
		return ControlPolicyDescription{}, auditErrorAt(ErrDeclaration, "control.context.scope")
	}
	hold := slices.Contains(actions, HoldPlaced) || slices.Contains(actions, HoldReleased)
	matter := HoldMatterDescription{}
	if hold {
		if policy.Matter.value.codec.value == nil {
			return ControlPolicyDescription{}, auditErrorAt(ErrDeclaration, "control.matter")
		}
		matter = HoldMatterDescription{
			Codec: policy.Matter.value.codec.Description(), Classification: policy.Matter.value.classification,
			Mode: policy.Matter.value.mode,
		}
	} else if policy.Matter.value.codec.value != nil {
		return ControlPolicyDescription{}, auditErrorAt(ErrDeclaration, "control.matter")
	}
	description := ControlPolicyDescription{
		Resource: policy.Resource, Purpose: policy.Purpose, Retention: policy.Retention,
		Consequence: policy.Consequence, Context: policy.Context.description(), Matter: matter,
		Actions: actions, Reasons: reasons,
	}
	description.Semantics = policy.Semantics.description(PolicyFingerprint{})
	description.Semantics.Fingerprint = controlPolicyFingerprint(description)
	return description, nil
}

func controlNeedsScope(actions []ControlAction) bool {
	for _, action := range actions {
		switch action {
		case HistoryRead, CorrectionAppended, DisputeAppended, IntegrityVerified, InventoryRead,
			HoldPlaced, HoldReleased, PurgePlanned, AttemptContinuationAuthorized, AttemptAccessDenied:
			return true
		}
	}
	return false
}

func requiredSearchableScope(context ContextPolicy) bool {
	for _, fact := range context.value.facts {
		if fact.kind == ScopeContext {
			return fact.presence == ContextRequired && searchableMode(fact.mode)
		}
	}
	return false
}

func searchableMode(mode StorageMode) bool {
	return mode == AsPlaintext || mode == AsToken || mode == AsIndexedProtected
}

func validateManifestProviders(view ManifestView) error {
	protection := false
	tokens := false
	inspectMode := func(mode StorageMode) {
		protection = protection || mode == AsProtected || mode == AsIndexedProtected
		tokens = tokens || mode == AsToken || mode == AsIndexedProtected
	}
	for _, declaration := range view.Declarations {
		if declaration.Kind == ResourceDeclaration {
			inspectMode(declaration.Subject.Mode)
		}
		if declaration.TargetPresent {
			inspectMode(declaration.Target.Mode)
		}
		for _, field := range declaration.Fields {
			inspectMode(field.Mode)
		}
		for _, fact := range declaration.Context.Facts {
			inspectMode(fact.Mode)
		}
	}
	for _, fact := range view.Control.Context.Facts {
		inspectMode(fact.Mode)
	}
	inspectMode(view.Control.Matter.Mode)
	if protection && !completeProvider(view.Protection.Algorithm, view.Protection.Profile, view.Protection.KeyID) {
		return auditErrorAt(ErrDeclaration, "catalog.protection")
	}
	if tokens && !completeProvider(view.Tokens.Algorithm, view.Tokens.Profile, view.Tokens.KeyID) {
		return auditErrorAt(ErrDeclaration, "catalog.tokens")
	}
	if len(view.Control.Actions) != 0 {
		for _, declaration := range view.Declarations {
			for _, fact := range declaration.Context.Facts {
				if fact.Kind == ScopeContext && !searchableMode(fact.Mode) {
					return auditErrorAt(ErrDeclaration, "catalog.context.scope")
				}
			}
		}
	}
	return nil
}

func completeProvider(algorithm, profile, keyID string) bool {
	return validateProviderDescription(algorithm, profile, keyID) == nil
}

func controlPolicyZero(policy ControlPolicy) bool {
	return policy.Resource == "" && policy.Semantics.value.version == 0 && policy.Purpose == "" && policy.Retention == "" &&
		policy.Consequence == 0 && len(policy.Context.value.facts) == 0 && policy.Matter.value.codec.value == nil &&
		len(policy.Actions) == 0 && len(policy.Reasons) == 0
}

func canonicalControlActions(input []ControlAction) ([]ControlAction, error) {
	if len(input) == 0 || len(input) > MaxCodesPerDeclaration {
		return nil, auditErrorAt(ErrDeclaration, "control.actions")
	}
	actions := slices.Clone(input)
	slices.Sort(actions)
	for index, action := range actions {
		if !validControlAction(action) || index > 0 && action == actions[index-1] {
			return nil, auditErrorAt(ErrDeclaration, "control.actions")
		}
	}
	return actions, nil
}

func canonicalControlReasons(input []ControlReasonPolicy, actions []ControlAction) ([]ControlReasonDescription, error) {
	reasons := make([]ControlReasonDescription, len(input))
	seen := make(map[ControlAction]struct{}, len(input))
	for index, policy := range input {
		if !slices.Contains(actions, policy.value.action) || !controlReasonAction(policy.value.action) || len(policy.value.codes) == 0 {
			return nil, auditErrorAt(ErrDeclaration, "control.reasons")
		}
		if _, duplicate := seen[policy.value.action]; duplicate {
			return nil, auditErrorAt(ErrDeclaration, "control.reasons")
		}
		seen[policy.value.action] = struct{}{}
		codes, err := canonicalCodes(policy.value.codes, "control.reasons")
		if err != nil {
			return nil, err
		}
		reasons[index] = ControlReasonDescription{Action: policy.value.action, Codes: codes}
	}
	for _, action := range actions {
		if controlReasonAction(action) {
			if _, ok := seen[action]; !ok {
				return nil, auditErrorAt(ErrDeclaration, "control.reasons")
			}
		}
	}
	slices.SortFunc(reasons, func(left, right ControlReasonDescription) int {
		return strings.Compare(string(left.Action), string(right.Action))
	})
	return reasons, nil
}

func controlReasonAction(action ControlAction) bool {
	return action == HistoryDenied || action == ControlDenied || action == CorrectionAppended || action == DisputeAppended || action == AttemptAccessDenied
}

func validControlAction(action ControlAction) bool {
	return action == HistoryRead || action == HistoryDenied || action == ControlDenied || action == CorrectionAppended ||
		action == DisputeAppended || action == IntegrityVerified || action == InventoryRead || action == HoldPlaced ||
		action == HoldReleased || action == PurgePlanned || action == AttemptContinuationAuthorized || action == AttemptAccessDenied
}

func validateOperationMembership(seals []*declarationSeal, declarations map[Declaration]PolicyFingerprint) error {
	for _, seal := range seals {
		if seal.description.Kind != OperationDeclaration {
			continue
		}
		if len(seal.operationMembers) != len(seal.description.Members) {
			return auditErrorAt(ErrDeclaration, "operation.members")
		}
		for index, member := range seal.operationMembers {
			if member.key != seal.description.Members[index] {
				return auditErrorAt(ErrDeclaration, "operation.members")
			}
			if _, found := declarations[member.declaration]; !found {
				return auditErrorAt(ErrDeclaration, "operation.members")
			}
		}
	}
	return nil
}

func collectCodecDescriptions(declarations []DeclarationDescription, control ControlPolicyDescription) ([]CodecDescription, error) {
	byName := make(map[string]CodecDescription)
	add := func(codec CodecDescription) error {
		if codec.Name == "" {
			return nil
		}
		if prior, exists := byName[codec.Name]; exists {
			if !sameCodecDescription(prior, codec) {
				return auditErrorAt(ErrDeclaration, "codecs")
			}
			return nil
		}
		byName[codec.Name] = codec
		return nil
	}
	for _, declaration := range declarations {
		for _, field := range declaration.Fields {
			if err := add(field.Codec); err != nil {
				return nil, err
			}
		}
	}
	if err := add(control.Matter.Codec); err != nil {
		return nil, err
	}
	codecs := make([]CodecDescription, 0, len(byName))
	for _, codec := range byName {
		codec.ReadVersions = slices.Clone(codec.ReadVersions)
		codecs = append(codecs, codec)
	}
	slices.SortFunc(codecs, func(left, right CodecDescription) int { return strings.Compare(left.Name, right.Name) })
	return codecs, nil
}

func sameCodecDescription(left, right CodecDescription) bool {
	return left.Name == right.Name && left.WriteVersion == right.WriteVersion && left.Fingerprint == right.Fingerprint && slices.Equal(left.ReadVersions, right.ReadVersions)
}

func collectContextDescriptions(declarations []DeclarationDescription, control ControlPolicyDescription) []ContextPolicyDescription {
	byWire := make(map[string]ContextPolicyDescription)
	for _, declaration := range declarations {
		var output bytes.Buffer
		writeContextDescription(&output, declaration.Context)
		byWire[output.String()] = cloneContextPolicyDescription(declaration.Context)
	}
	if len(control.Context.Facts) != 0 {
		var output bytes.Buffer
		writeContextDescription(&output, control.Context)
		byWire[output.String()] = cloneContextPolicyDescription(control.Context)
	}
	keys := make([]string, 0, len(byWire))
	for key := range byWire {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	contexts := make([]ContextPolicyDescription, len(keys))
	for index, key := range keys {
		contexts[index] = byWire[key]
	}
	return contexts
}

func controlPolicyFingerprint(description ControlPolicyDescription) PolicyFingerprint {
	canonical := cloneControlPolicyDescription(description)
	canonical.Semantics.Fingerprint = PolicyFingerprint{}
	digest := sha256.New()
	writeFrame(digest, []byte("frostgrove.audit/control-policy/v1"))
	writeControlDescription(digest, canonical)
	var output PolicyFingerprint
	copy(output[:], digest.Sum(nil))
	return output
}

func cloneControlPolicyDescription(input ControlPolicyDescription) ControlPolicyDescription {
	output := input
	output.Semantics = clonePolicySemanticsDescription(input.Semantics)
	output.Context = cloneContextPolicyDescription(input.Context)
	output.Matter.Codec.ReadVersions = slices.Clone(input.Matter.Codec.ReadVersions)
	output.Actions = slices.Clone(input.Actions)
	output.Reasons = make([]ControlReasonDescription, len(input.Reasons))
	for index, reason := range input.Reasons {
		output.Reasons[index] = reason
		output.Reasons[index].Codes = slices.Clone(reason.Codes)
	}
	output.Outcomes = make([]ControlOutcomeDescription, len(input.Outcomes))
	for index, outcome := range input.Outcomes {
		output.Outcomes[index] = outcome
		output.Outcomes[index].Codes = slices.Clone(outcome.Codes)
	}
	return output
}

func writeControlDescription(output interface{ Write([]byte) (int, error) }, value ControlPolicyDescription) {
	writeFrame(output, []byte(value.Resource))
	writePolicySemanticsDescription(output, value.Semantics)
	writeFrame(output, []byte(value.Purpose))
	writeFrame(output, []byte(value.Retention))
	writeUint32(output, uint32(value.Consequence))
	writeContextDescription(output, value.Context)
	writeCodecDescription(output, value.Matter.Codec)
	writeUint32(output, uint32(value.Matter.Classification))
	writeUint32(output, uint32(value.Matter.Mode))
	writeUint32(output, uint32(len(value.Actions)))
	for _, action := range value.Actions {
		writeFrame(output, []byte(action))
	}
	writeUint32(output, uint32(len(value.Reasons)))
	for _, reason := range value.Reasons {
		writeFrame(output, []byte(reason.Action))
		writeUint32(output, uint32(len(reason.Codes)))
		for _, code := range reason.Codes {
			writeFrame(output, []byte(code))
		}
	}
	writeUint32(output, uint32(len(value.Outcomes)))
	for _, outcome := range value.Outcomes {
		writeFrame(output, []byte(outcome.Action))
		writeUint32(output, uint32(len(outcome.Codes)))
		for _, code := range outcome.Codes {
			writeFrame(output, []byte(code))
		}
	}
}

func (c *Catalog) Ref() CatalogRef {
	if c == nil || c.value == nil {
		return CatalogRef{}
	}
	return c.value.manifest.Ref()
}

func (c *Catalog) Manifest() Manifest {
	if c == nil || c.value == nil {
		return Manifest{}
	}
	return cloneManifest(c.value.manifest)
}

func (c *Catalog) accepts(declaration Declaration) bool {
	if c == nil || c.value == nil || nilByReflection(declaration) {
		return false
	}
	compiled, ok := declaration.(compiledDeclaration)
	if !ok || compiled.sealedDeclaration() == nil {
		return false
	}
	fingerprint, ok := c.value.declarations[declaration]
	return ok && samePolicyFingerprint(fingerprint, compiled.sealedDeclaration().description.Semantics.Fingerprint)
}

func Retain(catalog *Catalog) HistoricalCatalog {
	if catalog == nil || catalog.value == nil {
		panic(auditErrorAt(ErrInvalid, "catalog"))
	}
	return HistoricalCatalog{value: &historicalCatalog{manifest: catalog.Manifest()}}
}

func Lineage(active *Catalog, retained ...HistoricalCatalog) (*CatalogSet, error) {
	if active == nil || active.value == nil {
		return nil, auditErrorAt(ErrInvalid, "catalog")
	}
	if len(retained)+1 > MaxCatalogs {
		return nil, auditTooLarge("catalogs", MaxCatalogs)
	}
	manifests := make([]Manifest, 0, len(retained)+1)
	for _, historical := range retained {
		if historical.value == nil || !validManifest(historical.value.manifest) {
			return nil, auditErrorAt(ErrDeclaration, "catalogs")
		}
		manifests = append(manifests, cloneManifest(historical.value.manifest))
	}
	manifests = append(manifests, active.Manifest())
	slices.SortFunc(manifests, func(left, right Manifest) int {
		if left.Ref().Generation < right.Ref().Generation {
			return -1
		}
		if left.Ref().Generation > right.Ref().Generation {
			return 1
		}
		return 0
	})
	digest, err := CatalogSetDigestOf(manifests)
	if err != nil {
		return nil, err
	}
	if manifests[len(manifests)-1].Ref() != active.Ref() {
		return nil, auditErrorAt(ErrDeclaration, "catalogs.active")
	}
	if err := validateLineageCompatibility(manifests); err != nil {
		return nil, err
	}
	byRef := make(map[CatalogRef]int, len(manifests))
	for index, manifest := range manifests {
		byRef[manifest.Ref()] = index
	}
	declarations := make(map[Declaration]PolicyFingerprint, len(active.value.declarations))
	for declaration, fingerprint := range active.value.declarations {
		declarations[declaration] = fingerprint
	}
	return &CatalogSet{value: &catalogSet{
		active: active.Ref(), digest: digest, manifests: manifests, byRef: byRef, declarations: declarations,
	}}, nil
}

func CatalogSetDigestOf(manifests []Manifest) (CatalogSetDigest, error) {
	if len(manifests) == 0 || len(manifests) > MaxCatalogs {
		return CatalogSetDigest{}, auditErrorAt(ErrDeclaration, "catalogs")
	}
	total := 0
	var previous CatalogRef
	for index, manifest := range manifests {
		if !validManifest(manifest) {
			return CatalogSetDigest{}, auditErrorAt(ErrDeclaration, "catalogs")
		}
		ref := manifest.Ref()
		if index == 0 {
			if ref.Generation != 1 || manifest.Previous() != (CatalogRef{}) {
				return CatalogSetDigest{}, auditErrorAt(ErrDeclaration, "catalogs")
			}
		} else if ref.ID != previous.ID || ref.Generation != previous.Generation+1 || manifest.Previous() != previous {
			return CatalogSetDigest{}, auditErrorAt(ErrDeclaration, "catalogs")
		}
		total += len(manifest.value.canonical)
		if total > MaxCatalogSetBytes {
			return CatalogSetDigest{}, auditTooLarge("catalogs", MaxCatalogSetBytes)
		}
		previous = ref
	}
	if err := validateLineageCompatibility(manifests); err != nil {
		return CatalogSetDigest{}, err
	}
	digest := sha256.New()
	writeFrame(digest, []byte("frostgrove.audit/catalog-set/v1"))
	writeUint32(digest, uint32(len(manifests)))
	for _, manifest := range manifests {
		writeFrame(digest, manifest.value.canonical)
	}
	var output CatalogSetDigest
	copy(output[:], digest.Sum(nil))
	if output == (CatalogSetDigest{}) {
		return CatalogSetDigest{}, fmt.Errorf("%w: catalog set digest is zero", ErrDeclaration)
	}
	return output, nil
}

func validateLineageCompatibility(manifests []Manifest) error {
	fingerprints := make(map[PolicyFingerprint][]byte)
	policyVersions := make(map[string]PolicyFingerprint)
	codecVersions := make(map[string]CodecSemanticFingerprint)
	retentionRules := make(map[RetentionClass]RetentionRuleView)
	for _, manifest := range manifests {
		for _, rule := range manifest.value.view.Retention {
			if prior, exists := retentionRules[rule.Class]; exists && prior != rule {
				return auditErrorAt(ErrDeclaration, "catalogs.retention")
			}
			retentionRules[rule.Class] = rule
		}
		for _, codec := range manifest.value.view.Codecs {
			for _, version := range codec.ReadVersions {
				key := codec.Name + "\x00" + fmt.Sprint(version)
				if prior, exists := codecVersions[key]; exists && prior != codec.Fingerprint {
					return auditErrorAt(ErrDeclaration, "catalogs.codecs")
				}
				codecVersions[key] = codec.Fingerprint
			}
		}
		for _, declaration := range manifest.value.view.Declarations {
			var wire bytes.Buffer
			writeDeclarationDescription(&wire, declaration)
			fingerprint := declaration.Semantics.Fingerprint
			if prior, exists := fingerprints[fingerprint]; exists && !bytes.Equal(prior, wire.Bytes()) {
				return auditErrorAt(ErrDeclaration, "catalogs.semantics")
			}
			fingerprints[fingerprint] = bytes.Clone(wire.Bytes())
			versionKey := stableDeclarationKey(declaration) + "\x00" + fmt.Sprint(declaration.Semantics.Version)
			if prior, exists := policyVersions[versionKey]; exists && prior != fingerprint {
				return auditErrorAt(ErrDeclaration, "catalogs.policy_version")
			}
			policyVersions[versionKey] = fingerprint
		}
		control := manifest.value.view.Control
		if control.Resource != "" {
			key := "control:" + string(control.Resource) + "\x00" + fmt.Sprint(control.Semantics.Version)
			if prior, exists := policyVersions[key]; exists && prior != control.Semantics.Fingerprint {
				return auditErrorAt(ErrDeclaration, "catalogs.policy_version")
			}
			policyVersions[key] = control.Semantics.Fingerprint
		}
	}
	for index := 1; index < len(manifests); index++ {
		if err := rejectPrivacyWeakening(manifests[index-1].value.view, manifests[index].value.view); err != nil {
			return err
		}
		if err := validateHistoricalFields(manifests[:index], manifests[index]); err != nil {
			return err
		}
	}
	return nil
}

func validateHistoricalFields(ancestors []Manifest, current Manifest) error {
	for _, declaration := range current.value.view.Declarations {
		for _, field := range declaration.Fields {
			if !field.HistoricalOnly {
				continue
			}
			found := false
			for _, ancestor := range ancestors {
				for _, prior := range ancestor.value.view.Declarations {
					if stableDeclarationKey(prior) != stableDeclarationKey(declaration) {
						continue
					}
					for _, priorField := range prior.Fields {
						if priorField.Name == field.Name && priorField.Reconstruct && !priorField.HistoricalOnly &&
							priorField.Codec.Name == field.Codec.Name && slices.Contains(field.Codec.ReadVersions, priorField.Codec.WriteVersion) {
							found = true
							break
						}
					}
				}
				if found {
					break
				}
			}
			if !found {
				return auditErrorAt(ErrDeclaration, "catalogs.historical_field")
			}
		}
	}
	return nil
}

func rejectPrivacyWeakening(previous, current ManifestView) error {
	prior := make(map[string]DeclarationDescription, len(previous.Declarations))
	for _, declaration := range previous.Declarations {
		prior[stableDeclarationKey(declaration)] = declaration
	}
	for _, declaration := range current.Declarations {
		old, exists := prior[stableDeclarationKey(declaration)]
		if !exists {
			continue
		}
		if privacyWeakened(old.Subject, declaration.Subject) || old.TargetPresent && (!declaration.TargetPresent || privacyWeakened(old.Target, declaration.Target)) {
			return auditErrorAt(ErrDeclaration, "catalogs.privacy")
		}
		oldFields := make(map[FieldName]FieldDescription, len(old.Fields))
		for _, field := range old.Fields {
			oldFields[field.Name] = field
		}
		for _, field := range declaration.Fields {
			if priorField, ok := oldFields[field.Name]; ok && fieldPrivacyWeakened(priorField, field) {
				return auditErrorAt(ErrDeclaration, "catalogs.privacy")
			}
		}
		oldFacts := make(map[ContextFactKind]ContextFactDescription, len(old.Context.Facts))
		for _, fact := range old.Context.Facts {
			oldFacts[fact.Kind] = fact
		}
		for _, fact := range declaration.Context.Facts {
			if priorFact, ok := oldFacts[fact.Kind]; ok && contextFactWeakened(priorFact, fact) {
				return auditErrorAt(ErrDeclaration, "catalogs.privacy")
			}
			delete(oldFacts, fact.Kind)
		}
		for _, removed := range oldFacts {
			if removed.Presence == ContextRequired {
				return auditErrorAt(ErrDeclaration, "catalogs.privacy")
			}
		}
	}
	if contextDescriptionWeakened(previous.Control.Context, current.Control.Context) {
		return auditErrorAt(ErrDeclaration, "catalogs.privacy")
	}
	return nil
}

func stableDeclarationKey(value DeclarationDescription) string {
	if value.Kind == OperationDeclaration {
		return operationDeclarationKey(value.Operation)
	}
	return fmt.Sprintf("%d:%s:%s", value.Kind, value.Resource, value.Action)
}

func privacyWeakened(previous, current SubjectDescription) bool {
	if previous == (SubjectDescription{}) {
		return false
	}
	return current == (SubjectDescription{}) || current.Classification < previous.Classification || weakerMode(previous.Mode, current.Mode)
}

func fieldPrivacyWeakened(previous, current FieldDescription) bool {
	return current.Classification < previous.Classification || weakerMode(previous.Mode, current.Mode) ||
		previous.Reconstruct && !current.Reconstruct || !previous.QueryIndex && current.QueryIndex
}

func contextDescriptionWeakened(previous, current ContextPolicyDescription) bool {
	prior := make(map[ContextFactKind]ContextFactDescription, len(previous.Facts))
	for _, fact := range previous.Facts {
		prior[fact.Kind] = fact
	}
	for _, fact := range current.Facts {
		if old, ok := prior[fact.Kind]; ok && contextFactWeakened(old, fact) {
			return true
		}
		delete(prior, fact.Kind)
	}
	for _, removed := range prior {
		if removed.Presence == ContextRequired {
			return true
		}
	}
	return false
}

func contextFactWeakened(previous, current ContextFactDescription) bool {
	if previous.Presence == ContextRequired && current.Presence != ContextRequired ||
		current.Classification < previous.Classification || weakerMode(previous.Mode, current.Mode) {
		return true
	}
	for _, provenance := range current.Allowed {
		if !slices.Contains(previous.Allowed, provenance) {
			return true
		}
	}
	return false
}

func weakerMode(previous, current StorageMode) bool {
	if previous == current {
		return false
	}
	switch previous {
	case AsPlaintext:
		return false
	case AsIndexedProtected:
		return current == AsPlaintext
	case AsProtected:
		return current != AsRedacted
	case AsToken:
		return current != AsRedacted
	case AsRedacted:
		return true
	default:
		return true
	}
}

func (s *CatalogSet) Active() CatalogRef {
	if s == nil || s.value == nil {
		return CatalogRef{}
	}
	return s.value.active
}

func (s *CatalogSet) Digest() CatalogSetDigest {
	if s == nil || s.value == nil {
		return CatalogSetDigest{}
	}
	return s.value.digest
}

func (s *CatalogSet) Manifests() []Manifest {
	if s == nil || s.value == nil {
		return nil
	}
	output := make([]Manifest, len(s.value.manifests))
	for index, manifest := range s.value.manifests {
		output[index] = cloneManifest(manifest)
	}
	return output
}

func (s *CatalogSet) Manifest(reference CatalogRef) (Manifest, bool) {
	if s == nil || s.value == nil {
		return Manifest{}, false
	}
	index, ok := s.value.byRef[reference]
	if !ok {
		return Manifest{}, false
	}
	return cloneManifest(s.value.manifests[index]), true
}

func (s *CatalogSet) accepts(declaration Declaration) bool {
	if s == nil || s.value == nil || nilByReflection(declaration) {
		return false
	}
	compiled, ok := declaration.(compiledDeclaration)
	if !ok || compiled.sealedDeclaration() == nil {
		return false
	}
	fingerprint, ok := s.value.declarations[declaration]
	return ok && samePolicyFingerprint(fingerprint, compiled.sealedDeclaration().description.Semantics.Fingerprint)
}
