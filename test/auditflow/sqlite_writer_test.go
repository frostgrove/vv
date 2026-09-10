package auditflow_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
)

type sqliteAuditWriter struct {
	database     *sql.DB
	source       crudsql.DB
	state        audit.StoreCatalogState
	capabilities audit.Capabilities
	limits       audit.Limits
	backing      audit.Backing
	backingID    audit.BackingID
	logID        audit.LogID
}

type sqliteAuditExecution struct {
	writer      *sqliteAuditWriter
	transaction *sql.Tx
	authority   audit.Authority
}

type revisionSummary struct {
	action           string
	actorKind        int
	actorProvenance  int
	actorMode        int
	actorPlaintext   []byte
	actorToken       []byte
	scopeProvenance  int
	scopeMode        int
	scopePlaintext   []byte
	scopeToken       []byte
	subjectMode      int
	subjectPlaintext []byte
	subjectToken     []byte
	fieldsRedacted   int
}

func newSQLiteAuditWriter(database *sql.DB, source crudsql.DB, catalogs *audit.CatalogSet) (*sqliteAuditWriter, error) {
	state, err := audit.NewStoreCatalogState(catalogs.Active(), catalogs.Digest())
	if err != nil {
		return nil, err
	}
	capabilities, err := audit.NewCapabilities(audit.CapabilitySpec{
		Transactions: audit.SupportSupported, CrossSystemAtomic: audit.SupportSupported,
		Persistence: audit.SupportUnsupported, Idempotency: audit.SupportUnsupported,
		Reconciliation: audit.SupportUnsupported, StableSearch: audit.SupportUnsupported,
		ExactInspection: audit.SupportUnsupported, AttemptLifecycle: audit.SupportUnsupported,
		Holds: audit.SupportUnsupported, PurgePlanning: audit.SupportUnsupported,
	})
	if err != nil {
		return nil, err
	}
	limits, err := audit.NewLimits(audit.LimitSpec{
		RevisionBytes: 1 << 20, AppendRequestBytes: 2 << 20,
		PageRevisions: 100, PageBytes: 2 << 20, ExactTargets: 100, ExactBytes: 2 << 20,
		PositionBytes: 64, InventoryCandidates: 100, InventoryCohorts: 100, InventoryBytes: 2 << 20,
		SnapshotBytes: 1 << 20, SearchCohortRevisions: 1000, SearchCohortBytes: 4 << 20,
		AttemptTransitions: 32, AttemptStateBytes: 1 << 20, AttemptOpenLifetime: 24 * time.Hour,
	})
	if err != nil {
		return nil, err
	}
	backing, err := audit.BackingFor(database)
	if err != nil {
		return nil, err
	}
	writer := &sqliteAuditWriter{
		database: database, source: source, state: state,
		capabilities: capabilities, limits: limits, backing: backing,
	}
	copy(writer.backingID[:], []byte("auditflow-backing"))
	copy(writer.logID[:], []byte("auditflow-log-id"))
	return writer, nil
}

func (w *sqliteAuditWriter) Capabilities() audit.Capabilities  { return w.capabilities }
func (w *sqliteAuditWriter) Limits() audit.Limits              { return w.limits }
func (w *sqliteAuditWriter) Backing() audit.Backing            { return w.backing }
func (w *sqliteAuditWriter) BackingID() audit.BackingID        { return w.backingID }
func (w *sqliteAuditWriter) LogID() audit.LogID                { return w.logID }
func (w *sqliteAuditWriter) Catalogs() audit.StoreCatalogState { return w.state }
func (w *sqliteAuditWriter) TransactionSource() any            { return w.source }

func (w *sqliteAuditWriter) BindTransaction(executor crud.Executor) (audit.Execution, error) {
	transaction, ok := crudsql.TopLevelTransaction(executor)
	if !ok {
		return nil, audit.Failure(audit.Refused, errors.New("auditflow: transaction is not the CRUD root"))
	}
	authority, err := audit.AuthorityFor(w.database, transaction)
	if err != nil {
		return nil, err
	}
	return &sqliteAuditExecution{writer: w, transaction: transaction, authority: authority}, nil
}

func (w *sqliteAuditWriter) Append(ctx context.Context, request audit.AppendRequest) (audit.AppendResult, error) {
	transaction, err := w.database.BeginTx(ctx, nil)
	if err != nil {
		return audit.AppendResult{}, audit.Failure(audit.NotWritten, err)
	}
	authority, err := audit.AuthorityFor(w.database, transaction)
	if err != nil {
		_ = transaction.Rollback()
		return audit.AppendResult{}, err
	}
	execution := sqliteAuditExecution{writer: w, transaction: transaction, authority: authority}
	result, err := execution.Append(ctx, request)
	if err != nil {
		_ = transaction.Rollback()
		return audit.AppendResult{}, err
	}
	if err := transaction.Commit(); err != nil {
		return audit.AppendResult{}, audit.Failure(audit.Unconfirmed, err)
	}
	return result, nil
}

