package audit

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"time"

	"github.com/frostgrove/vv/crud"
)

type DenialLimiter interface{}
type CursorKeys interface{}

type HistoryProfile uint8

const PublicOnePageDevelopmentAlpha HistoryProfile = 1

func (p HistoryProfile) String() string {
	if p == PublicOnePageDevelopmentAlpha {
		return "public_one_page_development_alpha"
	}
	return "unknown"
}

type HistoryConfig struct {
	Profile        HistoryProfile
	Recorder       *Recorder
	Log            Log
	Exact          ExactLog
	Attempts       AttemptLog
	Access         AccessAuthority
	Denials        DenialLimiter
	Cursors        CursorKeys
	CursorLifetime time.Duration
	Revealer       Revealer
	Verifier       Verifier
}

type historyRuntime struct {
	recorder *Recorder
	log      Log
	exact    ExactLog
	access   AccessAuthority
	verifier Verifier
}

func NewHistory(config HistoryConfig) (*History, error) {
	if config.Profile != PublicOnePageDevelopmentAlpha || config.Recorder == nil || config.Recorder.value == nil || nilByReflection(config.Log) || nilByReflection(config.Access) {
		return nil, auditErrorAt(ErrInvalid, "history.config")
	}
	if !nilByReflection(config.Attempts) || !nilByReflection(config.Denials) || !nilByReflection(config.Cursors) || config.CursorLifetime != 0 || !nilByReflection(config.Revealer) {
		return nil, auditErrorAt(ErrUnsupported, "history.config")
	}
	writer := config.Recorder.value.writer
	if config.Log.BackingID() != writer.BackingID() || config.Log.LogID() != writer.LogID() || !SameBacking(config.Log.Backing(), writer.Backing()) {
		return nil, auditErrorAt(ErrWrongStore, "history.log")
	}
	state := config.Log.Catalogs()
	if !state.HasActive() || state.Active() != config.Recorder.value.active || state.SetDigest() != config.Recorder.value.set {
		return nil, auditErrorAt(ErrWrongCatalog, "history.log")
	}
	limits := config.Log.Limits().View()
	if limits.PageRevisions == 0 || limits.PageBytes == 0 {
		return nil, auditErrorAt(ErrWrongStore, "history.limits")
	}
	if err := validateHistoryVerifier(config.Recorder.value.catalogs, config.Verifier); err != nil {
		return nil, err
	}
	if err := validateHistoryExact(config.Recorder, config.Exact); err != nil {
		return nil, err
	}
	runtime := &historyRuntime{recorder: config.Recorder, log: config.Log, exact: config.Exact, access: config.Access, verifier: config.Verifier}
	return &History{value: historyValue{runtime: runtime}}, nil
}

func validateHistoryVerifier(catalogs *CatalogSet, verifier Verifier) error {
	required := make(map[SignatureDescription]struct{})
	for _, manifest := range catalogs.Manifests() {
		if !validManifest(manifest) {
			return auditErrorAt(ErrWrongCatalog, "history.catalogs")
		}
		policy := manifest.View().Integrity
		if policy.RequiresSignature {
			required[policy.Signature] = struct{}{}
		}
	}
	if len(required) == 0 {
		if !nilByReflection(verifier) {
			return auditErrorAt(ErrDeclaration, "history.verifier")
		}
		return nil
	}
	if nilByReflection(verifier) {
		return auditErrorAt(ErrDeclaration, "history.verifier")
	}
	descriptions := verifier.Descriptions()
	if len(descriptions) != len(required) || len(descriptions) > MaxCatalogs {
		return auditErrorAt(ErrDeclaration, "history.verifier")
	}
	seen := make(map[SignatureDescription]struct{}, len(descriptions))
	for _, description := range descriptions {
		if err := validateProviderDescription(description.Algorithm, description.Profile, description.KeyID); err != nil {
			return auditErrorAt(ErrDeclaration, "history.verifier")
		}
		if _, duplicate := seen[description]; duplicate {
			return auditErrorAt(ErrDeclaration, "history.verifier")
		}
		if _, admitted := required[description]; !admitted {
			return auditErrorAt(ErrDeclaration, "history.verifier")
		}
		seen[description] = struct{}{}
	}
	return nil
}

