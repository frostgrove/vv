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

type backend struct {
	dialect string
	read    func(ctx context.Context, source crud.Source) (*schemaRead, error)
}

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

type builder struct {
	tables []*tableBuild
	byKey  map[tableKey]*tableBuild

	bare map[tableKey]bool

	schema string
}

type tableKey struct{ schema, name string }

type tableBuild struct {
	t     Table
	cons  []*Constraint
	byCon map[conBuildKey]*Constraint
}

type conBuildKey struct {
	name   string
	family conFamily
}

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

type schemaRead struct {
	tables []Table
	bare   map[tableKey]bool
}

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

type snapshot struct {
	tables    []Table
	byName    map[string]int
	byRef     map[tableKey]int
	byCons    map[consKey]*Constraint
	byConsRef map[qualifiedConsKey]*Constraint

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
