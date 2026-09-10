package auditcrud

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/security"
)

type softAlphaRow struct {
	ID        int64      `db:"id,pk"`
	Name      string     `db:"name"`
	DeletedAt *time.Time `db:"deleted_at,serverowned,tombstone"`
}

type softAlphaPatch struct {
	Name string `db:"name"`
}

type softAlphaCore struct {
	meta *crud.Meta
	base *alphaCore
}

type softAlphaFixture struct {
	core    *softAlphaCore
	secured crud.Core[softAlphaRow, int64]
	writer  *alphaWriter
}

var _ crud.Core[softAlphaRow, int64] = (*softAlphaCore)(nil)
var _ crud.ScopedSaver[softAlphaRow, int64] = (*softAlphaCore)(nil)
var _ crud.ScopedDeleter[softAlphaRow, int64] = (*softAlphaCore)(nil)
var _ crud.ScopedRestorer[softAlphaRow, int64] = (*softAlphaCore)(nil)
var _ crud.TombstoneLoader[softAlphaRow, int64] = (*softAlphaCore)(nil)

func TestSecuredCommitsSoftDeleteAndRestoreWithTheirAuditRevisions(t *testing.T) {
	fixture := newSoftAlphaFixture(t)
	ctx := context.Background()

	created, err := fixture.secured.Save(ctx, &softAlphaRow{Name: "recoverable"})
	if err != nil {
		t.Fatalf("create through Secured: %v", err)
	}
	assertSoftAlphaState(t, fixture, created.ID, false, audit.EntityCreated)

	deleted, err := fixture.secured.Delete(ctx, created.ID)
	if err != nil {
		t.Fatalf("soft delete through Secured: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("soft delete count = %d, want 1", deleted)
	}
	assertSoftAlphaState(t, fixture, created.ID, true, audit.EntityCreated, audit.EntitySoftDeleted)

	restored, err, ok := crud.RestoreOf(fixture.secured, ctx, created.ID)
	if !ok {
		t.Fatal("Secured did not preserve Restore for a tombstone resource")
	}
	if err != nil {
		t.Fatalf("restore through Secured: %v", err)
	}
	if restored != 1 {
		t.Fatalf("restore count = %d, want 1", restored)
	}
	assertSoftAlphaState(t, fixture, created.ID, false, audit.EntityCreated, audit.EntitySoftDeleted, audit.EntityRestored)

	begin, commit, rollback := fixture.core.base.settlements()
	if begin != 3 || commit != 3 || rollback != 0 {
		t.Fatalf("transactions = begin:%d commit:%d rollback:%d, want 3/3/0", begin, commit, rollback)
	}
}

func assertSoftAlphaState(t *testing.T, fixture *softAlphaFixture, id int64, deleted bool, actions ...audit.EntityAction) {
	t.Helper()
	rows, _ := fixture.core.base.source.database.snapshot()
	stored, ok := rows[id]
	if !ok {
		t.Fatalf("business row %d was not committed", id)
	}
	row := decodeSoftAlpha(stored)
	if row.Name != "recoverable" || (row.DeletedAt != nil) != deleted {
		t.Fatalf("committed business row = %+v, want deleted=%v", row, deleted)
	}
	revisions := fixture.writer.committedRevisions()
	if len(revisions) != len(actions) {
		t.Fatalf("committed audit revision count = %d, want %d", len(revisions), len(actions))
	}
	for index, action := range actions {
		if len(revisions[index].Items) != 1 || revisions[index].Items[0].Action != audit.Action(action) {
			t.Fatalf("audit revision %d items = %+v, want one %s item", index, revisions[index].Items, action)
		}
		if action == audit.EntitySoftDeleted {
			for _, change := range revisions[index].Items[0].Changes {
				if change.Field == "deleted_at" {
					t.Fatal("soft-delete evidence was derived from the post-mutation tombstone instead of the admitted live row")
				}
			}
		}
	}
}

func newSoftAlphaFixture(t *testing.T) *softAlphaFixture {
	t.Helper()
	meta, err := crud.NewMeta[softAlphaRow]("auditcrud_soft_alpha_rows")
	if err != nil {
		t.Fatal(err)
	}
	operationContext := audit.ContextFacts(audit.GeneratedOperationFact(audit.Internal, audit.AsPlaintext))
	resource := audit.Define(audit.Policy[softAlphaRow, int64]{
		Model:     meta,
		Semantics: audit.Semantics(1, audit.PolicyGolden("soft.alpha.resource", strings.Repeat("03", 32))),
		Descriptor: audit.Descriptor{
			Resource: "soft.alpha.row", Owner: "alpha.team", Purpose: "business.audit",
			Retention: "business.forever", Consequence: audit.Required, Context: operationContext,
		},
		Subject: audit.PlaintextSubject(func(id int64) string { return fmt.Sprintf("soft-row:%d", id) }, audit.Internal),
		Actions: audit.Actions(
			audit.EntityCreated,
			audit.EntityChanged,
			audit.EntitySoftDeleted,
			audit.EntityRestored,
		),
		Fields: audit.Fields[softAlphaRow](
			audit.Value[softAlphaRow]("Name", "name", audit.Text(), audit.Internal),
			audit.Optional[softAlphaRow, time.Time]("DeletedAt", "deleted_at", audit.Time(), audit.Internal),
		),
	})
	semantic, err := audit.HMACSemanticDigester("semantic-soft-alpha", bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	identities, err := audit.HMACIdentityKeyring(audit.HMACIdentityKey{KeyID: "identity-soft-alpha", Key: bytes.Repeat([]byte{4}, 32), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := audit.Compile(audit.CatalogSpec{
		ID: "soft.alpha.audit", Owner: "alpha.team", Generation: 1,
		Retention: audit.RetentionRules(audit.KeepForever("business.forever")),
		Semantics: semantic.Description(), Identities: identities.ActiveDescription(), Integrity: audit.IntegrityOnly(),
	}, resource)
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := audit.Lineage(catalog)
	if err != nil {
		t.Fatal(err)
	}
	database := &alphaDatabase{rows: make(map[int64]alphaRow), nextID: 1}
	source := &alphaSource{database: database}
	writer := newAlphaWriter(t, source, catalogs)
	database.writer = writer
	resolver, err := audit.StaticContext(audit.Context{})
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := audit.New(audit.Config{
		Catalogs: catalogs, Writer: writer, Context: resolver,
		Semantics: semantic, Identities: identities,
	})
	if err != nil {
		t.Fatal(err)
	}
	base := &alphaCore{source: source}
	core := &softAlphaCore{meta: meta, base: base}
	secured := Secured(recorder, resource, security.Policy[softAlphaRow, int64]{
		Authorize: func(context.Context, security.Action) error { return nil },
	})(core)
	return &softAlphaFixture{core: core, secured: secured, writer: writer}
}

func (core *softAlphaCore) Meta() *crud.Meta { return core.meta }

func (core *softAlphaCore) Source() crud.Source { return core.base.source }

func (core *softAlphaCore) GetByID(ctx context.Context, id int64, _ ...crud.Option) (softAlphaRow, error) {
	rows, err := core.base.rows(ctx)
	if err != nil {
		return softAlphaRow{}, err
	}
	stored, ok := rows[id]
	if !ok {
		return softAlphaRow{}, crud.ErrNotFound
	}
	row := decodeSoftAlpha(stored)
	if row.DeletedAt != nil {
		return softAlphaRow{}, crud.ErrNotFound
	}
	return row, nil
}

func (core *softAlphaCore) Get(ctx context.Context, options ...crud.Option) (crud.PaginatedResponse[softAlphaRow], error) {
	rows, err := core.GetAll(ctx, options...)
	if err != nil {
		return crud.PaginatedResponse[softAlphaRow]{}, err
	}
	return crud.NewPaginatedResponse(rows, 1, len(rows), int64(len(rows))), nil
}

func (core *softAlphaCore) GetAll(ctx context.Context, _ ...crud.Option) ([]softAlphaRow, error) {
	rows, err := core.base.rows(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	result := make([]softAlphaRow, 0, len(ids))
	for _, id := range ids {
		row := decodeSoftAlpha(rows[id])
		if row.DeletedAt == nil {
			result = append(result, row)
		}
	}
	return result, nil
}

func (core *softAlphaCore) First(ctx context.Context, options ...crud.Option) (softAlphaRow, error) {
	rows, err := core.GetAll(ctx, options...)
	if err != nil {
		return softAlphaRow{}, err
	}
	if len(rows) == 0 {
		return softAlphaRow{}, crud.ErrNotFound
	}
	return rows[0], nil
}

func (core *softAlphaCore) Save(ctx context.Context, model *softAlphaRow) (softAlphaRow, error) {
	tx, err := core.base.transaction(ctx)
	if err != nil {
		return softAlphaRow{}, err
	}
	row := *model
	if row.ID == 0 {
		row.ID = tx.nextID
		tx.nextID++
	}
	row.DeletedAt = nil
	tx.rows[row.ID] = encodeSoftAlpha(row)
	return row, nil
}

func (core *softAlphaCore) SaveOnly(ctx context.Context, model *softAlphaRow) error {
	_, err := core.Save(ctx, model)
	return err
}

func (core *softAlphaCore) SaveScoped(ctx context.Context, model *softAlphaRow, _ *crud.ScopedSave[softAlphaRow]) error {
	tx, err := core.base.transaction(ctx)
	if err != nil {
		return err
	}
	tx.rows[model.ID] = encodeSoftAlpha(*model)
	return nil
}

func (core *softAlphaCore) Update(ctx context.Context, id int64, dto any, _ ...crud.Option) (softAlphaRow, error) {
	tx, err := core.base.transaction(ctx)
	if err != nil {
		return softAlphaRow{}, err
	}
	stored, ok := tx.rows[id]
	if !ok {
		return softAlphaRow{}, crud.ErrNotFound
	}
	row := decodeSoftAlpha(stored)
	if row.DeletedAt != nil {
		return softAlphaRow{}, crud.ErrNotFound
	}
	patch, ok := dto.(softAlphaPatch)
	if !ok {
		return softAlphaRow{}, crud.ErrBadRequest
	}
	row.Name = patch.Name
	tx.rows[id] = encodeSoftAlpha(row)
	return row, nil
}

func (*softAlphaCore) UpdateAll(context.Context, any, ...crud.Option) (int64, error) {
	return 0, audit.ErrUnsupported
}

func (core *softAlphaCore) Aggregate(ctx context.Context, options ...crud.Option) ([]crud.AggregateRow, error) {
	rows, err := core.GetAll(ctx, options...)
	if err != nil {
		return nil, err
	}
	return []crud.AggregateRow{{Value: map[string]any{"count": int64(len(rows))}}}, nil
}

func (*softAlphaCore) SaveAll(context.Context, []*softAlphaRow) error { return audit.ErrUnsupported }

func (core *softAlphaCore) Delete(ctx context.Context, ids ...int64) (int64, error) {
	return core.DeleteScoped(ctx, &crud.ScopedDelete[int64]{IDs: ids})
}

func (core *softAlphaCore) DeleteScoped(ctx context.Context, deletion *crud.ScopedDelete[int64]) (int64, error) {
	tx, err := core.base.transaction(ctx)
	if err != nil {
		return 0, err
	}
	var count int64
	for _, id := range deletion.IDs {
		stored, ok := tx.rows[id]
		if !ok {
			continue
		}
		row := decodeSoftAlpha(stored)
		if row.DeletedAt != nil {
			continue
		}
		deletedAt := time.Unix(1_900_000_000, 0).UTC()
		row.DeletedAt = &deletedAt
		tx.rows[id] = encodeSoftAlpha(row)
		count++
	}
	return count, nil
}

func (*softAlphaCore) DeleteAll(context.Context, ...crud.Option) (int64, error) {
	return 0, audit.ErrUnsupported
}

func (core *softAlphaCore) Count(ctx context.Context, options ...crud.Option) (int64, error) {
	rows, err := core.GetAll(ctx, options...)
	return int64(len(rows)), err
}

func (core *softAlphaCore) Exists(ctx context.Context, options ...crud.Option) (bool, error) {
	rows, err := core.GetAll(ctx, options...)
	return len(rows) != 0, err
}

func (core *softAlphaCore) ExistsUnscoped(ctx context.Context, _ ...crud.Option) (bool, error) {
	rows, err := core.base.rows(ctx)
	return len(rows) != 0, err
}

func (core *softAlphaCore) Tx(ctx context.Context, fn func(context.Context) error) error {
	return core.base.Tx(ctx, fn)
}

func (*softAlphaCore) SupportsRestore() bool { return true }

func (core *softAlphaCore) Restore(ctx context.Context, ids ...int64) (int64, error) {
	return core.RestoreScoped(ctx, &crud.ScopedRestore[int64]{IDs: ids})
}

func (core *softAlphaCore) RestoreScoped(ctx context.Context, restore *crud.ScopedRestore[int64]) (int64, error) {
	tx, err := core.base.transaction(ctx)
	if err != nil {
		return 0, err
	}
	var count int64
	for _, id := range restore.IDs {
		stored, ok := tx.rows[id]
		if !ok {
			continue
		}
		row := decodeSoftAlpha(stored)
		if row.DeletedAt == nil {
			continue
		}
		row.DeletedAt = nil
		tx.rows[id] = encodeSoftAlpha(row)
		count++
	}
	return count, nil
}

func (core *softAlphaCore) LoadTombstones(ctx context.Context, ids []int64, _ crud.Predicate, _ *crud.RelationScopes) ([]softAlphaRow, error) {
	rows, err := core.base.rows(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]softAlphaRow, 0, len(ids))
	for _, id := range ids {
		stored, ok := rows[id]
		if !ok {
			continue
		}
		row := decodeSoftAlpha(stored)
		if row.DeletedAt != nil {
			result = append(result, row)
		}
	}
	return result, nil
}

func encodeSoftAlpha(row softAlphaRow) alphaRow {
	return alphaRow{ID: row.ID, Name: row.Name, DeletedAt: cloneSoftAlphaTime(row.DeletedAt)}
}

func decodeSoftAlpha(row alphaRow) softAlphaRow {
	return softAlphaRow{ID: row.ID, Name: row.Name, DeletedAt: cloneSoftAlphaTime(row.DeletedAt)}
}

func cloneSoftAlphaTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
