package cache

import (
	"fmt"
	"math"
	"time"
)

type Freshness struct {
	freshFor time.Duration
	staleFor time.Duration
	always   bool
	reason   string
	set      bool
}

func Expiring(freshFor, staleFor time.Duration) Freshness {
	return Freshness{freshFor: freshFor, staleFor: staleFor, set: true}
}

func AlwaysFresh(reason string) Freshness {
	return Freshness{always: true, reason: reason, set: true}
}

type Retention struct {
	expiresAfter time.Duration
	capacityOnly bool
	set          bool
}

func ExpireAfter(duration time.Duration) Retention {
	return Retention{expiresAfter: duration, set: true}
}

func CapacityBoundedRetention() Retention {
	return Retention{capacityOnly: true, set: true}
}

type NegativeCaching struct {
	duration time.Duration
	enabled  bool
	set      bool
}

func NoNegativeCaching() NegativeCaching { return NegativeCaching{set: true} }

func CacheAbsenceFor(duration time.Duration) NegativeCaching {
	return NegativeCaching{duration: duration, enabled: true, set: true}
}

type JitterPolicy struct {
	subtractUpTo time.Duration
	enabled      bool
	set          bool
}

func NoJitter() JitterPolicy { return JitterPolicy{set: true} }

func SubtractUpTo(duration time.Duration) JitterPolicy {
	return JitterPolicy{subtractUpTo: duration, enabled: true, set: true}
}

type FailurePolicy uint8

const (
	Propagate FailurePolicy = iota + 1
	AsMiss
	Ignore
)

type CorruptionPolicy uint8

const (
	RefuseCorrupt CorruptionPolicy = iota + 1
	CorruptAsMiss
)

type FlightSaturationMode uint8

const (
	RejectFlight FlightSaturationMode = iota + 1
	ServeStaleFlight
	WaitForFlight
)

type FlightSaturationPolicy struct {
	mode    FlightSaturationMode
	timeout time.Duration
}

func Reject() FlightSaturationPolicy {
	return FlightSaturationPolicy{mode: RejectFlight}
}

func ServeStale() FlightSaturationPolicy {
	return FlightSaturationPolicy{mode: ServeStaleFlight}
}

func WaitBounded(timeout time.Duration) FlightSaturationPolicy {
	return FlightSaturationPolicy{mode: WaitForFlight, timeout: timeout}
}

type TransientSaturationMode uint8

const (
	RejectTransientMode TransientSaturationMode = iota + 1
	WaitForTransientMode
)

type TransientSaturationPolicy struct {
	mode    TransientSaturationMode
	timeout time.Duration
}

const (
	MaximumTransientWaiters = 4096
	defaultTransientWaiters = 64
)

func RejectTransient() TransientSaturationPolicy {
	return TransientSaturationPolicy{mode: RejectTransientMode}
}

func WaitForTransient(timeout time.Duration) TransientSaturationPolicy {
	return TransientSaturationPolicy{mode: WaitForTransientMode, timeout: timeout}
}

type StalePolicy uint8

const (
	RefreshBlocking StalePolicy = iota + 1
	ServeWhileRefreshing
	ServeOnLoaderError
)

type LastWaiterPolicy uint8

const (
	CancelLoader LastWaiterPolicy = iota + 1
	FinishLoader
)

type Policy struct {
	Freshness           Freshness
	Retention           Retention
	Negative            NegativeCaching
	Jitter              JitterPolicy
	MaxKeyBytes         int
	MaxValueBytes       int
	MaxValueDepth       int
	MaxFlights          int
	FlightSaturation    FlightSaturationPolicy
	Stale               StalePolicy
	LastWaiter          LastWaiterPolicy
	MaxBatchKeys        int
	MaxBatchKeyBytes    int
	MaxBatchResultBytes int
	MaxTransientBytes   int64
	MaxTransientWaiters int
	TransientSaturation TransientSaturationPolicy
	ReadFailure         FailurePolicy
	WriteFailure        FailurePolicy
	InvalidateFailure   FailurePolicy
	Corruption          CorruptionPolicy

	profile            string
	disabled           bool
	transientDefaulted bool
	transientExplicit  bool
	transientSet       bool
	transientResolved  int64
}

