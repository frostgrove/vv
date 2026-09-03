package port

import "strings"

type Operations uint16

const (
	OpList Operations = 1 << iota
	OpQuery
	OpCount
	OpCountQuery
	OpGet
	OpCreate
	OpUpdate
	OpReplace
	OpDelete
	OpBulkDelete
)

const (
	Reads   = OpList | OpQuery | OpCount | OpCountQuery | OpGet
	Writes  = OpCreate | OpUpdate | OpReplace
	Deletes = OpDelete | OpBulkDelete

	AllOperations = Reads | Writes | Deletes
)

func (this Operations) Has(operation Operations) bool { return this&operation != 0 }

var operationNames = []struct {
	operation Operations
	name      string
}{
	{OpList, "list"},
	{OpQuery, "query"},
	{OpCount, "count"},
	{OpCountQuery, "count-query"},
	{OpGet, "get"},
	{OpCreate, "create"},
	{OpUpdate, "update"},
	{OpReplace, "replace"},
	{OpDelete, "delete"},
	{OpBulkDelete, "bulk-delete"},
}

func (this Operations) String() string {
	if this == 0 {
		return "none"
	}
	names := make([]string, 0, len(operationNames))
	for _, known := range operationNames {
		if this.Has(known.operation) {
			names = append(names, known.name)
		}
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}
