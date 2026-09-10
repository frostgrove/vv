package audit

import (
	"bytes"
	"context"
	"reflect"
	"slices"
	"time"
)

type Log interface {
	StoreInfo
	Search(context.Context, StoreQuery) (StoredPage, error)
}

type SearchProgressView struct {
	Pages     uint32
	Cohorts   uint32
	Revisions uint32
	Bytes     uint64
}

type StoreQueryView struct {
	Log             LogID
	Catalogs        []CatalogRef
	Class           QueryClass
	Resources       []Resource
	OperationName   OperationName
	Operation       OperationID
	Coordinates     []QueryCoordinateView
	Actions         []Action
	Classifications []Classification
	Fields          FieldProjectionView
	Context         ContextProjectionView
	Time            TimeWindowView
	Direction       HistoryDirection
	Changed         ChangedFieldFilterView
	Outcomes        []Outcome
	Reasons         []Reason
	Limit           uint32
	MaxBytes        uint64
	Progress        SearchProgressView
	Snapshot        []byte
	SnapshotExpiry  time.Time
	After           StorePosition
}

type storeQueryOrigin struct{ marker byte }

type storeQuery struct {
	view   StoreQueryView
	origin *storeQueryOrigin
}

type StoreQuery struct {
	value storeQuery
}

func (q StoreQuery) View() StoreQueryView { return cloneStoreQueryView(q.value.view) }

func newStoreQuery(view StoreQueryView) StoreQuery {
	return StoreQuery{value: storeQuery{view: cloneStoreQueryView(view), origin: &storeQueryOrigin{}}}
}

func cloneStoreQueryView(view StoreQueryView) StoreQueryView {
	view.Catalogs = slices.Clone(view.Catalogs)
	view.Resources = slices.Clone(view.Resources)
	view.Coordinates = cloneCoordinates(view.Coordinates)
	view.Actions = slices.Clone(view.Actions)
	view.Classifications = slices.Clone(view.Classifications)
	view.Fields.Fields = slices.Clone(view.Fields.Fields)
	view.Context.Facts = slices.Clone(view.Context.Facts)
	view.Changed.Fields = slices.Clone(view.Changed.Fields)
	view.Changed.Excluded = slices.Clone(view.Changed.Excluded)
	view.Outcomes = slices.Clone(view.Outcomes)
	view.Reasons = slices.Clone(view.Reasons)
	view.Snapshot = bytes.Clone(view.Snapshot)
	view.After = cloneStorePosition(view.After)
	return view
}

type StoredRevisionData struct {
	Revision        RevisionWireView
	RecordedAt      time.Time
	Position        StorePosition
	ActiveHoldCount uint32
	HoldEpoch       uint64
	ActiveHoldSet   HoldSetDigest
	HoldTransitions []RevisionRef
}

type StoredRevisionView struct {
	Revision        RevisionWireView
	RecordedAt      time.Time
	Position        StorePosition
	ActiveHoldCount uint32
	HoldEpoch       uint64
	ActiveHoldSet   HoldSetDigest
	HoldTransitions []RevisionRef
}

type storedRevision struct {
	view StoredRevisionView
	size uint64
}

type StoredRevision struct {
	value storedRevision
}

func NewStoredRevision(data StoredRevisionData) (StoredRevision, error) {
	revision, err := newRevision(data.Revision)
	if err != nil || data.RecordedAt.IsZero() || !data.RecordedAt.Equal(canonicalTime(data.RecordedAt)) || len(data.Position.Bytes()) == 0 {
		return StoredRevision{}, auditErrorAt(ErrMalformedEvidence, "stored_revision")
	}
	view := revision.View()
	if err := validateStoredRevisionIntegrity(view); err != nil {
		return StoredRevision{}, err
	}
	for _, reference := range data.HoldTransitions {
		if !reference.valid() {
			return StoredRevision{}, auditErrorAt(ErrMalformedEvidence, "hold_transitions")
		}
	}
	stored := StoredRevisionView{
		Revision: view, RecordedAt: data.RecordedAt, Position: cloneStorePosition(data.Position),
		ActiveHoldCount: data.ActiveHoldCount, HoldEpoch: data.HoldEpoch, ActiveHoldSet: data.ActiveHoldSet,
		HoldTransitions: slices.Clone(data.HoldTransitions),
	}
	return StoredRevision{value: storedRevision{view: stored, size: encodedRevisionSize(view)}}, nil
}

func (r StoredRevision) View() StoredRevisionView {
	view := r.value.view
	view.Revision = cloneRevisionView(view.Revision)
	view.Position = cloneStorePosition(view.Position)
	view.HoldTransitions = slices.Clone(view.HoldTransitions)
	return view
}

func (r StoredRevision) EncodedBytes() uint64 { return r.value.size }

