package jobs

import (
	"fmt"
	"log/slog"
)

type HandlerFailure struct {
	cause       error
	panicked    bool
	initialized bool
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
func (f HandlerFailure) IsZero() bool   { return !f.initialized }
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

func invokeHandlerContained(handler func() error) (result error) {
	defer func() {
		if recover() != nil {
			result = HandlerFailure{panicked: true, initialized: true}
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
	if !failure.valid() {
		return classifierFailureDisposition()
	}
	if classifier == nil {
		reason := ReasonHandlerFailure
		if failure.panicked {
			reason = ReasonPanic
		}
		result, _ = RetryDisposition(reason, PublicFailure{}, 0, RetryCostCharged)
		return result
	}
	result = classifierFailureDisposition()
	defer func() {
		if recover() != nil || !validClassifierDisposition(result, failure) {
			result = classifierFailureDisposition()
		}
	}()
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
