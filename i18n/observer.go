package i18n

import (
	"context"
	"errors"
	"time"
)

type Operation uint8

const (
	OperationResolve Operation = iota + 1
	OperationRender
	OperationCompile
	OperationLoad
	OperationActivate
	OperationRollback
)

func (o Operation) String() string {
	switch o {
	case OperationResolve:
		return "resolve"
	case OperationRender:
		return "render"
	case OperationCompile:
		return "compile"
	case OperationLoad:
		return "load"
	case OperationActivate:
		return "activate"
	case OperationRollback:
		return "rollback"
	default:
		return "unknown"
	}
}

func (o Operation) Valid() bool { return o >= OperationResolve && o <= OperationRollback }

type Outcome uint8

const (
	OutcomeSuccess Outcome = iota + 1
	OutcomeFallback
	OutcomeDefault
	OutcomeNoMatch
	OutcomeMissing
	OutcomeInvalid
	OutcomeLimited
	OutcomeCanceled
	OutcomeTimedOut
)

func (o Outcome) String() string {
	switch o {
	case OutcomeSuccess:
		return "success"
	case OutcomeFallback:
		return "fallback"
	case OutcomeDefault:
		return "default"
	case OutcomeNoMatch:
		return "no_match"
	case OutcomeMissing:
		return "missing"
	case OutcomeInvalid:
		return "invalid"
	case OutcomeLimited:
		return "limited"
	case OutcomeCanceled:
		return "canceled"
	case OutcomeTimedOut:
		return "timed_out"
	default:
		return "unknown"
	}
}

func (o Outcome) Valid() bool { return o >= OutcomeSuccess && o <= OutcomeTimedOut }

type Reason uint8

const (
	ReasonNone Reason = iota
	ReasonExact
	ReasonLookup
	ReasonBestFit
	ReasonWildcard
	ReasonPolicyDefault
	ReasonMalformed
	ReasonExcluded
	ReasonUnsupported
	ReasonLimit
	ReasonMissingTemplate
	ReasonSchemaMismatch
	ReasonInvalidArgument
	ReasonOutputLimit
	ReasonContextCanceled
	ReasonContextDeadline
	ReasonTemplateFailure
	ReasonConflict
	ReasonSnapshotMissing
	ReasonInvalidArtifact
	ReasonArtifactIO
	ReasonIncompatibleArtifact
)

func (r Reason) String() string {
	switch r {
	case ReasonNone:
		return "none"
	case ReasonExact:
		return "exact"
	case ReasonLookup:
		return "lookup"
	case ReasonBestFit:
		return "best_fit"
	case ReasonWildcard:
		return "wildcard"
	case ReasonPolicyDefault:
		return "policy_default"
	case ReasonMalformed:
		return "malformed"
	case ReasonExcluded:
		return "excluded"
	case ReasonUnsupported:
		return "unsupported"
	case ReasonLimit:
		return "limit"
	case ReasonMissingTemplate:
		return "missing_template"
	case ReasonSchemaMismatch:
		return "schema_mismatch"
	case ReasonInvalidArgument:
		return "invalid_argument"
	case ReasonOutputLimit:
		return "output_limit"
	case ReasonContextCanceled:
		return "context_canceled"
	case ReasonContextDeadline:
		return "context_deadline"
	case ReasonTemplateFailure:
		return "template_failure"
	case ReasonConflict:
		return "conflict"
	case ReasonSnapshotMissing:
		return "snapshot_missing"
	case ReasonInvalidArtifact:
		return "invalid_artifact"
	case ReasonArtifactIO:
		return "artifact_io"
	case ReasonIncompatibleArtifact:
		return "incompatible_artifact"
	default:
		return "unknown"
	}
}

func (r Reason) Valid() bool { return r >= ReasonNone && r <= ReasonIncompatibleArtifact }

type Observation struct {
	Operation Operation
	Outcome   Outcome
	Reason    Reason
	Duration  time.Duration
	Count     int
}

type Observer func(context.Context, Observation)

func notifyObserver(ctx context.Context, observer Observer, observation Observation) {
	if observer == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	observer(ctx, observation)
}

func notifyTerminalOperation(ctx context.Context, observer Observer, operation Operation, started time.Time, err error) {
	if observer == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	outcome, reason := terminalOperationResult(err)
	notifyObserver(ctx, observer, Observation{Operation: operation, Outcome: outcome, Reason: reason, Duration: time.Since(started), Count: 1})
}

func terminalOperationResult(err error) (Outcome, Reason) {
	switch {
	case err == nil:
		return OutcomeSuccess, ReasonNone
	case errors.Is(err, context.DeadlineExceeded):
		return OutcomeTimedOut, ReasonContextDeadline
	case errors.Is(err, context.Canceled):
		return OutcomeCanceled, ReasonContextCanceled
	case errors.Is(err, ErrPinUnavailable), errors.Is(err, ErrLimitExceeded):
		return OutcomeLimited, ReasonLimit
	case errors.Is(err, ErrSnapshotNotFound):
		return OutcomeMissing, ReasonSnapshotMissing
	case errors.Is(err, ErrConflict):
		return OutcomeInvalid, ReasonConflict
	case errors.Is(err, ErrIncompatibleArtifact):
		return OutcomeInvalid, ReasonIncompatibleArtifact
	case errors.Is(err, ErrArtifactIO):
		return OutcomeInvalid, ReasonArtifactIO
	case errors.Is(err, ErrInvalidArtifact):
		return OutcomeInvalid, ReasonInvalidArtifact
	case errors.Is(err, ErrInvalidCatalog), errors.Is(err, ErrInvalidMessage):
		return OutcomeInvalid, ReasonSchemaMismatch
	default:
		return OutcomeInvalid, ReasonTemplateFailure
	}
}

func contextOperationResult(err error) (Outcome, Reason) {
	if errors.Is(err, context.DeadlineExceeded) {
		return OutcomeTimedOut, ReasonContextDeadline
	}
	return OutcomeCanceled, ReasonContextCanceled
}
