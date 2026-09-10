package auditpg

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
)

type stubDatabase struct {
	mu          sync.Mutex
	queries     int
	executions  int
	fingerprint string
	missing     string
	backing     [16]byte
	log         [16]byte
	active      audit.CatalogRef
	set         audit.CatalogSetDigest
	position    int64
	revisions   map[string]stubRevision
	idempotency map[string]stubIdempotency
}

type stubRevision struct {
	wire       []byte
	intent     []byte
	semantic   []byte
	catalog    string
	operation  string
	recordedAt time.Time
	position   int64
}

type stubIdempotency struct {
	semantic []byte
	revision []byte
}

type stubConnector struct{ state *stubDatabase }

func (c stubConnector) Connect(context.Context) (driver.Conn, error) {
	return &stubConnection{state: c.state}, nil
}

func (stubConnector) Driver() driver.Driver { return stubDriver{} }

type stubDriver struct{}

func (stubDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type stubConnection struct{ state *stubDatabase }

func (*stubConnection) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (*stubConnection) Close() error                        { return nil }
func (*stubConnection) Begin() (driver.Tx, error)           { return stubTransaction{}, nil }
func (*stubConnection) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return stubTransaction{}, nil
}

func (c *stubConnection) ExecContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Result, error) {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	c.state.executions++
	if strings.Contains(query, ".idempotency") && strings.Contains(query, "INSERT INTO") {
		key := idempotencyKey(arguments[0].Value.(string), arguments[1].Value.(string), arguments[2].Value.([]byte))
		if _, exists := c.state.idempotency[key]; !exists {
			c.state.idempotency[key] = stubIdempotency{semantic: bytes.Clone(arguments[3].Value.([]byte)), revision: bytes.Clone(arguments[4].Value.([]byte))}
		}
	}
	return driver.RowsAffected(1), nil
}

