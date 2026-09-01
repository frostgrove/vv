package jobspg

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/jobs"
)

const DefaultSchema = "frostgrove_jobs"
const SchemaVersion = 4

var ErrNotReady = errors.New("jobspg: driver is not ready")
var ErrSchemaMismatch = errors.New("jobspg: schema mismatch")
var ErrCatalogMismatch = errors.New("jobspg: catalog mismatch")

type SchemaManagement uint8

const (
	ManageSchema SchemaManagement = iota
	VerifySchema
)

func (management SchemaManagement) Valid() bool {
	return management == ManageSchema || management == VerifySchema
}

type Spec struct {
	DB               *sql.DB
	Source           crud.Source
	Namespace        jobs.Namespace
	Catalog          jobs.Catalog
	Schema           string
	Backend          jobs.BackendID
	Entropy          io.Reader
	SchemaManagement SchemaManagement
}

type Driver struct {
	db               *sql.DB
	source           crud.Source
	namespace        jobs.Namespace
	catalog          jobs.Catalog
	description      jobs.BackendDescription
	repo             repository
	entropy          io.Reader
	entropyMu        sync.Mutex
	schemaManagement SchemaManagement
	ready            atomic.Bool
}

var _ jobs.Sender = (*Driver)(nil)
var _ jobs.DeliveryDriver = (*Driver)(nil)
var _ jobs.RetentionSweeper = (*Driver)(nil)

func Open(ctx context.Context, db *sql.DB, namespace jobs.Namespace, catalog jobs.Catalog) (*Driver, error) {
	driver, err := New(Spec{DB: db, Namespace: namespace, Catalog: catalog})
	if err != nil {
		return nil, err
	}
	if err := driver.Prepare(ctx); err != nil {
		return nil, err
	}
	return driver, nil
}

func New(spec Spec) (*Driver, error) {
	if spec.DB == nil || spec.Namespace.IsZero() || spec.Catalog.Len() == 0 || spec.Catalog.Fingerprint() == "" {
		return nil, fmt.Errorf("jobspg: %w: database, namespace, and catalog are required", jobs.ErrInvalid)
	}
	if !spec.SchemaManagement.Valid() {
		return nil, fmt.Errorf("jobspg: %w: schema management", jobs.ErrInvalid)
	}
	if spec.Source != nil && !crud.SameDataSource(crud.KeyOf(spec.Source), spec.DB) {
		return nil, fmt.Errorf("jobspg: %w: CRUD source must use the configured database", jobs.ErrInvalid)
	}
	schema := spec.Schema
	if schema == "" {
		schema = DefaultSchema
	}
	if !validSchema(schema) {
		return nil, fmt.Errorf("jobspg: %w: invalid PostgreSQL schema", jobs.ErrInvalid)
	}
	backend := spec.Backend
	if backend.IsZero() {
		backend = defaultBackend(schema, spec.Namespace)
	}
	failures, err := jobs.Failures(jobs.FailureProcessCrash)
	if err != nil {
		return nil, err
	}
	durability, err := jobs.NewDurabilityProfile(jobs.AckLocalPersistence, jobs.AcknowledgedLossExcludedForDeclaredFailures, failures)
	if err != nil {
		return nil, err
	}
	resources, err := jobs.NewResourceProfile(jobs.ResourceProfileSpec{
		SteadyBase: jobs.ResourcesSpec{MaxConcurrentDBOps: 2},
		PerWorker:  jobs.ResourcesSpec{MaxConcurrentDBOps: 1},
		Lifecycle:  jobs.ResourcesSpec{PinnedConnections: 1, MaxConcurrentDBOps: 1},
	})
	if err != nil {
		return nil, err
	}
	description, err := jobs.NewBackendDescriptionWithResources(backend, durability, jobs.Capabilities{Priority: true, Debounce: true, Unique: true, Scheduled: true}, resources)
	if err != nil {
		return nil, err
	}
	entropy := spec.Entropy
	if entropy == nil {
		entropy = rand.Reader
	}
	return &Driver{
		db:               spec.DB,
		source:           spec.Source,
		namespace:        spec.Namespace,
		catalog:          spec.Catalog,
		description:      description,
		repo:             newRepository(schema),
		entropy:          entropy,
		schemaManagement: spec.SchemaManagement,
	}, nil
}

func (d *Driver) Description() jobs.BackendDescription {
	if d == nil {
		return jobs.BackendDescription{}
	}
	return d.description
}

func (d *Driver) Namespace() jobs.Namespace {
	if d == nil {
		return jobs.Namespace{}
	}
	return d.namespace
}

func (d *Driver) Catalog() jobs.Catalog {
	if d == nil {
		return jobs.Catalog{}
	}
	return d.catalog
}

func (d *Driver) SchemaManagement() SchemaManagement {
	if d == nil {
		return ManageSchema
	}
	return d.schemaManagement
}

func (d *Driver) Prepare(ctx context.Context) error {
	if d == nil {
		return ErrNotReady
	}
	d.ready.Store(false)
	if d.schemaManagement == ManageSchema {
		if err := d.Migrate(ctx); err != nil {
			return err
		}
		if err := d.BindCatalog(ctx); err != nil {
			return err
		}
	}
	return d.Check(ctx)
}

func (d *Driver) Migrate(ctx context.Context) error {
	if d == nil || d.db == nil {
		return ErrNotReady
	}
	return d.repo.migrate(ctx, d.db)
}

func (d *Driver) BindCatalog(ctx context.Context) error {
	if d == nil || d.db == nil {
		return ErrNotReady
	}
	return d.repo.bindCatalog(ctx, d.db, d.namespace, d.catalog)
}

func (d *Driver) Check(ctx context.Context) error {
	if d == nil || d.db == nil {
		return ErrNotReady
	}
	d.ready.Store(false)
	if err := d.repo.check(ctx, d.db, d.namespace, d.catalog); err != nil {
		return err
	}
	d.ready.Store(true)
	return nil
}

func (d *Driver) CheckSchema(ctx context.Context) error {
	return d.Check(ctx)
}

func (d *Driver) requireReady() error {
	if d == nil || d.db == nil || !d.ready.Load() {
		return ErrNotReady
	}
	return nil
}

func (d *Driver) token() ([]byte, error) {
	value := make([]byte, 32)
	d.entropyMu.Lock()
	_, err := io.ReadFull(d.entropy, value)
	d.entropyMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("jobspg: lease entropy: %w", err)
	}
	return value, nil
}

func defaultBackend(schema string, namespace jobs.Namespace) jobs.BackendID {
	digest := sha256.New()
	_, _ = digest.Write([]byte("frostgrove.jobs.postgres.v1\x00"))
	_, _ = digest.Write([]byte(schema))
	value := namespace.Digest()
	_, _ = digest.Write(value[:])
	var raw [jobs.BackendIDBytes]byte
	copy(raw[:], digest.Sum(nil))
	backend, _ := jobs.BackendIDFromBytes(raw)
	return backend
}

func validSchema(value string) bool {
	if len(value) == 0 || len(value) > 63 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		current := value[index]
		if current < 'a' || current > 'z' {
			if current < '0' || current > '9' {
				if current != '_' {
					return false
				}
			}
		}
	}
	return true
}