type Profile struct {
	name     string
	provider ProviderKind
	policy   Policy
	err      error
}

var (
	Hot  = profile("Hot", MemoryProviderKind, hotDefaults())
	Warm = profile("Warm", PostgreSQLProviderKind, Policy{
		Freshness:           Expiring(30*time.Minute, 10*time.Minute),
		Retention:           ExpireAfter(45 * time.Minute),
		Negative:            CacheAbsenceFor(2 * time.Minute),
		Jitter:              SubtractUpTo(3 * time.Minute),
		MaxKeyBytes:         16 << 10,
		MaxValueBytes:       16 << 20,
		MaxValueDepth:       128,
		MaxFlights:          1,
		FlightSaturation:    WaitBounded(time.Second),
		Stale:               ServeOnLoaderError,
		LastWaiter:          CancelLoader,
		MaxBatchKeys:        256,
		MaxBatchKeyBytes:    1 << 20,
		MaxBatchResultBytes: 64 << 20,
		MaxTransientBytes:   256 << 20,
		MaxTransientWaiters: 64,
		TransientSaturation: WaitForTransient(time.Second),
		ReadFailure:         AsMiss,
		WriteFailure:        Propagate,
		InvalidateFailure:   Propagate,
		Corruption:          RefuseCorrupt,
	})
	Durable = profile("Durable", PostgreSQLProviderKind, Policy{
		Freshness:           Expiring(24*time.Hour, time.Hour),
		Retention:           ExpireAfter(7 * 24 * time.Hour),
		Negative:            CacheAbsenceFor(5 * time.Minute),
		Jitter:              SubtractUpTo(30 * time.Minute),
		MaxKeyBytes:         16 << 10,
		MaxValueBytes:       16 << 20,
		MaxValueDepth:       128,
		MaxFlights:          2,
		FlightSaturation:    WaitBounded(2 * time.Second),
		Stale:               ServeOnLoaderError,
		LastWaiter:          FinishLoader,
		MaxBatchKeys:        256,
		MaxBatchKeyBytes:    4 << 20,
		MaxBatchResultBytes: 128 << 20,
		MaxTransientBytes:   512 << 20,
		MaxTransientWaiters: 64,
		TransientSaturation: WaitForTransient(2 * time.Second),
		ReadFailure:         Propagate,
		WriteFailure:        Propagate,
		InvalidateFailure:   Propagate,
		Corruption:          RefuseCorrupt,
	})
	Disabled = Profile{name: "Disabled", provider: NoProviderKind, policy: disabledDefaults()}
)

func hotDefaults() Policy {
	return Policy{
		Freshness:           Expiring(10*time.Minute, 5*time.Minute),
		Retention:           ExpireAfter(15 * time.Minute),
		Negative:            NoNegativeCaching(),
		Jitter:              SubtractUpTo(time.Minute),
		MaxKeyBytes:         16 << 10,
		MaxValueBytes:       16 << 20,
		MaxValueDepth:       128,
		MaxFlights:          1,
		FlightSaturation:    WaitBounded(250 * time.Millisecond),
		Stale:               ServeWhileRefreshing,
		LastWaiter:          CancelLoader,
		MaxBatchKeys:        256,
		MaxBatchKeyBytes:    1 << 20,
		MaxBatchResultBytes: 64 << 20,
		MaxTransientBytes:   256 << 20,
		MaxTransientWaiters: 64,
		TransientSaturation: WaitForTransient(250 * time.Millisecond),
		ReadFailure:         AsMiss,
		WriteFailure:        Ignore,
		InvalidateFailure:   Propagate,
		Corruption:          CorruptAsMiss,
		profile:             "Hot",
		transientDefaulted:  true,
		transientResolved:   256 << 20,
	}
}

