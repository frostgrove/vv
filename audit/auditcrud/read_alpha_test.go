package auditcrud

import (
	"context"
	"testing"

	"github.com/frostgrove/vv/crud"
)

func TestSecuredPreservesTheCompleteReadSurfaceWithoutWritingEvidence(t *testing.T) {
	fixture := newAlphaFixture(t)
	fixture.core.seed(
		alphaRow{ID: 2, Name: "second"},
		alphaRow{ID: 1, Name: "first"},
	)
	ctx := context.Background()

	page, err := fixture.secured.Get(ctx)
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != 1 || page.Total != 2 {
		t.Fatalf("Get = %+v, %v", page, err)
	}
	all, err := fixture.secured.GetAll(ctx)
	if err != nil || len(all) != 2 || all[0].ID != 1 || all[1].ID != 2 {
		t.Fatalf("GetAll = %+v, %v", all, err)
	}
	first, err := fixture.secured.First(ctx)
	if err != nil || first.ID != 1 {
		t.Fatalf("First = %+v, %v", first, err)
	}
	aggregate, err := fixture.secured.Aggregate(ctx)
	if err != nil || len(aggregate) != 1 || aggregate[0].Value["count"] != int64(2) {
		t.Fatalf("Aggregate = %+v, %v", aggregate, err)
	}
	count, err := fixture.secured.Count(ctx)
	if err != nil || count != 2 {
		t.Fatalf("Count = %d, %v", count, err)
	}
	exists, err := fixture.secured.Exists(ctx)
	if err != nil || !exists {
		t.Fatalf("Exists = %v, %v", exists, err)
	}
	unscoped, err, ok := crud.ExistsUnscopedOf(fixture.secured, ctx)
	if !ok || err != nil || !unscoped {
		t.Fatalf("ExistsUnscoped = %v, %v, supported=%v", unscoped, err, ok)
	}
	if fixture.secured.Meta() != fixture.core.meta {
		t.Fatal("Meta did not preserve the exact validated descriptor")
	}
	if source, ok := crud.SourceOf(fixture.secured); !ok || source != fixture.core.source {
		t.Fatal("Source did not preserve the exact validated source")
	}
	if crud.SupportsRestore(fixture.secured) {
		t.Fatal("hard-delete resource unexpectedly advertises Restore")
	}
	if revisions := fixture.writer.committedRevisions(); len(revisions) != 0 {
		t.Fatalf("read surface wrote %d audit revisions", len(revisions))
	}
}