func (w *sqliteAuditWriter) LookupIdempotency(_ context.Context, request audit.IdempotencyLookupRequest) (audit.IdempotencyLookupResult, error) {
	return audit.NewIdempotencyLookupResult(request, audit.IdempotencyLookupResultData{State: audit.AbsentNow})
}

func (w *sqliteAuditWriter) Lookup(_ context.Context, request audit.LookupRequest) (audit.LookupResult, error) {
	return audit.NewLookupResult(request, audit.LookupResultData{
		State: audit.AbsentNow, Visibility: audit.Committed,
	})
}

func (e *sqliteAuditExecution) Authority() audit.Authority { return e.authority }

func (e *sqliteAuditExecution) EntityHead(ctx context.Context, request audit.EntityHeadRequest) (audit.EntityHeadResult, error) {
	view := request.View()
	commitment := view.Commitments.Active().Bytes()
	if len(commitment) != 32 {
		return audit.EntityHeadResult{}, audit.Failure(audit.Refused, errors.New("auditflow: invalid subject commitment"))
	}
	var chainBytes, leafBytes []byte
	var terminal int
	err := e.transaction.QueryRowContext(ctx,
		`SELECT chain, leaf, terminal FROM auditflow_entity_heads WHERE resource = ? AND scoped = ? AND scope = ? AND subject = ?`,
		string(view.Resource), boolInt(view.ScopePresent), view.Scope[:], commitment,
	).Scan(&chainBytes, &leafBytes, &terminal)
	if errors.Is(err, sql.ErrNoRows) {
		return audit.NewEntityHeadResult(request, audit.EntityHeadResultData{
			State: audit.EntityGenesis, Chain: view.Candidate, Authority: e.authority,
		})
	}
	if err != nil {
		return audit.EntityHeadResult{}, audit.Failure(audit.NotWritten, err)
	}
	if len(chainBytes) != 32 || len(leafBytes) != 32 {
		return audit.EntityHeadResult{}, audit.Failure(audit.Corrupt, errors.New("auditflow: malformed entity head"))
	}
	var chain audit.EntityChainID
	var leaf audit.LeafDigest
	copy(chain[:], chainBytes)
	copy(leaf[:], leafBytes)
	state := audit.EntityExisting
	if terminal != 0 {
		state = audit.EntityTerminal
	}
	return audit.NewEntityHeadResult(request, audit.EntityHeadResultData{
		State: state, Chain: chain, Previous: leaf, Authority: e.authority,
	})
}

