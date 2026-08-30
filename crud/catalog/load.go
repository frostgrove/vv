package catalog

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/frostgrove/vv/crud"
)

// Load reads source's catalog scope and answers a Catalog over it. PostgreSQL
// contributes every non-system schema the role may use; MySQL and MariaDB
// contribute DATABASE(); SQLite contributes main. Everything after this call
// is memory.
//
// It reads through crud.Source.Query and nothing else, because Exec and Query
// are all the seam has. SQLite's PRAGMAs return rows, so they go through Query
// like anything else.
//
// A failure is a failure: it answers a nil Catalog and never a half-built one
// beside a nil error. Degrading quietly to a schema with nothing in it would
// mean a feature that reads this is off in production and the only symptom is
// that it never reports anything ([[D-021]]).
//
// It takes no options. §16 of ROADMAP-errors.md still owes a number for catalog
// load time, and a knob whose default nobody has decided is surface for nobody;
// the caller's context is the only bound.
func Load(ctx context.Context, source crud.Source) (Catalog, error) {
	b, err := backendFor(ctx, source)
	if err != nil {
		return nil, err
	}
	read, err := b.read(ctx, source)
	if err != nil {
		return nil, err
	}
	c := &loaded{source: source, backend: b, now: time.Now}
	c.snap.Store(newSnapshot(read))
	return c, nil
}

// backend is one engine's introspection, plus the name that engine answers to in
// the vocabulary errs.Detail.Dialect uses.
type backend struct {
	dialect string
	read    func(ctx context.Context, source crud.Source) (*schemaRead, error)
}

// backendFor chooses the statements before any of them run.
//
// MariaDB has to be told from MySQL here rather than later, and not for tidiness:
// information_schema.STATISTICS has an EXPRESSION column on MySQL 8.4 and none
// on MariaDB 11.4, where selecting it fails outright with error 1054. One
// statement text cannot serve both, so the dialect has to be known before the
// first statement is sent. crud.Dialect.Name answers "mysql" for both, which is
// exactly what errs/sqlerr says it cannot be the source of.
func backendFor(ctx context.Context, source crud.Source) (backend, error) {
	d := source.Dialect()
	if d == nil {
		return backend{}, fmt.Errorf("%w: the source has no dialect", ErrUnknownDialect)
	}
	switch d.Name() {
	case "postgres":
		return backend{dialect: "postgres", read: readPostgres}, nil
	case "sqlite":
		return backend{dialect: "sqlite", read: readSQLite}, nil
	case "mysql":
		maria, err := isMariaDB(ctx, source)
		if err != nil {
			return backend{}, err
		}
		if maria {
			return backend{dialect: "mariadb", read: readMariaDB}, nil
		}
		return backend{dialect: "mysql", read: readMySQL}, nil
	}
	return backend{}, fmt.Errorf("%w: %q", ErrUnknownDialect, d.Name())
}

// isMariaDB reads the server's own version banner. It is a documented server
// API rather than localised error text, so [[D-039]] is not in play — but it
// costs one round trip per Load, and a MySQL-compatible proxy that rewrites the
// banner would be misread.
func isMariaDB(ctx context.Context, source crud.Source) (bool, error) {
	var banner string
	err := eachRow(ctx, source, "the server version", "SELECT VERSION()", nil, func(rows crud.Rows) error {
		return rows.Scan(&banner)
	})
	if err != nil {
		return false, err
	}
	return strings.Contains(strings.ToLower(banner), "mariadb"), nil
}

