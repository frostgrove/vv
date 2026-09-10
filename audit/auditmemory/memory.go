package auditmemory

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/crud"
)

type LogSpec struct {
	Limits audit.LimitSpec
}

type revisionRecord struct {
	revision audit.Revision
	stored   audit.StoredHeader
}

type searchCandidate struct {
	stored   audit.StoredRevision
	observed time.Time
	revision audit.RevisionID
}

type entityHead struct {
	chain    audit.EntityChainID
	previous audit.LeafDigest
	terminal bool
}

type log struct {
	mu          sync.RWMutex
	backing     audit.Backing
	backingID   audit.BackingID
	logID       audit.LogID
	limits      audit.Limits
	catalogs    audit.StoreCatalogState
	manifests   map[audit.CatalogRef]audit.Manifest
	mutations   []audit.CatalogMutationView
	revisions   map[audit.RevisionID]revisionRecord
	idempotency map[idempotencyKey]audit.RevisionID
	heads       map[entityKey]entityHead
	sequence    uint64
}

type Log struct{ value *log }

type idempotencyKey struct {
	catalog   audit.CatalogID
	operation audit.OperationName
	token     audit.IdempotencyToken
}

type entityKey struct {
	resource   audit.Resource
	scope      audit.EvidenceScopeCommitment
	scoped     bool
	algorithm  string
	profile    string
	keyID      string
	commitment [32]byte
}

func NewLog(spec LogSpec) (*Log, error) {
	limits, err := memoryLimits(spec.Limits)
	if err != nil {
		return nil, err
	}
	value := &log{
		limits: limits, catalogs: audit.NewEmptyStoreCatalogState(),
		manifests: make(map[audit.CatalogRef]audit.Manifest), revisions: make(map[audit.RevisionID]revisionRecord),
		idempotency: make(map[idempotencyKey]audit.RevisionID), heads: make(map[entityKey]entityHead),
	}
	value.backingID, err = randomID[audit.BackingID]()
	if err != nil {
		return nil, audit.Failure(audit.NotWritten, err)
	}
	value.logID, err = randomID[audit.LogID]()
	if err != nil {
		return nil, audit.Failure(audit.NotWritten, err)
	}
	if value.backingID == (audit.BackingID{}) || value.logID == (audit.LogID{}) {
		return nil, audit.Failure(audit.NotWritten, errors.New("auditmemory: random source returned zero"))
	}
	value.backing, err = audit.BackingFor(value)
	if err != nil {
		return nil, err
	}
	return &Log{value: value}, nil
}

func randomID[T ~[16]byte]() (T, error) {
	var value T
	_, err := rand.Read(value[:])
	return value, err
}

func memoryLimits(spec audit.LimitSpec) (audit.Limits, error) {
	if spec == (audit.LimitSpec{}) {
		spec = audit.LimitSpec{
			RevisionBytes: 16 << 20, AppendRequestBytes: 40 << 20,
			PageRevisions: 1000, PageBytes: 32 << 20, ExactTargets: 10_000, ExactBytes: 32 << 20,
			PositionBytes: 4096, InventoryCandidates: 10_000, InventoryCohorts: 10_000,
			InventoryBytes: 32 << 20, SnapshotBytes: 8 << 20,
			SearchCohortRevisions: 1_000_000, SearchCohortBytes: 256 << 20,
			AttemptTransitions: 259, AttemptStateBytes: 4 << 20, AttemptOpenLifetime: 365 * 24 * time.Hour,
		}
	}
	return audit.NewLimits(spec)
}

type Deployment struct {
	log    *log
	closed bool
	mu     sync.Mutex
}

func NewDeployment(source *Log) (*Deployment, error) {
	if source == nil || source.value == nil {
		return nil, audit.Failure(audit.Refused, errors.New("auditmemory: log is nil"))
	}
	return &Deployment{log: source.value}, nil
}

func (d *Deployment) Capabilities() audit.Capabilities { return memoryCapabilities() }
func (d *Deployment) Limits() audit.Limits             { return d.log.limits }
func (d *Deployment) Backing() audit.Backing           { return d.log.backing }
func (d *Deployment) BackingID() audit.BackingID       { return d.log.backingID }
func (d *Deployment) LogID() audit.LogID               { return d.log.logID }

func (d *Deployment) Catalogs() audit.StoreCatalogState {
	d.log.mu.RLock()
	defer d.log.mu.RUnlock()
	return d.log.catalogs
}

func (d *Deployment) CatalogMutations(ctx context.Context) (audit.CatalogMutationLog, error) {
	if err := ctx.Err(); err != nil {
		return audit.CatalogMutationLog{}, err
	}
	if d == nil || d.log == nil {
		return audit.CatalogMutationLog{}, audit.Failure(audit.Refused, errors.New("auditmemory: deployment is nil"))
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return audit.CatalogMutationLog{}, audit.Failure(audit.Closed, errors.New("auditmemory: deployment is closed"))
	}
	d.log.mu.RLock()
	defer d.log.mu.RUnlock()
	result, err := audit.NewCatalogMutationLog(d.log.backingID, d.log.logID, d.log.mutations)
	if err != nil {
		return audit.CatalogMutationLog{}, audit.Failure(audit.Corrupt, err)
	}
	return result, nil
}

