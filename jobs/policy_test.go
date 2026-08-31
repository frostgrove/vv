package jobs

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestProfilesExposeExactBoundedDefaults(t *testing.T) {
	tests := []struct {
		profile           Profile
		name              string
		queue             string
		priority          int
		concurrency       int
		attempt           time.Duration
		elapsed           time.Duration
		retries           int
		handlerDeferrals  int
		deliveryDeferrals int
		initial           time.Duration
		maximum           time.Duration
		retention         time.Duration
		intentRetention   time.Duration
	}{
		{Interactive, "Interactive", "interactive", 50, 4, 2 * time.Minute, 2 * time.Hour, 3, 64, 64, time.Second, time.Minute, 24 * time.Hour, 7 * 24 * time.Hour},
		{Default, "Default", "default", 100, 2, 10 * time.Minute, 24 * time.Hour, 5, 256, 256, 5 * time.Second, 5 * time.Minute, 7 * 24 * time.Hour, 30 * 24 * time.Hour},
		{Heavy, "Heavy", "heavy", 100, 1, 30 * time.Minute, 72 * time.Hour, 5, 256, 256, 15 * time.Second, 15 * time.Minute, 14 * 24 * time.Hour, 30 * 24 * time.Hour},
		{Batch, "Batch", "batch", 200, 1, 2 * time.Hour, 7 * 24 * time.Hour, 5, 512, 512, 30 * time.Second, 30 * time.Minute, 30 * 24 * time.Hour, 90 * 24 * time.Hour},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, err := test.profile.Build()
			if err != nil {
				t.Fatal(err)
			}
			if test.profile.Name() != test.name || test.profile.workerConcurrency != test.concurrency || policy.Queue.String() != test.queue || policy.Priority != test.priority || policy.AttemptTimeout != test.attempt || policy.ProgressTimeout != 0 || policy.MaxElapsed != test.elapsed || policy.MaxRetries != test.retries || policy.MaxHandlerDeferrals != test.handlerDeferrals || policy.MaxDeliveryDeferrals != test.deliveryDeferrals || policy.Backoff != (BackoffPolicy{Initial: test.initial, Maximum: test.maximum, Jitter: FullJitter}) || policy.Retention != test.retention || policy.IntentRetention != test.intentRetention || policy.Payload != DefaultPayloadLimit() {
				t.Fatalf("unexpected profile: %#v", policy)
			}
		})
	}
}

func TestProfileOptionsAreOrderIndependentAndDescribed(t *testing.T) {
	queue, err := ParseQueueName("documents")
	if err != nil {
		t.Fatal(err)
	}
	first := Default.With(
		OnQueue(queue),
		RetryBackoff(Exponential(time.Minute, 30*time.Minute, FullJitter)),
		MaxElapsed(48*time.Hour),
		AttemptTimeout(time.Hour),
		ProgressTimeout(30*time.Minute),
		Retries(0),
		MaxHandlerDeferrals(0),
		MaxDeliveryDeferrals(1),
		RetainFor(10*24*time.Hour),
		RetainIntentsFor(40*24*time.Hour),
		MaxBytes(128<<10),
		DecodedBytes(512<<10),
		MaxDepth(32),
	)
	second := Default.With(
		MaxDepth(32),
		DecodedBytes(512<<10),
		MaxBytes(128<<10),
		RetainIntentsFor(40*24*time.Hour),
		RetainFor(10*24*time.Hour),
		MaxDeliveryDeferrals(1),
		MaxHandlerDeferrals(0),
		Retries(0),
		ProgressTimeout(30*time.Minute),
		AttemptTimeout(time.Hour),
		MaxElapsed(48*time.Hour),
		RetryBackoff(Exponential(time.Minute, 30*time.Minute, FullJitter)),
		OnQueue(queue),
	)
	left, err := first.Build()
	if err != nil {
		t.Fatal(err)
	}
	right, err := second.Build()
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("option order changed policy:\n%#v\n%#v", left, right)
	}
	description := describePolicy(left)
	want := []string{"attempt_timeout", "backoff", "intent_retention", "max_decoded_bytes", "max_delivery_deferrals", "max_elapsed", "max_handler_deferrals", "max_payload_bytes", "max_payload_depth", "max_retries", "progress_timeout", "queue", "retention"}
	if !reflect.DeepEqual(description.Overrides, want) || description.ProgressTimeout != 30*time.Minute || description.MaxRetries != 0 || description.MaxHandlerDeferrals != 0 || description.MaxDeliveryDeferrals != 1 || description.Profile != "Default" {
		t.Fatalf("unexpected description: %#v", description)
	}
}

func TestProgressTimeoutZeroIsAnExplicitDisabledOverride(t *testing.T) {
	policy, err := Default.With(ProgressTimeout(time.Minute), ProgressTimeout(0)).Build()
	if err != nil {
		t.Fatal(err)
	}
	description := describePolicy(policy)
	if policy.ProgressTimeout != 0 || description.ProgressTimeout != 0 || !reflect.DeepEqual(description.Overrides, []string{"progress_timeout"}) || description.Profile != "Default" {
		t.Fatalf("disabled progress timeout = %#v", description)
	}
	snapshot, err := NewPolicySnapshot(policy)
	if err != nil || snapshot.ProgressTimeout() != 0 {
		t.Fatalf("disabled progress snapshot = (%s, %v)", snapshot.ProgressTimeout(), err)
	}
}

