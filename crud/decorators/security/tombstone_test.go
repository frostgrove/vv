package security_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/decorators/security"
	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/frostgrove/vv/utils"
)

type archivedDocument struct {
	ID        int64                `db:"id,pk,noauto"`
	TenantID  int64                `db:"tenant_id,immutable"`
	Title     string               `db:"title"`
	DeletedAt utils.Opt[time.Time] `db:"deleted_at,serverowned,tombstone"`
}

type archivedDocumentUpdate struct{ Title *string }

type wrappedArchivedDocument struct {
	ID        *int64               `db:"id,pk,noauto"`
	Title     string               `db:"title"`
	Note      sql.NullString       `db:"note"`
	DeletedAt utils.Opt[time.Time] `db:"deleted_at,serverowned,tombstone"`
}

type wrappedArchivedDocumentUpdate struct{ Title *string }

func archivedBlueprint(t *testing.T) *sqlrepo.Blueprint[archivedDocument, int64, archivedDocumentUpdate] {
	t.Helper()
	bp, err := sqlrepo.TryDefine[archivedDocument, int64, archivedDocumentUpdate](
		"security_archived_documents", sqlrepo.IndependentTable())
	if err != nil {
		t.Fatal(err)
	}
	return bp
}

func TestRestoreHasItsOwnAuthorizationAndScope(t *testing.T) {
	recorder := crudtest.Postgres()
	seen := security.Read
	policy := security.Policy[archivedDocument, int64]{
		Scope: func(context.Context) (crud.Predicate, error) { return crud.Eq("TenantID", int64(7)), nil },
		Authorize: func(_ context.Context, action security.Action) error {
			seen = action
			if action != security.Restore {
				return security.ErrForbidden
			}
			return nil
		},
	}
	repository := archivedBlueprint(t).Bind(recorder, security.Gate(policy))
	if _, err := repository.Restore(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	if seen != security.Restore {
		t.Fatalf("authorizer saw %s, want restore", seen)
	}
	sql := crudtest.Normalize(recorder.Last().SQL)
	for _, want := range []string{`"deleted_at" IS NOT NULL`, `"tenant_id" = $1`, `"id" = $2`} {
		if !strings.Contains(sql, want) {
			t.Fatalf("scoped Restore omitted %s: %s", want, sql)
		}
	}

	denied := archivedBlueprint(t).Bind(crudtest.Postgres(), security.Gate(
		security.Policy[archivedDocument, int64]{Authorize: func(_ context.Context, action security.Action) error {
			if action == security.Update {
				return nil
			}
			return security.ErrForbidden
		}},
	))
	if _, err := denied.Restore(context.Background(), 42); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("Update-only policy restored a tombstone: %v", err)
	}
}

func TestRestoreInspectionReadsOnlyTombstonesAndPinsSnapshot(t *testing.T) {
	deleted := time.Date(2026, 8, 31, 10, 0, 0, 0, time.UTC)
	recorder := crudtest.Postgres().Push(crudtest.Rows([]any{int64(9), int64(7), "old", deleted}))
	inspected := false
	policy := security.Policy[archivedDocument, int64]{
		Scope: func(context.Context) (crud.Predicate, error) { return crud.Eq("TenantID", int64(7)), nil },
		Inspect: func(_ context.Context, action security.Action, row *archivedDocument) error {
			inspected = action == security.Restore && row.ID == 9
			return nil
		},
	}
	repository := archivedBlueprint(t).Bind(recorder, security.Gate(policy))
	if _, err := repository.Restore(context.Background(), 9); err != nil {
		t.Fatal(err)
	}
	if !inspected {
		t.Fatal("Restore did not inspect the archived row under the Restore action")
	}
	statements := recorder.Statements()
	if len(statements) != 2 {
		t.Fatalf("statements = %v", recorder.SQL())
	}
	read, write := crudtest.Normalize(statements[0].SQL), crudtest.Normalize(statements[1].SQL)
	if !strings.Contains(read, `"deleted_at" IS NOT NULL`) || strings.Contains(read, `"deleted_at" IS NULL`) {
		t.Fatalf("inspection escaped the tombstone-only view: %s", read)
	}
	for _, want := range []string{`"deleted_at" =`, `"title" =`, `"tenant_id" =`} {
		if !strings.Contains(write, want) {
			t.Fatalf("conditional Restore omitted snapshot term %s: %s", want, write)
		}
	}
}

func TestInspectedLifecycleNormalisesWrappedIDsAndSQLNullSnapshots(t *testing.T) {
	deleted := time.Date(2026, 8, 31, 11, 0, 0, 0, time.UTC)
	for _, action := range []security.Action{security.Delete, security.Restore} {
		t.Run(action.String(), func(t *testing.T) {
			bp, err := sqlrepo.TryDefine[wrappedArchivedDocument, int64, wrappedArchivedDocumentUpdate](
				"security_wrapped_archived_documents", sqlrepo.IndependentTable())
			if err != nil {
				t.Fatal(err)
			}
			rowDeleted := any(nil)
			if action == security.Restore {
				rowDeleted = deleted
			}
			recorder := crudtest.Postgres().Push(crudtest.Rows([]any{int64(9), "old", nil, rowDeleted}))
			inspected := false
			repository := bp.Bind(recorder, security.Gate(security.Policy[wrappedArchivedDocument, int64]{
				Inspect: func(_ context.Context, got security.Action, row *wrappedArchivedDocument) error {
					inspected = got == action && row.ID != nil && *row.ID == 9 && !row.Note.Valid
					return nil
				},
			}))

			if action == security.Restore {
				if _, err := repository.Restore(context.Background(), 9); err != nil {
					t.Fatalf("Restore with *ID = %v", err)
				}
			} else if _, err := repository.Delete(context.Background(), 9); err != nil {
				t.Fatalf("Delete with *ID = %v", err)
			}
			if !inspected {
				t.Fatal("policy did not inspect the wrapped-id row")
			}
			statements := recorder.Statements()
			if len(statements) != 2 {
				t.Fatalf("statements = %v", recorder.SQL())
			}
			write := crudtest.Normalize(statements[1].SQL)
			if !strings.Contains(write, `"note" IS NULL`) {
				t.Fatalf("SQL NULL scanner snapshot was not canonicalised: %s", write)
			}
			if strings.Contains(write, `"note" =`) {
				t.Fatalf("SQL NULL scanner snapshot became an impossible equality: %s", write)
			}
		})
	}
}
