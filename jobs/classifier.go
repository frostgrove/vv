package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/frostgrove/vv/port"
)

type HandlerFailure struct {
	cause       error
	panicked    bool
	initialized bool

	// What the handler panicked with, and where. Kept unexported and out of
	// Error(), String() and MarshalJSON, because a panic value is application data
	// and this type reaches an error surface. Discarding them entirely, which is
	// what happened before, left an operator with "jobs: handler failed" and no
	// way to find out what failed.
	recovered any
	stack     []byte
}

func (f HandlerFailure) Error() string { return "jobs: handler failed" }
func (f HandlerFailure) String() string {
	return "[job handler failure]"
}
func (f HandlerFailure) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, f.String())
}
func (f HandlerFailure) LogValue() slog.Value { return slog.StringValue(f.String()) }
func (HandlerFailure) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: handler failure cannot be serialized", ErrUnsupported)
}
func (f HandlerFailure) Unwrap() error {
	if !f.valid() || f.panicked {
		return nil
	}
	return f.cause
}
func (f HandlerFailure) Panicked() bool { return f.valid() && f.panicked }

// The value the handler panicked with, and the stack at the moment it did. Both
// are empty for an ordinary error return. They are handed out for logging and
// nothing else: they are not in Error(), not in the log value, and not
// serializable.
func (f HandlerFailure) Recovered() (any, bool) {
	if !f.valid() || !f.panicked {
		return nil, false
	}
	return f.recovered, f.recovered != nil
}

func (f HandlerFailure) Stack() []byte {
	if !f.valid() || !f.panicked {
		return nil
	}
	return append([]byte(nil), f.stack...)
}
func (f HandlerFailure) IsZero() bool { return !f.initialized }
func (f HandlerFailure) valid() bool {
	return f.initialized && (f.panicked && f.cause == nil || !f.panicked && f.cause != nil)
}

type ErrorClassifier func(HandlerFailure) Disposition

func Classify(classifier ErrorClassifier) WorkerOption {
	return workerOption(func(options *workerOptions) error {
		if options.classifierSet || classifier == nil {
			return invalid("worker error classifier")
		}
		options.classifier = classifier
		options.classifierSet = true
		return nil
	})
}

// A panic is contained here and turned into a disposition, so nothing above ever
// sees it. That made it invisible: the value and the stack were dropped on the
// floor and the operator got "jobs: handler failed". They are kept on the failure
// and written once, through the caller's own logger ([[D-062]]), which is the
// only channel this library is allowed to use.
func invokeHandlerContained(ctx context.Context, handler func() error) (result error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			failure := HandlerFailure{panicked: true, initialized: true, recovered: recovered, stack: debug.Stack()}
			// The type and the stack, never the value. Formatting a panic payload
			// runs application code on a path that is already unwinding, and it
			// is what puts application data into a log line nobody chose the
			// destination of. A consumer who wants the value takes it from
			// Recovered() and formats it under its own risk.
			port.Logger(ctx).ErrorContext(ctx, "jobs: handler panicked",
				"panic_type", fmt.Sprintf("%T", recovered),
				"stack", string(failure.stack))
			result = failure
		}
	}()
	if err := handler(); err != nil {
		return HandlerFailure{cause: err, initialized: true}
	}
	return nil
}

func classifyHandlerResult(classifier ErrorClassifier, result error) Disposition {
	if result == nil {
		return SuccessDisposition()
	}
	failure, ok := result.(HandlerFailure)
	if !ok {
		failure = HandlerFailure{cause: result, initialized: true}
	}
	return classifyHandlerFailure(classifier, failure)
}

func classifyHandlerFailure(classifier ErrorClassifier, failure HandlerFailure) (result Disposition) {
	result = classifierFailureDisposition()
	defer func() {
		if recover() != nil || classifier != nil && !validClassifierDisposition(result, failure) {
			result = classifierFailureDisposition()
		}
	}()
	if !failure.valid() {
		return result
	}
	if classifier == nil {
		if disposition, ok := classifiedHandlerDisposition(failure); ok {
			return disposition
		}
		reason := ReasonHandlerFailure
		if failure.panicked {
			reason = ReasonPanic
		}
		result, _ = RetryDisposition(reason, PublicFailure{}, 0, RetryCostCharged)
		return result
	}
	result = classifier(failure)
	return result
}

func validClassifierDisposition(disposition Disposition, failure HandlerFailure) bool {
	if !disposition.valid() || !disposition.allowedForAttempt() {
		return false
	}
	switch disposition.Kind() {
	case DispositionSucceeded:
		return !failure.panicked
	case DispositionDeferred:
		return !failure.panicked && disposition.Reason() == ReasonDependency
	case DispositionRetry, DispositionPermanentFailure, DispositionDiscard, DispositionQuarantine:
		if failure.panicked {
			return disposition.Reason() == ReasonPanic
		}
		return disposition.Reason() == ReasonHandlerFailure
	default:
		return false
	}
}

func classifierFailureDisposition() Disposition {
	disposition, _ := RetryDisposition(ReasonClassifier, PublicFailure{}, 0, RetryCostCharged)
	return disposition
}