func disabledDefaults() Policy {
	policy := hotDefaults()
	policy.profile = "Disabled"
	policy.disabled = true
	policy.transientDefaulted = true
	policy.transientResolved = policy.MaxTransientBytes
	return policy
}

func profile(name string, provider ProviderKind, policy Policy) Profile {
	policy.profile = name
	policy.transientDefaulted = policy.MaxTransientBytes > 0
	policy.transientResolved = policy.MaxTransientBytes
	normalized, err := normalizePolicy(policy)
	return Profile{name: name, provider: provider, policy: normalized, err: err}
}

func (this Profile) Name() string { return this.name }

func (this Profile) Build() (Policy, error) {
	if this.err != nil {
		return Policy{}, this.err
	}
	if this.name == "" || !validProviderKind(this.provider) || (this.policy.disabled != (this.provider == NoProviderKind)) {
		return Policy{}, failure("build profile", fmt.Errorf("%w: profile name or provider is invalid", ErrInvalid))
	}
	return normalizePolicy(this.policy)
}

type Option interface{ apply(*Policy) error }

type policyOption func(*Policy) error

func (this policyOption) apply(policy *Policy) error { return this(policy) }

func (this Profile) With(options ...Option) Profile {
	if this.err != nil {
		return this
	}
	policy := this.policy
	for index, option := range options {
		if option == nil {
			this.err = fmt.Errorf("cache: profile %s option %d: %w", this.name, index, ErrInvalid)
			return this
		}
		if err := option.apply(&policy); err != nil {
			this.err = fmt.Errorf("cache: profile %s option %d: %w", this.name, index, err)
			return this
		}
	}
	policy.profile = this.name
	this.policy, this.err = normalizePolicy(policy)
	return this
}

func MaxValueBytes(value int) Option {
	return policyOption(func(policy *Policy) error {
		if value <= 0 {
			return fmt.Errorf("%w: max value bytes must be positive", ErrInvalid)
		}
		policy.MaxValueBytes = value
		maximumEnvelopeBytes, err := maxEnvelopeBytes(*policy)
		if err != nil {
			return err
		}
		if policy.MaxBatchResultBytes < maximumEnvelopeBytes {
			policy.MaxBatchResultBytes = maximumEnvelopeBytes
		}
		if !policy.transientExplicit {
			policy.MaxTransientBytes = 0
			policy.transientDefaulted = true
			policy.transientResolved = 0
		}
		return nil
	})
}

func MaxFlights(value int) Option {
	return policyOption(func(policy *Policy) error {
		if value <= 0 {
			return fmt.Errorf("%w: max flights must be positive", ErrInvalid)
		}
		policy.MaxFlights = value
		if !policy.transientExplicit {
			policy.MaxTransientBytes = 0
			policy.transientDefaulted = true
			policy.transientResolved = 0
		}
		return nil
	})
}

func MaxTransientBytes(value int64) Option {
	return policyOption(func(policy *Policy) error {
		if value <= 0 {
			return fmt.Errorf("%w: max transient bytes must be positive", ErrInvalid)
		}
		policy.MaxTransientBytes = value
		policy.transientDefaulted = false
		policy.transientExplicit = true
		policy.transientResolved = 0
		return nil
	})
}

func MaxTransientWaiters(value int) Option {
	return policyOption(func(policy *Policy) error {
		if value <= 0 || value > MaximumTransientWaiters {
			return fmt.Errorf("%w: max transient waiters is invalid", ErrInvalid)
		}
		policy.MaxTransientWaiters = value
		if !policy.transientExplicit {
			policy.MaxTransientBytes = 0
			policy.transientDefaulted = true
			policy.transientResolved = 0
		}
		return nil
	})
}