func (h *History) Profile() HistoryProfile {
	if h == nil || h.value.runtime == nil {
		return 0
	}
	return PublicOnePageDevelopmentAlpha
}

type historyTarget struct {
	view          AccessTargetView
	declarations  []Declaration
	scopeDeclared bool
	scopeMode     StorageMode
}

func (h *History) Subject(ctx context.Context, subject SubjectRef, query Query) (Page, error) {
	if subject.value.declaration == nil {
		return Page{}, auditErrorAt(ErrInvalid, "history.subject")
	}
	description := subject.value.declaration.(compiledDeclaration).sealedDeclaration().description
	target := targetForDescription(AccessSubjectTarget, description, subject.value.declaration)
	target.view.Subject = subject.value.view.Subject
	target.view.SubjectMode = subject.value.view.Mode
	target.view.SubjectClass = subject.value.view.Classification
	if target.view.SubjectMode != AsPlaintext || target.view.SubjectClass != Public {
		return Page{}, auditErrorAt(ErrUnsupported, "history.subject")
	}
	return h.search(ctx, target, query)
}

func (h *History) Target(ctx context.Context, target EventTargetRef, query Query) (Page, error) {
	if target.value.declaration == nil {
		return Page{}, auditErrorAt(ErrInvalid, "history.target")
	}
	description := target.value.declaration.(compiledDeclaration).sealedDeclaration().description
	bound := targetForDescription(AccessEventTarget, description, target.value.declaration)
	bound.view.Target = target.value.view.Target
	bound.view.TargetMode = target.value.view.Mode
	bound.view.TargetClass = target.value.view.Classification
	if bound.view.TargetMode != AsPlaintext || bound.view.TargetClass != Public {
		return Page{}, auditErrorAt(ErrUnsupported, "history.target")
	}
	return h.search(ctx, bound, query)
}

func (h *ResourceHistory[M, ID]) Subject(ctx context.Context, id ID, query Query) (Page, error) {
	if h == nil || h.history == nil || h.policy == nil {
		return Page{}, auditErrorAt(ErrInvalid, "history.resource")
	}
	subject, err := h.policy.SubjectRef(id)
	if err != nil {
		return Page{}, err
	}
	return h.history.Subject(ctx, subject, query)
}

func (h *ResourceHistory[M, ID]) Revisions(ctx context.Context, query Query) (Page, error) {
	if h == nil || h.history == nil || h.policy == nil || h.policy.value == nil {
		return Page{}, auditErrorAt(ErrInvalid, "history.resource")
	}
	description := h.policy.Description()
	return h.history.search(ctx, targetForDescription(AccessResourceTarget, description, h.policy), query)
}

type eventTargetRef struct {
	declaration Declaration
	view        EventTargetRefView
}

type EventTargetRef struct {
	value eventTargetRef
}

type EventTargetRefView struct {
	Resource       Resource
	Action         Action
	Target         Reference
	Mode           StorageMode
	Classification Classification
}

func (r EventTargetRef) View() EventTargetRefView { return r.value.view }

func (e *EventType[E]) TargetRef(reference Reference) (EventTargetRef, error) {
	if e == nil || e.value == nil || !e.value.target.configured || !e.value.target.present || !validOpaqueReference(string(reference), MaxReferenceBytes) {
		return EventTargetRef{}, auditErrorAt(ErrInvalid, "event.target")
	}
	if e.value.target.mode != AsPlaintext || e.value.target.classification != Public {
		return EventTargetRef{}, auditErrorAt(ErrUnsupported, "event.target")
	}
	description := e.Description()
	return EventTargetRef{value: eventTargetRef{declaration: e, view: EventTargetRefView{
		Resource: description.Resource, Action: description.Action, Target: reference,
		Mode: e.value.target.mode, Classification: e.value.target.classification,
	}}}, nil
}

func (h *EventHistory[E]) Target(ctx context.Context, reference Reference, query Query) (Page, error) {
	if h == nil || h.history == nil || h.event == nil {
		return Page{}, auditErrorAt(ErrInvalid, "history.event")
	}
	target, err := h.event.TargetRef(reference)
	if err != nil {
		return Page{}, err
	}
	return h.history.Target(ctx, target, query)
}

