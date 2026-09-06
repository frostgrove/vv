package faults_test

import (
	"context"
	"errors"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/decorators/faults"
)

type opaqueDocCore struct{ crud.Core[Doc, int64] }

func TestTheEnricherForwardsUnscopedExistence(t *testing.T) {
	ctx := context.Background()
	recorder := crudtest.Postgres()
	enriched := Docs.Bind(recorder, faults.Enrich[Doc, int64]()).Unwrap()

	recorder.Push(crudtest.Rows([]any{int64(1)}))
	found, err, supported := crud.ExistsUnscopedOf(enriched, ctx)
	if !supported {
		t.Fatal("a transparent decorator dropped a capability the repository underneath it has")
	}
	if err != nil || !found {
		t.Fatalf("found=%v err=%v for a row the repository was handed", found, err)
	}

	t.Run("and fails closed over a core that cannot answer", func(t *testing.T) {
		blind := crudtest.Postgres()
		opaque := crud.Chain[Doc, int64](
			opaqueDocCore{Core: Docs.Bind(blind).Unwrap()}, faults.Enrich[Doc, int64]())

		blind.Push(crudtest.Rows([]any{int64(1)}))
		_, err, supported := crud.ExistsUnscopedOf(opaque, ctx)
		if !supported {
			t.Fatal("the enricher stopped answering at all, which hides the refusal from the layer above")
		}
		if !errors.Is(err, crud.ErrNoUnscopedExists) {
			t.Fatalf("err = %v, want ErrNoUnscopedExists", err)
		}
		if len(blind.Statements()) != 0 {
			t.Fatalf("the enricher reached past the opaque core: %v", blind.SQL())
		}
	})
}