func (c *stubConnection) QueryContext(_ context.Context, query string, arguments []driver.NamedValue) (driver.Rows, error) {
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	c.state.queries++
	if strings.Contains(query, "WITH managed_tables(name) AS") {
		schema := Schema{Name: arguments[0].Value.(string)}
		values := make([][]driver.Value, 0)
		for _, descriptor := range expectedSchemaDescriptors(schema) {
			if descriptor.object == c.state.missing {
				continue
			}
			values = append(values, []driver.Value{descriptor.kind, descriptor.object, descriptor.member, descriptor.detail})
		}
		return &stubRows{columns: []string{"kind", "object", "member", "detail"}, values: values}, nil
	}
	if strings.Contains(query, "WITH inventory_rows AS MATERIALIZED") {
		var activeID, activeGeneration, activeDigest, setDigest driver.Value
		if c.state.active != (audit.CatalogRef{}) {
			activeID = string(c.state.active.ID)
			activeGeneration = int64(c.state.active.Generation)
			activeDigest = bytes.Clone(c.state.active.Digest[:])
			setDigest = bytes.Clone(c.state.set[:])
		}
		return &stubRows{
			columns: []string{"valid", "backing_id", "log_id", "active_catalog_id", "active_generation", "active_digest", "catalog_set_digest"},
			values:  [][]driver.Value{{true, c.state.backing[:], c.state.log[:], activeID, activeGeneration, activeDigest, setDigest}},
		}, nil
	}
	if strings.Contains(query, "SELECT singleton FROM") && strings.Contains(query, ".settings WHERE singleton FOR SHARE") {
		return &stubRows{columns: []string{"singleton"}, values: [][]driver.Value{{true}}}, nil
	}
	if strings.Contains(query, ".settings WHERE singleton") {
		var activeID, activeGeneration, activeDigest, setDigest driver.Value
		if c.state.active != (audit.CatalogRef{}) {
			activeID = string(c.state.active.ID)
			activeGeneration = int64(c.state.active.Generation)
			activeDigest = bytes.Clone(c.state.active.Digest[:])
			setDigest = bytes.Clone(c.state.set[:])
		}
		return &stubRows{
			columns: []string{"version", "fingerprint", "backing_id", "log_id", "active_catalog_id", "active_generation", "active_digest", "catalog_set_digest"},
			values:  [][]driver.Value{{int64(SchemaVersion), c.state.fingerprint, c.state.backing[:], c.state.log[:], activeID, activeGeneration, activeDigest, setDigest}},
		}, nil
	}
	if strings.Contains(query, "to_regclass") {
		name := arguments[0].Value.(string)
		if strings.HasSuffix(name, "."+c.state.missing) {
			return &stubRows{columns: []string{"to_regclass"}, values: [][]driver.Value{{nil}}}, nil
		}
		return &stubRows{columns: []string{"to_regclass"}, values: [][]driver.Value{{name}}}, nil
	}
	if strings.Contains(query, "INSERT INTO") && strings.Contains(query, ".revisions") {
		revisionID := bytes.Clone(arguments[0].Value.([]byte))
		key := string(revisionID)
		if _, exists := c.state.revisions[key]; exists {
			return &stubRows{columns: []string{"position", "recorded_at"}}, nil
		}
		c.state.position++
		record := stubRevision{
			wire: bytes.Clone(arguments[6].Value.([]byte)), intent: bytes.Clone(arguments[5].Value.([]byte)), semantic: bytes.Clone(arguments[4].Value.([]byte)),
			catalog: arguments[1].Value.(string), operation: arguments[3].Value.(string),
			recordedAt: time.Unix(1_800_000_000+c.state.position, 0).UTC(), position: c.state.position,
		}
		c.state.revisions[key] = record
		return &stubRows{columns: []string{"position", "recorded_at"}, values: [][]driver.Value{{record.position, record.recordedAt}}}, nil
	}
	if strings.Contains(query, "SELECT semantic, revision_id FROM") && strings.Contains(query, ".idempotency") {
		key := idempotencyKey(arguments[0].Value.(string), arguments[1].Value.(string), arguments[2].Value.([]byte))
		value, found := c.state.idempotency[key]
		if !found {
			return &stubRows{columns: []string{"semantic", "revision_id"}}, nil
		}
		return &stubRows{columns: []string{"semantic", "revision_id"}, values: [][]driver.Value{{bytes.Clone(value.semantic), bytes.Clone(value.revision)}}}, nil
	}
	if strings.Contains(query, ".idempotency i JOIN") {
		key := idempotencyKey(arguments[0].Value.(string), arguments[1].Value.(string), arguments[2].Value.([]byte))
		value, found := c.state.idempotency[key]
		if !found {
			return &stubRows{columns: []string{"wire", "intent", "recorded_at", "position"}}, nil
		}
		return revisionRows(c.state.revisions[string(value.revision)]), nil
	}
	if strings.Contains(query, "SELECT wire, intent, recorded_at, position") && strings.Contains(query, ".revisions") {
		record, found := c.state.revisions[string(arguments[0].Value.([]byte))]
		if !found {
			return &stubRows{columns: []string{"wire", "intent", "recorded_at", "position"}}, nil
		}
		if len(arguments) >= 3 && (record.catalog != arguments[1].Value.(string) || record.operation != arguments[2].Value.(string)) {
			return &stubRows{columns: []string{"wire", "intent", "recorded_at", "position"}}, nil
		}
		return revisionRows(record), nil
	}
	return nil, errors.New("unexpected query: " + query)
}

func revisionRows(record stubRevision) driver.Rows {
	return &stubRows{
		columns: []string{"wire", "intent", "recorded_at", "position"},
		values:  [][]driver.Value{{bytes.Clone(record.wire), bytes.Clone(record.intent), record.recordedAt, record.position}},
	}
}

func idempotencyKey(catalog, operation string, token []byte) string {
	return catalog + "\x00" + operation + "\x00" + string(token)
}

type stubTransaction struct{}

func (stubTransaction) Commit() error   { return nil }
func (stubTransaction) Rollback() error { return nil }

type stubRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *stubRows) Columns() []string { return r.columns }
func (*stubRows) Close() error        { return nil }
func (r *stubRows) Next(destination []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(destination, r.values[r.index])
	r.index++
	return nil
}

