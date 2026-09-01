package jobs

import (
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"time"
)

type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

type Clock interface {
	Now() time.Time
	NewTimer(time.Duration) Timer
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) NewTimer(duration time.Duration) Timer {
	return systemTimer{Timer: time.NewTimer(duration)}
}

type systemTimer struct{ *time.Timer }

func (timer systemTimer) C() <-chan time.Time { return timer.Timer.C }

type WorkersSpec struct {
	Namespace       Namespace
	Catalog         Catalog
	Driver          DeliveryDriver
	Build           BuildID
	Identity        TrustedIdentityRestorer
	Clock           Clock
	Entropy         io.Reader
	LeaseTTL        time.Duration
	Heartbeat       time.Duration
	PollInterval    time.Duration
	ReclaimInterval time.Duration
	ShutdownGrace   time.Duration
	ClaimItems      int
	ClaimBytes      int
	InFlightBytes   int
	PulseWaiters    int
}

func (WorkersSpec) String() string { return "[job workers spec]" }
func (spec WorkersSpec) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, spec.String())
}

type WorkersDescription struct {
	Namespace       Namespace
	Backend         BackendDescription
	Build           BuildID
	Plan            WorkerPlanDescription
	LeaseTTL        time.Duration
	Heartbeat       time.Duration
	PollInterval    time.Duration
	ReclaimInterval time.Duration
	ShutdownGrace   time.Duration
	ClaimItems      int
	ClaimBytes      int
	InFlightBytes   int
	PulseWaiters    int
}

func (description WorkersDescription) String() string {
	return fmt.Sprintf("[job workers description bindings=%d concurrency=%d]", len(description.Plan.Bindings), description.Plan.TotalConcurrency)
}
func (description WorkersDescription) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, description.String())
}
func (description WorkersDescription) LogValue() slog.Value {
	return slog.StringValue(description.String())
}

type resolvedWorkersConfig struct {
	namespace       Namespace
	catalog         Catalog
	driver          DeliveryDriver
	backend         BackendDescription
	build           BuildID
	identity        TrustedIdentityRestorer
	clock           Clock
	entropy         *entropySource
	leaseTTL        time.Duration
	heartbeat       time.Duration
	pollInterval    time.Duration
	reclaimInterval time.Duration
	shutdownGrace   time.Duration
	claimItems      int
	claimBytes      int
	inFlightBytes   int
	pulseWaiters    int
}

type Workers struct {
	config resolvedWorkersConfig
	plan   WorkerPlan
}

func NewWorkers(spec WorkersSpec, consumers ...Consumer) (*Workers, error) {
	config, err := resolveWorkersConfig(spec)
	if err != nil {
		return nil, err
	}
	plan, err := NewWorkerPlan(config.catalog, consumers...)
	if err != nil {
		return nil, err
	}
	if plan.CatalogFingerprint() != config.catalog.Fingerprint() {
		return nil, invalid("worker plan catalog fingerprint")
	}
	backend, err := ValidateDeliveryDriver(config.driver)
	if err != nil {
		return nil, err
	}
	config.backend = backend
	return &Workers{config: config, plan: plan}, nil
}

func (workers *Workers) Describe() WorkersDescription {
	if workers == nil {
		return WorkersDescription{}
	}
	return WorkersDescription{
		Namespace:       workers.config.namespace,
		Backend:         workers.config.backend,
		Build:           workers.config.build,
		Plan:            workers.plan.Describe(),
		LeaseTTL:        workers.config.leaseTTL,
		Heartbeat:       workers.config.heartbeat,
		PollInterval:    workers.config.pollInterval,
		ReclaimInterval: workers.config.reclaimInterval,
		ShutdownGrace:   workers.config.shutdownGrace,
		ClaimItems:      workers.config.claimItems,
		ClaimBytes:      workers.config.claimBytes,
		InFlightBytes:   workers.config.inFlightBytes,
		PulseWaiters:    workers.config.pulseWaiters,
	}
}

func (workers *Workers) String() string {
	if workers == nil {
		return "[job workers]"
	}
	return fmt.Sprintf("[job workers bindings=%d concurrency=%d]", workers.plan.Len(), workers.plan.TotalConcurrency())
}
func (workers *Workers) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, workers.String())
}
func (workers *Workers) LogValue() slog.Value { return slog.StringValue(workers.String()) }