func (h *EventHistory[E]) Events(ctx context.Context, query Query) (Page, error) {
	if h == nil || h.history == nil || h.event == nil || h.event.value == nil {
		return Page{}, auditErrorAt(ErrInvalid, "history.event")
	}
	description := h.event.Description()
	return h.history.search(ctx, targetForDescription(AccessEventTypeTarget, description, h.event), query)
}

func (h *OperationHistory) Operation(ctx context.Context, operation OperationID, query Query) (Page, error) {
	if operation == (OperationID{}) {
		return Page{}, auditErrorAt(ErrInvalid, "history.operation")
	}
	target, err := h.target(AccessOperationTarget)
	if err != nil {
		return Page{}, err
	}
	target.view.Operation = operation
	return h.history.search(ctx, target, query)
}

func (h *OperationHistory) Revisions(ctx context.Context, query Query) (Page, error) {
	target, err := h.target(AccessOperationTypeTarget)
	if err != nil {
		return Page{}, err
	}
	return h.history.search(ctx, target, query)
}

func (h *OperationHistory) target(kind AccessTargetKind) (historyTarget, error) {
	if h == nil || h.history == nil || h.operation == nil || h.operation.value == nil {
		return historyTarget{}, auditErrorAt(ErrInvalid, "history.operation")
	}
	target := historyTarget{view: AccessTargetView{Kind: kind, OperationName: h.operation.Description().Operation}, declarations: []Declaration{h.operation}}
	for _, member := range h.operation.value.members {
		description := member.declaration.(compiledDeclaration).sealedDeclaration().description
		mergeDescriptionTarget(&target, description, member.declaration)
	}
	canonicalizeHistoryTarget(&target)
	return target, nil
}

func (h *History) search(ctx context.Context, target historyTarget, input Query) (Page, error) {
	if h == nil || h.value.runtime == nil || ctx == nil || len(target.declarations) == 0 {
		return Page{}, auditErrorAt(ErrInvalid, "history")
	}
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	if err := h.validateRuntime(ctx); err != nil {
		return Page{}, err
	}
	for _, declaration := range target.declarations {
		if !h.value.runtime.recorder.value.catalogContains(declaration) {
			return Page{}, auditErrorAt(ErrWrongCatalog, "history.declaration")
		}
	}
	class := queryClassForTarget(target.view.Kind)
	query, err := normalizeBasicQuery(input, class, target.view.Actions, target.view.Fields, target.view.Context)
	if err != nil {
		return Page{}, err
	}
	requester, err := h.resolveRequester(ctx)
	if err != nil {
		return Page{}, err
	}
	if err := resolveQueryScope(&query, requester, target); err != nil {
		return Page{}, err
	}
	catalogs := catalogRefs(h.value.runtime.recorder.value.catalogs)
	request := newAccessRequest(AccessRequestView{
		Intent: HistoryDisclosureAccess, Requester: requester.View(), Target: target.view,
		Query: query, Catalogs: catalogs,
	})
	runtimeContext := valueFreeContext{Context: ctx}
	decision, err := h.value.runtime.access.AuthorizeAudit(runtimeContext, request)
	if contextErr := ctx.Err(); contextErr != nil {
		return Page{}, contextErr
	}
	if err != nil {
		return Page{}, auditError(ErrDenied, err)
	}
	if decision.value.origin != request.value.origin || decision.value.view.Verdict != AccessAllowed {
		return Page{}, auditErrorAt(ErrDenied, "history.access")
	}
	grant := decision.View().Grant
	if err := validateBasicGrant(request.value.view, grant); err != nil {
		if errors.Is(err, ErrUnsupported) {
			return Page{}, err
		}
		return Page{}, auditErrorAt(ErrDenied, "history.access")
	}
	for _, classification := range grant.Classifications {
		if classification != Public {
			return Page{}, auditErrorAt(ErrUnsupported, "history.access_evidence")
		}
	}
	now := canonicalTime(h.value.runtime.recorder.value.clock.Now())
	limits := h.value.runtime.log.Limits().View()
	if !grant.ExpiresAt.After(now) || grant.MaxRevisions > limits.PageRevisions || grant.MaxBytes > limits.PageBytes {
		return Page{}, auditErrorAt(ErrDenied, "history.access")
	}
	if err := h.validateRuntime(ctx); err != nil {
		return Page{}, err
	}
	storeQuery := newStoreQuery(StoreQueryView{
		Log: h.value.runtime.log.LogID(), Catalogs: grant.Catalogs, Class: query.Class,
		Resources: grant.Resources, OperationName: target.view.OperationName, Operation: target.view.Operation,
		Coordinates: targetCoordinates(target, query.Scope), Actions: grant.Actions,
		Classifications: grant.Classifications, Fields: grantedFields(query.Fields, grant.Fields),
		Context: grantedContext(query.Context, grant.Context), Direction: grant.Direction,
		Limit: grant.MaxRevisions, MaxBytes: grant.MaxBytes,
	})
	stored, err := h.value.runtime.log.Search(runtimeContext, storeQuery)
	if contextErr := ctx.Err(); contextErr != nil {
		return Page{}, contextErr
	}
	if err != nil {
		return Page{}, mapStoreError(err)
	}
	if err := h.validateRuntime(ctx); err != nil {
		return Page{}, err
	}
	if err := validateStoredPage(storeQuery, stored); err != nil {
		return Page{}, err
	}
	if err := verifyHistoryEvidence(runtimeContext, h.value.runtime.recorder.value.catalogs, h.value.runtime.verifier, stored); err != nil {
		return Page{}, err
	}
	return pageFromStored(stored, grant, storeQuery.View()), nil
}

