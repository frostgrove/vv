package jobs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type queueSenderFunc func(context.Context, Placement) (PlacementResult, error)

func (queueSenderFunc) Description() BackendDescription { return queueTestBackendDescription(1) }
func (fn queueSenderFunc) Place(ctx context.Context, placement Placement) (PlacementResult, error) {
	return fn(ctx, placement)
}

type boundQueueSender struct {
	description BackendDescription
	place       func(context.Context, Placement) (PlacementResult, error)
}

func (s boundQueueSender) Description() BackendDescription { return s.description }
func (s boundQueueSender) Place(ctx context.Context, placement Placement) (PlacementResult, error) {
	return s.place(ctx, placement)
}

func TestEnqueueBuildsBoundedImmutablePlacement(t *testing.T) {
	definition := testQueueDefinition(t, "tests.enqueue", String(SchemaVersion(1)))
	var captured Placement
	sender := queueSenderFunc(func(_ context.Context, placement Placement) (PlacementResult, error) {
		captured = placement
		return mustPlacementResult(placement.Candidate(), PlacementCreated), nil
	})
	queue := testQueue(t, queueMustName("tests"), definition, sender, bytes.NewReader(make([]byte, 64)))
	deadline := time.Date(2035, 2, 3, 4, 5, 6, 7, time.FixedZone("offset", 6*60*60))
	id, err := Enqueue(context.Background(), queue, definition, "secret payload", After(time.Minute), StartBefore(deadline))
	if err != nil {
		t.Fatal(err)
	}
	if id != captured.Candidate() || captured.Namespace() != queueTestNamespace(t, "tests") || captured.Definition() != definition.Name() || captured.Queue() != definition.Policy().Queue || captured.Mode() != PlacementRegular {
		t.Fatalf("placement identity mismatch: %+v", captured)
	}
	if captured.Delay() != time.Minute || !captured.StartBefore().Equal(deadline) || captured.StartBefore().Location() != time.UTC {
		t.Fatalf("placement timing = %s / %v", captured.Delay(), captured.StartBefore())
	}
	if got := string(captured.Payload().Bytes()); got != "secret payload" {
		t.Fatalf("payload = %q", got)
	}
	if captured.IntentDigest().IsZero() || captured.WireDigest().IsZero() || !captured.PayloadDigest().IsZero() || !captured.Partition().Global() || captured.Priority() != definition.Policy().Priority || !captured.Policy().valid() {
		t.Fatal("placement digests or policy are missing")
	}
	if got := fmt.Sprintf("%+v", queue); got != "[job queue]" {
		t.Fatalf("queue format = %q", got)
	}
}

func TestEnqueueOnceProducesStableIntentAndPayloadOutcomes(t *testing.T) {
	definition := testQueueDefinition(t, "tests.once", String(SchemaVersion(1)))
	placements := make([]Placement, 0, 4)
	outcomes := []EnqueueOnceOutcome{EnqueueCreated, EnqueueExistingSamePayload, EnqueueConflict, EnqueueCreated}
	sender := queueSenderFunc(func(_ context.Context, placement Placement) (PlacementResult, error) {
		index := len(placements)
		placements = append(placements, placement)
		id := placement.Candidate()
		if outcomes[index] != EnqueueCreated {
			id = placements[0].Candidate()
		}
		return mustPlacementResult(id, PlacementOutcome(outcomes[index])), nil
	})
	queue := testQueue(t, queueMustName("tests"), definition, sender, bytes.NewReader(make([]byte, 128)))
	intent := Intent("request-123")
	firstID, firstOutcome, err := EnqueueOnce(context.Background(), queue, definition, intent, "same")
	if err != nil || firstOutcome != EnqueueCreated {
		t.Fatalf("first = %v/%v/%v", firstID, firstOutcome, err)
	}
	secondID, secondOutcome, err := EnqueueOnce(context.Background(), queue, definition, intent, "same")
	if err != nil || secondOutcome != EnqueueExistingSamePayload || secondID != firstID {
		t.Fatalf("second = %v/%v/%v", secondID, secondOutcome, err)
	}
	conflictID, conflictOutcome, err := EnqueueOnce(context.Background(), queue, definition, intent, "different")
	if err != nil || conflictOutcome != EnqueueConflict || conflictID != firstID {
		t.Fatalf("conflict = %v/%v/%v", conflictID, conflictOutcome, err)
	}
	_, _, err = EnqueueOnce(context.Background(), queue, definition, Intent("request-456"), "same")
	if err != nil {
		t.Fatal(err)
	}
	if placements[0].IntentDigest() != placements[1].IntentDigest() || placements[0].IntentDigest() != placements[2].IntentDigest() || placements[0].IntentDigest() == placements[3].IntentDigest() {
		t.Fatal("intent digest stability or isolation failed")
	}
	if placements[0].PayloadDigest() != placements[1].PayloadDigest() || placements[0].PayloadDigest() == placements[2].PayloadDigest() {
		t.Fatal("payload digest stability or conflict failed")
	}
	for _, placement := range placements {
		if placement.Mode() != PlacementOnce {
			t.Fatal("once placement has regular mode")
		}
	}
}

func TestEnqueueRejectsNonMemberBeforeEncoding(t *testing.T) {
	var encodes atomic.Int32
	codec := &countingStringCodec{encodes: &encodes}
	member := testQueueDefinition(t, "tests.member", codec)
	other := testQueueDefinition(t, "tests.member", codec)
	sender := queueSenderFunc(func(context.Context, Placement) (PlacementResult, error) {
		t.Fatal("sender called")
		return PlacementResult{}, nil
	})
	queue := testQueue(t, queueMustName("tests"), member, sender, bytes.NewReader(make([]byte, 16)))
	if _, err := Enqueue(context.Background(), queue, other, "value"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v", err)
	}
	if encodes.Load() != 0 {
		t.Fatalf("encodes = %d", encodes.Load())
	}
}

