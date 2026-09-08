package eventpg

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/event"
)

// Three, because they are read by three different people. ErrSpec is a wiring
// error a composition root reads at start-up, ErrSchemaMismatch an operations
// error a deployment reads, ErrNotReady a lifecycle error a probe reads. None
// of them crosses the store seam as a sentinel: they travel as the cause of an
// event.Failure and are reached through event.CauseOf.
var (
	ErrSpec           = errors.New("eventpg: this store cannot be assembled from this spec")
	ErrSchemaMismatch = errors.New("eventpg: the deployed schema is not the one this build expects")
	ErrNotReady       = errors.New("eventpg: this store has not verified its schema")
)

// The zero value is unset and resolves to VerifySchema. Nothing migrates unless
// a deployment asked for it by name.
type SchemaManagement uint8

const (
	UnsetSchemaManagement SchemaManagement = iota
	VerifySchema
	ManageSchema
)

func (this SchemaManagement) Valid() bool {
	return this == UnsetSchemaManagement || this == VerifySchema || this == ManageSchema
}

func (this SchemaManagement) String() string {
	switch this {
	case VerifySchema:
		return "[schema management verify]"
	case ManageSchema:
		return "[schema management manage]"
	case UnsetSchemaManagement:
		return "[schema management unset]"
	default:
		return "[schema management " + strconv.Itoa(int(this)) + "]"
	}
}

type Spec struct {
	DB     *sql.DB
	Source crud.Source

	Schema           Schema
	SchemaManagement SchemaManagement

	MaxBatch   int
	StreamPage int
	MaxRead    int
}

var _ event.Store = (*Store)(nil)

type Store struct {
	db         *sql.DB
	source     crud.Source
	schema     Schema
	management SchemaManagement
	backing    event.Backing
	limits     event.Limits
	closed     atomic.Bool
	state      atomic.Pointer[readiness]
}

// The database and the schema together, and comparable so event.Backing can
// hold it. A *sql.DB alone would make two schemas of one database one backing,
// and a cursor minted over the other one would then be accepted rather than
// refused.
type backing struct {
	db     *sql.DB
	schema string
}

// Performs no I/O, starts nothing and reads no environment: a store over a DSN
// that resolves nowhere is a store, and it fails at the first operation rather
// than at construction.
func New(spec Spec) (*Store, error) {
	if spec.DB == nil {
		return nil, fmt.Errorf("%w: Spec.DB is nil, and a store over no database is a store that refuses everything at its first call", ErrSpec)
	}
	if spec.Source == nil {
		return nil, fmt.Errorf("%w: Spec.Source is nil, and a store that cannot see the caller's transaction writes outside it silently", ErrSpec)
	}
	if !crud.SameDataSource(crud.KeyOf(spec.Source), spec.DB) {
		return nil, fmt.Errorf("%w: Spec.Source reads another data source than Spec.DB, and atomic across two handles is a word with no meaning", ErrSpec)
	}
	if !spec.SchemaManagement.Valid() {
		return nil, fmt.Errorf("%w: Spec.SchemaManagement is %s, and the three it may be are %s, %s and %s",
			ErrSpec, spec.SchemaManagement, UnsetSchemaManagement, VerifySchema, ManageSchema)
	}
	schema, err := spec.Schema.Resolved()
	if err != nil {
		return nil, err
	}
	limits, err := limitsOf(spec, schema)
	if err != nil {
		return nil, err
	}
	held, err := event.NewBacking(backing{db: spec.DB, schema: schema.Name})
	if err != nil {
		return nil, err
	}
	management := spec.SchemaManagement
	if management == UnsetSchemaManagement {
		management = VerifySchema
	}
	return &Store{
		db:         spec.DB,
		source:     spec.Source,
		schema:     schema,
		management: management,
		backing:    held,
		limits:     limits,
	}, nil
}

func limitsOf(spec Spec, schema Schema) (event.Limits, error) {
	maxBatch, err := bound("Spec.MaxBatch", spec.MaxBatch, DefaultMaxBatch, event.MaxBatchCount)
	if err != nil {
		return event.Limits{}, err
	}
	resident := event.ResidentPage(schema.MaxPayload)
	page := min(DefaultPage, resident)
	streamPage, err := bound("Spec.StreamPage", spec.StreamPage, page, event.MaxPageCount)
	if err != nil {
		return event.Limits{}, err
	}
	maxRead, err := bound("Spec.MaxRead", spec.MaxRead, page, event.MaxPageCount)
	if err != nil {
		return event.Limits{}, err
	}
	if err := fits("Spec.StreamPage", streamPage, schema.MaxPayload, resident); err != nil {
		return event.Limits{}, err
	}
	if err := fits("Spec.MaxRead", maxRead, schema.MaxPayload, resident); err != nil {
		return event.Limits{}, err
	}
	return event.Limits{
		MaxPayload: schema.MaxPayload,
		MaxBatch:   maxBatch,
		MaxKey:     schema.MaxKey,
		StreamPage: streamPage,
		MaxRead:    maxRead,
	}, nil
}

// The kernel's own resident rule, applied one door earlier so the refusal names
// the number the caller set and the one it may not pass.
func fits(name string, chosen, maxPayload, resident int) error {
	if chosen > resident {
		return fmt.Errorf("%w: %s is %d at a payload bound of %d, more than the %d envelopes one read may hold",
			ErrSpec, name, chosen, maxPayload, resident)
	}
	return nil
}

func bound(name string, chosen, byDefault, ceiling int) (int, error) {
	switch {
	case chosen == 0:
		return byDefault, nil
	case chosen < 0:
		return 0, fmt.Errorf("%w: %s is %d", ErrSpec, name, chosen)
	case chosen > ceiling:
		return 0, fmt.Errorf("%w: %s is %d, above the kernel ceiling of %d", ErrSpec, name, chosen, ceiling)
	}
	return chosen, nil
}

func (this *Store) Schema() Schema { return this.schema }

func (this *Store) SchemaManagement() SchemaManagement { return this.management }

func (this *Store) Capabilities() event.Capabilities {
	return event.Capabilities{
		Transactions:       event.Supported,
		Persistence:        event.Supported,
		MonotoneVisibility: event.Unsupported,
		SharedBacking:      event.Supported,
	}
}

func (this *Store) Limits() event.Limits { return this.limits }

func (this *Store) Backing() event.Backing { return this.backing }

// It closes no *sql.DB: the pool was opened by the composition root and is
// shared with every other store, repository and subsystem over it.
func (this *Store) Close() error {
	this.closed.Store(true)
	return nil
}