func TransientSaturation(value TransientSaturationPolicy) Option {
	return policyOption(func(policy *Policy) error {
		if err := validateTransientSaturation(value); err != nil {
			return err
		}
		policy.TransientSaturation = value
		policy.transientSet = true
		return nil
	})
}

func FlightSaturation(value FlightSaturationPolicy) Option {
	return policyOption(func(policy *Policy) error {
		policy.FlightSaturation = value
		return nil
	})
}

func StaleBehavior(value StalePolicy) Option {
	return policyOption(func(policy *Policy) error {
		policy.Stale = value
		return nil
	})
}

func NegativeFor(value time.Duration) Option {
	return policyOption(func(policy *Policy) error {
		if value <= 0 {
			return fmt.Errorf("%w: negative duration must be positive", ErrInvalid)
		}
		policy.Negative = CacheAbsenceFor(value)
		return nil
	})
}

func normalizePolicy(policy Policy) (Policy, error) {
	if policy.MaxTransientWaiters == 0 {
		policy.MaxTransientWaiters = defaultTransientWaiters
	}
	if policy.transientDefaulted && policy.MaxTransientBytes != 0 && policy.MaxTransientBytes != policy.transientResolved {
		policy.transientDefaulted = false
		policy.transientExplicit = true
		policy.transientResolved = 0
	}
	if policy.MaxTransientBytes != 0 && !policy.transientDefaulted && !policy.transientExplicit {
		policy.transientExplicit = true
	}
	if !policy.disabled && policy.profile == "" {
		base := hotDefaults()
		overlayPolicy(&base, policy)
		policy = base
	}
	if err := resolveTransientDefaults(&policy); err != nil {
		return Policy{}, failure("build policy", err)
	}
	if err := validatePolicy(policy); err != nil {
		return Policy{}, failure("build policy", err)
	}
	return policy, nil
}

func overlayPolicy(target *Policy, source Policy) {
	dependentTransientChanged := source.MaxValueBytes != 0 || source.MaxFlights != 0 || source.MaxTransientWaiters != 0
	if source.Freshness.set {
		target.Freshness = source.Freshness
	}
	if source.Retention.set {
		target.Retention = source.Retention
	}
	if source.Negative.set {
		target.Negative = source.Negative
	}
	if source.Jitter.set {
		target.Jitter = source.Jitter
	}
	if source.MaxKeyBytes != 0 {
		target.MaxKeyBytes = source.MaxKeyBytes
	}
	if source.MaxValueBytes != 0 {
		target.MaxValueBytes = source.MaxValueBytes
	}
	if source.MaxValueDepth != 0 {
		target.MaxValueDepth = source.MaxValueDepth
	}
	if source.MaxFlights != 0 {
		target.MaxFlights = source.MaxFlights
	}
	if source.FlightSaturation != (FlightSaturationPolicy{}) {
		target.FlightSaturation = source.FlightSaturation
	}
	if source.Stale != 0 {
		target.Stale = source.Stale
	}
	if source.LastWaiter != 0 {
		target.LastWaiter = source.LastWaiter
	}
	if source.MaxBatchKeys != 0 {
		target.MaxBatchKeys = source.MaxBatchKeys
	}
	if source.MaxBatchKeyBytes != 0 {
		target.MaxBatchKeyBytes = source.MaxBatchKeyBytes
	}
	if source.MaxBatchResultBytes != 0 {
		target.MaxBatchResultBytes = source.MaxBatchResultBytes
	}
	if dependentTransientChanged && source.MaxTransientBytes == 0 {
		target.MaxTransientBytes = 0
		target.transientDefaulted = true
		target.transientExplicit = false
		target.transientResolved = 0
	}
	if source.MaxTransientBytes != 0 {
		target.MaxTransientBytes = source.MaxTransientBytes
		target.transientDefaulted = source.transientDefaulted
		target.transientExplicit = source.transientExplicit || !source.transientDefaulted
		target.transientResolved = source.transientResolved
	}
	if source.MaxTransientWaiters != 0 {
		target.MaxTransientWaiters = source.MaxTransientWaiters
	}
	if source.TransientSaturation != (TransientSaturationPolicy{}) || source.transientSet {
		target.TransientSaturation = source.TransientSaturation
		target.transientSet = source.transientSet
	}
	if source.ReadFailure != 0 {
		target.ReadFailure = source.ReadFailure
	}
	if source.WriteFailure != 0 {
		target.WriteFailure = source.WriteFailure
	}
	if source.InvalidateFailure != 0 {
		target.InvalidateFailure = source.InvalidateFailure
	}
	if source.Corruption != 0 {
		target.Corruption = source.Corruption
	}
}

