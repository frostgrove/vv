package jobs

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type workersConfigDriver struct {
	description      BackendDescription
	descriptionCalls atomic.Int32
	operationCalls   atomic.Int32
	panicDescription bool
}

func (driver *workersConfigDriver) Description() BackendDescription {
	driver.descriptionCalls.Add(1)
	if driver.panicDescription {
		panic("private driver description panic")
	}
	return driver.description
}

func (driver *workersConfigDriver) Claim(context.Context, ClaimRequest) (ClaimBatch, error) {
	driver.operationCalls.Add(1)
	panic("claim must not run during construction")
}

func (driver *workersConfigDriver) Renew(context.Context, RenewRequest) (RenewResult, error) {
	driver.operationCalls.Add(1)
	panic("renew must not run during construction")
}

func (driver *workersConfigDriver) Apply(context.Context, ApplyRequest) (ApplyResult, error) {
	driver.operationCalls.Add(1)
	panic("apply must not run during construction")
}

func (driver *workersConfigDriver) Recover(context.Context, RecoverRequest) (RecoverResult, error) {
	driver.operationCalls.Add(1)
	panic("recover must not run during construction")
}

type workersConfigIdentity struct{ calls *atomic.Int32 }

func (identity *workersConfigIdentity) RestoreIdentity(context.Context, IdentityRestoreRequest) (RestoredIdentity, error) {
	identity.calls.Add(1)
	return RestoredIdentity{}, errors.New("identity must not run during construction")
}

type workersConfigClock struct{ calls atomic.Int32 }

func (clock *workersConfigClock) Now() time.Time {
	clock.calls.Add(1)
	panic("clock must not run during construction")
}

func (clock *workersConfigClock) NewTimer(time.Duration) Timer {
	clock.calls.Add(1)
	panic("timer must not run during construction")
}

func TestNewWorkersResolvesSafeDefaultsWithoutLifecycleEffects(t *testing.T) {
	spec, consumer, driver, identityCalls := workersConfigFixture(t, "workers.defaults")
	var entropyReads atomic.Int32
	clock := &workersConfigClock{}
	spec.Clock = clock
	spec.Entropy = &countingEntropyReader{reads: &entropyReads}
	workers, err := NewWorkers(spec, consumer)
	if err != nil {
		t.Fatal(err)
	}
	description := workers.Describe()
	if description.Namespace != spec.Namespace || description.Backend != driver.description || description.Build != spec.Build {
		t.Fatalf("workers identity snapshot = %#v", description)
	}
	if description.Plan.CatalogFingerprint != spec.Catalog.Fingerprint() || description.Plan.TotalConcurrency != 1 || len(description.Plan.Bindings) != 1 {
		t.Fatalf("workers plan snapshot = %#v", description.Plan)
	}
	if description.LeaseTTL != DefaultLeaseTTL || description.Heartbeat != DefaultHeartbeat || description.OperationTimeout != DefaultOperationTimeout || description.PollInterval != DefaultPollInterval || description.ReclaimInterval != DefaultReclaimInterval || description.ShutdownGrace != DefaultShutdownGrace {
		t.Fatalf("workers duration defaults = %#v", description)
	}
	if description.ClaimItems != DefaultClaimItems || description.ClaimBytes != DefaultClaimBytes || description.InFlightBytes != DefaultWorkerInFlightBytes || description.PulseWaiters != DefaultTransientWaiters {
		t.Fatalf("workers budget defaults = %#v", description)
	}
	if driver.descriptionCalls.Load() != 1 || driver.operationCalls.Load() != 0 || identityCalls.Load() != 0 || clock.calls.Load() != 0 || entropyReads.Load() != 0 {
		t.Fatalf("construction effects: description=%d operations=%d identity=%d clock=%d entropy=%d", driver.descriptionCalls.Load(), driver.operationCalls.Load(), identityCalls.Load(), clock.calls.Load(), entropyReads.Load())
	}
	driver.description = queueTestBackendDescription(2)
	if workers.Describe().Backend != description.Backend || driver.descriptionCalls.Load() != 1 {
		t.Fatal("workers did not retain the validated driver description")
	}
}

