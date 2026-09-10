package audittest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
)

type AttemptStoreRequest struct {
	Catalogs *audit.CatalogSet
	Change   audit.CatalogChangeRef
	Clock    func() time.Time
}

type AttemptStore struct {
	Writer   audit.Writer
	Log      audit.Log
	Exact    audit.ExactLog
	Attempts audit.AttemptLog
	Types    audit.AttemptTypeState
	Reopen   func() (AttemptStore, error)
	Close    func() error
}

type AttemptStoreFactory func(context.Context, AttemptStoreRequest) (AttemptStore, error)

type attemptStartInput struct {
	Target audit.Reference
	Note   string
}

type attemptFinishInput struct {
	Result string
}

type attemptCheckpointInput struct{}

type attemptMemberInput struct {
	At time.Time
}

type attemptFixture struct {
	best       *audit.AttemptType[attemptStartInput, attemptCheckpointInput, attemptFinishInput]
	alternate  *audit.AttemptType[attemptStartInput, attemptCheckpointInput, attemptFinishInput]
	required   *audit.AttemptType[attemptStartInput, attemptCheckpointInput, attemptFinishInput]
	targetless *audit.AttemptType[attemptStartInput, attemptCheckpointInput, attemptFinishInput]
	finishGets *atomic.Int64
	catalogs   *audit.CatalogSet
	semantic   audit.SemanticDigester
	identity   audit.IdentityKeyring
	signer     audit.Signer
	verifier   audit.Verifier
}

type attemptRuntime struct {
	attempts *audit.Attempts
}

type attemptLimitedWriter struct {
	audit.Writer
	limits audit.Limits
}

func (w attemptLimitedWriter) Limits() audit.Limits { return w.limits }

type attemptObservingWriter struct {
	audit.Writer
	appends *atomic.Int64
}

func (w attemptObservingWriter) Append(ctx context.Context, request audit.AppendRequest) (audit.AppendResult, error) {
	w.appends.Add(1)
	return w.Writer.Append(ctx, request)
}

