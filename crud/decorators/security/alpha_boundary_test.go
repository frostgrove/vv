package security_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/decorators/security"
)

func TestUpdateMaterializesCallerOptionsOnceBeforeAppendingSecurityConstraints(t *testing.T) {
	calls := 0
	assigning := crud.Option(func(options *crud.Options) {
		calls++
		options.Filter = []crud.Predicate{crud.Eq("Title", "before")}
	})
	recorder := crudtest.Postgres().Push(
		crudtest.Rows(docRow(42, 7, "before")),
		crudtest.Rows(docRow(42, 7, "before")),
		crudtest.Rows(docRow(42, 7, "after")),
	)

	updated, err := gated(recorder).Update(
		withTenant(context.Background(), 7),
		42,
		DocUpdate{Title: ptrTo("after")},
		assigning,
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "after" || calls != 1 {
		t.Fatalf("Update returned %+v after evaluating the caller option %d times", updated, calls)
	}
	statements := recorder.Statements()
	if len(statements) != 3 {
		t.Fatalf("Update issued %d statements, want policy read, mutation read and write: %v", len(statements), recorder.SQL())
	}
	for _, statement := range statements[1:] {
		sql := crudtest.Normalize(statement.SQL)
		if !strings.Contains(sql, `"title" =`) || !strings.Contains(sql, `"tenant_id" =`) {
			t.Fatalf("materialized caller filter erased a security constraint: %s", sql)
		}
		callerAt := slices.Index(statement.Args, any("before"))
		scopeAt := slices.Index(statement.Args, any(int64(7)))
		if callerAt < 0 || scopeAt < 0 || callerAt >= scopeAt {
			t.Fatalf("caller binds must precede the appended security binds: %v", statement.Args)
		}
	}
}

func TestUpdateRefusesUnsupportedCallerOptionsBeforeInspectionIO(t *testing.T) {
	recorder := crudtest.Postgres()
	_, err := gated(recorder).Update(
		withTenant(context.Background(), 7),
		42,
		DocUpdate{Title: ptrTo("after")},
		crud.Limit(1),
	)
	var schema *crud.SchemaError
	if !errors.As(err, &schema) || schema.Field != "Limit" {
		t.Fatalf("Update returned %v, want a Limit schema refusal", err)
	}
	if len(recorder.Statements()) != 0 {
		t.Fatalf("invalid mutation options reached storage: %v", recorder.SQL())
	}
}

func TestRestoreAuthorizesAnEmptyIDSet(t *testing.T) {
	deniedCalls := 0
	deniedRecorder := crudtest.Postgres()
	denied := archivedBlueprint(t).Bind(deniedRecorder, security.Gate(
		security.Policy[archivedDocument, int64]{Authorize: func(context.Context, security.Action) error {
			deniedCalls++
			return security.ErrForbidden
		}},
	))
	if n, err := denied.Restore(context.Background()); n != 0 || !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("denied empty Restore = (%d, %v), want the authorization refusal", n, err)
	}
	if deniedCalls != 1 || len(deniedRecorder.Statements()) != 0 {
		t.Fatalf("denied empty Restore authorized %d times and issued %v", deniedCalls, deniedRecorder.SQL())
	}

	allowedCalls := 0
	allowedRecorder := crudtest.Postgres()
	allowed := archivedBlueprint(t).Bind(allowedRecorder, security.Gate(
		security.Policy[archivedDocument, int64]{Authorize: func(context.Context, security.Action) error {
			allowedCalls++
			return nil
		}},
	))
	if n, err := allowed.Restore(context.Background()); n != 0 || err != nil {
		t.Fatalf("allowed empty Restore = (%d, %v), want a no-op", n, err)
	}
	if allowedCalls != 1 || len(allowedRecorder.Statements()) != 0 {
		t.Fatalf("allowed empty Restore authorized %d times and issued %v", allowedCalls, allowedRecorder.SQL())
	}
}
