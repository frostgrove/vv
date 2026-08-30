package crud

import (
	"fmt"
	"strings"
)

// TableRef is a physical table identifier split into database identifier
// components. Schema is PostgreSQL's schema, MySQL's database, or SQLite's
// attached-database name; Name is always the table component.
//
// Components are deliberately not parsed from a dotted string. A dot can be a
// literal character inside a quoted identifier, and guessing whether it is a
// separator would make the same declaration mean different physical tables.
// Use [NewTableRefInSchema] when the qualifier is known separately.
type TableRef struct {
	Schema string
	Name   string
}

// TableRefError reports an invalid structured table identifier before a
// statement reaches a datasource.
type TableRefError struct {
	Component string
	Value     string
	Reason    string
}

func (this *TableRefError) Error() string {
	if this.Component == "" {
		return "crud: invalid table reference: " + this.Reason
	}
	return fmt.Sprintf("crud: invalid table %s %q: %s", this.Component, this.Value, this.Reason)
}

// NewTableRef validates one unqualified table-name component. It refuses a dot
// rather than guessing that the part before it is a schema; use
// NewTableRefInSchema for a qualified table.
func NewTableRef(name string) (TableRef, error) {
	if strings.Contains(name, ".") {
		return TableRef{}, &TableRefError{Component: "name", Value: name,
			Reason: "a dotted string is ambiguous; pass schema and table as separate components"}
	}
	ref := TableRef{Name: name}
	return ref, ref.Validate()
}

// NewTableRefInSchema validates a qualified physical table. The two supplied
// strings are exact identifier components; neither is split on dots.
func NewTableRefInSchema(schema, name string) (TableRef, error) {
	if schema == "" {
		return TableRef{}, &TableRefError{Component: "schema", Reason: "cannot be empty for a qualified table"}
	}
	ref := TableRef{Schema: schema, Name: name}
	return ref, ref.Validate()
}

// Validate checks the engine-independent invariants of a table reference.
// Dialect-specific length and character rules remain the database's contract;
// quoted identifiers may legitimately contain spaces, quotes, and dots.
func (this TableRef) Validate() error {
	if this.Name == "" {
		return &TableRefError{Component: "name", Reason: "cannot be empty"}
	}
	if strings.IndexByte(this.Name, 0) >= 0 {
		return &TableRefError{Component: "name", Value: this.Name, Reason: "contains a NUL byte"}
	}
	if strings.IndexByte(this.Schema, 0) >= 0 {
		return &TableRefError{Component: "schema", Value: this.Schema, Reason: "contains a NUL byte"}
	}
	return nil
}

// Components returns the exact identifier parts in driver order. The returned
// slice is fresh, so an adapter may hand it to a driver without exposing the
// TableRef to mutation.
func (this TableRef) Components() []string {
	if this.Schema == "" {
		return []string{this.Name}
	}
	return []string{this.Schema, this.Name}
}

// String is the conventional diagnostic spelling. It is not a serialisation:
// dots inside either component are not escaped and must never be parsed back.
func (this TableRef) String() string {
	if this.Schema == "" {
		return this.Name
	}
	return this.Schema + "." + this.Name
}

// quoteTable renders each already-validated identifier component
// independently. Public direct TableRef rendering goes through SQL.TableRef,
// which retains a validation error instead of producing a statement.
func quoteTable(d Dialect, table TableRef) string {
	if table.Schema == "" {
		return d.Quote(table.Name)
	}
	return d.Quote(table.Schema) + "." + d.Quote(table.Name)
}