func TestNewWorkersResolvesExplicitAndAdaptiveConfiguration(t *testing.T) {
	spec, consumer, _, _ := workersConfigFixture(t, "workers.explicit")
	var identityCalls atomic.Int32
	identity := &workersConfigIdentity{calls: &identityCalls}
	clock := &workersConfigClock{}
	var entropyReads atomic.Int32
	entropy := &countingEntropyReader{reads: &entropyReads}
	spec.Identity = identity
	spec.Clock = clock
	spec.Entropy = entropy
	spec.LeaseTTL = 4 * time.Second
	spec.Heartbeat = 2 * time.Second
	spec.OperationTimeout = 500 * time.Millisecond
	spec.PollInterval = 2 * time.Second
	spec.ReclaimInterval = 5 * time.Second
	spec.ShutdownGrace = 4 * time.Second
	spec.ClaimItems = MaxClaimItems
	spec.ClaimBytes = MaxDeliveryRecordBytes + 1024
	spec.InFlightBytes = spec.ClaimBytes + MaxDeliveryRecordBytes + MaxDecodedBytes
	spec.PulseWaiters = MaxTransientWaiters
	workers, err := NewWorkers(spec, consumer)
	if err != nil {
		t.Fatal(err)
	}
	description := workers.Describe()
	if description.LeaseTTL != spec.LeaseTTL || description.Heartbeat != spec.Heartbeat || description.OperationTimeout != spec.OperationTimeout || description.PollInterval != spec.PollInterval || description.ReclaimInterval != spec.ReclaimInterval || description.ShutdownGrace != spec.ShutdownGrace {
		t.Fatalf("explicit durations = %#v", description)
	}
	if description.ClaimItems != spec.ClaimItems || description.ClaimBytes != spec.ClaimBytes || description.InFlightBytes != spec.InFlightBytes || description.PulseWaiters != spec.PulseWaiters {
		t.Fatalf("explicit budgets = %#v", description)
	}
	if workers.config.clock.source != clock || workers.config.entropy.reader != entropy || workers.config.identity != identity || identityCalls.Load() != 0 || clock.calls.Load() != 0 || entropyReads.Load() != 0 {
		t.Fatal("explicit runtime dependencies were changed or invoked")
	}

	adaptiveSpec, adaptiveConsumer, _, _ := workersConfigFixture(t, "workers.adaptive")
	adaptiveSpec.LeaseTTL = 4 * time.Second
	var nilClock *workersConfigClock
	var nilEntropy *countingEntropyReader
	adaptiveSpec.Clock = nilClock
	adaptiveSpec.Entropy = nilEntropy
	adaptive, err := NewWorkers(adaptiveSpec, adaptiveConsumer)
	if err != nil {
		t.Fatal(err)
	}
	if adaptive.Describe().Heartbeat != time.Second || adaptive.Describe().OperationTimeout != time.Second || adaptive.Describe().ReclaimInterval != 2*time.Second {
		t.Fatalf("adaptive durations = %#v", adaptive.Describe())
	}
	if _, ok := adaptive.config.clock.source.(systemClock); !ok || adaptive.config.entropy.reader != rand.Reader {
		t.Fatal("nil-like runtime dependencies did not resolve to safe defaults")
	}
	adaptiveSpec.LeaseTTL = MinimumLeaseTTL
	minimum, err := NewWorkers(adaptiveSpec, adaptiveConsumer)
	if err != nil {
		t.Fatal(err)
	}
	if minimum.Describe().Heartbeat != MinimumLeaseTTL/4 || minimum.Describe().OperationTimeout != MinimumLeaseTTL/4 || minimum.Describe().ReclaimInterval != MinimumLeaseTTL/2 {
		t.Fatalf("minimum lease durations = %#v", minimum.Describe())
	}
}

