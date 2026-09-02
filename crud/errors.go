package crud

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound = errors.New("crud: not found")

	ErrNoTxSupport          = errors.New("crud: executor cannot begin transactions")
	ErrNoBulkInsertSupport  = errors.New("crud: executor cannot bulk insert rows")
	ErrNoBatchInsertSupport = errors.New("crud: repository has no batch insert capability")
	ErrNoCreateSupport      = errors.New("crud: repository has no insert-only create capability")
	ErrNoReplaceSupport     = errors.New("crud: repository has no version-aware replace capability")

	ErrExecutorScope = errors.New("crud: executor scope does not match repository source")

	ErrSchemaNotReady = errors.New("crud: database schema is not ready")

	ErrMissingID = errors.New("crud: primary key is required for save")

	ErrBadRequest = errors.New("crud: bad request")

	ErrReadOnly = errors.New("crud: repository is read-only")

	ErrNoTombstone = errors.New("crud: repository has no tombstone restore capability")

	ErrForbidden = errors.New("forbidden")

	ErrConflict = errors.New("conflict")

	ErrCreateRaced = fmt.Errorf("crud: assigned-key create lost its race: %w", ErrConflict)

	ErrStaleVersion = fmt.Errorf("crud: the row was changed by someone else: %w", ErrConflict)
)

type ExecutorScopeReason string

const (
	ExecutorScopeMismatch          ExecutorScopeReason = "mismatch"
	ExecutorScopeMissingSource     ExecutorScopeReason = "missing_source"
	ExecutorScopeInvalidSource     ExecutorScopeReason = "invalid_source"
	ExecutorScopeTransactionSource ExecutorScopeReason = "transaction_source"
	ExecutorScopeMissingExecutor   ExecutorScopeReason = "missing_executor"
	ExecutorScopeInvalidSession    ExecutorScopeReason = "invalid_session"
)

type ExecutorScopeError struct {
	Reason ExecutorScopeReason
}

func (this *ExecutorScopeError) Error() string {
	if this == nil || this.Reason == "" {
		return ErrExecutorScope.Error()
	}
	return fmt.Sprintf("%s: %s", ErrExecutorScope, this.Reason)
}

func (this *ExecutorScopeError) Unwrap() error { return ErrExecutorScope }

type UnknownFieldError struct {
	Model string
	Field string
}

func (this *UnknownFieldError) Error() string {
	return fmt.Sprintf("crud: unknown field %q on model %s", this.Field, this.Model)
}

type SchemaError struct {
	Model  string
	Field  string
	Reason string
}

func (this *SchemaError) Error() string {
	if this.Field == "" {
		return fmt.Sprintf("crud: %s: %s", this.Model, this.Reason)
	}
	return fmt.Sprintf("crud: %s.%s: %s", this.Model, this.Field, this.Reason)
}

func errJoin(errs ...error) error { return errors.Join(errs...) }