// eachRow runs one introspection statement and hands each row to scan.
//
// The read follows crud/sqlrepo's idiom exactly — close, loop, then rows.Err —
// because a driver reports a mid-stream failure there and nowhere else, and a
// loop that ends without asking reads an empty schema as a complete one. pgx is
// the concrete reason the third arm is load-bearing rather than defensive: there
// a refused statement can arrive as a live Rows carrying the error on Err.
//
// what names the statement, never a schema object, so ErrIntrospection's own text
// says nothing about the schema. The wrapped driver error is the server's own
// text and stays inside the process on both call paths: Load fails before any
// request exists, and Reload — the request-time one — returns an error no
// transport recognises, so it renders as a 500 with a silent body ([[D-044]]).
func eachRow(ctx context.Context, source crud.Source, what, q string, args []any, scan func(crud.Rows) error) error {
	rows, err := source.Query(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("%w: reading %s: %w", ErrIntrospection, what, err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return fmt.Errorf("%w: reading %s: %w", ErrIntrospection, what, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: reading %s: %w", ErrIntrospection, what, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// building

// builder collects several statements' rows into tables, keeping the order each
// statement reported them in. Nothing here sorts: every statement carries its
// own ORDER BY, so the order is the engine's and it is the same on every run
// ([[D-014]]).
type builder struct {
	tables []*tableBuild
	byKey  map[tableKey]*tableBuild
	// bare records lookup state outside the public Table value. Adding a private
	// field to an exported struct would break external positional literals; and
	// visibility is not schema metadata in the first place.
	bare map[tableKey]bool
	// schema is the one every table of this read belongs to, where the engine
	// has exactly one — MySQL's DATABASE(), SQLite's "main". PostgreSQL leaves
	// it empty: there a table carries the schema its own row reported.
	schema string
}

type tableKey struct{ schema, name string }

type tableBuild struct {
	t     Table
	cons  []*Constraint
	byCon map[conBuildKey]*Constraint
}

// conBuildKey is what a constraint under construction is found again by. The
// family is in the key because a name alone is not one object: an index name and
// a foreign-key name live in different namespaces on MySQL and MariaDB, so
// UNIQUE KEY k (a) beside CONSTRAINT k FOREIGN KEY (a) is legal and
// TABLE_CONSTRAINTS answers two rows for k, and on PostgreSQL a CHECK named k
// and a bare unique index named k are equally legal.
type conBuildKey struct {
	name   string
	family conFamily
}

// conFamily groups the kinds one object can be reported under. The three key
// kinds are one family because the read that names the kind and the read that
// names the key parts are the same object seen at two levels of detail —
// MySQL announces PRIMARY in TABLE_CONSTRAINTS and fills its columns in from
// STATISTICS, which knows only that the index is unique. A foreign key and a
// check are each their own.
type conFamily uint8

const (
	famKey conFamily = iota + 1
	famForeignKey
	famCheck
)

func familyOf(k Kind) conFamily {
	switch k {
	case KindForeignKey:
		return famForeignKey
	case KindCheck:
		return famCheck
	}
	return famKey
}

func newBuilder() *builder {
	return &builder{byKey: map[tableKey]*tableBuild{}, bare: map[tableKey]bool{}}
}

func (this *builder) markBare(schema, name string) {
	this.bare[tableKey{schema: schema, name: name}] = true
}

// table finds or starts a table. First sight fixes its position.
func (this *builder) table(schema, name string) *tableBuild {
	k := tableKey{schema, name}
	if tb, ok := this.byKey[k]; ok {
		return tb
	}
	tb := &tableBuild{t: Table{Name: name, Schema: schema}, byCon: map[conBuildKey]*Constraint{}}
	this.byKey[k] = tb
	this.tables = append(this.tables, tb)
	return tb
}

// constraint finds or starts one of a table's constraints. A constraint arrives
// over several rows — one per key column — so every back-end asks for it by name
// and fills in what the current row adds.
//
// By name *and* family, and that is the difference between one description of a
// schema and two objects folded into one. Measured on MySQL 8.4 and MariaDB
// 11.4, a name that is both a unique key and a foreign key came back as one
// constraint carrying the key parts of both — Columns twice as long as
// Expressions and RefColumns, which is the position parallel a probe reads its
// results by ([[D-042]]) — and with a Kind that differed between the two servers
// for identical DDL, because it was whichever row the server returned first.
func (this *tableBuild) constraint(name string, kind Kind) *Constraint {
	k := conBuildKey{name: name, family: familyOf(kind)}
	if c, ok := this.byCon[k]; ok {
		return c
	}
	c := &Constraint{Name: name, Table: this.t.Name, Schema: this.t.Schema, Kind: kind}
	this.byCon[k] = c
	this.cons = append(this.cons, c)
	return c
}

// schemaRead is the public metadata plus the private resolution state from the
// same introspection pass.
type schemaRead struct {
	tables []Table
	bare   map[tableKey]bool
}

// finish flattens the build into the value Load stores. The primary key is
// taken from the primary-key constraint where one was read and left alone where
// a back-end filled it in itself — SQLite reports a rowid primary key through
// its column pragma and through no index at all.
func (this *builder) finish() *schemaRead {
	out := make([]Table, 0, len(this.tables))
	for _, tb := range this.tables {
		tb.t.Constraints = make([]Constraint, len(tb.cons))
		for i, c := range tb.cons {
			tb.t.Constraints[i] = *c
			if c.Kind == KindPrimaryKey && tb.t.PrimaryKey == nil {
				tb.t.PrimaryKey = c.Columns
			}
		}
		out = append(out, tb.t)
	}
	return &schemaRead{tables: out, bare: this.bare}
}

// ---------------------------------------------------------------------------
// the loaded catalog

// snapshot is one whole schema, swapped as a unit. A lookup that ran while a
// reload was half done would see two schemas at once, which is worse than
// seeing the old one.
type snapshot struct {
	tables    []Table
	byName    map[string]int
	byRef     map[tableKey]int
	byCons    map[consKey]*Constraint
	byConsRef map[qualifiedConsKey]*Constraint
	// refs is the inbound direction: which foreign keys point at a table. Built
	// here rather than walked per lookup because the walk is over every table in
	// the database and a lookup does no work ([[D-041]]).
	refs    map[string][]*Constraint
	refsRef map[tableKey][]*Constraint
}

type consKey struct{ table, name string }
type qualifiedConsKey struct {
	table tableKey
	name  string
}

func newSnapshot(read *schemaRead) *snapshot {
	tables := read.tables
	s := &snapshot{
		tables:    tables,
		byName:    make(map[string]int, len(tables)),
		byRef:     make(map[tableKey]int, len(tables)),
		byCons:    map[consKey]*Constraint{},
		byConsRef: map[qualifiedConsKey]*Constraint{},
		refs:      map[string][]*Constraint{},
		refsRef:   map[tableKey][]*Constraint{},
	}
	for i := range tables {
		key := tableKey{schema: tables[i].Schema, name: tables[i].Name}
		if _, seen := s.byRef[key]; !seen {
			s.byRef[key] = i
		}
		// PostgreSQL loads qualified tables outside search_path as well, but a
		// legacy bare lookup must still mean exactly what the server would bind.
		if read.bare[key] {
			s.byName[tables[i].Name] = i
		}
	}
	for i := range tables {
		tableRef := tableKey{schema: tables[i].Schema, name: tables[i].Name}
		for j := range tables[i].Constraints {
			c := &tables[i].Constraints[j]
			qk := qualifiedConsKey{table: tableRef, name: c.Name}
			if _, seen := s.byConsRef[qk]; !seen {
				s.byConsRef[qk] = c
			}
			// One name can be two objects on one table — a unique key and a
			// foreign key on MySQL, a CHECK and a bare unique index on
			// PostgreSQL — and this lookup answers one. The first wins, which
			// is the order the engine listed them in and is the same on every
			// run because every statement carries its ORDER BY ([[D-014]]).
			if bare, ok := s.byName[tables[i].Name]; ok && bare == i {
				k := consKey{table: tables[i].Name, name: c.Name}
				if _, seen := s.byCons[k]; !seen {
					s.byCons[k] = c
				}
			}
			if c.Kind == KindForeignKey && c.RefTable != "" {
				ref := tableKey{schema: c.RefSchema, name: c.RefTable}
				s.refsRef[ref] = append(s.refsRef[ref], c)
				if bare, ok := s.byName[c.RefTable]; ok {
					resolved := &s.tables[bare]
					if resolved.Schema == c.RefSchema {
						s.refs[c.RefTable] = append(s.refs[c.RefTable], c)
					}
				}
			}
		}
	}
	return s
}

// loaded is the Catalog Load returns. It is also a Reloader — see reload.go for
// why that is a separate interface.
type loaded struct {
	source  crud.Source
	backend backend
	snap    atomic.Pointer[snapshot]

	mu     sync.Mutex
	misses map[consKey]negative
	floor  time.Time
	now    func() time.Time
}

func (this *loaded) Dialect() string { return this.backend.dialect }

func (this *loaded) Table(name string) (*Table, bool) {
	s := this.snap.Load()
	i, ok := s.byName[name]
	if !ok {
		return nil, false
	}
	return &s.tables[i], true
}

// TableByRef performs an exact component lookup. A bare ref deliberately
// delegates to Table, retaining the engine's own unqualified-name semantics.
func (this *loaded) TableByRef(table crud.TableRef) (*Table, bool) {
	if table.Validate() != nil {
		return nil, false
	}
	if table.Schema == "" {
		return this.Table(table.Name)
	}
	s := this.snap.Load()
	i, ok := s.byRef[tableKey{schema: table.Schema, name: table.Name}]
	if !ok {
		return nil, false
	}
	return &s.tables[i], true
}

// ReferencedBy answers the inbound direction. A table nothing points at answers
// nil rather than an empty slice, because the two mean the same thing to every
// reader and one of them costs an allocation per lookup.
func (this *loaded) ReferencedBy(table string) []*Constraint {
	return this.snap.Load().refs[table]
}

func (this *loaded) ReferencedByRef(table crud.TableRef) []*Constraint {
	if table.Validate() != nil {
		return nil
	}
	if table.Schema == "" {
		return this.ReferencedBy(table.Name)
	}
	return this.snap.Load().refsRef[tableKey{schema: table.Schema, name: table.Name}]
}

func (this *loaded) Constraint(table, name string) (*Constraint, bool) {
	s := this.snap.Load()
	con, ok := s.byCons[consKey{table: table, name: name}]
	return con, ok
}

func (this *loaded) ConstraintByRef(table crud.TableRef, name string) (*Constraint, bool) {
	if table.Validate() != nil {
		return nil, false
	}
	if table.Schema == "" {
		return this.Constraint(table.Name, name)
	}
	con, ok := this.snap.Load().byConsRef[qualifiedConsKey{
		table: tableKey{schema: table.Schema, name: table.Name},
		name:  name,
	}]
	return con, ok
}

var (
	_ Catalog            = (*loaded)(nil)
	_ Reloader           = (*loaded)(nil)
	_ Referrers          = (*loaded)(nil)
	_ QualifiedCatalog   = (*loaded)(nil)
	_ QualifiedReferrers = (*loaded)(nil)
)