func validatePolicy(policy Policy) error {
	if !policy.Freshness.set || !policy.Retention.set || !policy.Negative.set || !policy.Jitter.set {
		return fmt.Errorf("%w: freshness, retention, negative caching, and jitter must resolve from a profile", ErrInvalid)
	}
	if policy.Freshness.always {
		if policy.Freshness.reason == "" {
			return fmt.Errorf("%w: always-fresh policy needs a reviewed reason", ErrInvalid)
		}
	} else if policy.Freshness.freshFor <= 0 || policy.Freshness.staleFor < 0 {
		return fmt.Errorf("%w: freshness duration is invalid", ErrInvalid)
	}
	window, ok := addDuration(policy.Freshness.freshFor, policy.Freshness.staleFor)
	if !policy.Freshness.always && !ok {
		return fmt.Errorf("%w: freshness window overflows", ErrInvalid)
	}
	if policy.Retention.capacityOnly {
		if policy.Freshness.always {
			return fmt.Errorf("%w: capacity-only retention cannot be always fresh", ErrInvalid)
		}
	} else if policy.Retention.expiresAfter <= 0 || (!policy.Freshness.always && policy.Retention.expiresAfter < window) {
		return fmt.Errorf("%w: retention is shorter than the fresh and stale window", ErrInvalid)
	}
	if policy.Negative.enabled {
		if policy.Negative.duration <= 0 {
			return fmt.Errorf("%w: enabled negative caching requires a positive duration", ErrInvalid)
		}
	} else if policy.Negative.duration != 0 {
		return fmt.Errorf("%w: disabled negative caching has a duration", ErrInvalid)
	}
	if policy.Jitter.enabled {
		if policy.Jitter.subtractUpTo <= 0 {
			return fmt.Errorf("%w: enabled jitter requires a positive duration", ErrInvalid)
		}
	} else if policy.Jitter.subtractUpTo != 0 {
		return fmt.Errorf("%w: disabled jitter has a duration", ErrInvalid)
	}
	if !policy.Retention.capacityOnly && policy.Negative.duration > policy.Retention.expiresAfter {
		return fmt.Errorf("%w: negative duration exceeds retention", ErrInvalid)
	}
	if policy.Freshness.always && policy.Jitter.enabled {
		return fmt.Errorf("%w: always-fresh policy cannot apply freshness jitter", ErrInvalid)
	}
	if !policy.Freshness.always && policy.Jitter.subtractUpTo >= policy.Freshness.freshFor {
		return fmt.Errorf("%w: negative or jitter duration is invalid", ErrInvalid)
	}
	if policy.MaxKeyBytes <= 0 || policy.MaxKeyBytes > MaxEncodedKeyBytes || policy.MaxValueBytes <= 0 ||
		policy.MaxValueDepth <= 0 || policy.MaxValueDepth > jsonSafeMaximumDepth || policy.MaxFlights <= 0 || policy.MaxBatchKeys <= 0 ||
		policy.MaxBatchKeyBytes <= 0 || policy.MaxBatchResultBytes <= 0 || policy.MaxTransientBytes <= 0 ||
		policy.MaxTransientWaiters <= 0 || policy.MaxTransientWaiters > MaximumTransientWaiters {
		return fmt.Errorf("%w: cache bounds must all be positive and finite", ErrInvalid)
	}
	maximumEnvelopeBytes, err := maxEnvelopeBytes(policy)
	if err != nil {
		return err
	}
	if policy.MaxBatchKeyBytes < policy.MaxKeyBytes || policy.MaxBatchResultBytes < maximumEnvelopeBytes {
		return fmt.Errorf("%w: batch budgets must fit one maximum key and value", ErrInvalid)
	}
	transient, err := transientPlanFor(policy)
	if err != nil {
		return err
	}
	if policy.MaxTransientBytes < transient.minimum {
		return fmt.Errorf("%w: transient budget cannot admit maximum operations and joined callers", ErrInvalid)
	}
	if policy.FlightSaturation.mode < RejectFlight || policy.FlightSaturation.mode > WaitForFlight ||
		(policy.FlightSaturation.mode == WaitForFlight && policy.FlightSaturation.timeout <= 0) {
		return fmt.Errorf("%w: flight saturation policy is invalid", ErrInvalid)
	}
	if err := validateTransientSaturation(policy.TransientSaturation); err != nil {
		return err
	}
	if policy.Stale < RefreshBlocking || policy.Stale > ServeOnLoaderError ||
		policy.LastWaiter < CancelLoader || policy.LastWaiter > FinishLoader {
		return fmt.Errorf("%w: loader policy is invalid", ErrInvalid)
	}
	if policy.ReadFailure != Propagate && policy.ReadFailure != AsMiss {
		return fmt.Errorf("%w: read failure must propagate or become a miss", ErrInvalid)
	}
	if policy.WriteFailure != Propagate && policy.WriteFailure != Ignore {
		return fmt.Errorf("%w: write failure must propagate or be ignored", ErrInvalid)
	}
	if policy.InvalidateFailure != Propagate && policy.InvalidateFailure != Ignore {
		return fmt.Errorf("%w: invalidate failure must propagate or be ignored", ErrInvalid)
	}
	if policy.Corruption != RefuseCorrupt && policy.Corruption != CorruptAsMiss {
		return fmt.Errorf("%w: corruption policy is invalid", ErrInvalid)
	}
	return nil
}

