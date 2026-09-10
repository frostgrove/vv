package auditpg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
)

type store struct {
	configured configured
	state      atomic.Pointer[readiness]
	closed     atomic.Bool
}

type Store struct {
	value *store
}

func New(spec Spec) (*Store, error) {
	configured, err := configure(spec)
	if err != nil {
		return nil, err
	}
	return &Store{value: &store{configured: configured}}, nil
}

func (s *Store) Capabilities() audit.Capabilities { return capabilities() }

func (s *Store) Limits() audit.Limits {
	if s == nil || s.value == nil {
		return audit.Limits{}
	}
	return s.value.configured.limits
}

func (s *Store) Backing() audit.Backing {
	if s == nil || s.value == nil || loadReadiness(&s.value.state) == nil {
		return audit.Backing{}
	}
	return s.value.configured.backing
}

func (s *Store) BackingID() audit.BackingID {
	if s == nil || s.value == nil || loadReadiness(&s.value.state) == nil {
		return audit.BackingID{}
	}
	return loadReadiness(&s.value.state).backingID
}

func (s *Store) LogID() audit.LogID {
	if s == nil || s.value == nil || loadReadiness(&s.value.state) == nil {
		return audit.LogID{}
	}
	return loadReadiness(&s.value.state).logID
}

func (s *Store) Catalogs() audit.StoreCatalogState {
	if s == nil || s.value == nil || loadReadiness(&s.value.state) == nil {
		return audit.NewEmptyStoreCatalogState()
	}
	return loadReadiness(&s.value.state).catalogs
}

func (s *Store) TransactionSource() any {
	if s == nil || s.value == nil {
		return nil
	}
	return s.value.configured.source
}

func (s *Store) Schema() Schema {
	if s == nil || s.value == nil {
		return Schema{}
	}
	return s.value.configured.schema
}

func (s *Store) Check(ctx context.Context) error {
	if s == nil || s.value == nil {
		return fmt.Errorf("%w: store is nil", ErrSpec)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.value.closed.Load() {
		return audit.Failure(audit.Closed, errors.New("auditpg: store is closed"))
	}
	s.value.state.Store(nil)
	conn, err := s.value.configured.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("auditpg: check checkout: %w", err)
	}
	defer func() { _ = conn.Close() }()
	ready, err := readReady(ctx, conn, s.value.configured.schema)
	if err != nil {
		return err
	}
	s.value.state.Store(&ready)
	return nil
}

func (s *Store) BindTransaction(executor crud.Executor) (audit.Execution, error) {
	tx, ok := crudsql.TopLevelTransaction(executor)
	if !ok {
		return nil, audit.Failure(audit.Refused, errors.New("auditpg: executor is not a proven top-level crudsql transaction"))
	}
	ready, err := s.ready()
	if err != nil {
		return nil, err
	}
	authority, err := audit.AuthorityFor(s.value.configured.source, tx)
	if err != nil {
		return nil, audit.Failure(audit.Refused, err)
	}
	return &execution{store: s, tx: tx, ready: ready, authority: authority}, nil
}

func (s *Store) Append(ctx context.Context, request audit.AppendRequest) (audit.AppendResult, error) {
	ready, err := s.ready()
	if err != nil {
		return audit.AppendResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return audit.AppendResult{}, err
	}
	conn, err := s.value.configured.db.Conn(ctx)
	if err != nil {
		return audit.AppendResult{}, classifySQL(err, false)
	}
	defer func() { _ = conn.Close() }()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return audit.AppendResult{}, classifySQL(err, false)
	}
	defer func() { _ = tx.Rollback() }()
	authority, err := audit.AuthorityFor(s.value.configured.source, tx)
	if err != nil {
		return audit.AppendResult{}, audit.Failure(audit.Refused, err)
	}
	execution := &execution{store: s, tx: tx, ready: ready, authority: authority}
	result, err := execution.Append(ctx, request)
	if err != nil {
		return audit.AppendResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return audit.AppendResult{}, classifySQL(err, true)
	}
	return result, nil
}