func (d *Deployment) InstallCatalog(ctx context.Context, manifest audit.Manifest, change audit.CatalogChangeRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d == nil || d.log == nil || len(manifest.Canonical()) == 0 || manifest.Ref() == (audit.CatalogRef{}) || !validCatalogChange(change) {
		return audit.Failure(audit.Refused, errors.New("auditmemory: invalid catalog"))
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return audit.Failure(audit.Closed, errors.New("auditmemory: deployment is closed"))
	}
	d.log.mu.Lock()
	defer d.log.mu.Unlock()
	if existing, ok := installedAt(d.log.manifests, manifest.Ref().ID, manifest.Ref().Generation); ok {
		prior, recorded := installedMutation(d.log.mutations, existing.Ref())
		if bytes.Equal(existing.Canonical(), manifest.Canonical()) && existing.Ref() == manifest.Ref() && recorded && prior.Change.View() == change.View() {
			return nil
		}
		return audit.Failure(audit.Conflict, errors.New("auditmemory: catalog generation conflicts"))
	}
	latest, hasLatest := latestInstalled(d.log.manifests)
	if !hasLatest {
		if manifest.Previous() != (audit.CatalogRef{}) {
			return audit.Failure(audit.Refused, errors.New("auditmemory: genesis predecessor is not empty"))
		}
		if manifest.Ref().Generation != 1 {
			return audit.Failure(audit.Refused, errors.New("auditmemory: first catalog is not genesis"))
		}
	} else if manifest.Previous() != latest.Ref() || manifest.Ref().ID != latest.Ref().ID || manifest.Ref().Generation != latest.Ref().Generation+1 {
		return audit.Failure(audit.Refused, errors.New("auditmemory: catalog is not the direct next generation"))
	}
	prospectiveManifests := cloneManifests(d.log.manifests)
	prospectiveManifests[manifest.Ref()] = manifest
	manifests := orderedManifests(prospectiveManifests, manifest.Ref())
	digest, err := audit.CatalogSetDigestOf(manifests)
	if err != nil {
		return audit.Failure(audit.Refused, err)
	}
	var prospectiveState audit.StoreCatalogState
	if d.log.catalogs.HasActive() {
		prospectiveState, err = audit.NewStoreCatalogState(d.log.catalogs.Active(), digest)
	} else {
		prospectiveState, err = audit.NewInactiveStoreCatalogState(digest)
	}
	if err != nil {
		return audit.Failure(audit.Refused, err)
	}
	prospectiveMutations := append(slices.Clone(d.log.mutations), audit.CatalogInstalled(manifest.Ref(), change))
	if _, err := audit.NewCatalogMutationLog(d.log.backingID, d.log.logID, prospectiveMutations); err != nil {
		return audit.Failure(audit.Refused, err)
	}
	d.log.manifests = prospectiveManifests
	d.log.catalogs = prospectiveState
	d.log.mutations = prospectiveMutations
	return nil
}

func (d *Deployment) ActivateCatalog(ctx context.Context, expected, next audit.CatalogRef, change audit.CatalogChangeRef, proof audit.CatalogActivationProof) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d == nil || d.log == nil || !proof.ValidFor(expected, next) || !validCatalogChange(change) {
		return audit.Failure(audit.Refused, errors.New("auditmemory: activation proof is invalid"))
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return audit.Failure(audit.Closed, errors.New("auditmemory: deployment is closed"))
	}
	d.log.mu.Lock()
	defer d.log.mu.Unlock()
	if prior, ok := activatedMutation(d.log.mutations, expected, next); ok {
		if prior.Change.View() == change.View() {
			return nil
		}
		return audit.Failure(audit.Conflict, errors.New("auditmemory: catalog activation change conflicts"))
	}
	if _, ok := d.log.manifests[next]; !ok {
		return audit.Failure(audit.Missing, errors.New("auditmemory: catalog is not installed"))
	}
	current := d.log.catalogs.Active()
	if d.log.catalogs.HasActive() != (expected != (audit.CatalogRef{})) || current != expected {
		return audit.Failure(audit.Conflict, errors.New("auditmemory: active catalog changed"))
	}
	prospectiveState, err := audit.NewStoreCatalogState(next, d.log.catalogs.SetDigest())
	if err != nil {
		return audit.Failure(audit.Refused, err)
	}
	prospectiveMutations := append(slices.Clone(d.log.mutations), audit.CatalogActivated(expected, next, change, proof))
	if _, err := audit.NewCatalogMutationLog(d.log.backingID, d.log.logID, prospectiveMutations); err != nil {
		return audit.Failure(audit.Refused, err)
	}
	d.log.catalogs = prospectiveState
	d.log.mutations = prospectiveMutations
	return nil
}