func TestDurabilityPolicyIsTypedDescribedAndResettable(t *testing.T) {
	policy, err := Default.With(
		AcceptAckModes(AckLocalPersistence, AckRemotePersistence),
		ProtectAcknowledgedEnqueuesFrom(FailureProcessCrash, FailureHostLoss),
	).Build()
	if err != nil {
		t.Fatal(err)
	}
	description := describePolicy(policy)
	if !reflect.DeepEqual(description.Overrides, []string{"accepted_ack_modes", "protected_enqueue_failures"}) || description.Durability != policy.Durability {
		t.Fatalf("durability description = %#v", description)
	}
	snapshot, err := NewPolicySnapshot(policy)
	if err != nil || snapshot.Durability() != policy.Durability {
		t.Fatalf("durability snapshot = (%+v, %v)", snapshot, err)
	}
	reset, err := Default.With(
		AcceptAckModes(AckLocalPersistence),
		ProtectAcknowledgedEnqueuesFrom(FailureProcessCrash),
		AcceptAnyAckMode(),
		AllowAcknowledgedLoss(),
	).Build()
	if err != nil || !reset.Durability.IsZero() || !reflect.DeepEqual(describePolicy(reset).Overrides, []string{"accepted_ack_modes", "protected_enqueue_failures"}) {
		t.Fatalf("reset durability = (%+v, %v)", reset.Durability, err)
	}
}

func TestPolicyRejectsEveryFiniteBoundViolation(t *testing.T) {
	valid, err := Default.Build()
	if err != nil {
		t.Fatal(err)
	}
	tests := []func(*Policy){
		func(value *Policy) { value.Queue = QueueName{} },
		func(value *Policy) { value.Priority = MaximumPriority + 1 },
		func(value *Policy) { value.AttemptTimeout = 0 },
		func(value *Policy) { value.AttemptTimeout = value.MaxElapsed + 1 },
		func(value *Policy) { value.ProgressTimeout = -1 },
		func(value *Policy) { value.ProgressTimeout = value.AttemptTimeout + 1 },
		func(value *Policy) { value.MaxElapsed = 0 },
		func(value *Policy) { value.MaxRetries = MaximumRetries + 1 },
		func(value *Policy) { value.MaxHandlerDeferrals = MaximumHandlerDeferrals + 1 },
		func(value *Policy) { value.MaxDeliveryDeferrals = MaximumDeliveryDeferrals + 1 },
		func(value *Policy) { value.Backoff.Initial = MinRetryDelay - 1 },
		func(value *Policy) { value.Backoff.Maximum = MaxRetryDelay + 1 },
		func(value *Policy) { value.Retention = MaxRetention + 1 },
		func(value *Policy) { value.IntentRetention = value.Retention - 1 },
		func(value *Policy) { value.Payload.MaxBytes = MaxPayloadBytes + 1 },
		func(value *Policy) { value.Payload.MaxDecodedBytes = MaxDecodedBytes + 1 },
		func(value *Policy) { value.Payload.MaxDepth = MaxPayloadDepth + 1 },
	}
	for index, mutate := range tests {
		value := valid
		mutate(&value)
		if err := validatePolicy(value); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case %d: expected ErrInvalid, got %v", index, err)
		}
	}
}

func TestProfileRejectsInvalidOptionsWithoutMutatingBase(t *testing.T) {
	invalidProfiles := []Profile{
		Default.With(Retries(MaximumRetries + 1)),
		Default.With(MaxHandlerDeferrals(-1)),
		Default.With(MaxDeliveryDeferrals(MaximumDeliveryDeferrals + 1)),
		Default.With(AttemptTimeout(MaximumAttemptTimeout + 1)),
		Default.With(ProgressTimeout(-1)),
		Default.With(ProgressTimeout(DefaultAttemptTimeout + 1)),
		Default.With(ProgressTimeout(MaximumAttemptTimeout + 1)),
		Default.With(MaxElapsed(MaximumMaxElapsed + 1)),
		Default.With(RetryBackoff(Exponential(MinRetryDelay-1, time.Second, FullJitter))),
		Default.With(RetainFor(MaxRetention + 1)),
		Default.With(MaxBytes(MaxPayloadBytes + 1)),
		Default.With(AcceptAckModes()),
		Default.With(AcceptAckModes(AckLocalPersistence, AckLocalPersistence)),
		Default.With(ProtectAcknowledgedEnqueuesFrom()),
		Default.With(ProtectAcknowledgedEnqueuesFrom(Failure(255))),
	}
	invalidWorkerConcurrency := Default
	invalidWorkerConcurrency.workerConcurrency = 0
	invalidProfiles = append(invalidProfiles, invalidWorkerConcurrency)
	for index, profile := range invalidProfiles {
		if _, err := profile.Build(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case %d: expected ErrInvalid, got %v", index, err)
		}
	}
	base, err := Default.Build()
	if err != nil || base.MaxRetries != DefaultRetries || base.Payload != DefaultPayloadLimit() {
		t.Fatalf("base profile changed: %#v, %v", base, err)
	}
}

func TestDescriptorDetectsDirectMutationsOfBuiltPolicy(t *testing.T) {
	policy, err := Default.Build()
	if err != nil {
		t.Fatal(err)
	}
	policy.MaxElapsed = 48 * time.Hour
	policy.ProgressTimeout = time.Minute
	policy.MaxDeliveryDeferrals = 1
	policy.Payload.MaxBytes = 128 << 10
	description := describePolicy(policy)
	want := []string{"max_delivery_deferrals", "max_elapsed", "max_payload_bytes", "progress_timeout"}
	if !reflect.DeepEqual(description.Overrides, want) || description.Profile != "Default" {
		t.Fatalf("direct mutations were hidden: %#v", description)
	}
}
