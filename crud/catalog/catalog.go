package catalog

import "github.com/frostgrove/vv/crud"

// Catalog is one database's schema, read once and answered from memory.
//
// Constraint takes the table as well as the name because an index name is unique
// per table on MySQL rather than per schema — every InnoDB table's primary index
// is called PRIMARY, and the corpus records MariaDB reporting a duplicate key as
// `for key 'PRIMARY'` with nothing naming the table. A bare name is ambiguous
// across the database.
//
// Neither lookup takes a context, and that is the whole point: a signature that
// accepted one would be a lazy loader, and a lazy loader cannot fail at start-up.
type Catalog interface {
	Table(name string) (*Table, bool)
	Constraint(table, name string) (*Constraint, bool)
	// Dialect names the engine in the vocabulary errs.Detail.Dialect uses —
	// "postgres", "mysql", "mariadb", "sqlite". It is not crud.Dialect.Name,
	// which answers "mysql" for MariaDB and so cannot tell the two apart.
	Dialect() string
}

// QualifiedCatalog is the optional capability for looking up a table by its
// already-separated schema and name. Load returns a catalog that implements
// it. Keeping it separate from Catalog preserves the deliberately small seam
// third-party catalogs implement, while consumers that must not guess at a
// qualified name can fail at declaration when the capability is absent.
//
// A TableRef with no Schema has the same meaning as the corresponding legacy
// bare-name lookup. Components are never joined and parsed again.
type QualifiedCatalog interface {
	TableByRef(table crud.TableRef) (*Table, bool)
	ConstraintByRef(table crud.TableRef, name string) (*Constraint, bool)
}

// Referrers is the optional interface a Catalog implements to answer which
// foreign keys point *at* a table — the inbound direction, which no lookup on
// Catalog can express because a constraint is recorded on the table that
// declares it.
//
// It is separate from Catalog for the reason Reloader is: the interface a
// consumer implements has to stay the small one. What Load returns implements
// this; a Catalog written elsewhere need not, and a reader that asks and is
// refused simply reports fewer violations.
//
// The order is the order the tables and their constraints were read in, which
// every loading statement fixes with an ORDER BY ([[D-014]]).
type Referrers interface {
	ReferencedBy(table string) []*Constraint
}

// QualifiedReferrers is the structured counterpart to Referrers. Load returns
// a catalog that implements it, so two schemas containing a table of the same
// name cannot merge their inbound foreign keys.
type QualifiedReferrers interface {
	ReferencedByRef(table crud.TableRef) []*Constraint
}

// Kind is what the engine said a constraint is.
//
// KindUnique and KindUniqueIndex are separate because two of the four engines
// distinguish them: PostgreSQL by whether a pg_constraint row backs the index,
// SQLite by pragma_index_list.origin. MySQL and MariaDB list every unique index
// in TABLE_CONSTRAINTS as UNIQUE, so on those two the distinction is one the
// server does not make and the Kind records that rather than inventing it.
type Kind uint8

const (
	KindPrimaryKey Kind = iota + 1
	KindUnique
	KindUniqueIndex
	KindForeignKey
	KindCheck
)

func (this Kind) String() string {
	switch this {
	case KindPrimaryKey:
		return "primary key"
	case KindUnique:
		return "unique"
	case KindUniqueIndex:
		return "unique index"
	case KindForeignKey:
		return "foreign key"
	case KindCheck:
		return "check"
	}
	return "unknown"
}

// Column is one column as the engine described it.
type Column struct {
	// Name and Position as the engine reported them; Position is 1-based and is
	// the engine's own ordinal, not this slice's index.
	Name     string
	Position int
	// Type is the engine's own spelling — "character varying(255)", "int(11)",
	// "VARCHAR(255)". It is carried for a human reader and never parsed.
	Type     string
	Nullable bool
	// Default is nil when the column has none. A pointer rather than a string
	// because MySQL reports a DEFAULT '' as the empty string, so a plain string
	// cannot tell "no default" from "defaults to nothing".
	Default *string
	// MaxLength is the declared character length where the engine enforces one,
	// and 0 otherwise. SQLite is always 0: it records VARCHAR(255) in Type and
	// enforces nothing, and a catalog answering 255 would claim an enforcement
	// that does not exist ([[D-019]] difference 6).
	MaxLength int
	Generated bool
}

