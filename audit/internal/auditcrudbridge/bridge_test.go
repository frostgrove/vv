package auditcrudbridge_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit/internal/auditcrudbridge"
)

type recorder struct {
	auditcrudbridge.Carrier
}

type opaque struct{ name string }

type nilContext struct{}

func (*nilContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*nilContext) Done() <-chan struct{}       { return nil }
func (*nilContext) Err() error                  { return nil }
func (*nilContext) Value(any) any               { return nil }

func TestOpaqueValuesCrossTheOneUseBridge(t *testing.T) {
	policy := &opaque{name: "policy"}
	operation := &opaque{name: "operation"}
	subject := &opaque{name: "subject"}
	replacementSubject := &opaque{name: "replacement subject"}
	draft := &opaque{name: "draft"}
	replacementDraft := &opaque{name: "replacement draft"}
	subjects := []any{subject}
	spec, err := auditcrudbridge.NewSpec(policy, operation, subjects, false)
	if err != nil {
		t.Fatalf("NewSpec() error = %v", err)
	}
	subjects[0] = replacementSubject
	items := []any{draft}
	batch, err := auditcrudbridge.NewBatch(items...)
	if err != nil {
		t.Fatalf("NewBatch() error = %v", err)
	}
	items[0] = replacementDraft
	var inspectedPolicy, inspectedOperation any
	var inspectedSubjects, inspectedItems []any
	var generated bool
	configured := &recorder{Carrier: auditcrudbridge.NewCarrier(func(_ context.Context, value auditcrudbridge.Spec) (auditcrudbridge.Runner, error) {
		var ok bool
		inspectedPolicy, inspectedOperation, inspectedSubjects, generated, ok = auditcrudbridge.InspectSpec(value)
		if !ok {
			return nil, errors.New("spec was not inspectable")
		}
		return func(ctx context.Context, mutation auditcrudbridge.Mutation) error {
			value, err := mutation(ctx)
			if err != nil {
				return err
			}
			inspectedItems, ok = auditcrudbridge.InspectBatch(value)
			if !ok {
				return errors.New("batch was not inspectable")
			}
			return nil
		}, nil
	})}
	guard, err := auditcrudbridge.Preflight(context.Background(), configured, spec)
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	copyOfGuard := guard
	var mutations atomic.Int64
	if err := guard.Run(context.Background(), func(context.Context) (auditcrudbridge.Batch, error) {
		mutations.Add(1)
		return batch, nil
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if inspectedPolicy != policy || inspectedOperation != operation || generated {
		t.Fatalf("inspected spec = %#v/%#v/%v", inspectedPolicy, inspectedOperation, generated)
	}
	if len(inspectedSubjects) != 1 || inspectedSubjects[0] != subject {
		t.Fatalf("inspected subjects = %#v", inspectedSubjects)
	}
	if len(inspectedItems) != 1 || inspectedItems[0] != draft {
		t.Fatalf("inspected items = %#v", inspectedItems)
	}
	if _, _, _, _, ok := auditcrudbridge.InspectSpec(spec); ok {
		t.Fatal("consumed spec remained inspectable")
	}
	if _, ok := auditcrudbridge.InspectBatch(batch); ok {
		t.Fatal("consumed batch remained inspectable")
	}
	if err := copyOfGuard.Run(context.Background(), func(context.Context) (auditcrudbridge.Batch, error) {
		mutations.Add(1)
		return auditcrudbridge.NewBatch()
	}); err == nil {
		t.Fatal("copied guard ran")
	}
	if mutations.Load() != 1 {
		t.Fatalf("mutation calls = %d, want 1", mutations.Load())
	}
}

func TestRunnerRecoveryCrossesTheBridgeExactlyOnce(t *testing.T) {
	recovery := &opaque{name: "recovery"}
	runner := func(ctx context.Context, mutation auditcrudbridge.Mutation) error {
		batch, err := mutation(ctx)
		if err != nil {
			return err
		}
		if _, ok := auditcrudbridge.InspectBatch(batch); !ok {
			return errors.New("batch was not inspectable")
		}
		return auditcrudbridge.ReportRecovery(recovery, nil)
	}
	configured := newInspectingRecorder(t, runner)
	spec, err := auditcrudbridge.NewSpec(&opaque{name: "policy"}, &opaque{name: "operation"}, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := auditcrudbridge.Preflight(context.Background(), configured, spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Run(context.Background(), func(context.Context) (auditcrudbridge.Batch, error) {
		return auditcrudbridge.NewBatch(&opaque{name: "draft"})
	}); err != nil {
		t.Fatal(err)
	}
	if got, ok := guard.TakeRecovery(); !ok || got != recovery {
		t.Fatalf("recovery = (%#v, %v)", got, ok)
	}
	if got, ok := guard.TakeRecovery(); ok || got != nil {
		t.Fatalf("recovery replay = (%#v, %v)", got, ok)
	}
}

func TestInvalidAndTypedNilInputsRefuseWithoutCallbacks(t *testing.T) {
	policy := &opaque{name: "policy"}
	operation := &opaque{name: "operation"}
	subject := &opaque{name: "subject"}
	var typedNil *opaque
	if _, err := auditcrudbridge.NewSpec(nil, operation, []any{subject}, false); err == nil {
		t.Fatal("nil policy was accepted")
	}
	if _, err := auditcrudbridge.NewSpec(typedNil, operation, []any{subject}, false); err == nil {
		t.Fatal("typed-nil policy was accepted")
	}
	if _, err := auditcrudbridge.NewSpec(policy, nil, []any{subject}, false); err == nil {
		t.Fatal("nil operation was accepted")
	}
	if _, err := auditcrudbridge.NewSpec(policy, operation, nil, false); err == nil {
		t.Fatal("missing known subject was accepted")
	}
	if _, err := auditcrudbridge.NewSpec(policy, operation, []any{typedNil}, false); err == nil {
		t.Fatal("typed-nil subject was accepted")
	}
	if _, err := auditcrudbridge.NewSpec(policy, operation, []any{subject}, true); err == nil {
		t.Fatal("known and generated subjects were mixed")
	}
	if _, err := auditcrudbridge.NewSpec(policy, operation, nil, true); err != nil {
		t.Fatalf("generated subject spec error = %v", err)
	}
	if _, err := auditcrudbridge.NewBatch(typedNil); err == nil {
		t.Fatal("typed-nil batch item was accepted")
	}
	validSpec := mustSpec(t, "invalid inputs")
	var prepares atomic.Int64
	configured := &recorder{Carrier: auditcrudbridge.NewCarrier(func(context.Context, auditcrudbridge.Spec) (auditcrudbridge.Runner, error) {
		prepares.Add(1)
		return nil, nil
	})}
	if _, err := auditcrudbridge.Preflight(context.Background(), struct{}{}, validSpec); err == nil {
		t.Fatal("recorder without a carrier was accepted")
	}
	var nilRecorder *recorder
	if _, err := auditcrudbridge.Preflight(context.Background(), nilRecorder, validSpec); err == nil {
		t.Fatal("typed-nil recorder was accepted")
	}
	var typedNilContext *nilContext
	if _, err := auditcrudbridge.Preflight(typedNilContext, configured, validSpec); err == nil {
		t.Fatal("typed-nil context was accepted")
	}
	if _, err := auditcrudbridge.Preflight(context.Background(), configured, auditcrudbridge.Spec{}); err == nil {
		t.Fatal("zero spec was accepted")
	}
	invalidCarrier := &recorder{Carrier: auditcrudbridge.NewCarrier(nil)}
	if _, err := auditcrudbridge.Preflight(context.Background(), invalidCarrier, validSpec); err == nil {
		t.Fatal("nil Prepare was accepted")
	}
	var mutations atomic.Int64
	if err := (auditcrudbridge.Guard{}).Run(context.Background(), func(context.Context) (auditcrudbridge.Batch, error) {
		mutations.Add(1)
		return auditcrudbridge.NewBatch()
	}); err == nil {
		t.Fatal("zero guard ran")
	}
	if prepares.Load() != 0 || mutations.Load() != 0 {
		t.Fatalf("callbacks ran: prepare=%d mutation=%d", prepares.Load(), mutations.Load())
	}
}

func TestBoundsRefuseBeforeCarrierOrMutation(t *testing.T) {
	values := make([]any, 257)
	for index := range values {
		values[index] = index
	}
	if _, err := auditcrudbridge.NewSpec(&opaque{name: "policy"}, &opaque{name: "operation"}, values, false); err == nil {
		t.Fatal("oversized subjects were accepted")
	}
	if _, err := auditcrudbridge.NewBatch(values...); err == nil {
		t.Fatal("oversized batch was accepted")
	}
}

func TestCarrierCopyCannotServeAnotherRecorder(t *testing.T) {
	var prepares atomic.Int64
	carrier := auditcrudbridge.NewCarrier(func(_ context.Context, value auditcrudbridge.Spec) (auditcrudbridge.Runner, error) {
		prepares.Add(1)
		if _, _, _, _, ok := auditcrudbridge.InspectSpec(value); !ok {
			return nil, errors.New("spec was not inspectable")
		}
		return inspectingRunner(nil), nil
	})
	first := &recorder{Carrier: carrier}
	second := &recorder{Carrier: carrier}
	if _, err := auditcrudbridge.Preflight(context.Background(), first, mustSpec(t, "first")); err != nil {
		t.Fatalf("first Preflight() error = %v", err)
	}
	if _, err := auditcrudbridge.Preflight(context.Background(), second, mustSpec(t, "second")); err == nil {
		t.Fatal("copied carrier served a foreign recorder")
	}
	copyOfFirst := *first
	if _, err := auditcrudbridge.Preflight(context.Background(), &copyOfFirst, mustSpec(t, "copy")); err == nil {
		t.Fatal("copied recorder retained carrier authority")
	}
	if prepares.Load() != 1 {
		t.Fatalf("prepare calls = %d, want 1", prepares.Load())
	}
}

func TestCopiedSpecCanBeClaimedOnlyOnceAcrossRecorders(t *testing.T) {
	var prepares atomic.Int64
	makeRecorder := func() *recorder {
		return &recorder{Carrier: auditcrudbridge.NewCarrier(func(_ context.Context, value auditcrudbridge.Spec) (auditcrudbridge.Runner, error) {
			prepares.Add(1)
			if _, _, _, _, ok := auditcrudbridge.InspectSpec(value); !ok {
				return nil, errors.New("spec was not inspectable")
			}
			return inspectingRunner(nil), nil
		})}
	}
	spec := mustSpec(t, "shared")
	copied := spec
	inputs := []struct {
		recorder *recorder
		spec     auditcrudbridge.Spec
	}{{makeRecorder(), spec}, {makeRecorder(), copied}}
	var successes atomic.Int64
	var wait sync.WaitGroup
	for _, input := range inputs {
		input := input
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := auditcrudbridge.Preflight(context.Background(), input.recorder, input.spec); err == nil {
				successes.Add(1)
			}
		}()
	}
	wait.Wait()
	if successes.Load() != 1 || prepares.Load() != 1 {
		t.Fatalf("successes/prepares = %d/%d, want 1/1", successes.Load(), prepares.Load())
	}
}

func TestGuardCopiesRaceToOneMutation(t *testing.T) {
	configured := newInspectingRecorder(t, nil)
	guard := mustGuard(t, configured, mustSpec(t, "guard race"))
	copies := []auditcrudbridge.Guard{guard, guard}
	var mutations atomic.Int64
	var successes atomic.Int64
	var wait sync.WaitGroup
	for _, copied := range copies {
		copied := copied
		wait.Add(1)
		go func() {
			defer wait.Done()
			err := copied.Run(context.Background(), func(context.Context) (auditcrudbridge.Batch, error) {
				mutations.Add(1)
				return auditcrudbridge.NewBatch(&opaque{name: "draft"})
			})
			if err == nil {
				successes.Add(1)
			}
		}()
	}
	wait.Wait()
	if successes.Load() != 1 || mutations.Load() != 1 {
		t.Fatalf("successes/mutations = %d/%d, want 1/1", successes.Load(), mutations.Load())
	}
}

func TestBatchCopyAndReplayAreRefused(t *testing.T) {
	configured := newInspectingRecorder(t, nil)
	first := mustGuard(t, configured, mustSpec(t, "first batch"))
	second := mustGuard(t, configured, mustSpec(t, "second batch"))
	batch, err := auditcrudbridge.NewBatch(&opaque{name: "draft"})
	if err != nil {
		t.Fatalf("NewBatch() error = %v", err)
	}
	copyOfBatch := batch
	if err := first.Run(context.Background(), func(context.Context) (auditcrudbridge.Batch, error) {
		return batch, nil
	}); err != nil {
		t.Fatalf("first Run() error = %v", err)
	}
	if err := second.Run(context.Background(), func(context.Context) (auditcrudbridge.Batch, error) {
		return copyOfBatch, nil
	}); err == nil {
		t.Fatal("copied consumed batch was accepted")
	}
}

func TestRunnerCannotSkipDuplicateOrRetainMutation(t *testing.T) {
	tests := []struct {
		name   string
		runner auditcrudbridge.Runner
	}{
		{
			name: "skip",
			runner: func(context.Context, auditcrudbridge.Mutation) error {
				return nil
			},
		},
		{
			name: "duplicate",
			runner: func(ctx context.Context, mutation auditcrudbridge.Mutation) error {
				batch, err := mutation(ctx)
				if err != nil {
					return err
				}
				if _, ok := auditcrudbridge.InspectBatch(batch); !ok {
					return errors.New("batch was not inspectable")
				}
				_, _ = mutation(ctx)
				return nil
			},
		},
		{
			name: "ignore batch",
			runner: func(ctx context.Context, mutation auditcrudbridge.Mutation) error {
				_, err := mutation(ctx)
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configured := newInspectingRecorder(t, test.runner)
			guard := mustGuard(t, configured, mustSpec(t, test.name))
			var mutations atomic.Int64
			if err := guard.Run(context.Background(), func(context.Context) (auditcrudbridge.Batch, error) {
				mutations.Add(1)
				return auditcrudbridge.NewBatch(&opaque{name: "draft"})
			}); err == nil {
				t.Fatal("invalid runner protocol succeeded")
			}
			want := int64(1)
			if test.name == "skip" {
				want = 0
			}
			if mutations.Load() != want {
				t.Fatalf("mutation calls = %d, want %d", mutations.Load(), want)
			}
		})
	}
}

func TestRunnerCannotInvokeRetainedMutationAfterRun(t *testing.T) {
	var retained auditcrudbridge.Mutation
	var mutations atomic.Int64
	want := errors.New("execution refused before mutation")
	configured := newInspectingRecorder(t, func(_ context.Context, mutation auditcrudbridge.Mutation) error {
		retained = mutation
		return want
	})
	guard := mustGuard(t, configured, mustSpec(t, "retained error"))
	if err := guard.Run(context.Background(), func(context.Context) (auditcrudbridge.Batch, error) {
		mutations.Add(1)
		return auditcrudbridge.NewBatch()
	}); !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want retained runner error", err)
	}
	if retained == nil {
		t.Fatal("runner did not retain mutation")
	}
	if _, err := retained(context.Background()); err == nil {
		t.Fatal("retained mutation ran after Guard.Run")
	}
	if mutations.Load() != 0 {
		t.Fatalf("mutation calls = %d, want 0", mutations.Load())
	}
}

func TestMutationErrorCannotBeSwallowedByRunner(t *testing.T) {
	configured := newInspectingRecorder(t, func(ctx context.Context, mutation auditcrudbridge.Mutation) error {
		_, _ = mutation(ctx)
		return nil
	})
	guard := mustGuard(t, configured, mustSpec(t, "mutation error"))
	want := errors.New("mutation failed")
	if err := guard.Run(context.Background(), func(context.Context) (auditcrudbridge.Batch, error) {
		return auditcrudbridge.Batch{}, want
	}); !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want mutation error", err)
	}
}

func mustSpec(t *testing.T, name string) auditcrudbridge.Spec {
	t.Helper()
	value, err := auditcrudbridge.NewSpec(&opaque{name: "policy " + name}, &opaque{name: "operation " + name}, []any{&opaque{name: "subject " + name}}, false)
	if err != nil {
		t.Fatalf("NewSpec() error = %v", err)
	}
	return value
}

func mustGuard(t *testing.T, configured *recorder, value auditcrudbridge.Spec) auditcrudbridge.Guard {
	t.Helper()
	guard, err := auditcrudbridge.Preflight(context.Background(), configured, value)
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	return guard
}

func newInspectingRecorder(t *testing.T, runner auditcrudbridge.Runner) *recorder {
	t.Helper()
	return &recorder{Carrier: auditcrudbridge.NewCarrier(func(_ context.Context, value auditcrudbridge.Spec) (auditcrudbridge.Runner, error) {
		if _, _, _, _, ok := auditcrudbridge.InspectSpec(value); !ok {
			return nil, errors.New("spec was not inspectable")
		}
		if runner != nil {
			return runner, nil
		}
		return inspectingRunner(nil), nil
	})}
}

func inspectingRunner(seen *[]any) auditcrudbridge.Runner {
	return func(ctx context.Context, mutation auditcrudbridge.Mutation) error {
		batch, err := mutation(ctx)
		if err != nil {
			return err
		}
		values, ok := auditcrudbridge.InspectBatch(batch)
		if !ok {
			return errors.New("batch was not inspectable")
		}
		if seen != nil {
			*seen = values
		}
		return nil
	}
}
