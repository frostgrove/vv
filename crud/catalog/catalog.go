package catalog

import "github.com/frostgrove/vv/crud"

type Catalog interface {
	Table(name string) (*Table, bool)
	Constraint(table, name string) (*Constraint, bool)

	Dialect() string
}

type QualifiedCatalog interface {
	TableByRef(table crud.TableRef) (*Table, bool)
	ConstraintByRef(table crud.TableRef, name string) (*Constraint, bool)
}

type Referrers interface {
	ReferencedBy(table string) []*Constraint
}

type QualifiedReferrers interface {
	ReferencedByRef(table crud.TableRef) []*Constraint
}

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

type Column struct {
	Name     string
	Position int

	Type     string
	Nullable bool

	Default *string

	MaxLength int
	Generated bool
}

type Table struct {
	Name string

	Schema string

	Columns []Column

	PrimaryKey []string

	Constraints []Constraint

	Definition string
}

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

type Constraint struct {
	Name   string
	Table  string
	Schema string
	Kind   Kind

	Columns []string

	Expressions []string

	Prefixes []int

	Partial bool

	Predicate string

	Definition string

	RefTable   string
	RefSchema  string
	RefColumns []string

	OnDelete string
	OnUpdate string

	Deferrable bool
}
