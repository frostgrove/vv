package sqlrepo_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/utils"
)

type ownedArticle struct {
	ID        string               `db:"id,pk"`
	Title     string               `db:"title"`
	DeletedAt utils.Opt[time.Time] `db:"deleted_at,serverowned,tombstone"`
}

type ownedArticleUpdate struct{ Title *string }

type versionedOwnedArticle struct {
	ID        string     `db:"id,pk"`
	Title     string     `db:"title"`
	Version   int64      `db:"version,version"`
	DeletedAt *time.Time `db:"deleted_at,tombstone"`
}

type versionedOwnedArticleUpdate struct{ Title *string }

func TestTaggedTombstoneMakesSoftDeleteTheDeclarativeDefault(t *testing.T) {
	bp, err := sqlrepo.TryDefine[ownedArticle, string, ownedArticleUpdate]("owned_articles")
	if err != nil {
		t.Fatal(err)
	}
	recorder := crudtest.Postgres()
	repository := bp.Bind(recorder)
	ctx := context.Background()

	if err := repository.SaveOnly(ctx, &ownedArticle{ID: "a", Title: "kept"}); err != nil {
		t.Fatal(err)
	}
	insert := crudtest.Normalize(recorder.Last().SQL)
	if strings.Contains(insert, "deleted_at") {
		t.Fatalf("generic Save persisted the server-owned tombstone: %s", insert)
	}

	if _, err := repository.Delete(ctx, "a"); err != nil {
		t.Fatalf("Delete = %v", err)
	}
	deletion := crudtest.Normalize(recorder.Last().SQL)
	if !strings.HasPrefix(deletion, `UPDATE "owned_articles" SET "deleted_at" = $1`) ||
		!strings.Contains(deletion, `"deleted_at" IS NULL`) || !strings.Contains(deletion, `"id" = $2`) {
		t.Fatalf("tagged tombstone did not compile into a scoped stamp: %s", deletion)
	}

	recorder.Push(crudtest.Rows())
	if _, err := repository.GetAll(ctx); err != nil {
		t.Fatal(err)
	}
	read := crudtest.Normalize(recorder.Last().SQL)
	if !strings.Contains(read, `WHERE "deleted_at" IS NULL`) {
		t.Fatalf("tagged tombstone did not compile into the live-row scope: %s", read)
	}

	if _, err := repository.Restore(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	restore := crudtest.Normalize(recorder.Last().SQL)
	if !strings.HasPrefix(restore, `UPDATE "owned_articles" SET "deleted_at" = NULL`) ||
		!strings.Contains(restore, `"deleted_at" IS NOT NULL`) || !strings.Contains(restore, `"id" = $1`) {
		t.Fatalf("Restore did not compile into a distinct archived-row action: %s", restore)
	}
}

func TestDeleteAndRestoreAdvanceVersionToCloseLifecycleABA(t *testing.T) {
	bp := sqlrepo.Define[versionedOwnedArticle, string, versionedOwnedArticleUpdate](
		"versioned_owned_articles", sqlrepo.IndependentTable())
	recorder := crudtest.Postgres()
	repository := bp.Bind(recorder)

	if _, err := repository.Delete(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	deleted := crudtest.Normalize(recorder.Last().SQL)
	for _, want := range []string{
		`SET "deleted_at" = $1, "version" = "version" + 1`,
		`"deleted_at" IS NULL`,
		`"id" = $2`,
	} {
		if !strings.Contains(deleted, want) {
			t.Fatalf("versioned Delete omitted %s: %s", want, deleted)
		}
	}

	if _, err := repository.Restore(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	restored := crudtest.Normalize(recorder.Last().SQL)
	for _, want := range []string{
		`SET "deleted_at" = NULL, "version" = "version" + 1`,
		`"deleted_at" IS NOT NULL`,
		`"id" = $1`,
	} {
		if !strings.Contains(restored, want) {
			t.Fatalf("versioned Restore omitted %s: %s", want, restored)
		}
	}

	if _, err := repository.DeleteAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	all := crudtest.Normalize(recorder.Last().SQL)
	if !strings.Contains(all, `SET "deleted_at" = $1, "version" = "version" + 1`) {
		t.Fatalf("versioned DeleteAll omitted the lifecycle version bump: %s", all)
	}
}

func TestTombstoneCannotReenterThroughAHandWrittenPatch(t *testing.T) {
	type patch struct{ DeletedAt crud.Opt[time.Time] }
	if _, err := sqlrepo.TryDefine[ownedArticle, string, patch]("owned_articles_patch"); err == nil || !strings.Contains(err.Error(), "tombstone") {
		t.Fatalf("TryDefine(malicious tombstone patch) = %v", err)
	}
}

func TestHardDeleteRepositoryRefusesRestore(t *testing.T) {
	type row struct {
		ID string `db:"id,pk"`
	}
	repository := sqlrepo.Define[row, string, struct{}]("hard_rows", sqlrepo.IndependentTable()).Bind(crudtest.Postgres())
	if repository.SupportsRestore() {
		t.Fatal("hard-delete repository advertised Restore")
	}
	service := port.NewService[row, string, struct{}](repository)
	if _, ok := port.RestorableOf[string](service); ok {
		t.Fatal("hard-delete repository caused the default service to advertise restore use cases")
	}
	if _, err := repository.Restore(context.Background(), "x"); err == nil || err != crud.ErrNoTombstone {
		t.Fatalf("Restore(hard delete) = %v", err)
	}
}

func TestLegacySoftDeleteSettingStillFreezesGenericWrites(t *testing.T) {
	type legacyRow struct {
		ID      string               `db:"id,pk"`
		Name    string               `db:"name"`
		Deleted utils.Opt[time.Time] `db:"deleted_at"`
	}
	type safePatch struct{ Name *string }

	soft := sqlrepo.Define[legacyRow, string, safePatch]("legacy_soft_rows",
		sqlrepo.SoftDelete("Deleted"), sqlrepo.IndependentTable())
	if soft.Meta().Tombstone != soft.Meta().Field("Deleted") {
		t.Fatalf("explicit SoftDelete did not publish a blueprint-local tombstone: %+v", soft.Meta().Tombstone)
	}
	forged := legacyRow{ID: "forged", Deleted: utils.Set(time.Now())}
	if err := port.ClearWriteProtected(soft.Meta(), &forged); err != nil {
		t.Fatal(err)
	}
	if forged.Deleted.IsDefined() {
		t.Fatalf("explicit SoftDelete let a forged lifecycle value cross the application boundary: %v", forged.Deleted)
	}
	recorder := crudtest.Postgres()
	softRepository := soft.Bind(recorder)
	if !softRepository.SupportsRestore() {
		t.Fatal("soft-delete repository did not advertise Restore")
	}
	if _, ok := port.RestorableOf[string](port.NewService[legacyRow, string, safePatch](softRepository)); !ok {
		t.Fatal("soft-delete repository did not publish restore application use cases")
	}
	if err := softRepository.SaveOnly(context.Background(), &legacyRow{
		ID: "x", Name: "live", Deleted: utils.Set(time.Now()),
	}); err != nil {
		t.Fatal(err)
	}
	if sql := crudtest.Normalize(recorder.Last().SQL); strings.Contains(sql, "deleted_at") {
		t.Fatalf("legacy SoftDelete let full Save dictate lifecycle state: %s", sql)
	}

	raw := sqlrepo.Define[legacyRow, string, safePatch]("legacy_raw_rows", sqlrepo.IndependentTable())
	if raw.Meta().Tombstone != nil {
		t.Fatalf("soft-delete metadata leaked into an independent raw view: %+v", raw.Meta().Tombstone)
	}
	recorder = crudtest.Postgres()
	if err := raw.Bind(recorder).SaveOnly(context.Background(), &legacyRow{ID: "x", Name: "raw"}); err != nil {
		t.Fatal(err)
	}
	if sql := crudtest.Normalize(recorder.Last().SQL); !strings.Contains(sql, "deleted_at") {
		t.Fatalf("soft-delete blueprint mutated an independent raw model view: %s", sql)
	}

	type unsafePatch struct{ Deleted crud.Opt[time.Time] }
	if _, err := sqlrepo.TryDefine[legacyRow, string, unsafePatch]("legacy_bad_patch",
		sqlrepo.SoftDelete("Deleted"), sqlrepo.IndependentTable()); err == nil || !strings.Contains(err.Error(), "update DTO") {
		t.Fatalf("legacy SoftDelete accepted a tombstone patch: %v", err)
	}
}

func TestLegacySoftDeleteValidatesTheTimestampContractAtDeclaration(t *testing.T) {
	type wrong struct {
		ID      string  `db:"id,pk"`
		Deleted *string `db:"deleted_at"`
	}
	if _, err := sqlrepo.TryDefine[wrong, string, struct{}]("legacy_wrong_tombstone",
		sqlrepo.SoftDelete("Deleted"), sqlrepo.IndependentTable()); err == nil || !strings.Contains(err.Error(), "time.Time") {
		t.Fatalf("TryDefine(*string SoftDelete) = %v", err)
	}

	type scanner struct {
		ID      string       `db:"id,pk"`
		Deleted sql.NullTime `db:"deleted_at"`
	}
	if _, err := sqlrepo.TryDefine[scanner, string, struct{}]("legacy_scanner_tombstone",
		sqlrepo.SoftDelete("Deleted"), sqlrepo.IndependentTable()); err != nil {
		t.Fatalf("TryDefine(sql.NullTime SoftDelete) = %v", err)
	}
}

func TestRestoreDoesNotPanicOnANonComparableDynamicInterfaceKey(t *testing.T) {
	type interfaceKeyRow struct {
		ID        any        `db:"id,pk"`
		DeletedAt *time.Time `db:"deleted_at,serverowned,tombstone"`
	}
	repository := sqlrepo.Define[interfaceKeyRow, any, struct{}](
		"interface_key_tombstones", sqlrepo.IndependentTable()).Bind(crudtest.Postgres())
	type nestedKey struct{ Part any }
	for _, key := range []any{[]byte("binary-key"), nestedKey{Part: []byte("nested-binary-key")}} {
		if _, err := repository.Restore(context.Background(), key, key); err != nil {
			t.Fatalf("Restore(%T with non-comparable dynamic content) = %v", key, err)
		}
	}

	key := any(nestedKey{Part: []byte("scoped-binary-key")})
	n, err, ok := crud.RestoreScopedOf(repository.Unwrap(), context.Background(), &crud.ScopedRestore[any]{
		IDs:       []any{key},
		Snapshots: map[any]crud.Predicate{"another": crud.Eq("ID", "another")},
	})
	if !ok || err != nil || n != 0 {
		t.Fatalf("RestoreScoped(non-comparable key) = n:%d err:%v ok:%v", n, err, ok)
	}
}

func TestASaveOverATombstonedKeyRewritesTheRowWithoutBringingItBack(t *testing.T) {
	type buriedRow struct {
		ID    string               `db:"id,pk"`
		Title string               `db:"title"`
		Gone  utils.Opt[time.Time] `db:"deleted_at"`
	}
	type buriedPatch struct{ Title *string }

	ctx := context.Background()
	buried := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	plain := sqlrepo.Define[buriedRow, string, buriedPatch]("buried_rows",
		sqlrepo.SoftDelete("Gone"), sqlrepo.IndependentTable())
	recorder := crudtest.Postgres().Push(crudtest.Rows([]any{"a", "rewritten", buried}))
	saved, err := plain.Bind(recorder).Save(ctx, &buriedRow{ID: "a", Title: "rewritten"})
	if err != nil {
		t.Fatal(err)
	}
	upsert := crudtest.Normalize(recorder.Last().SQL)
	written, _, _ := strings.Cut(upsert, " RETURNING ")
	if strings.Contains(written, "deleted_at") {
		t.Fatalf("an ordinary Save carries the tombstone column, so it un-deletes the row it lands on: %s", upsert)
	}
	if !saved.Gone.IsDefined() {
		t.Fatalf("Save answered with a live row for a key the database still holds as deleted: %+v", saved)
	}

	scoped := sqlrepo.Define[buriedRow, string, buriedPatch]("buried_scoped_rows",
		sqlrepo.SoftDelete("Gone"), sqlrepo.Scope(crud.Eq("Title", "mine")), sqlrepo.IndependentTable())
	recorder = crudtest.Postgres().
		ExecResult(crud.Result{RowsAffected: 1}).
		Push(crudtest.Rows([]any{"a", "mine", buried}))
	if _, err := scoped.Bind(recorder).Save(ctx, &buriedRow{ID: "a", Title: "mine"}); err != nil {
		t.Fatal(err)
	}
	update := crudtest.Normalize(recorder.SQL()[0])
	if !strings.HasPrefix(update, `UPDATE "buried_scoped_rows" SET "title" = $1`) {
		t.Fatalf("a scoped Save did not become the update half of the update/probe/insert sequence: %s", update)
	}
	if strings.Contains(update, "deleted_at") {
		t.Fatalf("the write narrowing picked up the soft-delete predicate, so a row its owner buried is out of reach: %s", update)
	}
}