func (d *Deployment) VerifyCatalogs(ctx context.Context, manifests []audit.Manifest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d == nil || d.log == nil || len(manifests) == 0 || len(manifests) > audit.MaxCatalogs {
		return audit.Failure(audit.Refused, errors.New("auditmemory: catalog inventory is invalid"))
	}
	expectedSet, err := audit.CatalogSetDigestOf(manifests)
	if err != nil {
		return audit.Failure(audit.Refused, err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return audit.Failure(audit.Closed, errors.New("auditmemory: deployment is closed"))
	}
	d.log.mu.RLock()
	defer d.log.mu.RUnlock()
	if len(d.log.manifests) != len(manifests) {
		return audit.Failure(audit.Conflict, errors.New("auditmemory: installed catalog inventory differs"))
	}
	for _, manifest := range manifests {
		installed, ok := d.log.manifests[manifest.Ref()]
		if !ok || !bytes.Equal(installed.Canonical(), manifest.Canonical()) {
			return audit.Failure(audit.Conflict, errors.New("auditmemory: installed catalog differs"))
		}
	}
	expectedActive := manifests[len(manifests)-1].Ref()
	if !d.log.catalogs.HasActive() || d.log.catalogs.Active() != expectedActive || d.log.catalogs.SetDigest() != expectedSet {
		return audit.Failure(audit.Conflict, errors.New("auditmemory: active catalog does not match the verified inventory"))
	}
	return nil
}

func (d *Deployment) InstallAndActivate(ctx context.Context, catalogs *audit.CatalogSet, change audit.CatalogChangeRef) error {
	if catalogs == nil {
		return audit.Failure(audit.Refused, errors.New("auditmemory: catalog set is nil"))
	}
	manifests := catalogs.Manifests()
	for _, manifest := range manifests {
		if err := d.InstallCatalog(ctx, manifest, change); err != nil {
			return err
		}
	}
	state := d.Catalogs()
	expected := audit.CatalogRef{}
	start := 0
	if state.HasActive() {
		expected = state.Active()
		start = len(manifests)
		for index, manifest := range manifests {
			if manifest.Ref() == expected {
				start = index + 1
				break
			}
		}
		if start == len(manifests) && expected != catalogs.Active() {
			return audit.Failure(audit.Conflict, errors.New("auditmemory: active catalog is outside the requested lineage"))
		}
	}
	for _, manifest := range manifests[start:] {
		proof, err := audit.NoAttemptCatalogActivation(expected, manifest.Ref())
		if err != nil {
			return err
		}
		if err := d.ActivateCatalog(ctx, expected, manifest.Ref(), change, proof); err != nil {
			return err
		}
		expected = manifest.Ref()
	}
	if d.Catalogs().Active() != catalogs.Active() || d.Catalogs().SetDigest() != catalogs.Digest() {
		return audit.Failure(audit.Conflict, errors.New("auditmemory: deployed lineage does not match"))
	}
	return nil
}

func orderedManifests(installed map[audit.CatalogRef]audit.Manifest, active audit.CatalogRef) []audit.Manifest {
	result := make([]audit.Manifest, 0, active.Generation)
	current := installed[active]
	for len(current.Canonical()) > 0 {
		result = append(result, current)
		if current.Previous() == (audit.CatalogRef{}) {
			break
		}
		current = installed[current.Previous()]
	}
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func validCatalogChange(change audit.CatalogChangeRef) bool {
	view := change.View()
	verified, err := audit.NewCatalogChangeRef(view.Ledger, view.Change)
	return err == nil && verified.View() == view
}

func installedAt(installed map[audit.CatalogRef]audit.Manifest, id audit.CatalogID, generation audit.CatalogGeneration) (audit.Manifest, bool) {
	for reference, manifest := range installed {
		if reference.ID == id && reference.Generation == generation {
			return manifest, true
		}
	}
	return audit.Manifest{}, false
}

func latestInstalled(installed map[audit.CatalogRef]audit.Manifest) (audit.Manifest, bool) {
	var latest audit.Manifest
	for _, manifest := range installed {
		if latest.Ref() == (audit.CatalogRef{}) || manifest.Ref().Generation > latest.Ref().Generation {
			latest = manifest
		}
	}
	return latest, latest.Ref() != (audit.CatalogRef{})
}

func cloneManifests(input map[audit.CatalogRef]audit.Manifest) map[audit.CatalogRef]audit.Manifest {
	output := make(map[audit.CatalogRef]audit.Manifest, len(input)+1)
	for reference, manifest := range input {
		output[reference] = manifest
	}
	return output
}

func installedMutation(mutations []audit.CatalogMutationView, catalog audit.CatalogRef) (audit.CatalogMutationView, bool) {
	for _, mutation := range mutations {
		if mutation.Kind == audit.CatalogInstallMutation && mutation.Catalog == catalog {
			return mutation, true
		}
	}
	return audit.CatalogMutationView{}, false
}

func activatedMutation(mutations []audit.CatalogMutationView, expected, active audit.CatalogRef) (audit.CatalogMutationView, bool) {
	for _, mutation := range mutations {
		if mutation.Kind == audit.CatalogActivateMutation && mutation.Expected == expected && mutation.Active == active {
			return mutation, true
		}
	}
	return audit.CatalogMutationView{}, false
}

func (d *Deployment) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return audit.Failure(audit.Closed, errors.New("auditmemory: deployment is closed"))
	}
	d.closed = true
	return nil
}