func RunOnlyAttempts(t *testing.T, factory AttemptStoreFactory) {
	t.Helper()
	if factory == nil {
		t.Fatal("nil attempt store factory")
	}
	fixture := newAttemptFixture(t)
	change, err := audit.NewCatalogChangeRef("attempt.deploy", "audittest")
	if err != nil {
		t.Fatal(err)
	}
	clock := &sequenceClock{next: time.Date(2070, 1, 1, 0, 0, 0, 0, time.UTC)}
	opened, err := factory(context.Background(), AttemptStoreRequest{Catalogs: fixture.catalogs, Change: change, Clock: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer closeAttemptStore(t, opened)
	if opened.Writer == nil || opened.Log == nil || opened.Exact == nil || opened.Attempts == nil || opened.Types == nil {
		t.Fatal("attempt store factory returned nil contracts")
	}
	var resolverCalls atomic.Int64
	resolver := audit.ContextResolverFunc(func(ctx context.Context) (audit.Context, error) {
		resolverCalls.Add(1)
		operationID, ok := ctx.Value(operationContextKey{}).(audit.OperationID)
		if !ok || operationID == (audit.OperationID{}) {
			return audit.Context{}, errors.New("audittest: operation id is missing")
		}
		operation, valueErr := audit.NewContextValue(operationID, audit.Verified)
		if valueErr != nil {
			return audit.Context{}, valueErr
		}
		return audit.Context{
			Actors:    []audit.Actor{{Kind: audit.HumanActor, Reference: "user:attempt", Provenance: audit.Verified}},
			Operation: operation,
		}, nil
	})
	runtime := openAttemptRuntime(t, opened, fixture, resolver, clock)
	input := attemptStartInput{Target: "order:42", Note: "charge"}
	finish := attemptFinishInput{Result: "accepted"}

	t.Run("failed cancelled and targetless transitions are explicit", func(t *testing.T) {
		cases := []struct {
			name       string
			operation  byte
			key        audit.IdempotencyKey
			completion audit.AttemptCompletion[attemptFinishInput]
			state      audit.AttemptState
			transition audit.AttemptTransitionKind
		}{
			{name: "failed", operation: 11, key: "attempt.failed", completion: audit.AttemptFailed[attemptFinishInput]("attempt.failed", finish), state: audit.AttemptFailedState, transition: audit.AttemptFailedTransition},
			{name: "cancelled", operation: 12, key: "attempt.cancelled", completion: audit.AttemptCancelled[attemptFinishInput]("attempt.cancelled", finish), state: audit.AttemptCancelledState, transition: audit.AttemptCancelledTransition},
		}
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				execution, runErr := fixture.best.Run(attemptContextWithOperation(test.operation), runtime.attempts, input, audit.AttemptRunSpec{IdempotencyKey: test.key}, func(context.Context, *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
					return test.completion, nil
				})
				if runErr != nil {
					t.Fatal(runErr)
				}
				assertAttemptState(t, execution.Attempt(), test.state)
				transition, present := execution.Attempt().Transition()
				if !present || transition.Kind != test.transition {
					t.Fatalf("transition = %v, want %v, present=%v", transition.Kind, test.transition, present)
				}
				receipt, _ := execution.Attempt().Receipt()
				if receipt.RevisionID() == (audit.RevisionID{}) {
					t.Fatal("terminal evidence is missing")
				}
			})
		}
		targetlessInput := attemptStartInput{Note: "targetless"}
		execution, runErr := fixture.targetless.Run(attemptContextWithOperation(13), runtime.attempts, targetlessInput, audit.AttemptRunSpec{IdempotencyKey: "attempt.targetless"}, func(context.Context, *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
			return audit.AttemptSucceeded(finish), nil
		})
		if runErr != nil {
			t.Fatal(runErr)
		}
		assertAttemptState(t, execution.Attempt(), audit.AttemptSucceededState)
	})

	t.Run("started is committed before callback and terminal replay is inert", func(t *testing.T) {
		ctx := attemptContextWithOperation(1)
		var nestedCallbacks atomic.Int64
		var startedRevision audit.RevisionID
		execution, runErr := fixture.best.Run(ctx, runtime.attempts, input, audit.AttemptRunSpec{IdempotencyKey: "attempt.happy"}, func(callbackCtx context.Context, _ *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
			nested, nestedErr := fixture.best.Run(callbackCtx, runtime.attempts, input, audit.AttemptRunSpec{IdempotencyKey: "attempt.happy"}, func(context.Context, *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
				nestedCallbacks.Add(1)
				return audit.AttemptSucceeded(finish), nil
			})
			if nestedErr != nil {
				return audit.AttemptCompletion[attemptFinishInput]{}, fmt.Errorf("nested replay: %w", nestedErr)
			}
			if nested.Invoked() {
				return audit.AttemptCompletion[attemptFinishInput]{}, errors.New("nested replay invoked callback")
			}
			state, present := nested.Attempt().ResultingState()
			if !present || state != audit.AttemptOpenState {
				return audit.AttemptCompletion[attemptFinishInput]{}, fmt.Errorf("nested replay state = %v, present=%v", state, present)
			}
			receipt, present := nested.Attempt().Receipt()
			if !present || receipt.Disposition() != audit.Replayed {
				return audit.AttemptCompletion[attemptFinishInput]{}, fmt.Errorf("nested replay receipt = %v, present=%v", receipt.Disposition(), present)
			}
			startedRevision = receipt.RevisionID()
			return audit.AttemptSucceeded(finish), nil
		})
		if runErr != nil {
			t.Fatal(runErr)
		}
		if !execution.Invoked() || execution.ApplicationError() != nil || nestedCallbacks.Load() != 0 {
			t.Fatalf("happy execution = invoked:%v app:%v nested:%d", execution.Invoked(), execution.ApplicationError(), nestedCallbacks.Load())
		}
		assertAttemptState(t, execution.Attempt(), audit.AttemptSucceededState)
		transition, present := execution.Attempt().Transition()
		if !present || transition.Kind != audit.AttemptSucceededTransition {
			t.Fatalf("terminal transition = %v, present=%v", transition.Kind, present)
		}
		var replayCallbacks atomic.Int64
		replay, replayErr := fixture.best.Run(ctx, runtime.attempts, input, audit.AttemptRunSpec{IdempotencyKey: "attempt.happy"}, func(context.Context, *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
			replayCallbacks.Add(1)
			return audit.AttemptSucceeded(finish), nil
		})
		if replayErr != nil {
			t.Fatal(replayErr)
		}
		if replay.Invoked() || replayCallbacks.Load() != 0 {
			t.Fatalf("terminal replay invoked callback: execution=%v callbacks=%d", replay.Invoked(), replayCallbacks.Load())
		}
		receipt, present := replay.Attempt().Receipt()
		if !present || receipt.Disposition() != audit.Replayed || receipt.RevisionID() != startedRevision {
			t.Fatalf("terminal replay receipt = disposition:%v revision:%x present:%v", receipt.Disposition(), receipt.RevisionID(), present)
		}
		handle, begun, beginErr := fixture.best.Begin(ctx, runtime.attempts, input, audit.AttemptBeginSpec{IdempotencyKey: "attempt.happy"})
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if handle != nil {
			t.Fatal("replayed begin returned a live handle")
		}
		receipt, present = begun.Receipt()
		if !present || receipt.Disposition() != audit.Replayed || receipt.RevisionID() != startedRevision {
			t.Fatalf("replayed begin receipt = disposition:%v revision:%x present:%v", receipt.Disposition(), receipt.RevisionID(), present)
		}
	})

	t.Run("concurrent same key invokes callback once", func(t *testing.T) {
		const workers = 24
		ctx := attemptContextWithOperation(2)
		ready := make(chan struct{})
		var callbackCalls atomic.Int64
		results := make([]audit.AttemptExecution, workers)
		errs := make([]error, workers)
		var group sync.WaitGroup
		group.Add(workers)
		for index := range workers {
			go func() {
				defer group.Done()
				<-ready
				results[index], errs[index] = fixture.best.Run(ctx, runtime.attempts, input, audit.AttemptRunSpec{IdempotencyKey: "attempt.concurrent"}, func(context.Context, *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
					callbackCalls.Add(1)
					return audit.AttemptSucceeded(finish), nil
				})
			}()
		}
		close(ready)
		group.Wait()
		invoked := 0
		for index, runErr := range errs {
			if runErr != nil {
				t.Fatalf("worker %d: %v", index, runErr)
			}
			if results[index].Invoked() {
				invoked++
			}
		}
		if invoked != 1 || callbackCalls.Load() != 1 {
			t.Fatalf("concurrent callbacks = invoked:%d callback:%d", invoked, callbackCalls.Load())
		}
	})

	t.Run("concurrent same type terminals do not strand open attempts", func(t *testing.T) {
		const workers = 8
		entered := make(chan struct{}, workers)
		release := make(chan struct{})
		results := make([]audit.AttemptExecution, workers)
		errs := make([]error, workers)
		var callbackCalls atomic.Int64
		beforeExtracts := fixture.finishGets.Load()
		var group sync.WaitGroup
		group.Add(workers)
		for index := range workers {
			go func() {
				defer group.Done()
				ctx := attemptContextWithOperation(byte(30 + index))
				key := audit.IdempotencyKey(fmt.Sprintf("attempt.type-race.%d", index))
				results[index], errs[index] = fixture.best.Run(ctx, runtime.attempts, input, audit.AttemptRunSpec{IdempotencyKey: key}, func(context.Context, *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
					callbackCalls.Add(1)
					entered <- struct{}{}
					<-release
					return audit.AttemptSucceeded(finish), nil
				})
			}()
		}
		for range workers {
			<-entered
		}
		close(release)
		group.Wait()
		for index, runErr := range errs {
			if runErr != nil {
				t.Fatalf("same-type worker %d: %v", index, runErr)
			}
			assertAttemptState(t, results[index].Attempt(), audit.AttemptSucceededState)
		}
		if callbackCalls.Load() != workers {
			t.Fatalf("same-type callbacks = %d, want %d", callbackCalls.Load(), workers)
		}
		if got := fixture.finishGets.Load() - beforeExtracts; got != workers {
			t.Fatalf("same-type finish extractors = %d, want %d", got, workers)
		}
	})

	t.Run("same key with different operation semantics conflicts", func(t *testing.T) {
		firstCtx := attemptContextWithOperation(3)
		if _, runErr := fixture.best.Run(firstCtx, runtime.attempts, input, audit.AttemptRunSpec{IdempotencyKey: "attempt.conflict"}, func(context.Context, *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
			return audit.AttemptSucceeded(finish), nil
		}); runErr != nil {
			t.Fatal(runErr)
		}
		var callbacks atomic.Int64
		changed := input
		changed.Note = "refund"
		_, runErr := fixture.best.Run(firstCtx, runtime.attempts, changed, audit.AttemptRunSpec{IdempotencyKey: "attempt.conflict"}, func(context.Context, *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
			callbacks.Add(1)
			return audit.AttemptSucceeded(finish), nil
		})
		if !errors.Is(runErr, audit.ErrConflict) || callbacks.Load() != 0 {
			t.Fatalf("changed operation = err:%v callbacks:%d", runErr, callbacks.Load())
		}
	})

	t.Run("start key is unique across the catalog", func(t *testing.T) {
		key := audit.IdempotencyKey("attempt.catalog-key")
		if _, runErr := fixture.best.Run(attemptContextWithOperation(16), runtime.attempts, input, audit.AttemptRunSpec{IdempotencyKey: key}, func(context.Context, *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
			return audit.AttemptSucceeded(finish), nil
		}); runErr != nil {
			t.Fatal(runErr)
		}
		var callbacks atomic.Int64
		_, runErr := fixture.alternate.Run(attemptContextWithOperation(17), runtime.attempts, input, audit.AttemptRunSpec{IdempotencyKey: key}, func(context.Context, *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
			callbacks.Add(1)
			return audit.AttemptSucceeded(finish), nil
		})
		if !errors.Is(runErr, audit.ErrConflict) || callbacks.Load() != 0 {
			t.Fatalf("catalog-wide start key = err:%v callbacks:%d", runErr, callbacks.Load())
		}
	})

	t.Run("direct finish race has one terminal winner", func(t *testing.T) {
		run, started, beginErr := fixture.best.Begin(attemptContextWithOperation(5), runtime.attempts, input, audit.AttemptBeginSpec{IdempotencyKey: "attempt.finish-race.start"})
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if run == nil {
			t.Fatal("inserted begin did not return a handle")
		}
		assertAttemptState(t, started, audit.AttemptOpenState)
		results := make([]audit.AttemptResult, 2)
		errs := make([]error, 2)
		ready := make(chan struct{})
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			<-ready
			results[0], errs[0] = run.Finish(context.Background(), audit.AttemptSucceeded(finish), audit.AttemptTransitionSpec{IdempotencyKey: "attempt.finish-race.success"})
		}()
		go func() {
			defer group.Done()
			<-ready
			results[1], errs[1] = run.Finish(context.Background(), audit.AttemptFailed[attemptFinishInput]("attempt.failed", finish), audit.AttemptTransitionSpec{IdempotencyKey: "attempt.finish-race.failed"})
		}()
		close(ready)
		group.Wait()
		winners := 0
		conflicts := 0
		for index, finishErr := range errs {
			if finishErr == nil {
				winners++
				state, present := results[index].ResultingState()
				if !present || state != audit.AttemptSucceededState && state != audit.AttemptFailedState {
					t.Fatalf("winning finish state = %v, present=%v", state, present)
				}
			} else if errors.Is(finishErr, audit.ErrConflict) {
				conflicts++
			} else {
				t.Fatalf("finish %d: %v", index, finishErr)
			}
		}
		if winners != 1 || conflicts != 1 {
			t.Fatalf("finish race = winners:%d conflicts:%d", winners, conflicts)
		}
	})

	t.Run("application error without completion stays open", func(t *testing.T) {
		appErr := errors.New("application failed")
		ctx := attemptContextWithOperation(6)
		execution, runErr := fixture.best.Run(ctx, runtime.attempts, input, audit.AttemptRunSpec{IdempotencyKey: "attempt.app-error"}, func(context.Context, *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
			return audit.AttemptCompletion[attemptFinishInput]{}, appErr
		})
		if runErr != nil {
			t.Fatal(runErr)
		}
		if !execution.Invoked() || !errors.Is(execution.ApplicationError(), appErr) {
			t.Fatalf("application error execution = invoked:%v err:%v", execution.Invoked(), execution.ApplicationError())
		}
		assertAttemptState(t, execution.Attempt(), audit.AttemptOpenState)
		var callbacks atomic.Int64
		replay, replayErr := fixture.best.Run(ctx, runtime.attempts, input, audit.AttemptRunSpec{IdempotencyKey: "attempt.app-error"}, func(context.Context, *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
			callbacks.Add(1)
			return audit.AttemptSucceeded(finish), nil
		})
		if replayErr != nil {
			t.Fatal(replayErr)
		}
		if replay.Invoked() || callbacks.Load() != 0 || replay.ApplicationError() != nil {
			t.Fatalf("open replay = invoked:%v callbacks:%d app:%v", replay.Invoked(), callbacks.Load(), replay.ApplicationError())
		}
		assertAttemptState(t, replay.Attempt(), audit.AttemptOpenState)
	})

	t.Run("callback cancellation without completion stays open", func(t *testing.T) {
		ctx := attemptContextWithOperation(14)
		execution, runErr := fixture.best.Run(ctx, runtime.attempts, input, audit.AttemptRunSpec{IdempotencyKey: "attempt.callback-cancelled"}, func(context.Context, *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
			return audit.AttemptCompletion[attemptFinishInput]{}, context.Canceled
		})
		if runErr != nil {
			t.Fatal(runErr)
		}
		if !execution.Invoked() || !errors.Is(execution.ApplicationError(), context.Canceled) {
			t.Fatalf("callback cancellation = invoked:%v err:%v", execution.Invoked(), execution.ApplicationError())
		}
		assertAttemptState(t, execution.Attempt(), audit.AttemptOpenState)
	})

	t.Run("completion and application error are independent", func(t *testing.T) {
		appErr := errors.New("response delivery failed")
		execution, runErr := fixture.best.Run(attemptContextWithOperation(7), runtime.attempts, input, audit.AttemptRunSpec{IdempotencyKey: "attempt.completion-error"}, func(context.Context, *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
			return audit.AttemptSucceeded(finish), appErr
		})
		if runErr != nil {
			t.Fatal(runErr)
		}
		if !execution.Invoked() || !errors.Is(execution.ApplicationError(), appErr) {
			t.Fatalf("completion with app error = invoked:%v err:%v", execution.Invoked(), execution.ApplicationError())
		}
		assertAttemptState(t, execution.Attempt(), audit.AttemptSucceededState)
	})

	t.Run("panic leaves open replay", func(t *testing.T) {
		panicValue := errors.New("callback panic")
		ctx := attemptContextWithOperation(8)
		var recovered any
		func() {
			defer func() { recovered = recover() }()
			_, _ = fixture.best.Run(ctx, runtime.attempts, input, audit.AttemptRunSpec{IdempotencyKey: "attempt.panic"}, func(context.Context, *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
				panic(panicValue)
			})
		}()
		if recovered != panicValue {
			t.Fatalf("recovered panic = %v", recovered)
		}
		var callbacks atomic.Int64
		replay, replayErr := fixture.best.Run(ctx, runtime.attempts, input, audit.AttemptRunSpec{IdempotencyKey: "attempt.panic"}, func(context.Context, *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
			callbacks.Add(1)
			return audit.AttemptSucceeded(finish), nil
		})
		if replayErr != nil {
			t.Fatal(replayErr)
		}
		if replay.Invoked() || callbacks.Load() != 0 {
			t.Fatalf("panic replay invoked callback: execution=%v callbacks=%d", replay.Invoked(), callbacks.Load())
		}
		assertAttemptState(t, replay.Attempt(), audit.AttemptOpenState)
	})

	t.Run("required follows durability capabilities", func(t *testing.T) {
		before := resolverCalls.Load()
		var callbacks atomic.Int64
		execution, runErr := fixture.required.Run(attemptContextWithOperation(9), runtime.attempts, input, audit.AttemptRunSpec{IdempotencyKey: "attempt.required"}, func(context.Context, *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
			callbacks.Add(1)
			return audit.AttemptSucceeded(finish), nil
		})
		capabilities := opened.Writer.Capabilities().View()
		if capabilities.Persistence != audit.SupportSupported || capabilities.Reconciliation != audit.SupportSupported {
			if !errors.Is(runErr, audit.ErrUnsupported) || resolverCalls.Load() != before || callbacks.Load() != 0 {
				t.Fatalf("required preflight = err:%v resolver:%d->%d callbacks:%d", runErr, before, resolverCalls.Load(), callbacks.Load())
			}
			return
		}
		if runErr != nil {
			t.Fatal(runErr)
		}
		if resolverCalls.Load() != before+1 || callbacks.Load() != 1 || !execution.Invoked() {
			t.Fatalf("durable required run = resolver:%d->%d callbacks:%d invoked:%v", before, resolverCalls.Load(), callbacks.Load(), execution.Invoked())
		}
		assertAttemptState(t, execution.Attempt(), audit.AttemptSucceededState)
	})

	t.Run("start reserves terminal capacity before callback", func(t *testing.T) {
		spec := opened.Writer.Limits().View()
		spec.AttemptStateBytes = 128 << 10
		limits, limitErr := audit.NewLimits(spec)
		if limitErr != nil {
			t.Fatal(limitErr)
		}
		limited := opened
		limited.Writer = attemptLimitedWriter{Writer: opened.Writer, limits: limits}
		limitedRuntime := openAttemptRuntime(t, limited, fixture, resolver, clock)
		var callbacks atomic.Int64
		_, runErr := fixture.best.Run(attemptContextWithOperation(15), limitedRuntime.attempts, input, audit.AttemptRunSpec{IdempotencyKey: "attempt.reserve"}, func(context.Context, *audit.AttemptProgress[attemptCheckpointInput]) (audit.AttemptCompletion[attemptFinishInput], error) {
			callbacks.Add(1)
			return audit.AttemptSucceeded(finish), nil
		})
		if !errors.Is(runErr, audit.ErrTooLarge) || callbacks.Load() != 0 {
			t.Fatalf("terminal reserve = err:%v callbacks:%d", runErr, callbacks.Load())
		}
	})

	t.Run("attempt append crosses the configured writer", func(t *testing.T) {
		var appends atomic.Int64
		observed := opened
		observed.Writer = attemptObservingWriter{Writer: opened.Writer, appends: &appends}
		observedRuntime := openAttemptRuntime(t, observed, fixture, resolver, clock)
		run, started, beginErr := fixture.best.Begin(attemptContextWithOperation(18), observedRuntime.attempts, input, audit.AttemptBeginSpec{IdempotencyKey: "attempt.observed"})
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if run == nil {
			t.Fatal("observed begin returned no handle")
		}
		assertAttemptState(t, started, audit.AttemptOpenState)
		if appends.Load() != 1 {
			t.Fatalf("configured writer appends = %d, want 1", appends.Load())
		}
	})

	t.Run("new store on same log observes replay", func(t *testing.T) {
		if opened.Reopen == nil {
			t.Fatal("attempt store does not provide reopen")
		}
		ctx := attemptContextWithOperation(10)
		run, started, beginErr := fixture.best.Begin(ctx, runtime.attempts, input, audit.AttemptBeginSpec{IdempotencyKey: "attempt.restart"})
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		if run == nil {
			t.Fatal("initial restart begin did not return handle")
		}
		first, present := started.Receipt()
		if !present || first.Disposition() != audit.Inserted {
			t.Fatalf("initial restart receipt = %v, present=%v", first.Disposition(), present)
		}
		reopened, reopenErr := opened.Reopen()
		if reopenErr != nil {
			t.Fatal(reopenErr)
		}
		defer closeAttemptStore(t, reopened)
		restarted := openAttemptRuntime(t, reopened, fixture, resolver, clock)
		handle, replay, replayErr := fixture.best.Begin(ctx, restarted.attempts, input, audit.AttemptBeginSpec{IdempotencyKey: "attempt.restart"})
		if replayErr != nil {
			t.Fatal(replayErr)
		}
		if handle != nil {
			t.Fatal("restart replay returned a live handle")
		}
		second, present := replay.Receipt()
		if !present || second.Disposition() != audit.Replayed || second.RevisionID() != first.RevisionID() {
			t.Fatalf("restart replay = disposition:%v revision:%x want:%x present:%v", second.Disposition(), second.RevisionID(), first.RevisionID(), present)
		}
	})

	t.Run("unsupported doors refuse immediately", func(t *testing.T) {
		before := resolverCalls.Load()
		var run *audit.AttemptRun[attemptCheckpointInput, attemptFinishInput]
		progress := new(audit.AttemptProgress[attemptCheckpointInput])
		checks := []struct {
			name string
			err  error
		}{
			{name: "resume", err: func() error {
				_, _, err := fixture.best.Resume(nil, nil, audit.OperationID{}, audit.AttemptAccessSpec{})
				return err
			}()},
			{name: "resolve_unknown", err: func() error {
				_, err := fixture.best.ResolveUnknown(nil, nil, audit.OperationID{}, audit.AttemptAccessSpec{}, audit.AttemptCompletion[attemptFinishInput]{}, audit.AttemptTransitionSpec{})
				return err
			}()},
			{name: "abandon", err: func() error {
				_, err := fixture.best.Abandon(nil, nil, audit.OperationID{}, audit.AttemptAccessSpec{}, audit.AttemptTransitionSpec{})
				return err
			}()},
			{name: "run_checkpoint", err: func() error {
				_, err := run.Checkpoint(nil, attemptCheckpointInput{}, audit.AttemptTransitionSpec{})
				return err
			}()},
			{name: "progress_checkpoint", err: func() error {
				_, err := progress.Checkpoint(nil, attemptCheckpointInput{}, audit.AttemptTransitionSpec{})
				return err
			}()},
			{name: "within", err: func() error { _, err := run.Within(nil, audit.AttemptGroupSpec{}, nil); return err }()},
		}
		for _, check := range checks {
			if !errors.Is(check.err, audit.ErrUnsupported) {
				t.Errorf("%s = %v", check.name, check.err)
			}
		}
		if resolverCalls.Load() != before {
			t.Fatalf("unsupported doors reached resolver: %d -> %d", before, resolverCalls.Load())
		}
	})
}

