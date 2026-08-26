package port

import (
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/query"
)

// The commands are what a transport hands a Service. Each one carries what the
// request meant and nothing about how it arrived: no status, no header, no
// framework context.
//
// The write commands carry their hook. The hook is transport-shaped — it closes
// over a fiber.Ctx or an *http.Request — but where it runs is not: UC-013
// guarantee 7 pins it as running after the server-owned fields are cleared, and
// that order is the Service's to keep. A hook left in the binding would start
// seeing an unsanitised model the moment the clearing moved down here.

// A ListCommand is a page of rows. Options are appended after the query
// document compiles, so a transport scope narrows the client's filter instead
// of replacing it ([[D-004]]).
type ListCommand struct {
	Query   *query.Request
	Options []crud.Option
}

// A CountCommand is the size of a result. The Service narrows the document
// first: paging left in would make the answer the size of one page.
type CountCommand struct {
	Query   *query.Request
	Options []crud.Option
}

// A GetCommand is one row by key. The Service drops everything but the shaping
// options — a filter or a sort on the way to a keyed row means nothing.
type GetCommand[ID comparable] struct {
	ID      ID
	Query   *query.Request
	Options []crud.Option
}

// A CreateCommand is one new row. Before runs after the Service has cleared
// what a client may not choose.
type CreateCommand[M any] struct {
	Model  M
	Before func(*M) error
}

// An UpdateCommand is a partial update. Before runs on the patch, not the row:
// nothing has been loaded yet.
type UpdateCommand[ID comparable, U any] struct {
	ID     ID
	Patch  U
	Before func(*U) error
}

// A ReplaceCommand is a whole row at a key the caller chose. The key on the
// command wins over anything the body carried.
type ReplaceCommand[ID comparable, M any] struct {
	ID     ID
	Model  M
	Before func(*M) error
}

// A DeleteCommand removes one row. Removing nothing is crud.ErrNotFound: the
// caller named a row, and it was not there.
type DeleteCommand[ID comparable] struct {
	ID ID
}

// A BulkDeleteCommand removes a set. Removing nothing is a count of zero and
// not an error: the caller named a set, and a set may be empty.
type BulkDeleteCommand[ID comparable] struct {
	IDs []ID
}