func TestNewWorkersOperationTimeoutAndReclaimBoundaries(t *testing.T) {
	base, consumer, _, _ := workersConfigFixture(t, "workers.duration-boundaries")
	tests := []struct {
		name             string
		leaseTTL         time.Duration
		heartbeat        time.Duration
		operationTimeout time.Duration
		reclaimInterval  time.Duration
		wantOperation    time.Duration
		wantReclaim      time.Duration
	}{
		{name: "minimum operation", operationTimeout: MinimumOperationTimeout, wantOperation: MinimumOperationTimeout, wantReclaim: DefaultReclaimInterval},
		{name: "exact safety", operationTimeout: (DefaultLeaseTTL - DefaultHeartbeat) / 3, wantOperation: (DefaultLeaseTTL - DefaultHeartbeat) / 3, wantReclaim: DefaultReclaimInterval},
		{name: "maximum operation", leaseTTL: MaximumLeaseTTL, operationTimeout: MaximumOperationTimeout, wantOperation: MaximumOperationTimeout, wantReclaim: DefaultReclaimInterval},
		{name: "fractional adaptive safety", leaseTTL: 4 * time.Second, heartbeat: 2 * time.Second, wantOperation: 2 * time.Second / 3, wantReclaim: 2 * time.Second},
		{name: "minimum reclaim", reclaimInterval: MinimumReclaimInterval, wantOperation: DefaultOperationTimeout, wantReclaim: MinimumReclaimInterval},
		{name: "maximum reclaim beyond lease", leaseTTL: time.Second, reclaimInterval: MaximumReclaimInterval, wantOperation: time.Second / 4, wantReclaim: MaximumReclaimInterval},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := base
			spec.LeaseTTL = test.leaseTTL
			spec.Heartbeat = test.heartbeat
			spec.OperationTimeout = test.operationTimeout
			spec.ReclaimInterval = test.reclaimInterval
			workers, err := NewWorkers(spec, consumer)
			if err != nil {
				t.Fatal(err)
			}
			description := workers.Describe()
			if description.OperationTimeout != test.wantOperation || description.ReclaimInterval != test.wantReclaim {
				t.Fatalf("durations = operation %s reclaim %s, want %s and %s", description.OperationTimeout, description.ReclaimInterval, test.wantOperation, test.wantReclaim)
			}
		})
	}
}

func TestNewWorkersRejectsUnsupportedDurabilityBeforeRuntimeEffects(t *testing.T) {
	policy, err := Default.With(ProtectAcknowledgedEnqueuesFrom(FailureProcessCrash)).Build()
	if err != nil {
		t.Fatal(err)
	}
	definition := MustDefine(DefinitionSpec[string]{Name: queueMustName("workers.durability"), Codec: String(1), Policy: policy})
	spec, _, driver, identityCalls := workersConfigFixture(t, "workers.durability-fixture")
	spec.Catalog = MustCatalog(definition)
	clock := &workersConfigClock{}
	var entropyReads atomic.Int32
	spec.Clock = clock
	spec.Entropy = &countingEntropyReader{reads: &entropyReads}
	possible, err := NewDurabilityProfile(AckBeforePersistence, AcknowledgedLossPossible, FailureSet{})
	if err != nil {
		t.Fatal(err)
	}
	driver.description, err = NewBackendDescription(queueTestBackendID(9), possible, Capabilities{})
	if err != nil {
		t.Fatal(err)
	}
	consumer := On(definition, Handler[string](func(context.Context, string) error {
		panic("handler must not run during construction")
	}), Concurrency(1))
	workers, err := NewWorkers(spec, consumer)
	if workers != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("weak durability = (%v, %v)", workers, err)
	}
	if driver.descriptionCalls.Load() != 1 || driver.operationCalls.Load() != 0 || identityCalls.Load() != 0 || clock.calls.Load() != 0 || entropyReads.Load() != 0 {
		t.Fatalf("durability rejection effects: description=%d operations=%d identity=%d clock=%d entropy=%d", driver.descriptionCalls.Load(), driver.operationCalls.Load(), identityCalls.Load(), clock.calls.Load(), entropyReads.Load())
	}
	driver.description = queueTestBackendDescription(9)
	workers, err = NewWorkers(spec, consumer)
	if err != nil || workers == nil || workers.Describe().Backend != driver.description {
		t.Fatalf("strong durability = (%v, %v)", workers, err)
	}
	weakDefinition := testQueueDefinition(t, "workers.durability-unrelated", String(1))
	spec.Catalog = MustCatalog(definition, weakDefinition)
	weakConsumer := On(weakDefinition, Handler[string](func(context.Context, string) error {
		panic("handler must not run during construction")
	}), Concurrency(1))
	driver.description, err = NewBackendDescription(queueTestBackendID(9), possible, Capabilities{})
	if err != nil {
		t.Fatal(err)
	}
	workers, err = NewWorkers(spec, weakConsumer)
	if err != nil || workers == nil || workers.Describe().Plan.TotalConcurrency != 1 {
		t.Fatalf("unrelated durability = (%v, %v)", workers, err)
	}
}