type Spec struct {
	Log   *Log
	Clock func() time.Time
}

type Store struct {
	log    *log
	source *source
	clock  func() time.Time
	mu     sync.RWMutex
	closed bool
}

func New(spec Spec) (*Store, error) {
	if spec.Log == nil || spec.Log.value == nil {
		return nil, audit.Failure(audit.Refused, errors.New("auditmemory: log is nil"))
	}
	clock := spec.Clock
	if clock == nil {
		clock = time.Now
	}
	store := &Store{log: spec.Log.value, clock: clock}
	store.source = &source{identity: spec.Log.value}
	return store, nil
}

func memoryCapabilities() audit.Capabilities {
	value, _ := audit.NewCapabilities(audit.CapabilitySpec{
		Transactions: audit.SupportSupported, CrossSystemAtomic: audit.SupportUnsupported,
		Persistence: audit.SupportUnsupported, Idempotency: audit.SupportSupported,
		Reconciliation: audit.SupportSupported, StableSearch: audit.SupportUnsupported,
		ExactInspection: audit.SupportSupported, AttemptLifecycle: audit.SupportUnsupported,
		Holds: audit.SupportUnsupported, PurgePlanning: audit.SupportUnsupported,
	})
	return value
}

func (s *Store) Capabilities() audit.Capabilities { return memoryCapabilities() }
func (s *Store) Limits() audit.Limits             { return s.log.limits }
func (s *Store) Backing() audit.Backing           { return s.log.backing }
func (s *Store) BackingID() audit.BackingID       { return s.log.backingID }
func (s *Store) LogID() audit.LogID               { return s.log.logID }
func (s *Store) TransactionSource() any           { return s.source }

func (s *Store) Catalogs() audit.StoreCatalogState {
	s.log.mu.RLock()
	defer s.log.mu.RUnlock()
	return s.log.catalogs
}

func (s *Store) CatalogMutations(ctx context.Context) (audit.CatalogMutationLog, error) {
	if err := ctx.Err(); err != nil {
		return audit.CatalogMutationLog{}, err
	}
	if s == nil || s.log == nil || s.isClosed() {
		return audit.CatalogMutationLog{}, audit.Failure(audit.Closed, errors.New("auditmemory: store is closed"))
	}
	s.log.mu.RLock()
	defer s.log.mu.RUnlock()
	result, err := audit.NewCatalogMutationLog(s.log.backingID, s.log.logID, s.log.mutations)
	if err != nil {
		return audit.CatalogMutationLog{}, audit.Failure(audit.Corrupt, err)
	}
	return result, nil
}

func (s *Store) BindTransaction(executor crud.Executor) (audit.Execution, error) {
	value, ok := executor.(*transactionExecutor)
	if !ok || value == nil || value.tx == nil || value.tx.store != s || !value.tx.live() {
		return nil, audit.Failure(audit.Refused, errors.New("auditmemory: transaction does not belong to this store"))
	}
	return &execution{store: s, tx: value.tx, authority: value.tx.authority}, nil
}

func (s *Store) Append(ctx context.Context, request audit.AppendRequest) (audit.AppendResult, error) {
	if err := ctx.Err(); err != nil {
		return audit.AppendResult{}, err
	}
	if s.isClosed() {
		return audit.AppendResult{}, audit.Failure(audit.Closed, errors.New("auditmemory: store is closed"))
	}
	authority, _ := audit.AuthorityFor(s.source, s)
	s.log.mu.Lock()
	defer s.log.mu.Unlock()
	return appendLocked(s.log, s.clock, request, authority)
}

func (s *Store) LookupIdempotency(ctx context.Context, request audit.IdempotencyLookupRequest) (audit.IdempotencyLookupResult, error) {
	if err := ctx.Err(); err != nil {
		return audit.IdempotencyLookupResult{}, err
	}
	view := request.View()
	s.log.mu.RLock()
	defer s.log.mu.RUnlock()
	key := idempotencyKey{catalog: view.Catalog.ID, operation: view.Domain.Operation, token: view.Domain.Token}
	revisionID, ok := s.log.idempotency[key]
	if !ok {
		return audit.NewIdempotencyLookupResult(request, audit.IdempotencyLookupResultData{State: audit.AbsentNow})
	}
	return audit.NewIdempotencyLookupResult(request, audit.IdempotencyLookupResultData{State: audit.Found, Stored: s.log.revisions[revisionID].stored})
}

func (s *Store) Lookup(ctx context.Context, request audit.LookupRequest) (audit.LookupResult, error) {
	if err := ctx.Err(); err != nil {
		return audit.LookupResult{}, err
	}
	view := request.View()
	if view.Key.Bytes() == nil {
		return audit.LookupResult{}, audit.Failure(audit.Refused, errors.New("auditmemory: lookup key is invalid"))
	}
	s.log.mu.RLock()
	defer s.log.mu.RUnlock()
	record, ok := s.log.revisions[view.Revision]
	if !ok {
		return audit.NewLookupResult(request, audit.LookupResultData{State: audit.AbsentNow, Visibility: audit.Committed})
	}
	return audit.NewLookupResult(request, audit.LookupResultData{State: audit.Found, Stored: record.stored, Visibility: audit.Committed})
}

