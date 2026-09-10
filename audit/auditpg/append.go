package auditpg

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"sort"

	"github.com/frostgrove/vv/audit"
)

type execution struct {
	store     *Store
	tx        *sql.Tx
	ready     readiness
	authority audit.Authority
	verified  bool
}

func (e *execution) Authority() audit.Authority {
	if e == nil {
		return audit.Authority{}
	}
	return e.authority
}

func (e *execution) ensure(ctx context.Context) error {
	if e == nil || e.store == nil || e.tx == nil || !e.authority.Valid() {
		return audit.Failure(audit.Refused, errors.New("auditpg: transaction execution is invalid"))
	}
	if e.verified {
		return nil
	}
	q := quoteIdentifier(e.store.value.configured.schema.Name)
	var singleton bool
	if err := e.tx.QueryRowContext(ctx, `SELECT singleton FROM `+q+`.settings WHERE singleton FOR SHARE`).Scan(&singleton); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return audit.Failure(audit.Refused, ErrSchemaMismatch)
		}
		return classifySQL(err, false)
	}
	if !singleton {
		return audit.Failure(audit.Refused, errors.New("auditpg: store readiness lock failed"))
	}
	actual, err := readReady(ctx, e.tx, e.store.value.configured.schema)
	if err != nil {
		return audit.Failure(audit.Refused, err)
	}
	if !sameReady(actual, e.ready) {
		return audit.Failure(audit.Conflict, errors.New("auditpg: transaction belongs to another or changed audit store"))
	}
	e.verified = true
	return nil
}

func (e *execution) EntityHead(ctx context.Context, request audit.EntityHeadRequest) (audit.EntityHeadResult, error) {
	if err := ctx.Err(); err != nil {
		return audit.EntityHeadResult{}, err
	}
	if err := e.ensure(ctx); err != nil {
		return audit.EntityHeadResult{}, err
	}
	view := request.View()
	aliases := orderedAliases(view.Commitments.Aliases())
	if view.Resource == "" || view.Candidate == (audit.EntityChainID{}) || len(aliases) == 0 {
		return audit.EntityHeadResult{}, audit.Failure(audit.Refused, errors.New("auditpg: entity head request is invalid"))
	}
	q := quoteIdentifier(e.store.value.configured.schema.Name)
	head, found, err := findAliasHead(ctx, e.tx, q, view.Resource, view.ScopePresent, view.Scope, aliases, true)
	if err != nil {
		return audit.EntityHeadResult{}, err
	}
	if !found {
		return audit.NewEntityHeadResult(request, audit.EntityHeadResultData{State: audit.EntityGenesis, Chain: view.Candidate, Authority: e.authority})
	}
	state := audit.EntityExisting
	if head.terminal {
		state = audit.EntityTerminal
	}
	return audit.NewEntityHeadResult(request, audit.EntityHeadResultData{State: state, Chain: head.chain, Previous: head.leaf, Authority: e.authority})
}