func newStubDB(t *testing.T) (*sql.DB, *stubDatabase) {
	t.Helper()
	fingerprint, err := (Schema{}).Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	state := &stubDatabase{fingerprint: fingerprint, revisions: make(map[string]stubRevision), idempotency: make(map[string]stubIdempotency)}
	state.backing[0], state.log[0] = 1, 2
	return sql.OpenDB(stubConnector{state: state}), state
}

func TestConstructorsPerformNoIOAndRequireOneDatasource(t *testing.T) {
	db, state := newStubDB(t)
	defer db.Close()
	source := crudsql.Postgres(db)
	store, err := New(Spec{DB: db, Source: source})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := NewDeployment(DeploymentSpec{Runtime: Spec{DB: db, Source: source}})
	if err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	queries, executions := state.queries, state.executions
	state.mu.Unlock()
	if queries != 0 || executions != 0 || store.BackingID() != (audit.BackingID{}) || deployment.LogID() != (audit.LogID{}) {
		t.Fatalf("constructor performed I/O or exposed readiness: queries=%d executions=%d", queries, executions)
	}
	other, _ := newStubDB(t)
	defer other.Close()
	if _, err := New(Spec{DB: db, Source: crudsql.Postgres(other)}); !errors.Is(err, ErrSpec) {
		t.Fatalf("different datasource error = %v", err)
	}
}

func TestCheckPublishesReadinessOnlyAfterCompleteVerification(t *testing.T) {
	db, state := newStubDB(t)
	defer db.Close()
	store, err := New(Spec{DB: db, Source: crudsql.Postgres(db)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.BackingID()[0] != 1 || store.LogID()[0] != 2 || store.Backing() == (audit.Backing{}) {
		t.Fatal("successful check did not publish persisted store identity")
	}
	state.missing = "revisions"
	if err := store.Check(context.Background()); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("missing-table check error = %v", err)
	}
	if store.BackingID() != (audit.BackingID{}) || store.LogID() != (audit.LogID{}) {
		t.Fatal("failed recheck retained stale readiness")
	}
	state.missing = ""
	state.backing = [16]byte{}
	if err := store.Check(context.Background()); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("zero-identity check error = %v", err)
	}
}

func TestBindTransactionAcceptsOnlyDirectCrudsqlRootAfterReadiness(t *testing.T) {
	db, _ := newStubDB(t)
	defer db.Close()
	source := crudsql.Postgres(db)
	store, err := New(Spec{DB: db, Source: source})
	if err != nil {
		t.Fatal(err)
	}
	rootTx, err := source.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer rootTx.Rollback(context.Background())
	if _, err := store.BindTransaction(rootTx); audit.CauseOf(err) != ErrNotReady {
		t.Fatalf("direct root before readiness cause = %v", audit.CauseOf(err))
	}
	if _, err := store.BindTransaction(crudsql.From(db, crudsql.WithTransaction())); err == nil || audit.CauseOf(err) == ErrNotReady {
		t.Fatalf("unproven executor was not refused first: %v", err)
	}
	if err := store.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	execution, err := store.BindTransaction(rootTx)
	if err != nil || execution == nil || !execution.Authority().Valid() {
		t.Fatalf("direct root bind = (%v, %v)", execution, err)
	}
	nested, err := rootTx.(crud.Beginner).Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer nested.Rollback(context.Background())
	if _, err := store.BindTransaction(nested); err == nil {
		t.Fatal("nested savepoint was accepted as the root transaction")
	}
}

func TestConcreteDeploymentAndRuntimeMethodSetsStayDisjoint(t *testing.T) {
	var store any = (*Store)(nil)
	if _, ok := store.(interface{ Migrate(context.Context) error }); ok {
		t.Fatal("runtime Store gained schema mutation authority")
	}
	var deployment any = (*Deployment)(nil)
	if _, ok := deployment.(audit.Writer); ok {
		t.Fatal("Deployment gained runtime append authority")
	}
}