func (r StoredRevision) valid() bool {
	return r.value.size > 0 && !r.value.view.RecordedAt.IsZero() && len(r.value.view.Position.Bytes()) > 0 && validateStoredRevisionIntegrity(r.value.view.Revision) == nil
}

type StoredPageData struct {
	Revisions []StoredRevision
	Position  StorePosition
	HasMore   bool
	Progress  SearchProgressView
	Snapshot  []byte
	ExpiresAt time.Time
}

type storedPage struct {
	data   StoredPageData
	origin *storeQueryOrigin
}

type StoredPage struct {
	value storedPage
}

func NewStoredPage(query StoreQuery, data StoredPageData) (StoredPage, error) {
	if err := validateStoredPage(query, StoredPage{value: storedPage{data: cloneStoredPageData(data), origin: query.value.origin}}); err != nil {
		return StoredPage{}, err
	}
	return StoredPage{value: storedPage{data: cloneStoredPageData(data), origin: query.value.origin}}, nil
}

func (p StoredPage) Revisions() []StoredRevision  { return cloneStoredRevisions(p.value.data.Revisions) }
func (p StoredPage) Position() StorePosition      { return cloneStorePosition(p.value.data.Position) }
func (p StoredPage) HasMore() bool                { return p.value.data.HasMore }
func (p StoredPage) Progress() SearchProgressView { return p.value.data.Progress }
func (p StoredPage) Snapshot() []byte             { return bytes.Clone(p.value.data.Snapshot) }
func (p StoredPage) ExpiresAt() time.Time         { return p.value.data.ExpiresAt }

func cloneStoredPageData(data StoredPageData) StoredPageData {
	data.Revisions = cloneStoredRevisions(data.Revisions)
	data.Position = cloneStorePosition(data.Position)
	data.Snapshot = bytes.Clone(data.Snapshot)
	return data
}

func cloneStoredRevisions(input []StoredRevision) []StoredRevision {
	output := make([]StoredRevision, len(input))
	for index, stored := range input {
		view := stored.View()
		output[index] = StoredRevision{value: storedRevision{view: view, size: stored.value.size}}
	}
	return output
}

func validateStoredPage(query StoreQuery, page StoredPage) error {
	if query.value.origin == nil || page.value.origin != query.value.origin {
		return auditErrorAt(ErrIntegrity, "history.store_result")
	}
	view := query.value.view
	data := page.value.data
	if !validBasicStoreQuery(view) || len(data.Revisions) > int(view.Limit) || len(data.Snapshot) != 0 || !data.ExpiresAt.IsZero() {
		return auditErrorAt(ErrIntegrity, "history.store_result")
	}
	if data.Progress.Pages != 1 || data.Progress.Cohorts != 0 || data.Progress.Revisions != uint32(len(data.Revisions)) {
		return auditErrorAt(ErrIntegrity, "history.store_result")
	}
	var total uint64
	seen := make(map[RevisionID]struct{}, len(data.Revisions))
	for index, revision := range data.Revisions {
		if !revision.valid() || !matchesStoreQuery(view, revision.value.view.Revision) {
			return auditErrorAt(ErrIntegrity, "history.store_result")
		}
		if index > 0 && !orderedStoredRevisions(data.Revisions[index-1], revision, view.Direction) {
			return auditErrorAt(ErrIntegrity, "history.store_result")
		}
		header := revision.value.view.Revision.Header
		if _, duplicate := seen[header.RevisionID]; duplicate {
			return auditErrorAt(ErrIntegrity, "history.store_result")
		}
		seen[header.RevisionID] = struct{}{}
		total += revision.EncodedBytes()
		if total > view.MaxBytes {
			return auditErrorAt(ErrIntegrity, "history.store_result")
		}
	}
	if data.Progress.Bytes != total {
		return auditErrorAt(ErrIntegrity, "history.store_result")
	}
	if len(data.Revisions) == 0 {
		if len(data.Position.Bytes()) != 0 || data.HasMore {
			return auditErrorAt(ErrIntegrity, "history.store_result")
		}
	} else if !bytes.Equal(data.Position.Bytes(), data.Revisions[len(data.Revisions)-1].value.view.Position.Bytes()) {
		return auditErrorAt(ErrIntegrity, "history.store_result")
	}
	return nil
}

func orderedStoredRevisions(left, right StoredRevision, direction HistoryDirection) bool {
	leftHeader := left.value.view.Revision.Header
	rightHeader := right.value.view.Revision.Header
	comparison := leftHeader.ObservedAt.Compare(rightHeader.ObservedAt)
	if comparison == 0 {
		comparison = bytes.Compare(leftHeader.RevisionID[:], rightHeader.RevisionID[:])
	}
	if direction == OldestFirst {
		return comparison < 0
	}
	return comparison > 0
}

