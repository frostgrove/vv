package security_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/decorators/security"
)

type opaqueDocCore struct{ crud.Core[Doc, int64] }

func TestTheGateAnswersTheUnscopedProbeWithinItsOwnScope(t *testing.T) {
	recorder := crudtest.Postgres()
	gate := gated(recorder).Unwrap()

	recorder.Push(crudtest.Rows([]any{int64(1)}))
	found, err, supported := crud.ExistsUnscopedOf(gate, withTenant(context.Background(), 7),
		crud.Where(crud.Eq("ID", int64(3))))
	if !supported {
		t.Fatal("the gate refuses a question it is the only layer able to answer honestly")
	}
	if err != nil || !found {
		t.Fatalf("the gate answered found=%v err=%v for a row it was handed", found, err)
	}
	where := lastWhere(recorder)
	if !strings.Contains(where, "tenant_id") {
		t.Fatalf("the probe ran unnarrowed as %q — the layer above it just learned another tenant holds that id", where)
	}
	if !strings.Contains(where, `"id" = $`) {
		t.Fatalf("the caller's own narrowing was dropped: %q", where)
	}
	if got := len(recorder.Last().Args); got != 2 {
		t.Fatalf("the probe carries %d arguments, want the gate's tenant and the caller's id: %v", got, recorder.Last().Args)
	}

	t.Run("a scope it cannot resolve runs no statement", func(t *testing.T) {
		recorder.Reset()
		recorder.Push(crudtest.Rows([]any{int64(1)}))

		if _, err, _ := crud.ExistsUnscopedOf(gate, context.Background()); err == nil {
			t.Fatal("a probe with no tenant in the context was answered instead of refused")
		}
		if len(recorder.Statements()) != 0 {
			t.Fatalf("a statement ran for a scope that could not be resolved: %v", recorder.SQL())
		}
	})
}

func TestAnAssignedKeySaveRefusesWhenTheCoreBelowCannotAnswerTheProbe(t *testing.T) {
	ctx := withTenant(context.Background(), 7)

	blind := crudtest.Postgres()
	opaque := crud.Wrap[Doc, int64, DocUpdate](crud.Chain[Doc, int64](
		opaqueDocCore{Core: Docs.Bind(blind).Unwrap()}, security.Gate(tenantPolicy)))

	blind.Push(crudtest.Rows())
	_, err := opaque.Save(ctx, &Doc{ID: 3, TenantID: 7, Title: "t"})
	if !errors.Is(err, crud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound — a core that cannot be asked whether the id is taken must not be written through", err)
	}
	if len(blind.Statements()) != 1 {
		t.Fatalf("the refusal was not before the write: %v", blind.SQL())
	}

	t.Run("the same save reaches a write when the core can answer", func(t *testing.T) {
		recorder := crudtest.Postgres()
		plain := gated(recorder)

		recorder.Push(crudtest.Rows(), crudtest.Rows())
		if _, err := plain.Save(ctx, &Doc{ID: 3, TenantID: 7, Title: "t"}); errors.Is(err, crud.ErrNotFound) {
			t.Fatal("the control refuses too, so the assertion above proves nothing about the opaque core")
		}
		if len(recorder.Statements()) < 3 {
			t.Fatalf("the control never reached a write: %v", recorder.SQL())
		}
	})
}