func TestNewWorkersValidatesConfigurationAndDriverDescription(t *testing.T) {
	base, consumer, _, _ := workersConfigFixture(t, "workers.invalid")
	other := testQueueDefinition(t, "workers.other-catalog", String(1))
	var nilIdentity *workersConfigIdentity
	tests := []struct {
		name   string
		want   error
		change func(*WorkersSpec)
	}{
		{name: "namespace", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.Namespace = Namespace{} }},
		{name: "catalog", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.Catalog = Catalog{} }},
		{name: "catalog member", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.Catalog = MustCatalog(other) }},
		{name: "driver", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.Driver = nil }},
		{name: "build", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.Build = BuildID{} }},
		{name: "identity", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.Identity = nil }},
		{name: "typed nil identity", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.Identity = nilIdentity }},
		{name: "negative lease", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.LeaseTTL = -1 }},
		{name: "short lease", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.LeaseTTL = MinimumLeaseTTL - 1 }},
		{name: "large lease", want: ErrTooLarge, change: func(spec *WorkersSpec) { spec.LeaseTTL = MaximumLeaseTTL + 1 }},
		{name: "negative heartbeat", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.Heartbeat = -1 }},
		{name: "fragile heartbeat", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.Heartbeat = DefaultLeaseTTL/2 + 1 }},
		{name: "negative operation timeout", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.OperationTimeout = -1 }},
		{name: "short operation timeout", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.OperationTimeout = MinimumOperationTimeout - 1 }},
		{name: "large operation timeout", want: ErrTooLarge, change: func(spec *WorkersSpec) { spec.OperationTimeout = MaximumOperationTimeout + 1 }},
		{name: "unsafe operation timeout", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.OperationTimeout = (DefaultLeaseTTL-DefaultHeartbeat)/3 + 1 }},
		{name: "negative poll", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.PollInterval = -1 }},
		{name: "short poll", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.PollInterval = MinimumPollInterval - 1 }},
		{name: "large poll", want: ErrTooLarge, change: func(spec *WorkersSpec) { spec.PollInterval = MaximumPollInterval + 1 }},
		{name: "negative reclaim", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.ReclaimInterval = -1 }},
		{name: "short reclaim", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.ReclaimInterval = MinimumReclaimInterval - 1 }},
		{name: "large reclaim", want: ErrTooLarge, change: func(spec *WorkersSpec) { spec.ReclaimInterval = MaximumReclaimInterval + 1 }},
		{name: "negative shutdown", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.ShutdownGrace = -1 }},
		{name: "large shutdown", want: ErrTooLarge, change: func(spec *WorkersSpec) { spec.ShutdownGrace = MaxShutdownGrace + 1 }},
		{name: "negative claim items", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.ClaimItems = -1 }},
		{name: "large claim items", want: ErrTooLarge, change: func(spec *WorkersSpec) { spec.ClaimItems = MaxClaimItems + 1 }},
		{name: "negative claim bytes", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.ClaimBytes = -1 }},
		{name: "small claim bytes", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.ClaimBytes = MaxDeliveryRecordBytes - 1 }},
		{name: "large claim bytes", want: ErrTooLarge, change: func(spec *WorkersSpec) { spec.ClaimBytes = MaxClaimBytes + 1 }},
		{name: "negative in-flight bytes", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.InFlightBytes = -1 }},
		{name: "small in-flight bytes", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.InFlightBytes = 2*MaxDeliveryRecordBytes + MaxDecodedBytes - 1 }},
		{name: "large in-flight bytes", want: ErrTooLarge, change: func(spec *WorkersSpec) { spec.InFlightBytes = MaxWorkerInFlightBytes + 1 }},
		{name: "missing preparation headroom", want: ErrInvalid, change: func(spec *WorkersSpec) {
			spec.ClaimBytes = MaxDeliveryRecordBytes + 1
			spec.InFlightBytes = 2*MaxDeliveryRecordBytes + MaxDecodedBytes
		}},
		{name: "negative pulse waiters", want: ErrInvalid, change: func(spec *WorkersSpec) { spec.PulseWaiters = -1 }},
		{name: "large pulse waiters", want: ErrTooLarge, change: func(spec *WorkersSpec) { spec.PulseWaiters = MaxTransientWaiters + 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := base
			test.change(&spec)
			workers, err := NewWorkers(spec, consumer)
			if workers != nil || !errors.Is(err, test.want) {
				t.Fatalf("NewWorkers() = (%v, %v), want nil and %v", workers, err, test.want)
			}
		})
	}
	if workers, err := NewWorkers(base); workers != nil || !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing consumers = (%v, %v)", workers, err)
	}
	zeroDescription := base
	zeroDescription.Driver = &workersConfigDriver{}
	if workers, err := NewWorkers(zeroDescription, consumer); workers != nil || !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero driver description = (%v, %v)", workers, err)
	}
	panicking := base
	panicking.Driver = &workersConfigDriver{panicDescription: true}
	if workers, err := NewWorkers(panicking, consumer); workers != nil || !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "private") {
		t.Fatalf("panicking driver description = (%v, %v)", workers, err)
	}
}

