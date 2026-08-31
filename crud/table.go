package crud

import (
	"fmt"
	"strings"
)

type TableRef struct {
	Schema string
	Name   string
}

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

func NewTableRef(name string) (TableRef, error) {
	if strings.Contains(name, ".") {
		return TableRef{}, &TableRefError{Component: "name", Value: name,
			Reason: "a dotted string is ambiguous; pass schema and table as separate components"}
	}
	ref := TableRef{Name: name}
	return ref, ref.Validate()
}

func NewTableRefInSchema(schema, name string) (TableRef, error) {
	if schema == "" {
		return TableRef{}, &TableRefError{Component: "schema", Reason: "cannot be empty for a qualified table"}
	}
	ref := TableRef{Schema: schema, Name: name}
	return ref, ref.Validate()
}

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

func (this TableRef) Components() []string {
	if this.Schema == "" {
		return []string{this.Name}
	}
	return []string{this.Schema, this.Name}
}

func (this TableRef) String() string {
	if this.Schema == "" {
		return this.Name
	}
	return this.Schema + "." + this.Name
}

func quoteTable(d Dialect, table TableRef) string {
	if table.Schema == "" {
		return d.Quote(table.Name)
	}
	return d.Quote(table.Schema) + "." + d.Quote(table.Name)
}
