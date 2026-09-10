//go:build integration

package auditpg

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/audit/audittest"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
)

func TestPostgresRunOnlyAttemptConformance(t *testing.T) {
	dsn := os.Getenv("FROSTGROVE_AUDITPG_TEST_DSN")
	if dsn == "" {
		t.Fatal("FROSTGROVE_AUDITPG_TEST_DSN is required for the live auditpg suite")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	schema := Schema{Name: "auditpg_attempt_" + strconv.FormatInt(time.Now().UnixNano(), 36)}
	t.Cleanup(func() {
		if _, err := db.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+quoteIdentifier(schema.Name)+` CASCADE`); err != nil {
			t.Errorf("drop attempt schema: %v", err)
		}
	})
	source := crudsql.Postgres(db)
	captured := new(attemptAppendCapture)
	audittest.RunOnlyAttempts(t, func(ctx context.Context, request audittest.AttemptStoreRequest) (audittest.AttemptStore, error) {
		deployment, err := NewDeployment(DeploymentSpec{
			Runtime: Spec{DB: db, Source: source, Schema: schema}, SchemaManagement: ManageSchema,
		})
		if err != nil {
			return audittest.AttemptStore{}, err
		}
		if err := deployment.Prepare(ctx); err != nil {
			return audittest.AttemptStore{}, err
		}
		for _, manifest := range request.Catalogs.Manifests() {
			if err := deployment.InstallCatalog(ctx, manifest, request.Change); err != nil {
				return audittest.AttemptStore{}, err
			}
		}
		active := request.Catalogs.Active()
		proof, err := audit.NoAttemptCatalogActivation(audit.CatalogRef{}, active)
		if err != nil {
			return audittest.AttemptStore{}, err
		}
		if err := deployment.ActivateCatalog(ctx, audit.CatalogRef{}, active, request.Change, proof); err != nil {
			return audittest.AttemptStore{}, err
		}
		var open func() (audittest.AttemptStore, error)
		open = func() (audittest.AttemptStore, error) {
			store, openErr := New(Spec{DB: db, Source: source, Schema: schema})
			if openErr != nil {
				return audittest.AttemptStore{}, openErr
			}
			if openErr = store.Check(context.Background()); openErr != nil {
				return audittest.AttemptStore{}, openErr
			}
			return audittest.AttemptStore{
				Writer: &capturingAttemptWriter{Store: store, capture: captured},
				Log:    store, Exact: store, Attempts: store, Types: store,
				Reopen: open, Close: store.Close,
			}, nil
		}
		return open()
	})
	request, present := captured.inserted()
	if !present {
		t.Fatal("attempt conformance captured no inserted request")
	}
	reopened, err := New(Spec{DB: db, Source: source, Schema: schema})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	replayed, err := reopened.Append(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Disposition() != audit.Replayed {
		t.Fatalf("exact attempt request retry disposition = %v", replayed.Disposition())
	}
}

type attemptAppendCapture struct {
	mu      sync.Mutex
	request audit.AppendRequest
	present bool
}

func (c *attemptAppendCapture) record(request audit.AppendRequest) {
	c.mu.Lock()
	c.request = request
	c.present = true
	c.mu.Unlock()
}

func (c *attemptAppendCapture) inserted() (audit.AppendRequest, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.request, c.present
}

type capturingAttemptWriter struct {
	*Store
	capture *attemptAppendCapture
}

func (w *capturingAttemptWriter) Append(ctx context.Context, request audit.AppendRequest) (audit.AppendResult, error) {
	result, err := w.Store.Append(ctx, request)
	if err == nil && result.Disposition() == audit.Inserted {
		w.capture.record(request)
	}
	return result, err
}
