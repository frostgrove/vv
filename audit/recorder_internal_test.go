package audit

import (
	"context"
	"errors"
	"testing"
)

func TestStoreCancellationRemainsTheExactContextSignal(t *testing.T) {
	if got := mapStoreError(context.Canceled); got != context.Canceled {
		t.Fatalf("canceled store error = %v", got)
	}
	if got := mapStoreError(context.DeadlineExceeded); got != context.DeadlineExceeded {
		t.Fatalf("deadline store error = %v", got)
	}
	backend := errors.New("driver detail")
	if got := mapStoreError(backend); !errors.Is(got, ErrBackend) || got == backend {
		t.Fatalf("unclassified store error = %v", got)
	}
}

func TestUnclassifiedAppendOutcomeRemainsReconcilableUnknown(t *testing.T) {
	backend := errors.New("driver detail")
	if got := mapAppendError(backend); !errors.Is(got, ErrUnconfirmed) || errors.Is(got, ErrBackend) {
		t.Fatalf("unclassified append error = %v", got)
	}
	if got := mapAppendError(context.Canceled); !errors.Is(got, ErrUnconfirmed) {
		t.Fatalf("bare append cancellation = %v", got)
	}
	classified := Failure(NotWritten, backend)
	if got := mapAppendError(classified); !errors.Is(got, ErrNotWritten) {
		t.Fatalf("classified pre-write append error = %v", got)
	}
}

func TestValueFreeContextPreservesCancellationWithoutItsPrivateCause(t *testing.T) {
	type privateKey struct{}
	privateCause := errors.New("private cancellation detail")
	parent, cancel := context.WithCancelCause(context.WithValue(context.Background(), privateKey{}, "secret"))
	runtimeContext := valueFreeContext{Context: parent}
	cancel(privateCause)

	if runtimeContext.Value(privateKey{}) != nil {
		t.Fatal("value-free context exposed a caller value")
	}
	if runtimeContext.Err() != context.Canceled || context.Cause(runtimeContext) != context.Canceled {
		t.Fatalf("runtime cancellation = (%v, %v)", runtimeContext.Err(), context.Cause(runtimeContext))
	}
}