func TestWorkersDescriptionIsDetachedAndRedacted(t *testing.T) {
	spec, consumer, _, _ := workersConfigFixture(t, "workers.private-name")
	build, err := ParseBuildID("deploy:PRIVATE-build")
	if err != nil {
		t.Fatal(err)
	}
	spec.Build = build
	workers := mustWorkersConfig(t, spec, consumer)
	description := workers.Describe()
	wantFingerprint := spec.Catalog.Fingerprint()
	description.Namespace = Namespace{}
	description.Backend = BackendDescription{}
	description.Build = BuildID{}
	description.Plan.Bindings[0].Concurrency = MaxBindingConcurrency
	description.Plan.Bindings = nil
	description.Plan.CatalogFingerprint = "mutated"
	description.ClaimBytes = 1
	fresh := workers.Describe()
	if fresh.Namespace != spec.Namespace || fresh.Backend.IsZero() || fresh.Build != build || len(fresh.Plan.Bindings) != 1 || fresh.Plan.Bindings[0].Concurrency != 1 || fresh.Plan.CatalogFingerprint != wantFingerprint || fresh.ClaimBytes != DefaultClaimBytes {
		t.Fatalf("workers description was mutable: %#v", fresh)
	}
	secrets := []string{"workers.private-name", "deploy:PRIVATE-build", wantFingerprint}
	values := []string{
		fmt.Sprint(spec),
		fmt.Sprintf("%+v", spec),
		fmt.Sprintf("%#v", spec),
		fmt.Sprint(workers),
		fmt.Sprintf("%+v", workers),
		fmt.Sprintf("%#v", workers),
		workers.LogValue().String(),
		fmt.Sprint(fresh),
		fmt.Sprintf("%+v", fresh),
		fmt.Sprintf("%#v", fresh),
		fresh.LogValue().String(),
		slog.AnyValue(workers).Resolve().String(),
		slog.AnyValue(fresh).Resolve().String(),
	}
	for _, value := range values {
		for _, secret := range secrets {
			if strings.Contains(value, secret) {
				t.Fatalf("workers formatting leaked %q in %q", secret, value)
			}
		}
	}
	var nilWorkers *Workers
	if nilWorkers.Describe().Plan.Bindings != nil || nilWorkers.Describe().Backend != (BackendDescription{}) || fmt.Sprint(nilWorkers) != "[job workers]" {
		t.Fatal("nil workers description or formatting is unsafe")
	}
}

func TestWorkersSystemClockProvidesStoppableTimers(t *testing.T) {
	clock := systemClock{}
	if clock.Now().IsZero() {
		t.Fatal("system clock returned zero time")
	}
	timer := clock.NewTimer(time.Hour)
	if nilInterface(timer) || timer.C() == nil {
		t.Fatal("system clock returned an invalid timer")
	}
	timer.Stop()
	var _ Clock = clock
	var _ Timer = systemTimer{}
}

func workersConfigFixture(t *testing.T, name string) (WorkersSpec, Consumer, *workersConfigDriver, *atomic.Int32) {
	t.Helper()
	definition := testQueueDefinition(t, name, String(1))
	driver := &workersConfigDriver{description: queueTestBackendDescription(1)}
	identityCalls := &atomic.Int32{}
	identity := &workersConfigIdentity{calls: identityCalls}
	build, err := ParseBuildID("deploy:test-build")
	if err != nil {
		t.Fatal(err)
	}
	spec := WorkersSpec{
		Namespace: queueTestNamespace(t, "workers-app"),
		Catalog:   MustCatalog(definition),
		Driver:    driver,
		Build:     build,
		Identity:  identity,
	}
	consumer := On(definition, Handler[string](func(context.Context, string) error {
		panic("handler must not run during construction")
	}), Concurrency(1))
	return spec, consumer, driver, identityCalls
}

func mustWorkersConfig(t *testing.T, spec WorkersSpec, consumers ...Consumer) *Workers {
	t.Helper()
	workers, err := NewWorkers(spec, consumers...)
	if err != nil {
		t.Fatal(err)
	}
	return workers
}
