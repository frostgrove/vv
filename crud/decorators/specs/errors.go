package specs

import (
	"fmt"

	"github.com/frostgrove/vv/crud"
)

var (
	// ErrNotUnique is returned by FindOne when the specification matches more
	// than one row. It wraps crud.ErrConflict so transports can answer 409.
	ErrNotUnique = fmt.Errorf("specs: more than one row matches: %w", crud.ErrConflict)
	// ErrUnboundedDelete guards DeleteBy against an unrestricted declarative
	// specification, which would otherwise truncate the table. Raw SQL has no
	// declarative proof and is therefore refused here too; use DeleteAll for an
	// intentional whole-table operation.
	ErrUnboundedDelete = fmt.Errorf("specs: refusing to delete with an unrestricted specification: %w", crud.ErrBadRequest)
	// ErrUnboundedUpdate is the same guard for UpdateBy: an unrestricted
	// declarative specification would rewrite every row. Use UpdateAll if that
	// is the intent.
	ErrUnboundedUpdate = fmt.Errorf("specs: refusing to update with an unrestricted specification: %w", crud.ErrBadRequest)
)