func verifyHistoryEvidence(ctx context.Context, catalogs *CatalogSet, verifier Verifier, stored StoredPage) error {
	for _, revision := range stored.Revisions() {
		if err := verifyHistoryRevisionEvidence(ctx, catalogs, verifier, revision); err != nil {
			return err
		}
	}
	return nil
}

func verifyHistoryRevisionEvidence(ctx context.Context, catalogs *CatalogSet, verifier Verifier, revision StoredRevision) error {
	header := revision.View().Revision.Header
	manifest, found := catalogs.Manifest(header.Catalog)
	if !found || !validManifest(manifest) {
		return auditErrorAt(ErrUnknownCatalog, "history.catalog")
	}
	policy := manifest.View().Integrity
	if !policy.RequiresSignature {
		if header.Seal != (Seal{}) {
			return auditErrorAt(ErrIntegrity, "history.seal")
		}
		return nil
	}
	if nilByReflection(verifier) || header.Seal == (Seal{}) || (SignatureDescription{
		Algorithm: header.Seal.Algorithm(), Profile: header.Seal.Profile(), KeyID: header.Seal.KeyID(),
	}) != policy.Signature {
		return auditErrorAt(ErrIntegrity, "history.seal")
	}
	if err := verifier.Verify(ctx, header.Integrity, header.Seal); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return auditError(ErrIntegrity, err)
	}
	return nil
}

func grantedFields(requested FieldProjectionView, fields []FieldName) FieldProjectionView {
	if len(fields) == 0 {
		return FieldProjectionView{Kind: ProjectionNone}
	}
	if requested.Kind == ProjectionAll && len(fields) == len(requested.Fields) {
		return FieldProjectionView{Kind: ProjectionAll, Fields: slices.Clone(fields)}
	}
	return FieldProjectionView{Kind: ProjectionOnly, Fields: slices.Clone(fields)}
}

func grantedContext(requested ContextProjectionView, facts []ContextFactKind) ContextProjectionView {
	if len(facts) == 0 {
		return ContextProjectionView{Kind: ProjectionNone}
	}
	if requested.Kind == ProjectionAll && len(facts) == len(requested.Facts) {
		return ContextProjectionView{Kind: ProjectionAll, Facts: slices.Clone(facts)}
	}
	return ContextProjectionView{Kind: ProjectionOnly, Facts: slices.Clone(facts)}
}