func resolveTransientDefaults(policy *Policy) error {
	if policy.TransientSaturation == (TransientSaturationPolicy{}) && !policy.transientSet {
		if policy.FlightSaturation.mode == WaitForFlight {
			policy.TransientSaturation = WaitForTransient(policy.FlightSaturation.timeout)
		} else {
			policy.TransientSaturation = RejectTransient()
		}
	}
	plan, err := transientPlanFor(*policy)
	if err != nil {
		return err
	}
	if policy.MaxTransientBytes != 0 {
		if policy.transientDefaulted && policy.MaxTransientBytes == policy.transientResolved && policy.MaxTransientBytes < plan.minimum {
			policy.MaxTransientBytes = plan.minimum
			policy.transientResolved = plan.minimum
		}
		return nil
	}
	policy.MaxTransientBytes = plan.minimum
	policy.transientDefaulted = true
	policy.transientResolved = plan.minimum
	return nil
}

func validateTransientSaturation(policy TransientSaturationPolicy) error {
	if policy.mode < RejectTransientMode || policy.mode > WaitForTransientMode ||
		(policy.mode == WaitForTransientMode && policy.timeout <= 0) ||
		(policy.mode == RejectTransientMode && policy.timeout != 0) {
		return fmt.Errorf("%w: transient saturation policy is invalid", ErrInvalid)
	}
	return nil
}

func addDuration(left, right time.Duration) (time.Duration, bool) {
	if left < 0 || right < 0 || int64(left) > math.MaxInt64-int64(right) {
		return 0, false
	}
	return left + right, true
}