func validBasicStoreQuery(view StoreQueryView) bool {
	return view.Log != (LogID{}) && len(view.Catalogs) > 0 && len(view.Resources) > 0 && len(view.Actions) > 0 && len(view.Classifications) > 0 && view.Limit > 0 && view.Limit <= MaxPageRevisions && view.MaxBytes > 0 && (view.Direction == OldestFirst || view.Direction == NewestFirst) && len(view.Snapshot) == 0 && view.Progress == (SearchProgressView{}) && len(view.After.Bytes()) == 0
}

func matchesStoreQuery(query StoreQueryView, revision RevisionWireView) bool {
	header := revision.Header
	if header.Log != query.Log || !slices.Contains(query.Catalogs, header.Catalog) || !subset(header.Authorization.Resources, query.Resources) || !subset(header.Authorization.Actions, query.Actions) || !subset(header.Authorization.Classifications, query.Classifications) {
		return false
	}
	if query.OperationName != "" && header.Operation != query.OperationName || query.Operation != (OperationID{}) && header.OperationID != query.Operation {
		return false
	}
	if !matchesScope(query.Coordinates, revision.Context) {
		return false
	}
	for _, item := range revision.Items {
		if !slices.Contains(query.Resources, item.Resource) || !slices.Contains(query.Actions, item.Action) {
			continue
		}
		if matchesItemCoordinates(query.Coordinates, item) {
			return true
		}
	}
	return false
}

func matchesScope(coordinates []QueryCoordinateView, facts []StoredContextFactView) bool {
	for _, coordinate := range coordinates {
		if coordinate.Kind != QueryScope {
			continue
		}
		if len(coordinate.Alternatives) != 1 || coordinate.Match != QueryCoordinateExact {
			return false
		}
		matched := false
		for _, fact := range facts {
			if fact.Kind == ScopeContext && fact.Mode == AsPlaintext && bytes.Equal(fact.Plaintext, coordinate.Alternatives[0].Plaintext) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func matchesItemCoordinates(coordinates []QueryCoordinateView, item ItemWireView) bool {
	for _, coordinate := range coordinates {
		if coordinate.Kind == QueryScope {
			continue
		}
		if coordinate.Match != QueryCoordinateExact || len(coordinate.Alternatives) != 1 {
			return false
		}
		want := coordinate.Alternatives[0].Plaintext
		switch coordinate.Kind {
		case QuerySubject:
			if item.Subject.Mode != AsPlaintext || !bytes.Equal(item.Subject.Plaintext, want) {
				return false
			}
		case QueryTarget:
			if item.Target.Mode != AsPlaintext || !bytes.Equal(item.Target.Plaintext, want) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func validateStoredRevisionIntegrity(view RevisionWireView) error {
	if !reflect.DeepEqual(view.Header.Authorization, authorizationSummary(view.Items, view.Actors, view.Context)) {
		return auditErrorAt(ErrMalformedEvidence, "authorization")
	}
	for _, item := range view.Items {
		if item.Leaf != leafDigestOf(view.Header.Log, view.Header.Catalog, view.Header.RevisionID, item) {
			return auditErrorAt(ErrIntegrity, "item.leaf")
		}
	}
	if view.Header.Envelope != envelopeDigestOf(view) || view.Header.Integrity != integrityDigestOf(view.Header) {
		return auditErrorAt(ErrIntegrity, "revision")
	}
	return nil
}

type byteCounter uint64

func (c *byteCounter) Write(value []byte) (int, error) {
	*c += byteCounter(len(value))
	return len(value), nil
}

func encodedRevisionSize(view RevisionWireView) uint64 {
	var counter byteCounter
	writeRevisionHeader(&counter, view.Header, true)
	writeFrame(&counter, view.Header.Integrity[:])
	writeSeal(&counter, view.Header.Seal)
	writeUint32(&counter, uint32(len(view.Actors)))
	for _, actor := range view.Actors {
		writeUint32(&counter, uint32(actor.Ordinal))
		writeUint32(&counter, uint32(actor.Kind))
		writeUint32(&counter, uint32(actor.Provenance))
		writeStoredValue(&counter, actor.Reference)
	}
	writeUint32(&counter, uint32(len(view.Context)))
	for _, fact := range view.Context {
		writeUint32(&counter, uint32(fact.Kind))
		writeUint32(&counter, uint32(fact.Provenance))
		writeUint32(&counter, uint32(fact.Classification))
		writeUint32(&counter, uint32(fact.Mode))
		writeFrame(&counter, fact.Plaintext)
		writeBool(&counter, fact.Redacted)
		writeToken(&counter, fact.Token)
		writeProtected(&counter, fact.Protected)
	}
	writeUint32(&counter, uint32(len(view.Items)))
	for _, item := range view.Items {
		writeItem(&counter, item, true)
	}
	return uint64(counter)
}