func (h *History) validateRuntime(ctx context.Context) error {
	runtime := h.value.runtime
	writer := runtime.recorder.value.writer
	if frame, _ := ctx.Value(groupContextKey{}).(*groupFrame); frame != nil {
		return auditErrorAt(ErrTransaction, "history.transaction")
	}
	if _, found, err := crud.SourceBoundExecutorFor(ctx, writer.TransactionSource()); err != nil || found {
		return auditErrorAt(ErrTransaction, "history.transaction")
	}
	if runtime.log.BackingID() != writer.BackingID() || runtime.log.LogID() != writer.LogID() || !SameBacking(runtime.log.Backing(), writer.Backing()) {
		return auditErrorAt(ErrWrongStore, "history.log")
	}
	written := writer.Catalogs()
	stored := runtime.log.Catalogs()
	wantActive := runtime.recorder.value.active
	wantSet := runtime.recorder.value.set
	if !written.HasActive() || !stored.HasActive() || written.Active() != wantActive || stored.Active() != wantActive || written.SetDigest() != wantSet || stored.SetDigest() != wantSet {
		return auditErrorAt(ErrWrongCatalog, "history.log")
	}
	return nil
}

func (h *History) resolveRequester(ctx context.Context) (Context, error) {
	resolved, err := h.value.runtime.recorder.value.context.ResolveAuditContext(ctx)
	if contextErr := ctx.Err(); contextErr != nil {
		return Context{}, contextErr
	}
	if err != nil {
		return Context{}, auditError(ErrInvalid, err)
	}
	return validateAndCopyContext(resolved)
}

func resolveQueryScope(query *NormalizedQueryView, requester Context, target historyTarget) error {
	if query.Scope.Kind == 0 {
		if target.scopeDeclared {
			return auditErrorAt(ErrInvalid, "history.scope")
		}
		return nil
	}
	if !target.scopeDeclared || target.scopeMode != AsPlaintext {
		return auditErrorAt(ErrUnsupported, "history.scope")
	}
	if query.Scope.Kind == ScopeCurrent {
		scope, present := requester.Scope.Get()
		if !present {
			return auditErrorAt(ErrInvalid, "history.scope")
		}
		query.Scope = ScopeSelectorView{Kind: ScopeExact, Reference: scope}
	}
	return nil
}

func targetCoordinates(target historyTarget, scope ScopeSelectorView) []QueryCoordinateView {
	coordinates := make([]QueryCoordinateView, 0, 2)
	if scope.Kind == ScopeExact {
		encoded, _ := contextValueBytes(scope.Reference)
		coordinates = append(coordinates, exactPlaintextCoordinate(QueryScope, encoded))
	}
	if target.view.Subject != "" {
		coordinates = append(coordinates, exactPlaintextCoordinate(QuerySubject, []byte(target.view.Subject)))
	}
	if target.view.Target != "" {
		coordinates = append(coordinates, exactPlaintextCoordinate(QueryTarget, []byte(target.view.Target)))
	}
	return coordinates
}

func exactPlaintextCoordinate(kind QueryCoordinateKind, value []byte) QueryCoordinateView {
	return QueryCoordinateView{Kind: kind, Match: QueryCoordinateExact, Mode: AsPlaintext, Alternatives: []QueryCoordinateAlternativeView{{Plaintext: bytes.Clone(value)}}}
}

func queryClassForTarget(kind AccessTargetKind) QueryClass {
	switch kind {
	case AccessSubjectTarget:
		return SubjectHistoryQuery
	case AccessResourceTarget:
		return ResourceHistoryQuery
	case AccessEventTarget:
		return EventTargetHistoryQuery
	case AccessEventTypeTarget:
		return EventTypeHistoryQuery
	case AccessOperationTarget:
		return OperationInstanceHistoryQuery
	case AccessOperationTypeTarget:
		return OperationTypeHistoryQuery
	default:
		return 0
	}
}

func targetForDescription(kind AccessTargetKind, description DeclarationDescription, declaration Declaration) historyTarget {
	target := historyTarget{view: AccessTargetView{Kind: kind}, declarations: []Declaration{declaration}}
	mergeDescriptionTarget(&target, description, declaration)
	canonicalizeHistoryTarget(&target)
	return target
}

