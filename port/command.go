package port

import (
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
)

type ListCommand struct {
	Query   *query.Request
	Options []crud.Option
}

type CountCommand struct {
	Query   *query.Request
	Options []crud.Option
}

type GetCommand[ID comparable] struct {
	ID      ID
	Query   *query.Request
	Options []crud.Option
}

type CreateCommand[M any] struct {
	Model  M
	Before func(*M) error
}

type UpdateCommand[ID comparable, U any] struct {
	ID     ID
	Patch  U
	Before func(*U) error
}

type ReplaceCommand[ID comparable, M any] struct {
	ID     ID
	Model  M
	Before func(*M) error
}

type DeleteCommand[ID comparable] struct {
	ID ID
}

type BulkDeleteCommand[ID comparable] struct {
	IDs []ID
}

type RestoreCommand[ID comparable] struct {
	ID ID
}

type BulkRestoreCommand[ID comparable] struct {
	IDs []ID
}