func TestEnqueueValidatesOptionsContextIntentAndDriverResult(t *testing.T) {
	definition := testQueueDefinition(t, "tests.validation", String(SchemaVersion(1)))
	var calls atomic.Int32
	sender := queueSenderFunc(func(_ context.Context, placement Placement) (PlacementResult, error) {
		calls.Add(1)
		return mustPlacementResult(queueTestInvocationID(t, 7), PlacementCreated), nil
	})
	queue := testQueue(t, queueMustName("tests"), definition, sender, bytes.NewReader(make([]byte, 128)))
	if _, err := Enqueue(context.Background(), queue, definition, "value", After(time.Second), After(time.Second)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate delay error = %v", err)
	}
	if _, err := Enqueue(context.Background(), queue, definition, "value", After(0)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero delay error = %v", err)
	}
	if _, _, err := EnqueueOnce(context.Background(), queue, definition, Intent(""), "value"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty intent error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Enqueue(ctx, queue, definition, "value"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	if _, err := Enqueue(context.Background(), queue, definition, "value"); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("invalid driver result error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("driver calls = %d", calls.Load())
	}
}

func TestNewQueueRejectsMissingBackendIdentity(t *testing.T) {
	definition := testQueueDefinition(t, "tests.backend-required", String(1))
	sender := boundQueueSender{place: func(context.Context, Placement) (PlacementResult, error) {
		t.Fatal("sender called")
		return PlacementResult{}, nil
	}}
	_, err := NewQueue(QueueSpec{Namespace: queueTestNamespace(t, "tests"), Catalog: MustCatalog(definition), Sender: sender})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("backend error = %v", err)
	}
}

func TestQueueSnapshotsBackendContractAndRejectsIncompleteCapabilities(t *testing.T) {
	definition := testQueueDefinition(t, "tests.backend-contract", String(1))
	full := queueTestBackendDescription(7)
	sender := boundQueueSender{description: full, place: func(context.Context, Placement) (PlacementResult, error) {
		t.Fatal("sender called")
		return PlacementResult{}, nil
	}}
	queue, err := NewQueue(QueueSpec{Namespace: queueTestNamespace(t, "tests"), Catalog: MustCatalog(definition), Sender: sender})
	if err != nil {
		t.Fatal(err)
	}
	if queue.Description() != full || queue.Backend() != full.ID() || queue.Durability() != full.Durability() || queue.Capabilities() != full.Capabilities() || queue.Requirements() != StandardProducerRequirements() {
		t.Fatal("queue did not preserve one backend snapshot")
	}
	for _, capabilities := range []Capabilities{{Debounce: true, Scheduled: true}, {Priority: true, Scheduled: true}, {Priority: true, Debounce: true}} {
		description, err := NewBackendDescription(queueTestBackendID(8), queueTestDurability(), capabilities)
		if err != nil {
			t.Fatal(err)
		}
		_, err = NewQueue(QueueSpec{Namespace: queueTestNamespace(t, "tests"), Catalog: MustCatalog(definition), Sender: boundQueueSender{description: description, place: sender.place}})
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("capabilities %+v = %v", capabilities, err)
		}
	}
	coreDescription, err := NewBackendDescription(queueTestBackendID(9), queueTestDurability(), Capabilities{})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	coreSender := boundQueueSender{description: coreDescription, place: func(_ context.Context, placement Placement) (PlacementResult, error) {
		calls.Add(1)
		return mustPlacementResult(placement.Candidate(), PlacementCreated), nil
	}}
	var contextCalls atomic.Int32
	capture := mustContextCapture(t, ContextCaptureSpec{Provenance: mustIdentityProvenance(t, "framework.test"), Epoch: 1})
	provider := TrustedContextProviderFunc(func(context.Context, ContextCaptureRequest) (ContextCapture, error) {
		contextCalls.Add(1)
		return capture, nil
	})
	coreQueue, err := NewQueue(QueueSpec{Namespace: queueTestNamespace(t, "tests"), Catalog: MustCatalog(definition), Sender: coreSender, Context: provider, Requirements: ProducerCoreOnly(), Entropy: bytes.NewReader(make([]byte, 48))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Enqueue(context.Background(), coreQueue, definition, "value"); err != nil {
		t.Fatal(err)
	}
	if _, err := Enqueue(context.Background(), coreQueue, definition, "value", AtPriority(7)); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("priority preflight = %v", err)
	}
	if _, err := Enqueue(context.Background(), coreQueue, definition, "value", Collapse("same")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("debounce preflight = %v", err)
	}
	if _, err := Enqueue(context.Background(), coreQueue, definition, "value", After(time.Second)); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("scheduled preflight = %v", err)
	}
	if _, err := Enqueue(context.Background(), coreQueue, definition, "value", Debounce("same", MaxDelay(time.Minute)), After(time.Second)); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("scheduled debounce preflight = %v", err)
	}
	if _, err := EnqueueIn(context.Background(), coreQueue, panickingQueueStager{}, definition, "value", AtPriority(7)); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("staged priority preflight = %v", err)
	}
	if _, err := EnqueueIn(context.Background(), coreQueue, panickingQueueStager{}, definition, "value", Collapse("same")); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("staged debounce preflight = %v", err)
	}
	if _, err := EnqueueIn(context.Background(), coreQueue, panickingQueueStager{}, definition, "value", After(time.Second)); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("staged scheduling preflight = %v", err)
	}
	if _, err := EnqueueIn(context.Background(), coreQueue, panickingQueueStager{}, definition, "value", Debounce("same", MaxDelay(time.Minute)), After(time.Second)); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("staged scheduled debounce preflight = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("sender calls = %d", calls.Load())
	}
	if contextCalls.Load() != 1 {
		t.Fatalf("context calls = %d", contextCalls.Load())
	}
}