func mergeDescriptionTarget(target *historyTarget, description DeclarationDescription, declaration Declaration) {
	if !slices.Contains(target.declarations, declaration) {
		target.declarations = append(target.declarations, declaration)
	}
	if description.Resource != "" {
		target.view.Resources = append(target.view.Resources, description.Resource)
		target.view.Resource = description.Resource
	}
	if description.Action != "" {
		target.view.Actions = append(target.view.Actions, description.Action)
		target.view.Action = description.Action
	}
	for _, action := range description.Actions {
		target.view.Actions = append(target.view.Actions, Action(action))
	}
	if description.Subject.Classification.Valid() {
		target.view.Classifications = append(target.view.Classifications, description.Subject.Classification)
	}
	if description.TargetPresent && description.Target.Classification.Valid() {
		target.view.Classifications = append(target.view.Classifications, description.Target.Classification)
	}
	for _, field := range description.Fields {
		target.view.Fields = append(target.view.Fields, field.Name)
		target.view.Classifications = append(target.view.Classifications, field.Classification)
	}
	for _, fact := range description.Context.Facts {
		target.view.Context = append(target.view.Context, fact.Kind)
		target.view.Classifications = append(target.view.Classifications, fact.Classification)
		if fact.Kind == ScopeContext {
			target.scopeDeclared = true
			target.scopeMode = fact.Mode
		}
	}
}

func canonicalizeHistoryTarget(target *historyTarget) {
	slices.Sort(target.view.Resources)
	target.view.Resources = slices.Compact(target.view.Resources)
	slices.Sort(target.view.Actions)
	target.view.Actions = slices.Compact(target.view.Actions)
	slices.Sort(target.view.Fields)
	target.view.Fields = slices.Compact(target.view.Fields)
	slices.Sort(target.view.Context)
	target.view.Context = slices.Compact(target.view.Context)
	slices.Sort(target.view.Classifications)
	target.view.Classifications = slices.Compact(target.view.Classifications)
}

func catalogRefs(catalogs *CatalogSet) []CatalogRef {
	manifests := catalogs.Manifests()
	result := make([]CatalogRef, len(manifests))
	for index, manifest := range manifests {
		result[index] = manifest.Ref()
	}
	return result
}

type FieldKnowledge uint8

const (
	FieldKnown FieldKnowledge = iota + 1
	FieldAbsent
	FieldUnobserved
	FieldRedacted
	FieldTokenized
	FieldDestroyed
	FieldMissingKey
	FieldUnknownCodec
	FieldGap
	FieldBudgetExceeded
	FieldUnprojected
)

type ReadValueView struct {
	Field          FieldName
	Codec          CodecDescription
	Classification Classification
	Knowledge      FieldKnowledge
	Canonical      []byte
}

type ChangeReadView struct {
	Field  FieldName
	Before ReadValueView
	After  ReadValueView
}

type ActorReadView struct {
	Ordinal    uint8
	Kind       ActorKind
	Provenance Provenance
	Reference  ReadValueView
}

type contextFactReadView struct {
	kind           ContextFactKind
	provenance     Provenance
	classification Classification
	knowledge      FieldKnowledge
	canonical      []byte
}

type ContextFactReadView struct {
	value contextFactReadView
}

