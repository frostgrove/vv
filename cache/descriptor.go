package cache

import "time"

type ScopeMode string

const (
	GlobalScopeMode      ScopeMode = "global"
	PartitionedScopeMode ScopeMode = "partitioned"
)

type FreshnessMode string

const (
	ExpiringFreshnessMode FreshnessMode = "expiring"
	AlwaysFreshMode       FreshnessMode = "always"
)

type RetentionMode string

const (
	ExpiringRetentionMode RetentionMode = "expiring"
	CapacityRetentionMode RetentionMode = "capacity"
)

type NegativeMode string

const (
	NoNegativeMode       NegativeMode = "disabled"
	ExpiringNegativeMode NegativeMode = "expiring"
)

type JitterMode string

const (
	NoJitterMode       JitterMode = "disabled"
	SubtractJitterMode JitterMode = "subtract"
)

type FreshnessDescription struct {
	Mode     FreshnessMode
	FreshFor time.Duration
	StaleFor time.Duration
	Reason   string
}

type RetentionDescription struct {
	Mode         RetentionMode
	ExpiresAfter time.Duration
}

type NegativeDescription struct {
	Mode     NegativeMode
	Duration time.Duration
}

type JitterDescription struct {
	Mode         JitterMode
	SubtractUpTo time.Duration
}

type ClockSkewDescription struct {
	Mode  SkewMode
	Bound time.Duration
}

type PolicyDescription struct {
	Disabled            bool
	Freshness           FreshnessDescription
	Retention           RetentionDescription
	Negative            NegativeDescription
	Jitter              JitterDescription
	MaxKeyBytes         int
	MaxValueBytes       int
	MaxValueDepth       int
	MaxFlights          int
	FlightSaturation    FlightSaturationMode
	FlightWait          time.Duration
	Stale               StalePolicy
	LastWaiter          LastWaiterPolicy
	MaxBatchKeys        int
	MaxBatchKeyBytes    int
	MaxBatchResultBytes int
	ReadFailure         FailurePolicy
	WriteFailure        FailurePolicy
	InvalidateFailure   FailurePolicy
	Corruption          CorruptionPolicy
}

type Descriptor struct {
	LogicalName  string
	Application  string
	Environment  string
	Purpose      string
	Generation   Generation
	Scope        ScopeMode
	KeyVersion   KeyVersion
	ValueCodec   string
	ValueSchema  ValueSchema
	Profile      string
	ProviderKind ProviderKind
	ProviderID   ProviderID
	ResourceID   ResourceID
	Requires     []Capability
	Backend      BackendDescription
	ClockSkew    ClockSkewDescription
	Policy       PolicyDescription
	Activated    bool
}

func (this *Cache[K, V]) Describe() Descriptor {
	if this == nil {
		return Descriptor{}
	}
	if core := this.inner.Load(); core != nil && (core.activation == nil || core.activation.committed.Load()) {
		return describeCore(core)
	}
	if definition := this.definition.Load(); definition != nil {
		return definition.declaredDescriptor()
	}
	if automatic := this.automatic.Load(); automatic != nil {
		policy, err := automatic.profile.Build()
		if err == nil {
			return Descriptor{
				Profile:      automatic.profile.name,
				ProviderKind: automatic.profile.provider,
				Policy:       describePolicy(policy),
			}
		}
	}
	return Descriptor{}
}

func describeCore[K, V any](core *cacheCore[K, V]) Descriptor {
	return Descriptor{
		LogicalName:  core.name,
		Application:  core.scope.namespace.application,
		Environment:  core.scope.namespace.environment,
		Purpose:      core.scope.namespace.purpose,
		Generation:   core.scope.namespace.generation,
		Scope:        scopeModeOf(core.scope),
		KeyVersion:   core.keyVersion,
		ValueCodec:   core.valueDescriptor.id,
		ValueSchema:  core.valueDescriptor.schema,
		Profile:      core.policy.profile,
		ProviderKind: core.providerKind,
		ProviderID:   core.providerID,
		ResourceID:   core.resourceID,
		Requires:     append([]Capability(nil), core.requires...),
		Backend:      core.backendDescription,
		ClockSkew: ClockSkewDescription{
			Mode:  core.runtime.ClockSkew.mode,
			Bound: core.runtime.ClockSkew.bound,
		},
		Policy:    describePolicy(core.policy),
		Activated: true,
	}
}

func scopeModeOf[K any](scope Scope[K]) ScopeMode {
	if scope.global {
		return GlobalScopeMode
	}
	return PartitionedScopeMode
}

func describePolicy(policy Policy) PolicyDescription {
	if policy.disabled {
		return PolicyDescription{Disabled: true}
	}
	freshness := FreshnessDescription{
		Mode:     ExpiringFreshnessMode,
		FreshFor: policy.Freshness.freshFor,
		StaleFor: policy.Freshness.staleFor,
	}
	if policy.Freshness.always {
		freshness = FreshnessDescription{Mode: AlwaysFreshMode, Reason: policy.Freshness.reason}
	}
	retention := RetentionDescription{Mode: ExpiringRetentionMode, ExpiresAfter: policy.Retention.expiresAfter}
	if policy.Retention.capacityOnly {
		retention = RetentionDescription{Mode: CapacityRetentionMode}
	}
	negative := NegativeDescription{Mode: NoNegativeMode}
	if policy.Negative.enabled {
		negative = NegativeDescription{Mode: ExpiringNegativeMode, Duration: policy.Negative.duration}
	}
	jitter := JitterDescription{Mode: NoJitterMode}
	if policy.Jitter.enabled {
		jitter = JitterDescription{Mode: SubtractJitterMode, SubtractUpTo: policy.Jitter.subtractUpTo}
	}
	return PolicyDescription{
		Freshness:           freshness,
		Retention:           retention,
		Negative:            negative,
		Jitter:              jitter,
		MaxKeyBytes:         policy.MaxKeyBytes,
		MaxValueBytes:       policy.MaxValueBytes,
		MaxValueDepth:       policy.MaxValueDepth,
		MaxFlights:          policy.MaxFlights,
		FlightSaturation:    policy.FlightSaturation.mode,
		FlightWait:          policy.FlightSaturation.timeout,
		Stale:               policy.Stale,
		LastWaiter:          policy.LastWaiter,
		MaxBatchKeys:        policy.MaxBatchKeys,
		MaxBatchKeyBytes:    policy.MaxBatchKeyBytes,
		MaxBatchResultBytes: policy.MaxBatchResultBytes,
		ReadFailure:         policy.ReadFailure,
		WriteFailure:        policy.WriteFailure,
		InvalidateFailure:   policy.InvalidateFailure,
		Corruption:          policy.Corruption,
	}
}