func TestQueueEnforcesStandaloneAndTransactionalDurabilityRequirements(t *testing.T) {
	policy, err := Default.With(ProtectAcknowledgedEnqueuesFrom(FailureProcessCrash)).Build()
	if err != nil {
		t.Fatal(err)
	}
	var encodes atomic.Int32
	definition := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "tests.durable"), Codec: &countingStringCodec{encodes: &encodes}, Policy: policy})
	possible, _ := NewDurabilityProfile(AckBeforePersistence, AcknowledgedLossPossible, FailureSet{})
	weakDescription, _ := NewBackendDescription(queueTestBackendID(1), possible, Capabilities{Priority: true, Debounce: true, Scheduled: true})
	weak := boundQueueSender{description: weakDescription, place: func(context.Context, Placement) (PlacementResult, error) {
		t.Fatal("weak sender called")
		return PlacementResult{}, nil
	}}
	if _, err := NewQueue(QueueSpec{Namespace: queueTestNamespace(t, "tests"), Catalog: MustCatalog(definition), Sender: weak}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("weak standalone durability = %v", err)
	}
	strong := boundQueueSender{description: queueTestBackendDescription(1), place: func(context.Context, Placement) (PlacementResult, error) {
		t.Fatal("strong sender called")
		return PlacementResult{}, nil
	}}
	queue, err := NewQueue(QueueSpec{Namespace: queueTestNamespace(t, "tests"), Catalog: MustCatalog(definition), Sender: strong, Entropy: errReader{err: errors.New("entropy must not run")}})
	if err != nil {
		t.Fatal(err)
	}
	required, ok := queue.RequiredDurability(definition.Name())
	if !ok || required != policy.Durability || !queue.GlobalDurabilityRequirement().IsZero() {
		t.Fatalf("resolved durability = (%+v, %t)", required, ok)
	}
	strongTransaction := queueTestTransaction(queue.Backend())
	weakTransaction, _ := NewTransactionContext(queue.Backend(), strongTransaction.Binding(), possible)
	var stages atomic.Int32
	stager := queueStager{transaction: weakTransaction, stage: func(context.Context, Placement) (Staged, error) {
		stages.Add(1)
		return Staged{}, nil
	}}
	if _, err := EnqueueIn(context.Background(), queue, stager, definition, "value"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("weak transaction durability = %v", err)
	}
	if stages.Load() != 0 || encodes.Load() != 0 {
		t.Fatalf("effects before durability rejection: stages=%d encodes=%d", stages.Load(), encodes.Load())
	}
	remoteOnly, _ := AckModes(AckRemotePersistence)
	global, _ := NewDurabilityRequirement(remoteOnly, FailureSet{})
	if _, err := NewQueue(QueueSpec{Namespace: queueTestNamespace(t, "tests"), Catalog: MustCatalog(definition), Sender: strong, Durability: global}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("global ack mode requirement = %v", err)
	}
	beforeAndLocal, _ := AckModes(AckBeforePersistence, AckLocalPersistence)
	beforeAndRemote, _ := AckModes(AckBeforePersistence, AckRemotePersistence)
	global, _ = NewDurabilityRequirement(beforeAndLocal, FailureSet{})
	protected, _ := Failures(FailureProcessCrash)
	definitionRequirement, _ := NewDurabilityRequirement(beforeAndRemote, protected)
	impossiblePolicy, _ := Default.With(AcceptAckModes(AckBeforePersistence, AckRemotePersistence), ProtectAcknowledgedEnqueuesFrom(FailureProcessCrash)).Build()
	if impossiblePolicy.Durability != definitionRequirement {
		t.Fatal("definition requirement setup drifted")
	}
	impossible := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "tests.impossible-composition"), Codec: String(1), Policy: impossiblePolicy})
	if _, err := NewQueue(QueueSpec{Namespace: queueTestNamespace(t, "tests"), Catalog: MustCatalog(impossible), Sender: strong, Durability: global}); !errors.Is(err, ErrConflict) {
		t.Fatalf("jointly impossible queue durability = %v", err)
	}
}

func TestEnqueueInRejectsTransactionBeforeContextAndPayloadEffects(t *testing.T) {
	policy, err := Default.With(ProtectAcknowledgedEnqueuesFrom(FailureProcessCrash)).Build()
	if err != nil {
		t.Fatal(err)
	}
	var encodes atomic.Int32
	definition := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "tests.transaction-preflight"), Codec: &countingStringCodec{encodes: &encodes}, Policy: policy})
	capture := mustContextCapture(t, ContextCaptureSpec{Provenance: mustIdentityProvenance(t, "framework.test"), Epoch: 1})
	var captures atomic.Int32
	provider := TrustedContextProviderFunc(func(context.Context, ContextCaptureRequest) (ContextCapture, error) {
		captures.Add(1)
		return capture, nil
	})
	var sends atomic.Int32
	sender := boundQueueSender{description: queueTestBackendDescription(21), place: func(context.Context, Placement) (PlacementResult, error) {
		sends.Add(1)
		return PlacementResult{}, nil
	}}
	var entropyReads atomic.Int32
	queue, err := NewQueue(QueueSpec{
		Namespace: queueTestNamespace(t, "transaction-preflight"),
		Catalog:   MustCatalog(definition),
		Sender:    sender,
		Context:   provider,
		Entropy:   &countingEntropyReader{reads: &entropyReads},
	})
	if err != nil {
		t.Fatal(err)
	}
	possible, _ := NewDurabilityProfile(AckBeforePersistence, AcknowledgedLossPossible, FailureSet{})
	strong := queueTestTransaction(queue.Backend())
	weak, _ := NewTransactionContext(queue.Backend(), strong.Binding(), possible)
	var transactions atomic.Int32
	var stages atomic.Int32
	stager := countingQueueStager{transaction: weak, transactions: &transactions, stages: &stages}
	if _, err := EnqueueIn(context.Background(), queue, stager, definition, "value"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("weak transaction = %v", err)
	}
	if transactions.Load() != 1 || captures.Load() != 0 || encodes.Load() != 0 || entropyReads.Load() != 0 || stages.Load() != 0 || sends.Load() != 0 {
		t.Fatalf("effects = tx:%d capture:%d encode:%d entropy:%d stage:%d send:%d", transactions.Load(), captures.Load(), encodes.Load(), entropyReads.Load(), stages.Load(), sends.Load())
	}
}

