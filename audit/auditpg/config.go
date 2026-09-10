package auditpg

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/crud"
)

var (
	ErrSpec           = errors.New("auditpg: invalid store specification")
	ErrSchemaMismatch = errors.New("auditpg: deployed schema does not match this build")
	ErrNotReady       = errors.New("auditpg: schema has not been verified")
)

type SchemaManagement uint8

const (
	UnsetSchemaManagement SchemaManagement = iota
	VerifySchema
	ManageSchema
)

func (m SchemaManagement) Valid() bool {
	return m == UnsetSchemaManagement || m == VerifySchema || m == ManageSchema
}

func (m SchemaManagement) String() string {
	switch m {
	case UnsetSchemaManagement:
		return "[schema management unset]"
	case VerifySchema:
		return "[schema management verify]"
	case ManageSchema:
		return "[schema management manage]"
	default:
		return "[schema management " + strconv.Itoa(int(m)) + "]"
	}
}

type Spec struct {
	DB     *sql.DB
	Source crud.Source
	Schema Schema
	Limits audit.LimitSpec
}

type DeploymentSpec struct {
	Runtime          Spec
	SchemaManagement SchemaManagement
}

type configured struct {
	db      *sql.DB
	source  crud.Source
	schema  Schema
	limits  audit.Limits
	backing audit.Backing
}

type backingIdentity struct {
	db     *sql.DB
	schema string
}

type readiness struct {
	backingID audit.BackingID
	logID     audit.LogID
	catalogs  audit.StoreCatalogState
}

func configure(spec Spec) (configured, error) {
	if spec.DB == nil {
		return configured{}, fmt.Errorf("%w: DB is nil", ErrSpec)
	}
	if nilInterface(spec.Source) {
		return configured{}, fmt.Errorf("%w: Source is nil", ErrSpec)
	}
	if !crud.SameDataSource(crud.KeyOf(spec.Source), spec.DB) {
		return configured{}, fmt.Errorf("%w: Source and DB do not identify the same datasource", ErrSpec)
	}
	if _, ok := spec.Source.Dialect().(crud.Postgres); !ok {
		return configured{}, fmt.Errorf("%w: Source is not PostgreSQL", ErrSpec)
	}
	schema, err := spec.Schema.Resolved()
	if err != nil {
		return configured{}, err
	}
	limits, err := resolvedLimits(spec.Limits)
	if err != nil {
		return configured{}, fmt.Errorf("%w: limits: %v", ErrSpec, err)
	}
	backing, err := audit.BackingFor(backingIdentity{db: spec.DB, schema: schema.Name})
	if err != nil {
		return configured{}, fmt.Errorf("%w: backing identity: %v", ErrSpec, err)
	}
	return configured{db: spec.DB, source: spec.Source, schema: schema, limits: limits, backing: backing}, nil
}

func resolvedLimits(spec audit.LimitSpec) (audit.Limits, error) {
	if spec == (audit.LimitSpec{}) {
		spec = audit.LimitSpec{
			RevisionBytes: 16 << 20, AppendRequestBytes: 40 << 20,
			PageRevisions: 1000, PageBytes: 32 << 20, ExactTargets: 10_000, ExactBytes: 32 << 20,
			PositionBytes: 4096, InventoryCandidates: 10_000, InventoryCohorts: 10_000,
			InventoryBytes: 32 << 20, SnapshotBytes: 8 << 20,
			SearchCohortRevisions: 1_000_000, SearchCohortBytes: 256 << 20,
			AttemptTransitions: 259, AttemptStateBytes: 4 << 20, AttemptOpenLifetime: 365 * 24 * time.Hour,
		}
	}
	return audit.NewLimits(spec)
}

func capabilities() audit.Capabilities {
	value, _ := audit.NewCapabilities(audit.CapabilitySpec{
		Transactions: audit.SupportSupported, CrossSystemAtomic: audit.SupportSupported,
		Persistence: audit.SupportSupported, Idempotency: audit.SupportSupported,
		Reconciliation: audit.SupportSupported, StableSearch: audit.SupportUnsupported,
		ExactInspection: audit.SupportSupported, AttemptLifecycle: audit.SupportSupported,
		Holds: audit.SupportUnsupported, PurgePlanning: audit.SupportUnsupported,
	})
	return value
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

func loadReadiness(target *atomic.Pointer[readiness]) *readiness {
	return target.Load()
}