func (e *sqliteAuditExecution) Append(ctx context.Context, request audit.AppendRequest) (audit.AppendResult, error) {
	view := request.View()
	revision := view.Revision.View()
	for _, item := range revision.Items {
		if item.Kind != audit.EntityItem {
			continue
		}
		binding, ok := entityBinding(view.Entities, item.Chain)
		if !ok || binding.Resource() != item.Resource {
			return audit.AppendResult{}, audit.Failure(audit.Refused, errors.New("auditflow: entity binding is missing"))
		}
		if err := e.advanceHead(ctx, binding, item); err != nil {
			return audit.AppendResult{}, err
		}
	}
	summary, err := summarizeRevision(revision)
	if err != nil {
		return audit.AppendResult{}, audit.Failure(audit.Refused, err)
	}
	result, err := e.transaction.ExecContext(ctx, `INSERT INTO auditflow_revisions (
		revision_id, action, actor_kind, actor_provenance, actor_mode, actor_plaintext, actor_token,
		scope_provenance, scope_mode, scope_plaintext, scope_token,
		subject_mode, subject_plaintext, subject_token, fields_redacted
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		revision.Header.RevisionID[:], summary.action,
		summary.actorKind, summary.actorProvenance, summary.actorMode, summary.actorPlaintext, summary.actorToken,
		summary.scopeProvenance, summary.scopeMode, summary.scopePlaintext, summary.scopeToken,
		summary.subjectMode, summary.subjectPlaintext, summary.subjectToken, summary.fieldsRedacted,
	)
	if err != nil {
		return audit.AppendResult{}, audit.Failure(audit.NotWritten, err)
	}
	sequence, err := result.LastInsertId()
	if err != nil {
		return audit.AppendResult{}, audit.Failure(audit.NotWritten, fmt.Errorf("auditflow: read revision position: %w", err))
	}
	if sequence <= 0 {
		return audit.AppendResult{}, audit.Failure(audit.NotWritten, errors.New("auditflow: invalid revision position"))
	}
	var positionBytes [8]byte
	binary.BigEndian.PutUint64(positionBytes[:], uint64(sequence))
	position, err := audit.NewStorePosition(positionBytes[:])
	if err != nil {
		return audit.AppendResult{}, err
	}
	stored, err := audit.NewStoredHeader(audit.StoredHeaderData{
		Header: revision.Header, Intent: view.Intent, RecordedAt: time.Now().UTC(), Position: position,
	})
	if err != nil {
		return audit.AppendResult{}, audit.Failure(audit.Corrupt, err)
	}
	return audit.NewAppendResult(request, stored, audit.Inserted, e.authority)
}

func (e *sqliteAuditExecution) advanceHead(ctx context.Context, binding audit.EntityAliasBinding, item audit.ItemWireView) error {
	scope, scoped := binding.Scope()
	commitment := binding.Commitments().Active().Bytes()
	if len(commitment) != 32 || scoped != (scope != (audit.EvidenceScopeCommitment{})) {
		return audit.Failure(audit.Refused, errors.New("auditflow: invalid entity binding"))
	}
	var existingChain, existingLeaf []byte
	var terminal int
	err := e.transaction.QueryRowContext(ctx,
		`SELECT chain, leaf, terminal FROM auditflow_entity_heads WHERE resource = ? AND scoped = ? AND scope = ? AND subject = ?`,
		string(binding.Resource()), boolInt(scoped), scope[:], commitment,
	).Scan(&existingChain, &existingLeaf, &terminal)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if item.Previous != (audit.LeafDigest{}) || binding.ChainID() != item.Chain {
			return audit.Failure(audit.Conflict, errors.New("auditflow: entity genesis changed"))
		}
	case err != nil:
		return audit.Failure(audit.NotWritten, err)
	case terminal != 0 || !bytes.Equal(existingChain, item.Chain[:]) || !bytes.Equal(existingLeaf, item.Previous[:]):
		return audit.Failure(audit.Conflict, errors.New("auditflow: entity head changed"))
	}
	_, err = e.transaction.ExecContext(ctx, `INSERT INTO auditflow_entity_heads
		(resource, scoped, scope, subject, chain, leaf, terminal) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(resource, scoped, scope, subject) DO UPDATE SET chain = excluded.chain, leaf = excluded.leaf, terminal = excluded.terminal`,
		string(binding.Resource()), boolInt(scoped), scope[:], commitment, item.Chain[:], item.Leaf[:],
		boolInt(item.Action == audit.Action(audit.EntityHardDeleted)),
	)
	if err != nil {
		return audit.Failure(audit.NotWritten, err)
	}
	return nil
}

func entityBinding(bindings []audit.EntityAliasBinding, chain audit.EntityChainID) (audit.EntityAliasBinding, bool) {
	for _, binding := range bindings {
		if binding.ChainID() == chain {
			return binding, true
		}
	}
	return audit.EntityAliasBinding{}, false
}

func summarizeRevision(revision audit.RevisionWireView) (revisionSummary, error) {
	if len(revision.Actors) != 1 || len(revision.Items) != 1 {
		return revisionSummary{}, errors.New("auditflow: expected one actor and one item")
	}
	actor := revision.Actors[0]
	item := revision.Items[0]
	summary := revisionSummary{
		action: string(item.Action), actorKind: int(actor.Kind), actorProvenance: int(actor.Provenance),
		actorMode: int(actor.Mode), actorPlaintext: actor.Reference.Plaintext, actorToken: tokenWire(actor.Mode, actor.Reference.Token),
		subjectMode: int(item.Subject.Mode), subjectPlaintext: item.Subject.Plaintext,
		subjectToken: tokenWire(item.Subject.Mode, item.Subject.Token), fieldsRedacted: boolInt(allFieldsRedacted(item)),
	}
	for _, fact := range revision.Context {
		if fact.Kind != audit.ScopeContext {
			continue
		}
		summary.scopeProvenance = int(fact.Provenance)
		summary.scopeMode = int(fact.Mode)
		summary.scopePlaintext = fact.Plaintext
		summary.scopeToken = tokenWire(fact.Mode, fact.Token)
	}
	if summary.scopeMode == 0 {
		return revisionSummary{}, errors.New("auditflow: scope evidence is missing")
	}
	return summary, nil
}

func allFieldsRedacted(item audit.ItemWireView) bool {
	seen := false
	check := func(value audit.StoredValueView) bool {
		if value.State == audit.ValueAbsent {
			return true
		}
		seen = true
		return value.State == audit.ValueRedacted && value.Redacted && len(value.Plaintext) == 0 && len(tokenWire(value.Mode, value.Token)) == 0
	}
	for _, value := range item.Values {
		if !check(value) {
			return false
		}
	}
	for _, change := range item.Changes {
		if !check(change.Before) || !check(change.After) {
			return false
		}
	}
	return seen
}

func tokenWire(mode audit.StorageMode, token audit.Token) []byte {
	if mode != audit.AsToken && mode != audit.AsIndexedProtected {
		return nil
	}
	return token.Bytes()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

var _ audit.Writer = (*sqliteAuditWriter)(nil)
var _ audit.Execution = (*sqliteAuditExecution)(nil)