func resolveWorkersConfig(spec WorkersSpec) (resolvedWorkersConfig, error) {
	if !spec.Namespace.valid() || spec.Catalog.Len() == 0 || spec.Catalog.Fingerprint() == "" || !spec.Build.valid() || nilInterface(spec.Identity) {
		return resolvedWorkersConfig{}, invalid("workers namespace, catalog, build, or identity")
	}
	if nilInterface(spec.Driver) {
		return resolvedWorkersConfig{}, invalid("workers delivery driver")
	}
	leaseTTL, err := resolveWorkerLeaseTTL(spec.LeaseTTL)
	if err != nil {
		return resolvedWorkersConfig{}, err
	}
	heartbeat, err := resolveWorkerHeartbeat(spec.Heartbeat, leaseTTL)
	if err != nil {
		return resolvedWorkersConfig{}, err
	}
	pollInterval, err := resolveBoundedWorkerDuration(spec.PollInterval, DefaultPollInterval, MinimumPollInterval, MaximumPollInterval, "worker poll interval")
	if err != nil {
		return resolvedWorkersConfig{}, err
	}
	reclaimInterval, err := resolveBoundedWorkerDuration(spec.ReclaimInterval, DefaultReclaimInterval, MinimumReclaimInterval, MaximumReclaimInterval, "worker reclaim interval")
	if err != nil {
		return resolvedWorkersConfig{}, err
	}
	shutdownGrace, err := resolveWorkerShutdownGrace(spec.ShutdownGrace)
	if err != nil {
		return resolvedWorkersConfig{}, err
	}
	claimItems, err := resolveBoundedWorkerInt(spec.ClaimItems, DefaultClaimItems, MaxClaimItems, "worker claim items")
	if err != nil {
		return resolvedWorkersConfig{}, err
	}
	claimBytes, err := resolveWorkerClaimBytes(spec.ClaimBytes)
	if err != nil {
		return resolvedWorkersConfig{}, err
	}
	inFlightBytes, err := resolveWorkerInFlightBytes(spec.InFlightBytes, claimBytes)
	if err != nil {
		return resolvedWorkersConfig{}, err
	}
	pulseWaiters, err := resolveBoundedWorkerInt(spec.PulseWaiters, DefaultTransientWaiters, MaxTransientWaiters, "worker pulse waiters")
	if err != nil {
		return resolvedWorkersConfig{}, err
	}
	clock := spec.Clock
	if nilInterface(clock) {
		clock = systemClock{}
	}
	entropy := spec.Entropy
	if nilInterface(entropy) {
		entropy = rand.Reader
	}
	return resolvedWorkersConfig{
		namespace:       spec.Namespace,
		catalog:         spec.Catalog,
		driver:          spec.Driver,
		build:           spec.Build,
		identity:        spec.Identity,
		clock:           clock,
		entropy:         &entropySource{reader: entropy},
		leaseTTL:        leaseTTL,
		heartbeat:       heartbeat,
		pollInterval:    pollInterval,
		reclaimInterval: reclaimInterval,
		shutdownGrace:   shutdownGrace,
		claimItems:      claimItems,
		claimBytes:      claimBytes,
		inFlightBytes:   inFlightBytes,
		pulseWaiters:    pulseWaiters,
	}, nil
}

func resolveWorkerLeaseTTL(value time.Duration) (time.Duration, error) {
	if value < 0 {
		return 0, invalid("worker lease ttl")
	}
	if value == 0 {
		value = DefaultLeaseTTL
	}
	if value > MaximumLeaseTTL {
		return 0, tooLarge("worker lease ttl")
	}
	if value < MinimumLeaseTTL {
		return 0, invalid("worker lease ttl")
	}
	return value, nil
}

func resolveWorkerHeartbeat(value, leaseTTL time.Duration) (time.Duration, error) {
	if value < 0 {
		return 0, invalid("worker heartbeat")
	}
	if value == 0 {
		value = min(DefaultHeartbeat, leaseTTL/4)
	}
	if value <= 0 || value > leaseTTL/2 {
		return 0, invalid("worker heartbeat")
	}
	return value, nil
}

func resolveBoundedWorkerDuration(value, fallback, minimum, maximum time.Duration, field string) (time.Duration, error) {
	if value < 0 {
		return 0, invalid(field)
	}
	if value == 0 {
		value = fallback
	}
	if value > maximum {
		return 0, tooLarge(field)
	}
	if value < minimum {
		return 0, invalid(field)
	}
	return value, nil
}

func resolveWorkerShutdownGrace(value time.Duration) (time.Duration, error) {
	if value < 0 {
		return 0, invalid("worker shutdown grace")
	}
	if value == 0 {
		value = DefaultShutdownGrace
	}
	if value > MaxShutdownGrace {
		return 0, tooLarge("worker shutdown grace")
	}
	return value, nil
}

func resolveBoundedWorkerInt(value, fallback, maximum int, field string) (int, error) {
	if value < 0 {
		return 0, invalid(field)
	}
	if value == 0 {
		value = fallback
	}
	if value > maximum {
		return 0, tooLarge(field)
	}
	if value < 1 {
		return 0, invalid(field)
	}
	return value, nil
}

func resolveWorkerClaimBytes(value int) (int, error) {
	if value < 0 {
		return 0, invalid("worker claim bytes")
	}
	if value == 0 {
		value = DefaultClaimBytes
	}
	if value > MaxClaimBytes {
		return 0, tooLarge("worker claim bytes")
	}
	if value < MaxDeliveryRecordBytes {
		return 0, invalid("worker claim bytes")
	}
	return value, nil
}

func resolveWorkerInFlightBytes(value, claimBytes int) (int, error) {
	if value < 0 {
		return 0, invalid("worker in-flight bytes")
	}
	if value == 0 {
		value = DefaultWorkerInFlightBytes
	}
	if value > MaxWorkerInFlightBytes {
		return 0, tooLarge("worker in-flight bytes")
	}
	if value < MaxDeliveryRecordBytes+MaxDecodedBytes || claimBytes > value-MaxDecodedBytes {
		return 0, invalid("worker in-flight bytes")
	}
	return value, nil
}
