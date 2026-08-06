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
	// ErrReadOnly is returned when a write is attempted through a read-only view.
	ErrReadOnly = errors.New("crud: repository is read-only")
	// ErrForbidden is the sentinel every access-control layer wraps, so a
	// transport can answer 403 without importing the decorator that raised it.
	ErrForbidden = errors.New("forbidden")
	// ErrConflict marks a request that collided with the current state — a
	// findOne matching several rows, a duplicate key. Transports map it to 409.
	ErrConflict = errors.New("conflict")
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