func TestEnqueueContextProviderIsContainedBeforePayloadAndDriverEffects(t *testing.T) {
	secret := errors.New("identity provider password=private")
	validCapture := mustContextCapture(t, ContextCaptureSpec{Provenance: mustIdentityProvenance(t, "framework.test"), Epoch: 1})
	cases := []struct {
		name string
		want error
		call func(context.CancelFunc) TrustedContextProvider
	}{
		{"error", ErrDriver, func(context.CancelFunc) TrustedContextProvider {
			return TrustedContextProviderFunc(func(context.Context, ContextCaptureRequest) (ContextCapture, error) { return validCapture, secret })
		}},
		{"panic", ErrDriver, func(context.CancelFunc) TrustedContextProvider {
			return TrustedContextProviderFunc(func(context.Context, ContextCaptureRequest) (ContextCapture, error) { panic("provider private panic") })
		}},
		{"invalid", ErrInvalid, func(context.CancelFunc) TrustedContextProvider {
			return TrustedContextProviderFunc(func(context.Context, ContextCaptureRequest) (ContextCapture, error) { return ContextCapture{}, nil })
		}},
		{"cancel", context.Canceled, func(cancel context.CancelFunc) TrustedContextProvider {
			return TrustedContextProviderFunc(func(context.Context, ContextCaptureRequest) (ContextCapture, error) {
				cancel()
				return validCapture, secret
			})
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var encodes atomic.Int32
			definition := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "tests.provider-"+test.name), Codec: &countingStringCodec{encodes: &encodes}, Policy: Default.policy})
			var senderCalls atomic.Int32
			sender := boundQueueSender{description: queueTestBackendDescription(22), place: func(context.Context, Placement) (PlacementResult, error) {
				senderCalls.Add(1)
				return PlacementResult{}, nil
			}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var captures atomic.Int32
			provider := test.call(cancel)
			counted := TrustedContextProviderFunc(func(ctx context.Context, request ContextCaptureRequest) (ContextCapture, error) {
				captures.Add(1)
				return provider.Capture(ctx, request)
			})
			var entropyReads atomic.Int32
			queue, err := NewQueue(QueueSpec{Namespace: queueTestNamespace(t, "provider-"+test.name), Catalog: MustCatalog(definition), Sender: sender, Context: counted, Entropy: &countingEntropyReader{reads: &entropyReads}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = Enqueue(ctx, queue, definition, "value")
			if !errors.Is(err, test.want) || errors.Is(err, secret) || strings.Contains(fmt.Sprint(err), "private") {
				t.Fatalf("provider error = %v", err)
			}
			if captures.Load() != 1 || encodes.Load() != 0 || entropyReads.Load() != 0 || senderCalls.Load() != 0 {
				t.Fatalf("effects = capture:%d encode:%d entropy:%d sender:%d", captures.Load(), encodes.Load(), entropyReads.Load(), senderCalls.Load())
			}
		})
	}
}

func TestEnqueueOnceWithoutPayloadIdentityFailsBeforeExternalEffects(t *testing.T) {
	var encodes atomic.Int32
	definition := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "tests.once-identity"), Codec: &countingStringCodec{encodes: &encodes}, Policy: Default.policy})
	var captures atomic.Int32
	provider := TrustedContextProviderFunc(func(context.Context, ContextCaptureRequest) (ContextCapture, error) {
		captures.Add(1)
		return ContextCapture{}, nil
	})
	var entropyReads atomic.Int32
	var sends atomic.Int32
	sender := boundQueueSender{description: queueTestBackendDescription(23), place: func(context.Context, Placement) (PlacementResult, error) {
		sends.Add(1)
		return PlacementResult{}, nil
	}}
	queue, err := NewQueue(QueueSpec{Namespace: queueTestNamespace(t, "once-identity"), Catalog: MustCatalog(definition), Sender: sender, Context: provider, Entropy: &countingEntropyReader{reads: &entropyReads}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnqueueOnce(context.Background(), queue, definition, Intent("same"), "value"); err != ErrUnsupported {
		t.Fatalf("standalone once = %v", err)
	}
	if _, err := EnqueueOnceIn(context.Background(), queue, panickingQueueStager{}, definition, Intent("same"), "value"); err != ErrUnsupported {
		t.Fatalf("staged once = %v", err)
	}
	if captures.Load() != 0 || encodes.Load() != 0 || entropyReads.Load() != 0 || sends.Load() != 0 {
		t.Fatalf("effects = capture:%d encode:%d entropy:%d send:%d", captures.Load(), encodes.Load(), entropyReads.Load(), sends.Load())
	}
}

func TestSuccessfulStagedEnqueueInvokesEachBoundaryOnce(t *testing.T) {
	var encodes atomic.Int32
	definition := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "tests.staged-order"), Codec: &countingStringCodec{encodes: &encodes}, Policy: Default.policy})
	capture := mustContextCapture(t, ContextCaptureSpec{Provenance: mustIdentityProvenance(t, "framework.test"), Epoch: 1})
	var captures atomic.Int32
	provider := TrustedContextProviderFunc(func(context.Context, ContextCaptureRequest) (ContextCapture, error) {
		captures.Add(1)
		return capture, nil
	})
	var entropyReads atomic.Int32
	sender := boundQueueSender{description: queueTestBackendDescription(24)}
	queue, err := NewQueue(QueueSpec{Namespace: queueTestNamespace(t, "staged-order"), Catalog: MustCatalog(definition), Sender: sender, Context: provider, Entropy: &countingEntropyReader{reads: &entropyReads}})
	if err != nil {
		t.Fatal(err)
	}
	transaction := queueTestTransaction(queue.Backend())
	var transactions atomic.Int32
	var stages atomic.Int32
	stager := countingQueueStager{
		transaction:  transaction,
		transactions: &transactions,
		stages:       &stages,
		place: func(placement Placement) (Staged, error) {
			return mustStaged(transaction, mustPlacementResult(placement.Candidate(), PlacementCreated)), nil
		},
	}
	if _, err := EnqueueIn(context.Background(), queue, stager, definition, "value"); err != nil {
		t.Fatal(err)
	}
	if transactions.Load() != 1 || captures.Load() != 1 || encodes.Load() != 1 || entropyReads.Load() != 1 || stages.Load() != 1 {
		t.Fatalf("effects = tx:%d capture:%d encode:%d entropy:%d stage:%d", transactions.Load(), captures.Load(), encodes.Load(), entropyReads.Load(), stages.Load())
	}
}