func (e *execution) Append(ctx context.Context, request audit.AppendRequest) (audit.AppendResult, error) {
	if err := ctx.Err(); err != nil {
		return audit.AppendResult{}, err
	}
	if err := e.ensure(ctx); err != nil {
		return audit.AppendResult{}, err
	}
	requestView := request.View()
	view := requestView.Revision.View()
	if requestView.Intent == (audit.AppendIntentDigest{}) || view.Header.RevisionID == (audit.RevisionID{}) || len(view.Items) == 0 {
		return audit.AppendResult{}, audit.Failure(audit.Refused, errors.New("auditpg: append request is invalid"))
	}
	if requestView.Hold.Kind != 0 || requestView.Attempt.Chain != (audit.AttemptChainID{}) || len(requestView.Attempts) != 0 || len(requestView.HoldIDs) != 0 || len(requestView.HoldMatters) != 0 {
		return audit.AppendResult{}, audit.Failure(audit.Refused, errors.New("auditpg: attempt and hold appends are not supported by this profile version"))
	}
	for _, item := range view.Items {
		if item.Kind != audit.EventItem && item.Kind != audit.EntityItem {
			return audit.AppendResult{}, audit.Failure(audit.Refused, errors.New("auditpg: this profile version accepts event and entity items only"))
		}
		if item.Leaf == (audit.LeafDigest{}) || item.Kind == audit.EntityItem && item.Chain == (audit.EntityChainID{}) {
			return audit.AppendResult{}, audit.Failure(audit.Refused, errors.New("auditpg: item integrity identity is absent"))
		}
	}
	if !e.ready.catalogs.HasActive() || view.Header.Log != e.ready.logID || view.Header.Catalog != e.ready.catalogs.Active() || view.Header.CatalogSet != e.ready.catalogs.SetDigest() {
		return audit.AppendResult{}, audit.Failure(audit.StaleCatalog, errors.New("auditpg: append catalog or log is stale"))
	}
	wire, err := encodeEvidence(view)
	if err != nil {
		return audit.AppendResult{}, audit.Failure(audit.Refused, err)
	}
	limits := e.store.value.configured.limits.View()
	if uint64(len(wire)) > limits.RevisionBytes || uint64(len(wire))+uint64(len(requestView.Entities))*512 > limits.AppendRequestBytes {
		return audit.AppendResult{}, audit.Failure(audit.Refused, errors.New("auditpg: append exceeds configured byte limits"))
	}
	q := quoteIdentifier(e.store.value.configured.schema.Name)
	if view.Header.HasIdempotency {
		stored, replay, err := e.claimIdempotency(ctx, q, view.Header)
		if err != nil {
			return audit.AppendResult{}, err
		}
		if replay {
			return audit.NewAppendResult(request, stored, audit.Replayed, e.authority)
		}
	}
	stored, found, err := loadStoredRevision(ctx, e.tx, q, view.Header.RevisionID, true)
	if err != nil {
		return audit.AppendResult{}, err
	}
	if found {
		if stored.Intent() != requestView.Intent {
			return audit.AppendResult{}, audit.Failure(audit.Conflict, errors.New("auditpg: revision identity changed meaning"))
		}
		return audit.NewAppendResult(request, stored, audit.Replayed, e.authority)
	}
	var position int64
	var recordedAt sql.NullTime
	err = e.tx.QueryRowContext(ctx, `INSERT INTO `+q+`.revisions
(revision_id, catalog_id, catalog_generation, operation, semantic, intent, wire)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (revision_id) DO NOTHING
RETURNING position, recorded_at`, view.Header.RevisionID[:], string(view.Header.Catalog.ID), int64(view.Header.Catalog.Generation), string(view.Header.Operation), view.Header.Semantic[:], requestView.Intent[:], wire).Scan(&position, &recordedAt)
	if errors.Is(err, sql.ErrNoRows) {
		stored, found, err = loadStoredRevision(ctx, e.tx, q, view.Header.RevisionID, true)
		if err != nil {
			return audit.AppendResult{}, err
		}
		if !found || stored.Intent() != requestView.Intent {
			return audit.AppendResult{}, audit.Failure(audit.Conflict, errors.New("auditpg: revision identity changed concurrently"))
		}
		return audit.NewAppendResult(request, stored, audit.Replayed, e.authority)
	}
	if err != nil {
		return audit.AppendResult{}, classifySQL(err, true)
	}
	if !recordedAt.Valid {
		return audit.AppendResult{}, audit.Failure(audit.Corrupt, errors.New("auditpg: database returned no recorded time"))
	}
	if err := e.applyEntities(ctx, q, requestView.Entities, view.Items, view.Header.RevisionID); err != nil {
		return audit.AppendResult{}, err
	}
	storePosition, err := positionOf(position)
	if err != nil {
		return audit.AppendResult{}, audit.Failure(audit.Corrupt, err)
	}
	stored, err = audit.NewStoredHeader(audit.StoredHeaderData{Header: view.Header, Intent: requestView.Intent, RecordedAt: recordedAt.Time.UTC(), Position: storePosition})
	if err != nil {
		return audit.AppendResult{}, audit.Failure(audit.Corrupt, err)
	}
	return audit.NewAppendResult(request, stored, audit.Inserted, e.authority)
}