// Table is one table and everything read about it.
type Table struct {
	Name string
	// Schema is the table's exact PostgreSQL schema, MySQL database, or SQLite
	// database name. It is identity, not a dotted prefix folded into Name.
	Schema string
	// Columns in the engine's own order, pinned by an explicit ORDER BY.
	Columns []Column
	// PrimaryKey is the key's columns in key order, nil when the engine reported
	// no primary key.
	PrimaryKey []string
	// Constraints in a deterministic order: the constraint catalog first, then
	// the unique indexes no constraint backs, each by name.
	Constraints []Constraint
	// Definition is the engine's own CREATE TABLE text where the engine hands
	// one back in the read that lists tables, and empty where it does not. Only
	// SQLite fills it. Verbatim, and parsed by nothing.
	Definition string
}

// Column finds a column by name. A nil table answers false rather than
// panicking.
func (this *Table) Column(name string) (*Column, bool) {
	if this == nil {
		return nil, false
	}
	for i := range this.Columns {
		if this.Columns[i].Name == name {
			return &this.Columns[i], true
		}
	}
	return nil, false
}

// Constraint finds one of this table's constraints by name. A nil table answers
// false.
func (this *Table) Constraint(name string) (*Constraint, bool) {
	if this == nil {
		return nil, false
	}
	for i := range this.Constraints {
		if this.Constraints[i].Name == name {
			return &this.Constraints[i], true
		}
	}
	return nil, false
}

// Constraint is one key, foreign key or check, as the engine described it.
//
// Columns, Expressions and Prefixes are parallel by position: entry i of each
// describes key part i. Parallel rather than one slice of structs because a
// probe reads its results by column position ([[D-042]], citing [[D-014]]), and
// a map here would lose the order that carries the identity.
type Constraint struct {
	Name   string
	Table  string
	Schema string
	Kind   Kind
	// Columns are the key's columns in key order. An expression key has "" in
	// its position and the text in Expressions. Nil means not known; empty means
	// the engine reported none.
	Columns []string
	// Expressions holds the engine's own text for an expression key part, "" for
	// a plain column. Nil where the engine hands no text back at all: MariaDB
	// 11.4 has no expression indexes, and SQLite has them and reports only that
	// the part is one — index_xinfo marks it with cid = -2 and the text lives in
	// the index's DDL, which nothing here parses.
	Expressions []string
	// Prefixes holds a prefix length for a key part indexed on its first n
	// characters, 0 for a whole-column part. MySQL and MariaDB only. A prefix
	// key is the one those two engines cannot reproduce from a value.
	Prefixes []int
	// Partial reports that the engine applies this key to some rows only.
	// Partial with an empty Predicate means the predicate is not recoverable as
	// a value — see the package doc. PostgreSQL and SQLite only.
	Partial bool
	// Predicate is the partial index's WHERE clause as the engine renders it,
	// and empty when the engine does not hand one back.
	Predicate string
	// Definition is the engine's own text for the whole constraint or index —
	// pg_get_constraintdef, pg_get_indexdef, sqlite_master.sql. Verbatim, and
	// parsed by nothing.
	Definition string
	// RefTable, RefSchema and RefColumns describe a foreign key's target;
	// RefColumns is parallel to Columns by position.
	RefTable   string
	RefSchema  string
	RefColumns []string
	// OnDelete and OnUpdate are the engine's own spelling — "CASCADE", "SET
	// NULL", "NO ACTION". Empty for anything that is not a foreign key.
	OnDelete string
	OnUpdate string
	// Deferrable reports a constraint that can fire at COMMIT rather than at the
	// statement. PostgreSQL only.
	Deferrable bool
}