func TestQueueSerializesEntropyForConcurrentEnqueue(t *testing.T) {
	definition := testQueueDefinition(t, "tests.entropy", String(SchemaVersion(1)))
	reader := &exclusiveEntropyReader{}
	queue := testQueue(t, queueMustName("tests"), definition, successfulQueueSender(), reader)
	var wait sync.WaitGroup
	for range 64 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := Enqueue(context.Background(), queue, definition, "value"); err != nil {
				t.Errorf("enqueue: %v", err)
			}
		}()
	}
	wait.Wait()
	if reader.concurrent.Load() {
		t.Fatal("entropy reader was used concurrently")
	}
}

func TestLegacyIntentCompatibilityIsExplicit(t *testing.T) {
	definition := testQueueDefinition(t, "tests.legacy", String(1))
	captured := make([]Placement, 0, 3)
	sender := queueSenderFunc(func(_ context.Context, placement Placement) (PlacementResult, error) {
		captured = append(captured, placement)
		return mustPlacementResult(placement.Candidate(), PlacementCreated), nil
	})
	rolling, err := NewIntentDigestPlan(DigestRevision2, DigestRevision1)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := WithLegacyIntentCompatibility(rolling)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := NewQueue(QueueSpec{Namespace: queueTestNamespace(t, "tests"), Catalog: MustCatalog(definition), Sender: sender, Digests: plan, Entropy: bytes.NewReader(make([]byte, 48))})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnqueueOnce(context.Background(), queue, definition, Intent("legacy-key"), "value"); err != nil {
		t.Fatal(err)
	}
	if _, err := Enqueue(context.Background(), queue, definition, "value", Collapse("collapse-key")); err != nil {
		t.Fatal(err)
	}
	if _, err := Enqueue(context.Background(), queue, definition, "value", After(time.Second), Debounce("debounce-key", MaxDelay(time.Minute))); err != nil {
		t.Fatal(err)
	}
	for index, expected := range []struct {
		mode  PlacementMode
		value string
	}{{PlacementOnce, "legacy-key"}, {PlacementCollapse, "collapse-key"}, {PlacementDebounce, "debounce-key"}} {
		placement := captured[index]
		legacy, ok := placement.LegacyIntent()
		if !ok || legacy.Value() != expected.value || placement.Mode() != expected.mode || !placement.IntentDigests().validFor(placement.Namespace(), placement.Partition(), placement.Definition()) || len(placement.IntentDigests().ReservationKeys()) != 2 {
			t.Fatalf("legacy placement %d = %+v", index, placement)
		}
	}
}

func TestSenderFailureAndPanicAreOpaque(t *testing.T) {
	definition := testQueueDefinition(t, "tests.sender-failure", String(SchemaVersion(1)))
	secret := errors.New("postgres password=secret")
	queue := testQueue(t, queueMustName("tests"), definition, queueSenderFunc(func(context.Context, Placement) (PlacementResult, error) {
		return PlacementResult{}, secret
	}), bytes.NewReader(make([]byte, 16)))
	_, err := Enqueue(context.Background(), queue, definition, "value")
	if err != ErrAmbiguous || errors.Is(err, secret) || fmt.Sprint(err) != ErrAmbiguous.Error() {
		t.Fatalf("opaque error = %q", err)
	}
	panicQueue := testQueue(t, queueMustName("tests"), definition, queueSenderFunc(func(context.Context, Placement) (PlacementResult, error) {
		panic("secret panic")
	}), bytes.NewReader(make([]byte, 16)))
	if _, err := Enqueue(context.Background(), panicQueue, definition, "value"); err != ErrAmbiguous {
		t.Fatalf("panic error = %v", err)
	}
}

func TestQueueEntropyFailureIsOpaque(t *testing.T) {
	definition := testQueueDefinition(t, "tests.entropy-failure", String(SchemaVersion(1)))
	queue := testQueue(t, queueMustName("tests"), definition, queueSenderFunc(func(context.Context, Placement) (PlacementResult, error) {
		t.Fatal("sender called")
		return PlacementResult{}, nil
	}), errReader{err: errors.New("secret entropy path")})
	if _, err := Enqueue(context.Background(), queue, definition, "value"); err != ErrEntropy {
		t.Fatalf("error = %v", err)
	}
}

