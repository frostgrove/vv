package jobs

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDeclareMaterializesWithoutAHandler(t *testing.T) {
	automatic := Declare[string]()
	if automatic.Handler() != nil {
		t.Fatal("declaration-only automatic has a handler")
	}
	definition, err := Materialize(automatic, GeneratedDefinitionSpec[string]{
		Name:  queueMustName("tests.declared"),
		Codec: String(SchemaVersion(1)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if definition.Name() != automatic.Name() || !automatic.Describe().Automatic {
		t.Fatal("declaration did not materialize")
	}
}

func TestMaterializeReturnPreservesAutomaticActivationBinding(t *testing.T) {
	automatic := Auto(Handler[string](func(context.Context, string) error { return nil }))
	materialized, err := Materialize(automatic, GeneratedDefinitionSpec[string]{Name: queueMustName("tests.materialized-return"), Codec: String(1)})
	if err != nil || materialized != automatic {
		t.Fatalf("materialized = %p, %v", materialized, err)
	}
	queue := testQueue(t, queueMustName("tests"), materialized, successfulQueueSender(), bytes.NewReader(make([]byte, 16)))
	activation, err := queue.Activate()
	if err != nil {
		t.Fatal(err)
	}
	if err := Go(context.Background(), materialized, "value"); err != nil {
		t.Fatal(err)
	}
	if err := activation.Close(); err != nil {
		t.Fatal(err)
	}
	generated, ok := automatic.Definition()
	if !ok {
		t.Fatal("generated definition is missing")
	}
	if _, err := NewCatalog(generated); !errors.Is(err, ErrInvalid) {
		t.Fatalf("detached generated definition catalog = %v", err)
	}
}

func TestQueueActivationEnablesGoUntilClosedWithoutStartingHandlers(t *testing.T) {
	var handlerCalls atomic.Int64
	automatic := Auto(Handler[string](func(context.Context, string) error {
		handlerCalls.Add(1)
		return nil
	}))
	MustMaterialize(automatic, GeneratedDefinitionSpec[string]{
		Name:  queueMustName("tests.go"),
		Codec: String(SchemaVersion(1)),
	})
	var captured Placement
	sender := queueSenderFunc(func(_ context.Context, placement Placement) (PlacementResult, error) {
		captured = placement
		return mustPlacementResult(placement.Candidate(), PlacementCreated), nil
	})
	queue := testQueue(t, queueMustName("tests"), automatic, sender, bytes.NewReader(make([]byte, 32)))
	if err := Go(context.Background(), automatic, "value"); !errors.Is(err, ErrNotActivated) {
		t.Fatalf("Go before activation = %v", err)
	}
	activation, err := queue.Activate()
	if err != nil {
		t.Fatal(err)
	}
	if err := Go(context.Background(), automatic, "value"); err != nil {
		t.Fatal(err)
	}
	if captured.Definition() != automatic.Name() || string(captured.Payload().Bytes()) != "value" {
		t.Fatalf("placement = %+v", captured)
	}
	if handlerCalls.Load() != 0 {
		t.Fatal("activation or enqueue started a handler")
	}
	if err := activation.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Go(context.Background(), automatic, "value"); !errors.Is(err, ErrNotActivated) {
		t.Fatalf("Go after close = %v", err)
	}
}

func TestQueueActivationIsIdempotentForTheSameQueue(t *testing.T) {
	automatic := materializedAutomatic(t, "tests.idempotent")
	queue := testQueue(t, queueMustName("tests"), automatic, successfulQueueSender(), bytes.NewReader(make([]byte, 16)))
	first, err := queue.Activate()
	if err != nil {
		t.Fatal(err)
	}
	second, err := queue.Activate()
	if err != nil {
		t.Fatal(err)
	}
	if first != second || automatic.boundQueue() != queue {
		t.Fatal("same queue did not retain its activation generation")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOverlappingQueueActivationIsAllOrNothingAndCanRebind(t *testing.T) {
	leftOnly := materializedAutomatic(t, "tests.left")
	shared := materializedAutomatic(t, "tests.shared")
	rightOnly := materializedAutomatic(t, "tests.right")
	sender := successfulQueueSender()
	left := queueFromDeclarations(t, "left", sender, leftOnly, shared)
	right := queueFromDeclarations(t, "right", sender, shared, rightOnly)
	leftActivation, err := left.Activate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := right.Activate(); !errors.Is(err, ErrConflict) {
		t.Fatalf("overlapping activation = %v", err)
	}
	if leftOnly.boundQueue() != left || shared.boundQueue() != left || rightOnly.boundQueue() != nil {
		t.Fatal("failed activation partially committed")
	}
	if err := leftActivation.Close(); err != nil {
		t.Fatal(err)
	}
	rightActivation, err := right.Activate()
	if err != nil {
		t.Fatal(err)
	}
	if leftOnly.boundQueue() != nil || shared.boundQueue() != right || rightOnly.boundQueue() != right {
		t.Fatal("closed declarations did not rebind as one generation")
	}
	if err := rightActivation.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentQueueActivationHasOneWinner(t *testing.T) {
	automatic := materializedAutomatic(t, "tests.concurrent")
	sender := successfulQueueSender()
	left := testQueue(t, queueMustName("left"), automatic, sender, bytes.NewReader(make([]byte, 16)))
	right := testQueue(t, queueMustName("right"), automatic, sender, bytes.NewReader(make([]byte, 16)))
	start := make(chan struct{})
	results := make(chan activationResult, 2)
	var wait sync.WaitGroup
	for _, queue := range []*Queue{left, right} {
		wait.Add(1)
		go func(current *Queue) {
			defer wait.Done()
			<-start
			activation, err := current.Activate()
			results <- activationResult{activation: activation, err: err}
		}(queue)
	}
	close(start)
	wait.Wait()
	close(results)
	var winner *QueueActivation
	conflicted := 0
	for result := range results {
		switch {
		case result.err == nil:
			winner = result.activation
		case errors.Is(result.err, ErrConflict):
			conflicted++
		default:
			t.Fatalf("activation = %v", result.err)
		}
	}
	if winner == nil || conflicted != 1 || automatic.boundQueue() != left && automatic.boundQueue() != right {
		t.Fatalf("winner=%p conflicts=%d queue=%p", winner, conflicted, automatic.boundQueue())
	}
	if err := winner.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitDefinitionsDoNotRequireActivation(t *testing.T) {
	definition := testQueueDefinition(t, "tests.explicit", String(SchemaVersion(1)))
	queue := testQueue(t, queueMustName("tests"), definition, successfulQueueSender(), bytes.NewReader(make([]byte, 16)))
	consumer := On(definition, Handler[string](func(context.Context, string) error { return nil }))
	if err := validateConsumers(queue.Catalog(), consumer); err != nil {
		t.Fatal(err)
	}
	if _, err := Enqueue(context.Background(), queue, definition, "value"); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitConsumerCoexistsWithAutomaticProducerActivation(t *testing.T) {
	automatic := Declare[string]()
	consumer := On(automatic, Handler[string](func(context.Context, string) error { return nil }))
	MustMaterialize(automatic, GeneratedDefinitionSpec[string]{Name: queueMustName("tests.consumer-producer"), Codec: String(1)})
	queue := testQueue(t, queueMustName("tests"), automatic, successfulQueueSender(), bytes.NewReader(make([]byte, 32)))
	if err := validateConsumers(queue.Catalog(), consumer); err != nil {
		t.Fatal(err)
	}
	activation, err := queue.Activate()
	if err != nil {
		t.Fatal(err)
	}
	if err := Go(context.Background(), automatic, "go"); err != nil {
		t.Fatal(err)
	}
	if _, err := Enqueue(context.Background(), queue, automatic, "enqueue"); err != nil {
		t.Fatal(err)
	}
	if automatic.Handler() != nil {
		t.Fatal("On mutated a producer-only automatic declaration")
	}
	if err := activation.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGoCannotObserveAnUncommittedActivation(t *testing.T) {
	automatic := materializedAutomatic(t, "tests.activation-gate")
	queue := testQueue(t, queueMustName("tests"), automatic, successfulQueueSender(), bytes.NewReader(make([]byte, 16)))
	activation := &QueueActivation{queue: queue}
	automatic.activation.Store(activation)
	if err := Go(context.Background(), automatic, "value"); !errors.Is(err, ErrNotActivated) {
		t.Fatalf("Go through open activation gate = %v", err)
	}
	activation.state.Store(queueActivationActive)
	if err := Go(context.Background(), automatic, "value"); err != nil {
		t.Fatal(err)
	}
	activation.state.Store(queueActivationClosed)
	automatic.activation.Store(nil)
}

func TestStaleCloseCannotDetachNewActivation(t *testing.T) {
	automatic := materializedAutomatic(t, "tests.generation")
	left := testQueue(t, queueMustName("left"), automatic, successfulQueueSender(), bytes.NewReader(make([]byte, 16)))
	rightCalls := atomic.Int64{}
	rightSender := queueSenderFunc(func(_ context.Context, placement Placement) (PlacementResult, error) {
		rightCalls.Add(1)
		return mustPlacementResult(placement.Candidate(), PlacementCreated), nil
	})
	right := testQueue(t, queueMustName("right"), automatic, rightSender, bytes.NewReader(make([]byte, 16)))
	stale, err := left.Activate()
	if err != nil {
		t.Fatal(err)
	}
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	current, err := right.Activate()
	if err != nil {
		t.Fatal(err)
	}
	if err := stale.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Go(context.Background(), automatic, "value"); err != nil {
		t.Fatal(err)
	}
	if rightCalls.Load() != 1 || automatic.boundQueue() != right {
		t.Fatal("stale close detached the current generation")
	}
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentGoAndCloseAreLifecycleSafe(t *testing.T) {
	automatic := materializedAutomatic(t, "tests.close-race")
	queue := testQueue(t, queueMustName("tests"), automatic, successfulQueueSender(), bytes.NewReader(make([]byte, 16*128)))
	activation, err := queue.Activate()
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 65)
	var wait sync.WaitGroup
	for range 64 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- Go(context.Background(), automatic, "value")
		}()
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		<-start
		results <- activation.Close()
	}()
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil && !errors.Is(err, ErrNotActivated) {
			t.Fatalf("concurrent result = %v", err)
		}
	}
	if err := Go(context.Background(), automatic, "value"); !errors.Is(err, ErrNotActivated) {
		t.Fatalf("Go after concurrent close = %v", err)
	}
}

type activationResult struct {
	activation *QueueActivation
	err        error
}

func queueFromDeclarations(t *testing.T, namespace string, sender Sender, declarations ...Declaration) *Queue {
	t.Helper()
	catalog, err := NewCatalog(declarations...)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := NewQueue(QueueSpec{
		Namespace: queueTestNamespace(t, namespace),
		Catalog:   catalog,
		Sender:    sender,
		Entropy:   bytes.NewReader(make([]byte, 64)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return queue
}

func materializedAutomatic(t *testing.T, name string) *Automatic[string] {
	t.Helper()
	automatic := Declare[string]()
	MustMaterialize(automatic, GeneratedDefinitionSpec[string]{
		Name:  queueMustName(name),
		Codec: String(SchemaVersion(1)),
	})
	return automatic
}

func successfulQueueSender() Sender {
	return queueSenderFunc(func(_ context.Context, placement Placement) (PlacementResult, error) {
		return mustPlacementResult(placement.Candidate(), PlacementCreated), nil
	})
}
