package crud

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound is returned by GetByID and Update when no row matches.
	ErrNotFound = errors.New("crud: not found")
	// ErrNoTxSupport is returned when a transaction is requested from an
	// executor that cannot start one (e.g. a foreign *sql.Tx handed to us).
	ErrNoTxSupport = errors.New("crud: executor cannot begin transactions")
	// ErrMissingID is returned by Save when the model has a non-generated
	// primary key that was left at its zero value.
	ErrMissingID = errors.New("crud: primary key is required for save")
	// ErrBadRequest marks an invalid repository-level operation. It belongs at
	// the CRUD boundary rather than a transport because decorators such as specs
	// can reject a dangerous call before any HTTP or gRPC package is involved.
	// Transports render it as a 400 without importing the decorator that raised
	// it.
	ErrBadRequest = errors.New("crud: bad request")
	// ErrReadOnly is for a write attempted through a read-only view. The two
	// read-only facilities in the tree do not use it — crudfiber.ReadOnly does
	// not mount the write routes at all, and security.ReadOnly denies through
	// ErrForbidden so a transport answers 403 — so it is here for a decorator
	// that wants to say "read-only" rather than "forbidden", and for nothing else.
	ErrReadOnly = errors.New("crud: repository is read-only")
	// ErrForbidden is the sentinel every access-control layer wraps, so a
	// transport can answer 403 without importing the decorator that raised it.
	ErrForbidden = errors.New("forbidden")
	// ErrConflict marks a request that collided with the current state — a
	// findOne matching several rows, a duplicate key, a foreign key pointing
	// nowhere. The adapters classify the driver's integrity errors into it, so a
	// constraint violation reaches a client as 409 with a message rather than as
	// a 500 whose body says nothing. Transports map it to 409.
	ErrConflict = errors.New("conflict")

	// ErrCreateRaced is the narrow conflict a scoped Save reports when its
	// inspected Create branch lost a race for the assigned primary key. It is
	// distinct from ErrConflict because a unique constraint on another column,
	// a foreign-key failure or a serialization conflict is an ordinary 409, not
	// evidence that security should turn an update into a forbidden create.
	ErrCreateRaced = fmt.Errorf("crud: assigned-key create lost its race: %w", ErrConflict)
	// ErrStaleVersion is the optimistic lock refusing a write: the row was
	// changed by somebody else between the read and the update. It wraps
	// ErrConflict, so a transport answers 409 without knowing about versions,
	// and the caller's answer is to read the row again and reapply.
	ErrStaleVersion = fmt.Errorf("crud: the row was changed by someone else: %w", ErrConflict)
)

// UnknownFieldError is returned when a predicate or sort references a field
// that the model does not declare.
type UnknownFieldError struct {
	Model string
	Field string
}

func (e *UnknownFieldError) Error() string {
	return fmt.Sprintf("crud: unknown field %q on model %s", e.Field, e.Model)
}

// SchemaError reports a model or update-DTO declaration that cannot be used.
// It is raised eagerly by Define/New so a broken mapping fails at start-up.
type SchemaError struct {
	Model  string
	Field  string
	Reason string
}

func (e *SchemaError) Error() string {
	if e.Field == "" {
		return fmt.Sprintf("crud: %s: %s", e.Model, e.Reason)
	}
	return fmt.Sprintf("crud: %s.%s: %s", e.Model, e.Field, e.Reason)
}

func errJoin(errs ...error) error { return errors.Join(errs...) }
