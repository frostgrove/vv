package crud_test

import (
	"context"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/decorators/faults"
	"github.com/frostgrove/vv/crud/decorators/security"
	"github.com/frostgrove/vv/crud/sqlrepo"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/utils"
)

type lifecycleProbeRow struct {
	ID        int64                `db:"id,pk,noauto"`
	Title     string               `db:"title"`
	DeletedAt utils.Opt[time.Time] `db:"deleted_at,serverowned,tombstone"`
}

type lifecycleProbeUpdate struct{ Title *string }

type hardProbeRow struct {
	ID    int64  `db:"id,pk,noauto"`
	Title string `db:"title"`
}

type hardProbeUpdate struct{ Title *string }

type opaqueLifecycleCore[M any, ID comparable] struct{ crud.Core[M, ID] }

func hideLifecycle[M any, ID comparable](next crud.Core[M, ID]) crud.Core[M, ID] {
	return opaqueLifecycleCore[M, ID]{Core: next}
}

func TestRestoreCapabilityIsPreservedOnlyByLayersThatOwnIt(t *testing.T) {
	allow := security.Policy[lifecycleProbeRow, int64]{
		Authorize: func(context.Context, security.Action) error { return nil },
	}
	soft := sqlrepo.Define[lifecycleProbeRow, int64, lifecycleProbeUpdate](
		"lifecycle_probe_rows", sqlrepo.IndependentTable())
	preserved := soft.Bind(crudtest.Postgres(),
		security.Gate(allow), faults.Enrich[lifecycleProbeRow, int64]())
	if !preserved.SupportsRestore() {
		t.Fatal("security/fault decorators dropped a real Restore capability")
	}
	if _, ok := port.RestorableOf[int64](
		port.NewService[lifecycleProbeRow, int64, lifecycleProbeUpdate](preserved)); !ok {
		t.Fatal("preserved repository did not publish restore application use cases")
	}

	hidden := soft.Bind(crudtest.Postgres(), hideLifecycle[lifecycleProbeRow, int64])
	if hidden.SupportsRestore() {
		t.Fatal("Restore tunneled through an opaque decorator")
	}
	if _, ok := port.RestorableOf[int64](
		port.NewService[lifecycleProbeRow, int64, lifecycleProbeUpdate](hidden)); ok {
		t.Fatal("opaque decorator caused the application service to advertise Restore")
	}

	hardAllow := security.Policy[hardProbeRow, int64]{
		Authorize: func(context.Context, security.Action) error { return nil },
	}
	hard := sqlrepo.Define[hardProbeRow, int64, hardProbeUpdate](
		"hard_probe_rows", sqlrepo.IndependentTable()).Bind(crudtest.Postgres(),
		security.Gate(hardAllow), faults.Enrich[hardProbeRow, int64]())
	if hard.SupportsRestore() {
		t.Fatal("security/fault decorators manufactured Restore for a hard-delete repository")
	}
}