func (s *Store) LookupIdempotency(ctx context.Context, request audit.IdempotencyLookupRequest) (audit.IdempotencyLookupResult, error) {
	ready, err := s.ready()
	if err != nil {
		return audit.IdempotencyLookupResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return audit.IdempotencyLookupResult{}, err
	}
	view := request.View()
	if !ready.catalogs.HasActive() || view.Catalog != ready.catalogs.Active() || view.CatalogSet != ready.catalogs.SetDigest() || view.Domain.Token == (audit.IdempotencyToken{}) {
		return audit.IdempotencyLookupResult{}, audit.Failure(audit.StaleCatalog, errors.New("auditpg: idempotency lookup catalog is stale"))
	}
	q := quoteIdentifier(s.value.configured.schema.Name)
	if view.Domain.Kind != audit.RecordIdempotencyDomain {
		return s.lookupAttemptIdempotency(ctx, q, request)
	}
	row := s.value.configured.db.QueryRowContext(ctx, `SELECT r.wire, r.intent, r.recorded_at, r.position
FROM `+q+`.idempotency i JOIN `+q+`.revisions r ON r.revision_id=i.revision_id
WHERE i.catalog_id=$1 AND i.operation=$2 AND i.token=$3`, string(view.Catalog.ID), string(view.Domain.Operation), view.Domain.Token[:])
	stored, err := scanStored(row)
	if errors.Is(err, sql.ErrNoRows) {
		return audit.NewIdempotencyLookupResult(request, audit.IdempotencyLookupResultData{State: audit.AbsentNow})
	}
	if err != nil {
		return audit.IdempotencyLookupResult{}, classifyStored(err)
	}
	return audit.NewIdempotencyLookupResult(request, audit.IdempotencyLookupResultData{State: audit.Found, Stored: stored})
}

func (s *Store) lookupAttemptIdempotency(ctx context.Context, schema string, request audit.IdempotencyLookupRequest) (audit.IdempotencyLookupResult, error) {
	view := request.View()
	if view.Domain.Kind != audit.AttemptStartIdempotencyDomain && view.Domain.Kind != audit.AttemptCheckpointIdempotencyDomain && view.Domain.Kind != audit.AttemptFinishIdempotencyDomain || view.Idempotency.Domain() != audit.CommitIdempotency {
		return audit.IdempotencyLookupResult{}, audit.Failure(audit.Refused, errors.New("auditpg: attempt idempotency lookup is invalid"))
	}
	chain := view.Domain.Attempt
	if view.Domain.Kind == audit.AttemptStartIdempotencyDomain {
		chain = audit.AttemptChainID{}
	} else if chain == (audit.AttemptChainID{}) {
		return audit.IdempotencyLookupResult{}, audit.Failure(audit.Refused, errors.New("auditpg: attempt idempotency chain is absent"))
	}
	active := view.Idempotency.Active()
	description := active.Description()
	row := s.value.configured.db.QueryRowContext(ctx, `SELECT r.wire, r.intent, r.recorded_at, r.position
FROM `+schema+`.attempt_idempotency i JOIN `+schema+`.revisions r ON r.revision_id=i.revision_id
WHERE i.domain=$1 AND i.catalog_id=$2 AND i.chain_id=$3
AND i.algorithm=$4 AND i.profile=$5 AND i.key_id=$6 AND i.commitment=$7`,
		int(view.Domain.Kind), string(view.Domain.Catalog), chain[:],
		description.Algorithm, description.Profile, description.KeyID, active.Bytes())
	stored, err := scanStored(row)
	if errors.Is(err, sql.ErrNoRows) {
		return audit.NewIdempotencyLookupResult(request, audit.IdempotencyLookupResultData{State: audit.AbsentNow})
	}
	if err != nil {
		return audit.IdempotencyLookupResult{}, classifyStored(err)
	}
	return audit.NewIdempotencyLookupResult(request, audit.IdempotencyLookupResultData{State: audit.Found, Stored: stored})
}

