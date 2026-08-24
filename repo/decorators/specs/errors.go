package specs

import (
	"errors"
	"fmt"

	"github.com/shardit-io/go-rx-crud/crud"
)

var (
	// ErrNotUnique is returned by FindOne when the specification matches more
	// than one row. It wraps crud.ErrConflict so transports can answer 409.
	ErrNotUnique = fmt.Errorf("specs: more than one row matches: %w", crud.ErrConflict)
	// ErrUnboundedDelete guards DeleteBy against an empty specification, which
	// would otherwise truncate the table. Use DeleteAll if that is the intent.
	ErrUnboundedDelete = errors.New("specs: refusing to delete with an empty specification")
	// ErrUnboundedUpdate is the same guard for UpdateBy: an empty specification
	// would rewrite every row. Use UpdateAll if that is the intent.
	ErrUnboundedUpdate = errors.New("specs: refusing to update with an empty specification")
)