func TestQueueResolvesRequiredTenantAndRollingIntentAliases(t *testing.T) {
	definition := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "tests.tenant"), Codec: String(1), Policy: testPolicy(t), Partition: PartitionTenantRequired})
	catalog := MustCatalog(definition)
	if _, err := NewQueue(QueueSpec{Namespace: queueTestNamespace(t, "tests"), Catalog: catalog, Sender: successfulQueueSender()}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing partitioner error = %v", err)
	}
	plan, _ := NewIntentDigestPlan(DigestRevision2, DigestRevision1)
	var captured Placement
	sender := queueSenderFunc(func(_ context.Context, placement Placement) (PlacementResult, error) {
		captured = placement
		return mustPlacementResult(placement.Candidate(), PlacementCreated), nil
	})
	queue, err := NewQueue(QueueSpec{
		Namespace: queueTestNamespace(t, "tests"),
		Catalog:   catalog,
		Sender:    sender,
		Context: mustTenantContextProvider(t, TenantPartitionerFunc(func(context.Context) (ProducerPartition, error) {
			return Partition("private-tenant"), nil
		})),
		Digests: plan,
		Entropy: bytes.NewReader(make([]byte, 16)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnqueueOnce(context.Background(), queue, definition, Intent("request"), "payload"); err != nil {
		t.Fatal(err)
	}
	aliases := captured.IntentDigests().ReadCandidates()
	if captured.Partition().Global() || len(aliases) != 2 || aliases[0].Revision() != DigestRevision2 || aliases[1].Revision() != DigestRevision1 {
		t.Fatalf("tenant placement = %+v", captured)
	}
	if strings.Contains(fmt.Sprintf("%#v", captured), "private-tenant") {
		t.Fatal("placement exposed tenant partition")
	}
}

func TestTenantPartitionerIsSkippedForGlobalAndContainsFailures(t *testing.T) {
	global := testQueueDefinition(t, "tests.global", String(1))
	var calls atomic.Int32
	queue, err := NewQueue(QueueSpec{
		Namespace: queueTestNamespace(t, "tests"),
		Catalog:   MustCatalog(global),
		Sender:    successfulQueueSender(),
		Context: mustTenantContextProvider(t, TenantPartitionerFunc(func(context.Context) (ProducerPartition, error) {
			calls.Add(1)
			panic("private tenant lookup")
		})),
		Entropy: bytes.NewReader(make([]byte, 16)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Enqueue(context.Background(), queue, global, "value"); err != nil || calls.Load() != 0 {
		t.Fatalf("global enqueue = %v, calls=%d", err, calls.Load())
	}
	tenant := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "tests.tenant-failure"), Codec: String(1), Policy: testPolicy(t), Partition: PartitionTenantRequired})
	failing, err := NewQueue(QueueSpec{
		Namespace: queueTestNamespace(t, "tests"),
		Catalog:   MustCatalog(tenant),
		Sender:    successfulQueueSender(),
		Context: mustTenantContextProvider(t, TenantPartitionerFunc(func(context.Context) (ProducerPartition, error) {
			return ProducerPartition{}, errors.New("private tenant lookup")
		})),
		Entropy: bytes.NewReader(make([]byte, 16)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Enqueue(context.Background(), failing, tenant, "value"); err != ErrDriver || strings.Contains(fmt.Sprint(err), "private") {
		t.Fatalf("tenant failure = %v", err)
	}
}

func TestEnqueuePriorityCollapseDebounceAndSelfChainModes(t *testing.T) {
	definition := testQueueDefinition(t, "tests.collapse", String(1))
	placements := make([]Placement, 0, 4)
	var first InvocationID
	sender := queueSenderFunc(func(_ context.Context, placement Placement) (PlacementResult, error) {
		placements = append(placements, placement)
		if len(placements) == 1 || len(placements) == 3 {
			first = placement.Candidate()
			return mustPlacementResult(first, PlacementCreated), nil
		}
		return mustPlacementResult(first, PlacementCollapsed), nil
	})
	queue := testQueue(t, queueMustName("tests"), definition, sender, bytes.NewReader(make([]byte, 128)))
	for index := 0; index < 2; index++ {
		if _, err := Enqueue(context.Background(), queue, definition, "latest", Collapse("document:42"), After(time.Minute), AtPriority(50)); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 2; index++ {
		if _, err := Enqueue(context.Background(), queue, definition, "latest", Debounce("edits:42", MaxDelay(10*time.Minute)), After(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if placements[0].Mode() != PlacementCollapse || placements[0].Delay() != time.Minute || placements[0].MaxDelay() != 0 || placements[0].Priority() != 50 || placements[0].IntentDigest() != placements[1].IntentDigest() {
		t.Fatalf("collapse placement = %+v", placements[0])
	}
	if placements[2].Mode() != PlacementDebounce || placements[2].Delay() != time.Minute || placements[2].MaxDelay() != 10*time.Minute || placements[2].IntentDigest() != placements[3].IntentDigest() {
		t.Fatalf("debounce placement = %+v", placements[2])
	}
	if placements[0].IntentDigest() == placements[2].IntentDigest() {
		t.Fatal("different collapse keys shared a digest")
	}
	if _, err := Enqueue(context.Background(), queue, definition, "value", Debounce("key", MaxDelay(time.Minute))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("debounce without After error = %v", err)
	}
	if _, err := Enqueue(context.Background(), queue, definition, "value", Debounce("key", MaxDelay(time.Minute)), After(2*time.Minute)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unbounded debounce error = %v", err)
	}
	if _, _, err := EnqueueOnce(context.Background(), queue, definition, Intent("once"), "value", Collapse("key")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("once collapse error = %v", err)
	}
}

func TestEnqueueOnceUsesExplicitSemanticIdentityBeforeNondeterministicWire(t *testing.T) {
	codec := &changingStringCodec{}
	definition := MustDefine(DefinitionSpec[string]{Name: testJobName(t, "tests.semantic"), Codec: codec, Identity: stringPayloadIdentity{id: builtinCodecID("semantic-string"), version: 1}, Policy: testPolicy(t)})
	placements := make([]Placement, 0, 2)
	sender := queueSenderFunc(func(_ context.Context, placement Placement) (PlacementResult, error) {
		placements = append(placements, placement)
		outcome := PlacementCreated
		id := placement.Candidate()
		if len(placements) == 2 {
			outcome = PlacementExistingSamePayload
			id = placements[0].Candidate()
		}
		return mustPlacementResult(id, outcome), nil
	})
	queue := testQueue(t, queueMustName("tests"), definition, sender, bytes.NewReader(make([]byte, 64)))
	for index := 0; index < 2; index++ {
		if _, _, err := EnqueueOnce(context.Background(), queue, definition, Intent("same-request"), "same"); err != nil {
			t.Fatal(err)
		}
	}
	if placements[0].PayloadDigest() != placements[1].PayloadDigest() || placements[0].WireDigest() == placements[1].WireDigest() {
		t.Fatal("semantic identity followed nondeterministic wire bytes")
	}
	unsupported := testQueueDefinition(t, "tests.semantic-missing", &changingStringCodec{})
	unsupportedQueue := testQueue(t, queueMustName("tests"), unsupported, sender, bytes.NewReader(make([]byte, 16)))
	if _, _, err := EnqueueOnce(context.Background(), unsupportedQueue, unsupported, Intent("request"), "value"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported identity error = %v", err)
	}
}

type queueStager struct {
	transaction TransactionContext
	stage       func(context.Context, Placement) (Staged, error)
}

func (s queueStager) Transaction() TransactionContext { return s.transaction }
func (s queueStager) Stage(ctx context.Context, placement Placement) (Staged, error) {
	return s.stage(ctx, placement)
}

type countingQueueStager struct {
	transaction  TransactionContext
	transactions *atomic.Int32
	stages       *atomic.Int32
	place        func(Placement) (Staged, error)
}

func (s countingQueueStager) Transaction() TransactionContext {
	s.transactions.Add(1)
	return s.transaction
}

func (s countingQueueStager) Stage(_ context.Context, placement Placement) (Staged, error) {
	s.stages.Add(1)
	if s.place == nil {
		return Staged{}, nil
	}
	return s.place(placement)
}

type panickingQueueStager struct{}

func (panickingQueueStager) Transaction() TransactionContext { panic("private transaction") }
func (panickingQueueStager) Stage(context.Context, Placement) (Staged, error) {
	panic("stage must not run")
}

func TestEnqueueInUsesSourceBoundStagerWithoutClaimingCommit(t *testing.T) {
	definition := testQueueDefinition(t, "tests.staged", String(1))
	queue := testQueue(t, queueMustName("tests"), definition, successfulQueueSender(), bytes.NewReader(make([]byte, 128)))
	transaction := queueTestTransaction(queue.Backend())
	stager := queueStager{transaction: transaction, stage: func(_ context.Context, placement Placement) (Staged, error) {
		result := mustPlacementResult(placement.Candidate(), PlacementCreated)
		return mustStaged(transaction, result), nil
	}}
	staged, err := EnqueueIn(context.Background(), queue, stager, definition, "value")
	if err != nil || staged.IsZero() || staged.Outcome() != PlacementCreated || fmt.Sprint(staged) != "[staged job placement]" {
		t.Fatalf("staged = %+v, %v", staged, err)
	}
	stagedOnce, err := EnqueueOnceIn(context.Background(), queue, stager, definition, Intent("transaction-request"), "value")
	if err != nil || stagedOnce.Outcome() != PlacementCreated {
		t.Fatalf("staged once = %+v, %v", stagedOnce, err)
	}
	wrong := stager
	wrong.transaction = queueTestTransaction(queueTestBackendID(2))
	if _, err := EnqueueIn(context.Background(), queue, wrong, definition, "value"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong backend error = %v", err)
	}
	secret := errors.New("private transaction state")
	failing := queueStager{transaction: transaction, stage: func(context.Context, Placement) (Staged, error) {
		return Staged{}, secret
	}}
	if _, err := EnqueueIn(context.Background(), queue, failing, definition, "value"); err != ErrAmbiguous || errors.Is(err, secret) {
		t.Fatalf("stage failure = %v", err)
	}
	panicking := queueStager{transaction: transaction, stage: func(context.Context, Placement) (Staged, error) {
		panic("private transaction state")
	}}
	if _, err := EnqueueIn(context.Background(), queue, panicking, definition, "value"); err != ErrAmbiguous {
		t.Fatalf("stage panic = %v", err)
	}
	contradictory := queueStager{transaction: transaction, stage: func(_ context.Context, placement Placement) (Staged, error) {
		return mustStaged(transaction, mustPlacementResult(placement.Candidate(), PlacementCreated)), RejectPlacement(ErrSaturated)
	}}
	if _, err := EnqueueIn(context.Background(), queue, contradictory, definition, "value"); err != ErrAmbiguous {
		t.Fatalf("contradictory stage = %v", err)
	}
	var otherBinding [32]byte
	otherBinding[0] = 9
	binding, _ := TransactionBindingFromBytes(otherBinding)
	otherTransaction, _ := NewTransactionContext(queue.Backend(), binding, queueTestDurability())
	mismatched := queueStager{transaction: transaction, stage: func(_ context.Context, placement Placement) (Staged, error) {
		return mustStaged(otherTransaction, mustPlacementResult(placement.Candidate(), PlacementCreated)), nil
	}}
	if _, err := EnqueueIn(context.Background(), queue, mismatched, definition, "value"); err != ErrAmbiguous {
		t.Fatalf("mismatched transaction result = %v", err)
	}
	possible, _ := NewDurabilityProfile(AckBeforePersistence, AcknowledgedLossPossible, FailureSet{})
	otherDurability, _ := NewTransactionContext(queue.Backend(), transaction.Binding(), possible)
	mismatchedDurability := queueStager{transaction: transaction, stage: func(_ context.Context, placement Placement) (Staged, error) {
		return mustStaged(otherDurability, mustPlacementResult(placement.Candidate(), PlacementCreated)), nil
	}}
	if _, err := EnqueueIn(context.Background(), queue, mismatchedDurability, definition, "value"); err != ErrAmbiguous {
		t.Fatalf("mismatched transaction durability = %v", err)
	}
}

func TestEnqueueInRejectsInvalidTransactionBeforeStage(t *testing.T) {
	definition := testQueueDefinition(t, "tests.stager-validation", String(1))
	queue := testQueue(t, queueMustName("tests"), definition, successfulQueueSender(), bytes.NewReader(make([]byte, 64)))
	var typedNil *queueStager
	for name, stager := range map[string]Stager{
		"zero":      queueStager{},
		"panic":     panickingQueueStager{},
		"typed nil": typedNil,
	} {
		if _, err := EnqueueIn(context.Background(), queue, stager, definition, "value"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s transaction = %v", name, err)
		}
	}
}

func TestEnqueueInClassifiesMalformedStageResultsConservatively(t *testing.T) {
	definition := testQueueDefinition(t, "tests.stage-results", String(1))
	queue := testQueue(t, queueMustName("tests"), definition, successfulQueueSender(), bytes.NewReader(make([]byte, 128)))
	transaction := queueTestTransaction(queue.Backend())
	other := queueTestTransaction(queueTestBackendID(2))
	cases := []struct {
		name  string
		stage func(context.Context, Placement) (Staged, error)
		want  error
	}{
		{"zero success", func(context.Context, Placement) (Staged, error) { return Staged{}, nil }, ErrAmbiguous},
		{"sealed rejection", func(context.Context, Placement) (Staged, error) { return Staged{}, RejectPlacement(ErrSaturated) }, ErrSaturated},
		{"wrong backend", func(_ context.Context, placement Placement) (Staged, error) {
			return mustStaged(other, mustPlacementResult(placement.Candidate(), PlacementCreated)), nil
		}, ErrAmbiguous},
	}
	for _, test := range cases {
		stager := queueStager{transaction: transaction, stage: test.stage}
		if _, err := EnqueueIn(context.Background(), queue, stager, definition, "value"); err != test.want {
			t.Fatalf("%s = %v", test.name, err)
		}
	}
}

func TestPostPlacementFailuresAreConservativelyAmbiguousAndRedacted(t *testing.T) {
	definition := testQueueDefinition(t, "tests.ambiguity", String(1))
	secret := errors.New("private database address")
	cases := []struct {
		name   string
		sender Sender
		want   error
	}{
		{"rejected", queueSenderFunc(func(context.Context, Placement) (PlacementResult, error) {
			return PlacementResult{}, RejectPlacement(ErrSaturated)
		}), ErrSaturated},
		{"unknown", queueSenderFunc(func(context.Context, Placement) (PlacementResult, error) { return PlacementResult{}, secret }), ErrAmbiguous},
		{"cancelled", queueSenderFunc(func(context.Context, Placement) (PlacementResult, error) { return PlacementResult{}, context.Canceled }), ErrAmbiguous},
		{"joined ambiguity", queueSenderFunc(func(context.Context, Placement) (PlacementResult, error) {
			return PlacementResult{}, errors.Join(RejectPlacement(ErrSaturated), ErrAmbiguous)
		}), ErrAmbiguous},
		{"joined before sealing", queueSenderFunc(func(context.Context, Placement) (PlacementResult, error) {
			return PlacementResult{}, RejectPlacement(errors.Join(ErrSaturated, ErrAmbiguous))
		}), ErrAmbiguous},
		{"wrapped before sealing", queueSenderFunc(func(context.Context, Placement) (PlacementResult, error) {
			return PlacementResult{}, RejectPlacement(fmt.Errorf("wrapped: %w", ErrSaturated))
		}), ErrAmbiguous},
		{"result and rejection", queueSenderFunc(func(_ context.Context, placement Placement) (PlacementResult, error) {
			return mustPlacementResult(placement.Candidate(), PlacementCreated), RejectPlacement(ErrSaturated)
		}), ErrAmbiguous},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			queue := testQueue(t, queueMustName("tests"), definition, test.sender, bytes.NewReader(make([]byte, 16)))
			_, err := Enqueue(context.Background(), queue, definition, "value")
			if err != test.want || errors.Is(err, secret) || strings.Contains(fmt.Sprintf("%+v", err), "private") {
				t.Fatalf("error = %+v", err)
			}
		})
	}
}

type countingStringCodec struct{ encodes *atomic.Int32 }

func (*countingStringCodec) ID() CodecID            { return queueMustCodecID("counting") }
func (*countingStringCodec) Version() SchemaVersion { return SchemaVersion(1) }
func (c *countingStringCodec) Encode(value string, _ PayloadLimit) ([]byte, error) {
	c.encodes.Add(1)
	return []byte(value), nil
}
func (*countingStringCodec) Decode(value []byte, _ PayloadLimit) (string, error) {
	return string(value), nil
}

type countingEntropyReader struct{ reads *atomic.Int32 }

func (r *countingEntropyReader) Read(destination []byte) (int, error) {
	r.reads.Add(1)
	for index := range destination {
		destination[index] = byte(index + 1)
	}
	return len(destination), nil
}

type exclusiveEntropyReader struct {
	active     atomic.Int32
	concurrent atomic.Bool
	counter    atomic.Uint32
}

func (r *exclusiveEntropyReader) Read(destination []byte) (int, error) {
	if r.active.Add(1) != 1 {
		r.concurrent.Store(true)
	}
	defer r.active.Add(-1)
	value := byte(r.counter.Add(1))
	for index := range destination {
		destination[index] = value
	}
	time.Sleep(time.Microsecond)
	return len(destination), nil
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func testQueue[P any](t *testing.T, namespace Name, definition DefinitionOf[P], sender Sender, entropy io.Reader) *Queue {
	t.Helper()
	catalog, err := NewCatalog(definition)
	if err != nil {
		t.Fatal(err)
	}
	queue, err := NewQueue(QueueSpec{Namespace: queueTestNamespace(t, namespace.Value()), Catalog: catalog, Sender: sender, Entropy: entropy})
	if err != nil {
		t.Fatal(err)
	}
	return queue
}

func queueTestNamespace(t *testing.T, application string) Namespace {
	t.Helper()
	namespace, err := NamespaceOf(application, "test")
	if err != nil {
		t.Fatal(err)
	}
	return namespace
}

func queueTestBackendID(value byte) BackendID {
	var raw [BackendIDBytes]byte
	raw[0] = value
	id, err := BackendIDFromBytes(raw)
	if err != nil {
		panic(err)
	}
	return id
}

func queueTestBackendDescription(value byte) BackendDescription {
	description, err := NewBackendDescription(queueTestBackendID(value), queueTestDurability(), Capabilities{Priority: true, Debounce: true, Scheduled: true})
	if err != nil {
		panic(err)
	}
	return description
}

func queueTestTransaction(backend BackendID) TransactionContext {
	var raw [32]byte
	backendBytes := backend.Bytes()
	raw[0] = backendBytes[0]
	raw[1] = 1
	binding, err := TransactionBindingFromBytes(raw)
	if err != nil {
		panic(err)
	}
	transaction, err := NewTransactionContext(backend, binding, queueTestDurability())
	if err != nil {
		panic(err)
	}
	return transaction
}

func queueTestDurability() DurabilityProfile {
	failures, err := Failures(FailureProcessCrash)
	if err != nil {
		panic(err)
	}
	profile, err := NewDurabilityProfile(AckLocalPersistence, AcknowledgedLossExcludedForDeclaredFailures, failures)
	if err != nil {
		panic(err)
	}
	return profile
}

func mustPlacementResult(id InvocationID, outcome PlacementOutcome) PlacementResult {
	result, err := NewPlacementResult(id, outcome)
	if err != nil {
		panic(err)
	}
	return result
}

func mustStaged(transaction TransactionContext, result PlacementResult) Staged {
	staged, err := NewStaged(transaction, result)
	if err != nil {
		panic(err)
	}
	return staged
}