func (s *Store) Lookup(ctx context.Context, request audit.LookupRequest) (audit.LookupResult, error) {
	ready, err := s.ready()
	if err != nil {
		return audit.LookupResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return audit.LookupResult{}, err
	}
	view := request.View()
	if !reconcileMatches(view.Key.Bytes(), ready.backingID, ready.logID) {
		return audit.LookupResult{}, audit.Failure(audit.Refused, errors.New("auditpg: reconcile key belongs to another store"))
	}
	q := quoteIdentifier(s.value.configured.schema.Name)
	stored, err := scanStored(s.value.configured.db.QueryRowContext(ctx, `SELECT wire, intent, recorded_at, position
FROM `+q+`.revisions WHERE revision_id=$1 AND catalog_id=$2 AND operation=$3`, view.Revision[:], string(view.Catalog), string(view.Operation)))
	if errors.Is(err, sql.ErrNoRows) {
		return audit.NewLookupResult(request, audit.LookupResultData{State: audit.AbsentNow, Visibility: audit.Committed})
	}
	if err != nil {
		return audit.LookupResult{}, classifyStored(err)
	}
	return audit.NewLookupResult(request, audit.LookupResultData{State: audit.Found, Stored: stored, Visibility: audit.Committed})
}

func classifyStored(err error) error {
	if errors.Is(err, ErrSchemaMismatch) {
		return audit.Failure(audit.Corrupt, err)
	}
	return classifySQL(err, false)
}

func reconcileMatches(wire []byte, backing audit.BackingID, log audit.LogID) bool {
	return len(wire) >= 33 && wire[0] == 1 && string(wire[1:17]) == string(backing[:]) && string(wire[17:33]) == string(log[:])
}

func scanStored(row interface{ Scan(...any) error }) (audit.StoredHeader, error) {
	var wire, intentBytes []byte
	var recordedAt sql.NullTime
	var position int64
	if err := row.Scan(&wire, &intentBytes, &recordedAt, &position); err != nil {
		return audit.StoredHeader{}, err
	}
	view, err := decodeEvidence(wire)
	if err != nil {
		return audit.StoredHeader{}, err
	}
	intent, ok := digest32[audit.AppendIntentDigest](intentBytes)
	if !ok || !recordedAt.Valid {
		return audit.StoredHeader{}, errorsWire("stored header")
	}
	storePosition, err := positionOf(position)
	if err != nil {
		return audit.StoredHeader{}, err
	}
	data := audit.StoredHeaderData{Header: view.Header, Intent: intent, RecordedAt: recordedAt.Time.UTC(), Position: storePosition}
	for _, item := range view.Items {
		if item.Kind != audit.AttemptItem {
			continue
		}
		if data.AttemptTransitionPresent {
			return audit.StoredHeader{}, errorsWire("stored attempt header")
		}
		data.AttemptTransitionPresent = true
		data.AttemptTransition = item.Attempt
		data.AttemptProjection = storedAttemptProjection(item)
	}
	stored, err := audit.NewStoredHeader(data)
	if err != nil {
		return audit.StoredHeader{}, errorsWire("stored header")
	}
	return stored, nil
}

func (s *Store) ready() (readiness, error) {
	if s == nil || s.value == nil {
		return readiness{}, fmt.Errorf("%w: store is nil", ErrSpec)
	}
	current := loadReadiness(&s.value.state)
	if err := requireReady(current, s.value.closed.Load()); err != nil {
		return readiness{}, err
	}
	return *current, nil
}

func (s *Store) Close() error {
	if s == nil || s.value == nil {
		return fmt.Errorf("%w: store is nil", ErrSpec)
	}
	if !s.value.closed.CompareAndSwap(false, true) {
		return audit.Failure(audit.Closed, errors.New("auditpg: store is closed"))
	}
	return nil
}

var _ audit.Writer = (*Store)(nil)