func (v ContextFactReadView) Kind() ContextFactKind          { return v.value.kind }
func (v ContextFactReadView) Provenance() Provenance         { return v.value.provenance }
func (v ContextFactReadView) Classification() Classification { return v.value.classification }
func (v ContextFactReadView) Knowledge() FieldKnowledge      { return v.value.knowledge }
func (v ContextFactReadView) Reference() (Reference, bool) {
	if v.value.knowledge != FieldKnown || !slices.Contains([]ContextFactKind{ServiceContext, DeploymentContext, CorrelationContext, CausationContext, TraceContext}, v.value.kind) {
		return "", false
	}
	return Reference(v.value.canonical), true
}
func (v ContextFactReadView) ScopedReference() (ScopedReference, bool) {
	if v.value.knowledge != FieldKnown || v.value.kind != ScopeContext && v.value.kind != ClientContext {
		return ScopedReference{}, false
	}
	parts := bytes.SplitN(v.value.canonical, []byte{0}, 2)
	if len(parts) != 2 {
		return ScopedReference{}, false
	}
	return ScopedReference{Scope: Reference(parts[0]), Reference: Reference(parts[1])}, true
}
func (v ContextFactReadView) OperationID() (OperationID, bool) {
	if v.value.knowledge != FieldKnown || v.value.kind != OperationContext {
		return OperationID{}, false
	}
	raw, err := hex.DecodeString(string(v.value.canonical))
	if err != nil || len(raw) != 16 {
		return OperationID{}, false
	}
	var result OperationID
	copy(result[:], raw)
	return result, true
}
func (v ContextFactReadView) Source() (Source, bool) {
	if v.value.knowledge != FieldKnown || v.value.kind != SourceContext {
		return "", false
	}
	return Source(v.value.canonical), true
}

type ItemReadView struct {
	Ordinal      uint16
	Kind         ItemKind
	Resource     Resource
	Action       Action
	EntityState  EntityStateKind
	OccurredAt   time.Time
	Outcome      Outcome
	Reason       Reason
	Subject      ReadValueView
	Target       ReadValueView
	Values       []ReadValueView
	Changes      []ChangeReadView
	CorrectionOf ItemRef
	DisputeOf    ItemRef
}

type RevisionView struct {
	Ref         RevisionRef
	Operation   OperationName
	OperationID OperationID
	ObservedAt  time.Time
	RecordedAt  time.Time
	Retention   RetentionClass
	Consequence Consequence
	Actors      []ActorReadView
	Context     []ContextFactReadView
	Items       []ItemReadView
}

type page struct {
	revisions []RevisionView
	hasMore   bool
}

type Page struct {
	value page
}

func (p Page) Revisions() []RevisionView { return cloneRevisionReads(p.value.revisions) }
func (Page) Cursor() (Cursor, error)     { return Cursor{}, auditErrorAt(ErrUnsupported, "cursor") }
func (p Page) HasMore() bool             { return p.value.hasMore }

func pageFromStored(stored StoredPage, grant AccessGrantSpec, query StoreQueryView) Page {
	revisions := stored.Revisions()
	output := make([]RevisionView, len(revisions))
	for index, revision := range revisions {
		output[index] = revisionRead(revision.View(), grant, query)
	}
	return Page{value: page{revisions: output, hasMore: stored.HasMore()}}
}

func revisionRead(stored StoredRevisionView, grant AccessGrantSpec, query StoreQueryView) RevisionView {
	view := stored.Revision
	result := RevisionView{
		Ref:       RevisionRef{Catalog: view.Header.Catalog, Revision: view.Header.RevisionID},
		Operation: view.Header.Operation, OperationID: view.Header.OperationID,
		ObservedAt: view.Header.ObservedAt, RecordedAt: stored.RecordedAt,
		Retention: view.Header.Retention, Consequence: view.Header.Consequence,
		Items: make([]ItemReadView, 0, len(view.Items)),
	}
	if slices.Contains(grant.Context, ActorChainContext) {
		result.Actors = make([]ActorReadView, len(view.Actors))
		for index, actor := range view.Actors {
			result.Actors[index] = ActorReadView{Ordinal: actor.Ordinal, Kind: actor.Kind, Provenance: actor.Provenance, Reference: readValue(actor.Reference, true, grant)}
		}
	}
	for _, fact := range view.Context {
		if !slices.Contains(grant.Context, fact.Kind) {
			continue
		}
		read := readContextFact(fact, grant)
		result.Context = append(result.Context, read)
	}
	for _, item := range view.Items {
		if !slices.Contains(query.Resources, item.Resource) || !slices.Contains(query.Actions, item.Action) || !matchesItemCoordinates(query.Coordinates, item) {
			continue
		}
		read := ItemReadView{
			Ordinal: item.Ordinal, Kind: item.Kind, Resource: item.Resource, Action: item.Action,
			EntityState: item.EntityState, OccurredAt: item.OccurredAt, Outcome: item.Outcome, Reason: item.Reason,
			Subject: readValue(item.Subject, true, grant), Target: readValue(item.Target, true, grant),
			CorrectionOf: item.CorrectionOf, DisputeOf: item.DisputeOf,
			Values: make([]ReadValueView, len(item.Values)), Changes: make([]ChangeReadView, len(item.Changes)),
		}
		for field, value := range item.Values {
			read.Values[field] = readValue(value, slices.Contains(grant.Fields, value.Field), grant)
		}
		for field, change := range item.Changes {
			projected := slices.Contains(grant.Fields, change.Field)
			read.Changes[field] = ChangeReadView{Field: change.Field, Before: readValue(change.Before, projected, grant), After: readValue(change.After, projected, grant)}
		}
		result.Items = append(result.Items, read)
	}
	return result
}