func (s *Store) Search(ctx context.Context, query audit.StoreQuery) (audit.StoredPage, error) {
	if err := ctx.Err(); err != nil {
		return audit.StoredPage{}, err
	}
	if s.isClosed() {
		return audit.StoredPage{}, audit.Failure(audit.Closed, errors.New("auditmemory: store is closed"))
	}
	view := query.View()
	if view.Log != s.log.logID || view.Limit == 0 || view.MaxBytes == 0 || len(view.Resources) == 0 || len(view.Actions) == 0 || len(view.Catalogs) == 0 {
		return audit.StoredPage{}, audit.Failure(audit.Refused, errors.New("auditmemory: history query is invalid"))
	}
	limits := s.log.limits.View()
	s.log.mu.RLock()
	candidates := make([]searchCandidate, 0, min(len(s.log.revisions), int(limits.SearchCohortRevisions)))
	var cohortBytes uint64
	for _, record := range s.log.revisions {
		if err := ctx.Err(); err != nil {
			s.log.mu.RUnlock()
			return audit.StoredPage{}, err
		}
		revision := record.revision.View()
		if matchesHistoryQuery(view, revision) {
			if uint64(len(candidates)) >= uint64(limits.SearchCohortRevisions) {
				s.log.mu.RUnlock()
				return audit.StoredPage{}, audit.Failure(audit.Refused, errors.New("auditmemory: history candidate cohort exceeds configured revision limit"))
			}
			stored, err := audit.NewStoredRevision(audit.StoredRevisionData{
				Revision: revision, RecordedAt: record.stored.RecordedAt(), Position: record.stored.Position(),
			})
			if err != nil {
				s.log.mu.RUnlock()
				return audit.StoredPage{}, audit.Failure(audit.Corrupt, err)
			}
			if stored.EncodedBytes() > limits.SearchCohortBytes-cohortBytes {
				s.log.mu.RUnlock()
				return audit.StoredPage{}, audit.Failure(audit.Refused, errors.New("auditmemory: history candidate cohort exceeds configured byte limit"))
			}
			header := revision.Header
			candidates = append(candidates, searchCandidate{stored: stored, observed: header.ObservedAt, revision: header.RevisionID})
			cohortBytes += stored.EncodedBytes()
		}
	}
	s.log.mu.RUnlock()
	slices.SortFunc(candidates, func(left, right searchCandidate) int {
		comparison := left.observed.Compare(right.observed)
		if comparison == 0 {
			comparison = bytes.Compare(left.revision[:], right.revision[:])
		}
		if view.Direction == audit.NewestFirst {
			return -comparison
		}
		return comparison
	})
	result := make([]audit.StoredRevision, 0, min(len(candidates), int(view.Limit)))
	var total uint64
	hasMore := false
	for index, candidate := range candidates {
		if len(result) == int(view.Limit) {
			hasMore = true
			break
		}
		if candidate.stored.EncodedBytes() > view.MaxBytes-total {
			if len(result) == 0 {
				return audit.StoredPage{}, audit.Failure(audit.Refused, errors.New("auditmemory: page byte limit cannot hold a revision"))
			}
			hasMore = true
			break
		}
		result = append(result, candidate.stored)
		total += candidate.stored.EncodedBytes()
		if index+1 < len(candidates) && len(result) == int(view.Limit) {
			hasMore = true
		}
	}
	var position audit.StorePosition
	if len(result) > 0 {
		position = result[len(result)-1].View().Position
	}
	page, err := audit.NewStoredPage(query, audit.StoredPageData{
		Revisions: result, Position: position, HasMore: hasMore,
		Progress: audit.SearchProgressView{Pages: 1, Revisions: uint32(len(result)), Bytes: total},
	})
	if err != nil {
		return audit.StoredPage{}, audit.Failure(audit.Corrupt, err)
	}
	return page, nil
}

func matchesHistoryQuery(query audit.StoreQueryView, revision audit.RevisionWireView) bool {
	header := revision.Header
	if header.Log != query.Log || !slices.Contains(query.Catalogs, header.Catalog) || !historySubset(header.Authorization.Resources, query.Resources) || !historySubset(header.Authorization.Actions, query.Actions) || !historySubset(header.Authorization.Classifications, query.Classifications) {
		return false
	}
	if query.OperationName != "" && header.Operation != query.OperationName || query.Operation != (audit.OperationID{}) && header.OperationID != query.Operation {
		return false
	}
	if !matchesHistoryScope(query.Coordinates, revision.Context) {
		return false
	}
	for _, item := range revision.Items {
		if !slices.Contains(query.Resources, item.Resource) || !slices.Contains(query.Actions, item.Action) {
			continue
		}
		if matchesHistoryItem(query.Coordinates, item) {
			return true
		}
	}
	return false
}

