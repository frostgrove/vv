package specs

import (
	"fmt"

	"github.com/frostgrove/vv/crud"
)

var (
	ErrNotUnique = fmt.Errorf("specs: more than one row matches: %w", crud.ErrConflict)

	ErrUnboundedDelete = fmt.Errorf("specs: refusing to delete with an unrestricted specification: %w", crud.ErrBadRequest)

	ErrUnboundedUpdate = fmt.Errorf("specs: refusing to update with an unrestricted specification: %w", crud.ErrBadRequest)
)