func readValue(value StoredValueView, projected bool, grant AccessGrantSpec) ReadValueView {
	result := ReadValueView{Field: value.Field, Codec: value.Codec, Classification: value.Classification}
	result.Codec.ReadVersions = slices.Clone(value.Codec.ReadVersions)
	if !value.Classification.Valid() {
		return result
	}
	if !projected || !slices.Contains(grant.Classifications, value.Classification) {
		result.Knowledge = FieldUnprojected
		return result
	}
	switch {
	case value.State == ValueAbsent:
		result.Knowledge = FieldAbsent
	case value.State == ValueRedacted || value.Mode == AsRedacted:
		result.Knowledge = FieldRedacted
	case value.Mode == AsPlaintext:
		result.Knowledge = FieldKnown
		result.Canonical = bytes.Clone(value.Plaintext)
	case value.Mode == AsToken:
		result.Knowledge = FieldTokenized
	case value.Mode == AsProtected || value.Mode == AsIndexedProtected:
		result.Knowledge = FieldMissingKey
	default:
		result.Knowledge = FieldUnknownCodec
	}
	return result
}

func readContextFact(fact StoredContextFactView, grant AccessGrantSpec) ContextFactReadView {
	value := StoredValueView{Classification: fact.Classification, Mode: fact.Mode, State: ValuePresent, Plaintext: fact.Plaintext, Redacted: fact.Redacted, Token: fact.Token, Protected: fact.Protected}
	read := readValue(value, true, grant)
	return ContextFactReadView{value: contextFactReadView{
		kind: fact.Kind, provenance: fact.Provenance, classification: fact.Classification,
		knowledge: read.Knowledge, canonical: bytes.Clone(read.Canonical),
	}}
}

func cloneRevisionReads(input []RevisionView) []RevisionView {
	output := make([]RevisionView, len(input))
	for index, revision := range input {
		output[index] = cloneRevisionRead(revision)
	}
	return output
}

func cloneRevisionRead(input RevisionView) RevisionView {
	input.Actors = slices.Clone(input.Actors)
	for index := range input.Actors {
		input.Actors[index].Reference = cloneReadValue(input.Actors[index].Reference)
	}
	input.Context = slices.Clone(input.Context)
	for index := range input.Context {
		input.Context[index].value.canonical = bytes.Clone(input.Context[index].value.canonical)
	}
	input.Items = slices.Clone(input.Items)
	for index := range input.Items {
		input.Items[index] = cloneItemRead(input.Items[index])
	}
	return input
}

func cloneItemRead(input ItemReadView) ItemReadView {
	input.Subject = cloneReadValue(input.Subject)
	input.Target = cloneReadValue(input.Target)
	input.Values = slices.Clone(input.Values)
	for index := range input.Values {
		input.Values[index] = cloneReadValue(input.Values[index])
	}
	input.Changes = slices.Clone(input.Changes)
	for index := range input.Changes {
		input.Changes[index].Before = cloneReadValue(input.Changes[index].Before)
		input.Changes[index].After = cloneReadValue(input.Changes[index].After)
	}
	return input
}

func cloneReadValue(input ReadValueView) ReadValueView {
	input.Codec.ReadVersions = slices.Clone(input.Codec.ReadVersions)
	input.Canonical = bytes.Clone(input.Canonical)
	return input
}