func (e *execution) claimIdempotency(ctx context.Context, schema string, header audit.RevisionHeaderView) (audit.StoredHeader, bool, error) {
	if _, err := e.tx.ExecContext(ctx, `INSERT INTO `+schema+`.idempotency
(catalog_id, operation, token, semantic, revision_id) VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (catalog_id, operation, token) DO NOTHING`, string(header.Catalog.ID), string(header.Operation), header.Idempotency[:], header.Semantic[:], header.RevisionID[:]); err != nil {
		return audit.StoredHeader{}, false, classifySQL(err, true)
	}
	var semantic, revisionBytes []byte
	if err := e.tx.QueryRowContext(ctx, `SELECT semantic, revision_id FROM `+schema+`.idempotency
WHERE catalog_id=$1 AND operation=$2 AND token=$3 FOR UPDATE`, string(header.Catalog.ID), string(header.Operation), header.Idempotency[:]).Scan(&semantic, &revisionBytes); err != nil {
		return audit.StoredHeader{}, false, classifySQL(err, true)
	}
	if !bytes.Equal(semantic, header.Semantic[:]) {
		return audit.StoredHeader{}, false, audit.Failure(audit.Conflict, errors.New("auditpg: idempotency key changed meaning"))
	}
	revision, ok := id16[audit.RevisionID](revisionBytes)
	if !ok {
		return audit.StoredHeader{}, false, audit.Failure(audit.Corrupt, errors.New("auditpg: idempotency revision is malformed"))
	}
	if revision == header.RevisionID {
		return audit.StoredHeader{}, false, nil
	}
	stored, found, err := loadStoredRevision(ctx, e.tx, schema, revision, true)
	if err != nil {
		return audit.StoredHeader{}, false, err
	}
	if !found || stored.Revision().Semantic != header.Semantic {
		return audit.StoredHeader{}, false, audit.Failure(audit.Corrupt, errors.New("auditpg: idempotency row has no matching revision"))
	}
	return stored, true, nil
}

type entityHead struct {
	chain    audit.EntityChainID
	leaf     audit.LeafDigest
	terminal bool
}

func orderedAliases(input []audit.IdentityCommitment) []audit.IdentityCommitment {
	aliases := append([]audit.IdentityCommitment(nil), input...)
	sort.Slice(aliases, func(left, right int) bool {
		leftDescription, rightDescription := aliases[left].Description(), aliases[right].Description()
		leftKey := leftDescription.Algorithm + "\x00" + leftDescription.Profile + "\x00" + leftDescription.KeyID + "\x00" + string(aliases[left].Bytes())
		rightKey := rightDescription.Algorithm + "\x00" + rightDescription.Profile + "\x00" + rightDescription.KeyID + "\x00" + string(aliases[right].Bytes())
		return leftKey < rightKey
	})
	return aliases
}

func findAliasHead(ctx context.Context, tx *sql.Tx, schema string, resource audit.Resource, scopePresent bool, scope audit.EvidenceScopeCommitment, aliases []audit.IdentityCommitment, lock bool) (entityHead, bool, error) {
	var result entityHead
	found := false
	suffix := ""
	if lock {
		suffix = " FOR UPDATE OF c"
	}
	for _, alias := range aliases {
		description := alias.Description()
		var chainBytes, leafBytes []byte
		var terminal bool
		err := tx.QueryRowContext(ctx, `SELECT c.chain_id, c.leaf, c.terminal
FROM `+schema+`.entity_aliases a JOIN `+schema+`.entity_chains c ON c.chain_id=a.chain_id
WHERE a.resource=$1 AND a.scope_present=$2 AND a.scope=$3 AND a.algorithm=$4 AND a.profile=$5 AND a.key_id=$6 AND a.commitment=$7`+suffix,
			string(resource), scopePresent, scope[:], description.Algorithm, description.Profile, description.KeyID, alias.Bytes()).Scan(&chainBytes, &leafBytes, &terminal)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return entityHead{}, false, classifySQL(err, false)
		}
		chain, chainOK := digest32[audit.EntityChainID](chainBytes)
		leaf := audit.LeafDigest{}
		leafOK := len(leafBytes) == 0
		if len(leafBytes) > 0 {
			leaf, leafOK = digest32[audit.LeafDigest](leafBytes)
		}
		if !chainOK || !leafOK {
			return entityHead{}, false, audit.Failure(audit.Corrupt, errors.New("auditpg: entity head is malformed"))
		}
		candidate := entityHead{chain: chain, leaf: leaf, terminal: terminal}
		if found && candidate != result {
			return entityHead{}, false, audit.Failure(audit.Corrupt, errors.New("auditpg: entity aliases diverge"))
		}
		result, found = candidate, true
	}
	return result, found, nil
}