func newAttemptFixture(t testing.TB) attemptFixture {
	t.Helper()
	policy := audit.ContextFacts(
		audit.ActorChain(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Public, audit.AsPlaintext),
		audit.OperationFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Public, audit.AsPlaintext),
	)
	bestMember := declareAttemptMember("attempt.best.member", "attempt.best.event", audit.BestEffort, policy)
	bestOperation := audit.DeclareOperation(audit.OperationPolicy{
		Name: "attempt.best.run", Semantics: audit.Semantics(1), Retention: "attempt.forever",
		Consequence: audit.BestEffort, Context: policy, Members: audit.OperationMembers(bestMember),
	})
	finishGets := new(atomic.Int64)
	best := declareConformanceAttempt("attempt.best", bestOperation, audit.BestEffort, policy, true, "31", "32", "33", "34", finishGets)
	alternateMember := declareAttemptMember("attempt.alternate.member", "attempt.alternate.event", audit.BestEffort, policy)
	alternateOperation := audit.DeclareOperation(audit.OperationPolicy{
		Name: "attempt.alternate.run", Semantics: audit.Semantics(1), Retention: "attempt.forever",
		Consequence: audit.BestEffort, Context: policy, Members: audit.OperationMembers(alternateMember),
	})
	alternate := declareConformanceAttempt("attempt.alternate", alternateOperation, audit.BestEffort, policy, true, "71", "72", "73", "74", nil)
	requiredMember := declareAttemptMember("attempt.required.member", "attempt.required.event", audit.Required, policy)
	requiredOperation := audit.DeclareOperation(audit.OperationPolicy{
		Name: "attempt.required.run", Semantics: audit.Semantics(1), Retention: "attempt.forever",
		Consequence: audit.Required, Context: policy, Members: audit.OperationMembers(requiredMember),
	})
	required := declareConformanceAttempt("attempt.required", requiredOperation, audit.Required, policy, true, "41", "42", "43", "44", nil)
	targetlessMember := declareAttemptMember("attempt.targetless.member", "attempt.targetless.event", audit.BestEffort, policy)
	targetlessOperation := audit.DeclareOperation(audit.OperationPolicy{
		Name: "attempt.targetless.run", Semantics: audit.Semantics(1), Retention: "attempt.forever",
		Consequence: audit.BestEffort, Context: policy, Members: audit.OperationMembers(targetlessMember),
	})
	targetless := declareConformanceAttempt("attempt.targetless", targetlessOperation, audit.BestEffort, policy, false, "61", "62", "63", "64", nil)
	semantic, err := audit.HMACSemanticDigester("attempt-semantic", bytes.Repeat([]byte{21}, 32))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := audit.HMACIdentityKeyring(audit.HMACIdentityKey{KeyID: "attempt-identity", Key: bytes.Repeat([]byte{22}, 32), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := audit.HMACSigner(audit.HMACSigningKey{KeyID: "attempt-signature", Key: bytes.Repeat([]byte{23}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := audit.HMACVerifier(audit.HMACVerificationKey{KeyID: "attempt-signature", Key: bytes.Repeat([]byte{23}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := audit.Compile(audit.CatalogSpec{
		ID: "attempt.audit", Owner: "attempt.team", Generation: 1,
		Retention: audit.RetentionRules(audit.KeepForever("attempt.forever")),
		Semantics: semantic.Description(), Identities: identity.ActiveDescription(), Integrity: audit.RequireSignature(signer.Description()),
		Control: audit.ControlPolicy{
			Resource: "attempt.control", Semantics: audit.Semantics(1), Purpose: "attempt.accountability",
			Retention: "attempt.forever", Consequence: audit.Required,
			Context: audit.ContextFacts(audit.ScopeFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Public, audit.AsPlaintext)),
			Actions: audit.ControlActions(audit.AttemptContinuationAuthorized, audit.AttemptAccessDenied),
			Reasons: audit.ControlReasons(audit.ReasonsFor(audit.AttemptAccessDenied, audit.Reasons("attempt.access.denied"))),
		},
	}, bestMember, bestOperation, best, alternateMember, alternateOperation, alternate, requiredMember, requiredOperation, required, targetlessMember, targetlessOperation, targetless)
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := audit.Lineage(catalog)
	if err != nil {
		t.Fatal(err)
	}
	return attemptFixture{best: best, alternate: alternate, required: required, targetless: targetless, finishGets: finishGets, catalogs: catalogs, semantic: semantic, identity: identity, signer: signer, verifier: verifier}
}

func declareAttemptMember(resource audit.Resource, action audit.Action, consequence audit.Consequence, policy audit.ContextPolicy) *audit.EventType[attemptMemberInput] {
	return audit.Declare(audit.EventPolicy[attemptMemberInput]{
		Semantics: audit.Semantics(1, audit.PolicyGolden(audit.FixtureName(action), strings.Repeat("51", 32))),
		Descriptor: audit.Descriptor{
			Resource: resource, Action: action, Owner: "attempt.team", Purpose: "attempt.accountability",
			Retention: "attempt.forever", Consequence: consequence, Context: policy,
		},
		Target:     audit.NoEventTarget[attemptMemberInput](),
		Outcome:    audit.EventOutcome(audit.Outcomes("accepted"), func(attemptMemberInput) audit.Outcome { return "accepted" }),
		OccurredAt: audit.EventOccurredAt(func(value attemptMemberInput) time.Time { return value.At }),
	})
}

func declareConformanceAttempt(prefix audit.Resource, operation *audit.OperationType, consequence audit.Consequence, policy audit.ContextPolicy, targeted bool, startHash, successHash, failedHash, cancelledHash string, finishGets *atomic.Int64) *audit.AttemptType[attemptStartInput, attemptCheckpointInput, attemptFinishInput] {
	target := audit.NoAttemptTarget[attemptStartInput]()
	if targeted {
		target = audit.AttemptTarget(func(value attemptStartInput) audit.Reference { return value.Target }, audit.Public, audit.AsPlaintext)
	}
	finishValue := func(value attemptFinishInput) string {
		if finishGets != nil {
			finishGets.Add(1)
		}
		return value.Result
	}
	return audit.DeclareAttempt(audit.AttemptPolicy[attemptStartInput, attemptCheckpointInput, attemptFinishInput]{
		Operation: operation,
		Semantics: audit.Semantics(1,
			audit.AttemptStartGolden(audit.FixtureName(prefix+".start"), strings.Repeat(startHash, 32)),
			audit.AttemptFinishGolden(audit.FixtureName(prefix+".succeeded"), audit.AttemptSucceededTransition, "", strings.Repeat(successHash, 32)),
			audit.AttemptFinishGolden(audit.FixtureName(prefix+".failed"), audit.AttemptFailedTransition, "attempt.failed", strings.Repeat(failedHash, 32)),
			audit.AttemptFinishGolden(audit.FixtureName(prefix+".cancelled"), audit.AttemptCancelledTransition, "attempt.cancelled", strings.Repeat(cancelledHash, 32)),
		),
		Descriptor: audit.AttemptDescriptor{
			Resource: prefix, Owner: "attempt.team", Purpose: "attempt.accountability",
			Retention: "attempt.forever", Consequence: consequence, Context: policy,
		},
		MaxOpen:       time.Hour,
		MaxStateBytes: audit.MaxAttemptStateBytes,
		Continuity:    audit.AttemptOwnedBy(audit.AttemptEffectiveActorOwner),
		Start: audit.AttemptStart(
			target,
			audit.AttemptFields(audit.AttemptValue("note", func(value attemptStartInput) string { return value.Note }, audit.Text(), audit.Public)),
		),
		Checkpoints: audit.NoAttemptCheckpoints[attemptCheckpointInput](),
		Finish: audit.AttemptFinish(
			audit.AttemptReasons(
				audit.AttemptReasonsFor(audit.AttemptFailedTransition, audit.Reasons("attempt.failed")),
				audit.AttemptReasonsFor(audit.AttemptCancelledTransition, audit.Reasons("attempt.cancelled")),
			),
			audit.AttemptFinishFields(
				audit.AttemptFieldsFor(audit.AttemptSucceededTransition, audit.AttemptValue("result", finishValue, audit.Text(), audit.Public)),
				audit.AttemptFieldsFor(audit.AttemptFailedTransition, audit.AttemptValue("result", finishValue, audit.Text(), audit.Public)),
				audit.AttemptFieldsFor(audit.AttemptCancelledTransition, audit.AttemptValue("result", finishValue, audit.Text(), audit.Public)),
			),
		),
	})
}

func openAttemptRuntime(t testing.TB, store AttemptStore, fixture attemptFixture, resolver audit.ContextResolver, clock audit.Clock) attemptRuntime {
	t.Helper()
	recorder, err := audit.New(audit.Config{
		Catalogs: fixture.catalogs, Writer: store.Writer, Context: resolver,
		Semantics: fixture.semantic, Identities: fixture.identity, Signer: fixture.signer, Clock: clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	history, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: store.Log,
		Exact: store.Exact, Access: audit.AccessAuthorityFunc(func(_ context.Context, request audit.AccessRequest) (audit.AccessDecision, error) {
			return allowRequested(request)
		}), Verifier: fixture.verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	attempts, err := audit.NewAttempts(audit.AttemptsConfig{
		Profile: audit.RunOnlyAlpha, Recorder: recorder, State: store.Attempts, Types: store.Types,
		History: history, SettlementTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return attemptRuntime{attempts: attempts}
}

func attemptContextWithOperation(value byte) context.Context {
	return context.WithValue(context.Background(), operationContextKey{}, audit.OperationID{value})
}

func assertAttemptState(t testing.TB, result audit.AttemptResult, want audit.AttemptState) {
	t.Helper()
	state, present := result.ResultingState()
	if !present || state != want {
		t.Fatalf("attempt state = %v, want %v, present=%v", state, want, present)
	}
}

func closeAttemptStore(t testing.TB, store AttemptStore) {
	t.Helper()
	if store.Close != nil {
		if err := store.Close(); err != nil {
			t.Errorf("close attempt store: %v", err)
		}
	}
}