func matchesHistoryScope(coordinates []audit.QueryCoordinateView, facts []audit.StoredContextFactView) bool {
	for _, coordinate := range coordinates {
		if coordinate.Kind != audit.QueryScope {
			continue
		}
		if coordinate.Match != audit.QueryCoordinateExact || len(coordinate.Alternatives) != 1 {
			return false
		}
		found := false
		for _, fact := range facts {
			if fact.Kind == audit.ScopeContext && fact.Mode == audit.AsPlaintext && bytes.Equal(fact.Plaintext, coordinate.Alternatives[0].Plaintext) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func matchesHistoryItem(coordinates []audit.QueryCoordinateView, item audit.ItemWireView) bool {
	for _, coordinate := range coordinates {
		if coordinate.Kind == audit.QueryScope {
			continue
		}
		if coordinate.Match != audit.QueryCoordinateExact || len(coordinate.Alternatives) != 1 {
			return false
		}
		switch coordinate.Kind {
		case audit.QuerySubject:
			if item.Subject.Mode != audit.AsPlaintext || !bytes.Equal(item.Subject.Plaintext, coordinate.Alternatives[0].Plaintext) {
				return false
			}
		case audit.QueryTarget:
			if item.Target.Mode != audit.AsPlaintext || !bytes.Equal(item.Target.Plaintext, coordinate.Alternatives[0].Plaintext) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func historySubset[T comparable](values, ceiling []T) bool {
	for _, value := range values {
		if !slices.Contains(ceiling, value) {
			return false
		}
	}
	return true
}

func appendLocked(target *log, clock func() time.Time, request audit.AppendRequest, authority audit.Authority) (audit.AppendResult, error) {
	view := request.View()
	revision := view.Revision.View()
	if !target.catalogs.HasActive() || target.catalogs.Active() != revision.Header.Catalog || target.catalogs.SetDigest() != revision.Header.CatalogSet {
		return audit.AppendResult{}, audit.Failure(audit.StaleCatalog, errors.New("auditmemory: catalog changed"))
	}
	if existing, ok := target.revisions[revision.Header.RevisionID]; ok {
		if existing.stored.Intent() != view.Intent {
			return audit.AppendResult{}, audit.Failure(audit.Conflict, errors.New("auditmemory: revision identity conflicts"))
		}
		return audit.NewAppendResult(request, existing.stored, audit.Replayed, authority)
	}
	if revision.Header.HasIdempotency {
		key := idempotencyKey{catalog: revision.Header.Catalog.ID, operation: revision.Header.Operation, token: revision.Header.Idempotency}
		if revisionID, ok := target.idempotency[key]; ok {
			existing := target.revisions[revisionID]
			if existing.stored.Revision().Semantic != revision.Header.Semantic {
				return audit.AppendResult{}, audit.Failure(audit.Conflict, errors.New("auditmemory: idempotency key changed meaning"))
			}
			return audit.NewAppendResult(request, existing.stored, audit.Replayed, authority)
		}
	}
	if err := validateHeads(target, revision.Items, view.Entities); err != nil {
		return audit.AppendResult{}, err
	}
	target.sequence++
	positionBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(positionBytes, target.sequence)
	position, _ := audit.NewStorePosition(positionBytes)
	stored, err := audit.NewStoredHeader(audit.StoredHeaderData{
		Header: revision.Header, Intent: view.Intent, RecordedAt: clock().UTC(), Position: position,
	})
	if err != nil {
		return audit.AppendResult{}, audit.Failure(audit.Corrupt, err)
	}
	target.revisions[revision.Header.RevisionID] = revisionRecord{revision: view.Revision, stored: stored}
	if revision.Header.HasIdempotency {
		key := idempotencyKey{catalog: revision.Header.Catalog.ID, operation: revision.Header.Operation, token: revision.Header.Idempotency}
		target.idempotency[key] = revision.Header.RevisionID
	}
	advanceHeads(target, revision.Items, view.Entities)
	return audit.NewAppendResult(request, stored, audit.Inserted, authority)
}

func validateHeads(target *log, items []audit.ItemWireView, bindings []audit.EntityAliasBinding) error {
	bound := make(map[audit.EntityChainID][]entityKey, len(bindings))
	owners := make(map[entityKey]audit.EntityChainID, len(bindings))
	seenItems := make(map[audit.EntityChainID]struct{}, len(bindings))
	for _, binding := range bindings {
		keys, ok := keysForBinding(binding)
		if !ok {
			return audit.Failure(audit.Refused, errors.New("auditmemory: entity binding is invalid"))
		}
		if _, duplicate := bound[binding.ChainID()]; duplicate {
			return audit.Failure(audit.Conflict, errors.New("auditmemory: duplicate entity chain binding"))
		}
		for _, key := range keys {
			if owner, duplicate := owners[key]; duplicate && owner != binding.ChainID() {
				return audit.Failure(audit.Conflict, errors.New("auditmemory: entity alias belongs to another chain"))
			}
			owners[key] = binding.ChainID()
		}
		bound[binding.ChainID()] = keys
	}
	for _, item := range items {
		if item.Kind != audit.EntityItem {
			continue
		}
		if _, duplicate := seenItems[item.Chain]; duplicate {
			return audit.Failure(audit.Conflict, errors.New("auditmemory: duplicate entity chain"))
		}
		seenItems[item.Chain] = struct{}{}
		keys, hasBinding := bound[item.Chain]
		if !hasBinding {
			return audit.Failure(audit.Refused, errors.New("auditmemory: entity item has no alias binding"))
		}
		head, exists, consistent := headForKeys(target.heads, keys)
		if !consistent {
			return audit.Failure(audit.Corrupt, errors.New("auditmemory: entity aliases diverge"))
		}
		if !exists {
			if item.Previous != (audit.LeafDigest{}) {
				return audit.Failure(audit.Conflict, errors.New("auditmemory: entity genesis has a predecessor"))
			}
			continue
		}
		if head.chain != item.Chain || head.terminal || head.previous != item.Previous {
			return audit.Failure(audit.Conflict, errors.New("auditmemory: entity head changed"))
		}
	}
	return nil
}

func advanceHeads(target *log, items []audit.ItemWireView, bindings []audit.EntityAliasBinding) {
	bound := make(map[audit.EntityChainID][]entityKey, len(bindings))
	for _, binding := range bindings {
		if keys, ok := keysForBinding(binding); ok {
			bound[binding.ChainID()] = keys
		}
	}
	for _, item := range items {
		if item.Kind != audit.EntityItem {
			continue
		}
		head := entityHead{
			chain: item.Chain, previous: item.Leaf,
			terminal: item.Action == audit.Action(audit.EntityHardDeleted),
		}
		for _, key := range bound[item.Chain] {
			target.heads[key] = head
		}
	}
}

func keysForBinding(binding audit.EntityAliasBinding) ([]entityKey, bool) {
	if binding.Resource() == "" || binding.ChainID() == (audit.EntityChainID{}) || binding.Commitments().Domain() != audit.CommitEntitySubject {
		return nil, false
	}
	scope, scoped := binding.Scope()
	return entityKeys(binding.Resource(), scope, scoped, binding.Commitments())
}

func entityKeys(resource audit.Resource, scope audit.EvidenceScopeCommitment, scoped bool, commitments audit.IdentityCommitmentSet) ([]entityKey, bool) {
	aliases := commitments.Aliases()
	if resource == "" || len(aliases) == 0 || commitments.Domain() != audit.CommitEntitySubject || scoped != (scope != (audit.EvidenceScopeCommitment{})) {
		return nil, false
	}
	result := make([]entityKey, 0, len(aliases))
	seen := make(map[entityKey]struct{}, len(aliases))
	for _, alias := range aliases {
		commitment := alias.Bytes()
		description := alias.Description()
		if len(commitment) != 32 || description.Algorithm == "" || description.Profile == "" || description.KeyID == "" {
			return nil, false
		}
		var value [32]byte
		copy(value[:], commitment)
		key := entityKey{
			resource: resource, scope: scope, scoped: scoped,
			algorithm: description.Algorithm, profile: description.Profile, keyID: description.KeyID, commitment: value,
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, false
		}
		seen[key] = struct{}{}
		result = append(result, key)
	}
	return result, true
}

func headForKeys(heads map[entityKey]entityHead, keys []entityKey) (entityHead, bool, bool) {
	var result entityHead
	found := false
	for _, key := range keys {
		head, ok := heads[key]
		if !ok {
			continue
		}
		if found && head != result {
			return entityHead{}, false, false
		}
		result = head
		found = true
	}
	return result, found, true
}

func (s *Store) isClosed() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.closed
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return audit.Failure(audit.Closed, errors.New("auditmemory: store is closed"))
	}
	s.closed = true
	return nil
}

type source struct{ identity *log }

func (s *source) Exec(context.Context, string, ...any) (crud.Result, error) {
	return crud.Result{}, errors.New("auditmemory: source does not execute business statements")
}
func (s *source) Query(context.Context, string, ...any) (crud.Rows, error) {
	return nil, errors.New("auditmemory: source does not execute business queries")
}
func (s *source) Dialect() crud.Dialect { return crud.Postgres{} }
func (s *source) DataSource() any       { return s.identity }

type Tx struct {
	store     *Store
	executor  *transactionExecutor
	authority audit.Authority
	mu        sync.Mutex
	state     uint8
	requests  []audit.AppendRequest
}

type transactionExecutor struct{ tx *Tx }

func (e *transactionExecutor) Exec(context.Context, string, ...any) (crud.Result, error) {
	return crud.Result{}, errors.New("auditmemory: transaction does not execute business statements")
}
func (e *transactionExecutor) Query(context.Context, string, ...any) (crud.Rows, error) {
	return nil, errors.New("auditmemory: transaction does not execute business queries")
}
func (e *transactionExecutor) DataSource() any     { return e.tx.store.log }
func (e *transactionExecutor) InTransaction() bool { return e != nil && e.tx != nil && e.tx.live() }

func (s *Store) Begin(ctx context.Context) (*Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.isClosed() {
		return nil, audit.Failure(audit.Closed, errors.New("auditmemory: store is closed"))
	}
	tx := &Tx{store: s}
	tx.executor = &transactionExecutor{tx: tx}
	tx.authority, _ = audit.AuthorityFor(s.source, tx)
	return tx, nil
}

func WithTransaction(ctx context.Context, tx *Tx) context.Context {
	if tx == nil || tx.executor == nil || !tx.live() {
		return crud.BindExecutor(ctx, nil, nil)
	}
	return crud.BindExecutor(ctx, tx.store.source, tx.executor)
}

func (tx *Tx) live() bool {
	if tx == nil {
		return false
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.state == 0
}

func (tx *Tx) Commit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.state != 0 {
		return audit.Failure(audit.Conflict, errors.New("auditmemory: transaction is settled"))
	}
	tx.store.log.mu.Lock()
	defer tx.store.log.mu.Unlock()
	staged := &log{
		backing: tx.store.log.backing, backingID: tx.store.log.backingID, logID: tx.store.log.logID,
		limits: tx.store.log.limits, catalogs: tx.store.log.catalogs, manifests: tx.store.log.manifests,
		revisions: cloneRevisions(tx.store.log.revisions), idempotency: cloneIdempotency(tx.store.log.idempotency),
		heads: cloneHeads(tx.store.log.heads), sequence: tx.store.log.sequence,
	}
	for _, request := range tx.requests {
		if _, err := appendLocked(staged, tx.store.clock, request, tx.authority); err != nil {
			tx.state = 2
			return err
		}
	}
	tx.store.log.revisions = staged.revisions
	tx.store.log.idempotency = staged.idempotency
	tx.store.log.heads = staged.heads
	tx.store.log.sequence = staged.sequence
	tx.state = 1
	return nil
}

func cloneRevisions(input map[audit.RevisionID]revisionRecord) map[audit.RevisionID]revisionRecord {
	output := make(map[audit.RevisionID]revisionRecord, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func cloneIdempotency(input map[idempotencyKey]audit.RevisionID) map[idempotencyKey]audit.RevisionID {
	output := make(map[idempotencyKey]audit.RevisionID, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func cloneHeads(input map[entityKey]entityHead) map[entityKey]entityHead {
	output := make(map[entityKey]entityHead, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func (tx *Tx) Rollback(context.Context) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	if tx.state != 0 {
		return audit.Failure(audit.Conflict, errors.New("auditmemory: transaction is settled"))
	}
	tx.requests = nil
	tx.state = 2
	return nil
}

type execution struct {
	store     *Store
	tx        *Tx
	authority audit.Authority
}

func (e *execution) Authority() audit.Authority { return e.authority }

func (e *execution) EntityHead(ctx context.Context, request audit.EntityHeadRequest) (audit.EntityHeadResult, error) {
	if err := ctx.Err(); err != nil {
		return audit.EntityHeadResult{}, err
	}
	view := request.View()
	keys, ok := entityKeys(view.Resource, view.Scope, view.ScopePresent, view.Commitments)
	if !ok {
		return audit.EntityHeadResult{}, audit.Failure(audit.Refused, errors.New("auditmemory: subject commitment is invalid"))
	}
	e.store.log.mu.RLock()
	head, found, consistent := headForKeys(e.store.log.heads, keys)
	e.store.log.mu.RUnlock()
	if !consistent {
		return audit.EntityHeadResult{}, audit.Failure(audit.Corrupt, errors.New("auditmemory: entity aliases diverge"))
	}
	if !found {
		return audit.NewEntityHeadResult(request, audit.EntityHeadResultData{State: audit.EntityGenesis, Chain: view.Candidate, Authority: e.authority})
	}
	state := audit.EntityExisting
	if head.terminal {
		state = audit.EntityTerminal
	}
	return audit.NewEntityHeadResult(request, audit.EntityHeadResultData{State: state, Chain: head.chain, Previous: head.previous, Authority: e.authority})
}

func (e *execution) Append(ctx context.Context, request audit.AppendRequest) (audit.AppendResult, error) {
	if err := ctx.Err(); err != nil {
		return audit.AppendResult{}, err
	}
	e.tx.mu.Lock()
	defer e.tx.mu.Unlock()
	if e.tx.state != 0 {
		return audit.AppendResult{}, audit.Failure(audit.Closed, errors.New("auditmemory: transaction is settled"))
	}
	view := request.View().Revision.View()
	positionBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(positionBytes, uint64(len(e.tx.requests)+1))
	position, _ := audit.NewStorePosition(positionBytes)
	stored, err := audit.NewStoredHeader(audit.StoredHeaderData{Header: view.Header, Intent: request.View().Intent, RecordedAt: e.store.clock().UTC(), Position: position})
	if err != nil {
		return audit.AppendResult{}, audit.Failure(audit.Corrupt, err)
	}
	e.tx.requests = append(e.tx.requests, request)
	return audit.NewAppendResult(request, stored, audit.Inserted, e.authority)
}

var _ audit.Writer = (*Store)(nil)
var _ audit.Log = (*Store)(nil)
var _ audit.Execution = (*execution)(nil)