func (e *execution) applyEntities(ctx context.Context, schema string, bindings []audit.EntityAliasBinding, items []audit.ItemWireView, revision audit.RevisionID) error {
	byChain := make(map[audit.EntityChainID]audit.EntityAliasBinding, len(bindings))
	for _, binding := range bindings {
		if _, duplicate := byChain[binding.ChainID()]; duplicate || binding.Resource() == "" || len(binding.Commitments().Aliases()) == 0 {
			return audit.Failure(audit.Conflict, errors.New("auditpg: entity bindings are invalid"))
		}
		byChain[binding.ChainID()] = binding
	}
	entityCount := 0
	for _, item := range items {
		if item.Kind != audit.EntityItem {
			continue
		}
		entityCount++
		binding, ok := byChain[item.Chain]
		if !ok {
			return audit.Failure(audit.Refused, errors.New("auditpg: entity item has no binding"))
		}
		head, err := e.bindEntity(ctx, schema, binding)
		if err != nil {
			return err
		}
		if head.terminal || head.leaf != item.Previous {
			return audit.Failure(audit.Conflict, errors.New("auditpg: entity head changed"))
		}
		var previous any
		if item.Previous != (audit.LeafDigest{}) {
			previous = item.Previous[:]
		}
		if _, err := e.tx.ExecContext(ctx, `INSERT INTO `+schema+`.entity_transitions
(revision_id, ordinal, chain_id, previous_leaf, leaf, action) VALUES ($1,$2,$3,$4,$5,$6)`, revision[:], int(item.Ordinal), item.Chain[:], previous, item.Leaf[:], string(item.Action)); err != nil {
			return classifySQL(err, true)
		}
		terminal := item.Action == audit.Action(audit.EntityHardDeleted)
		result, err := e.tx.ExecContext(ctx, `UPDATE `+schema+`.entity_chains SET leaf=$1, terminal=$2
WHERE chain_id=$3 AND leaf IS NOT DISTINCT FROM $4 AND terminal=false`, item.Leaf[:], terminal, item.Chain[:], previous)
		if err != nil {
			return classifySQL(err, true)
		}
		if affected, err := result.RowsAffected(); err != nil || affected != 1 {
			return audit.Failure(audit.Conflict, errors.New("auditpg: entity head CAS failed"))
		}
		delete(byChain, item.Chain)
	}
	if entityCount != len(bindings) || len(byChain) != 0 {
		return audit.Failure(audit.Refused, errors.New("auditpg: entity bindings and items differ"))
	}
	return nil
}

func (e *execution) bindEntity(ctx context.Context, schema string, binding audit.EntityAliasBinding) (entityHead, error) {
	aliases := orderedAliases(binding.Commitments().Aliases())
	chain := binding.ChainID()
	if _, err := e.tx.ExecContext(ctx, `INSERT INTO `+schema+`.entity_chains (chain_id, resource)
VALUES ($1,$2) ON CONFLICT (chain_id) DO NOTHING`, chain[:], string(binding.Resource())); err != nil {
		return entityHead{}, classifySQL(err, true)
	}
	var resource string
	if err := e.tx.QueryRowContext(ctx, `SELECT resource FROM `+schema+`.entity_chains WHERE chain_id=$1 FOR UPDATE`, chain[:]).Scan(&resource); err != nil {
		return entityHead{}, classifySQL(err, false)
	}
	if resource != string(binding.Resource()) {
		return entityHead{}, audit.Failure(audit.Conflict, errors.New("auditpg: entity chain belongs to another resource"))
	}
	scope, scopePresent := binding.Scope()
	for _, alias := range aliases {
		description := alias.Description()
		if _, err := e.tx.ExecContext(ctx, `INSERT INTO `+schema+`.entity_aliases
(resource, scope_present, scope, algorithm, profile, key_id, commitment, chain_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`, string(binding.Resource()), scopePresent, scope[:], description.Algorithm, description.Profile, description.KeyID, alias.Bytes(), chain[:]); err != nil {
			return entityHead{}, classifySQL(err, true)
		}
	}
	head, found, err := findAliasHead(ctx, e.tx, schema, binding.Resource(), scopePresent, scope, aliases, true)
	if err != nil {
		return entityHead{}, err
	}
	if !found || head.chain != chain {
		return entityHead{}, audit.Failure(audit.Conflict, errors.New("auditpg: entity alias belongs to another chain"))
	}
	return head, nil
}

func loadStoredRevision(ctx context.Context, tx *sql.Tx, schema string, revision audit.RevisionID, lock bool) (audit.StoredHeader, bool, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	stored, err := scanStored(tx.QueryRowContext(ctx, `SELECT wire, intent, recorded_at, position FROM `+schema+`.revisions WHERE revision_id=$1`+suffix, revision[:]))
	if errors.Is(err, sql.ErrNoRows) {
		return audit.StoredHeader{}, false, nil
	}
	if err != nil {
		if errors.Is(err, ErrSchemaMismatch) {
			return audit.StoredHeader{}, false, audit.Failure(audit.Corrupt, err)
		}
		return audit.StoredHeader{}, false, classifySQL(err, false)
	}
	return stored, true, nil
}

var _ audit.Execution = (*execution)(nil)
