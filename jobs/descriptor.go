package jobs

import "time"

type CodecMode string

const (
	SafeCodecMode    CodecMode = "safe"
	TrustedCodecMode CodecMode = "trusted"
	CustomCodecMode  CodecMode = "custom"
)

type UpcastDescription struct {
	From        SchemaVersion
	To          SchemaVersion
	SourceCodec CodecID
	TargetCodec CodecID
}

type CodecDescription struct {
	ID                 CodecID
	CurrentVersion     SchemaVersion
	SupportedRevisions []SchemaVersion
	Mode               CodecMode
	Upcasts            []UpcastDescription
}

type PayloadIdentityDescription struct {
	ID        CodecID
	Version   SchemaVersion
	Available bool
	Automatic bool
}

type BackoffDescription struct {
	Initial time.Duration
	Maximum time.Duration
	Jitter  JitterMode
}

type PolicyDescription struct {
	Profile              string
	Overrides            []string
	Queue                QueueName
	Priority             int
	AttemptTimeout       time.Duration
	ProgressTimeout      time.Duration
	MaxElapsed           time.Duration
	MaxRetries           int
	MaxHandlerDeferrals  int
	MaxDeliveryDeferrals int
	Backoff              BackoffDescription
	Retention            time.Duration
	IntentRetention      time.Duration
	MaxPayloadBytes      int
	MaxDecodedBytes      int
	MaxPayloadDepth      int
	Durability           DurabilityRequirement
	Trace                TracePolicy
}

type Descriptor struct {
	Name            Name
	Codec           CodecDescription
	PayloadIdentity PayloadIdentityDescription
	Policy          PolicyDescription
	Partition       PartitionMode
	Automatic       bool
	Resolved        bool
}

type CatalogDescriptor struct {
	Definitions []Descriptor
	Fingerprint string
}

func describePolicy(policy Policy) PolicyDescription {
	profile := policy.profile
	overrides := effectivePolicyOverrides(policy)
	if profile == "" {
		profile = "Manual"
		overrides = overrideQueue | overridePriority | overrideAttemptTimeout | overrideProgressTimeout | overrideMaxElapsed | overrideRetries | overrideHandlerDeferrals | overrideDeliveryDeferrals | overrideBackoff | overrideRetention | overrideIntentRetention | overridePayloadBytes | overrideDecodedBytes | overridePayloadDepth | overrideAcceptedAckModes | overrideProtectedFailures | overrideTraceCorrelations
	}
	return PolicyDescription{
		Profile:              profile,
		Overrides:            policyOverrideNames(overrides),
		Queue:                policy.Queue,
		Priority:             policy.Priority,
		AttemptTimeout:       policy.AttemptTimeout,
		ProgressTimeout:      policy.ProgressTimeout,
		MaxElapsed:           policy.MaxElapsed,
		MaxRetries:           policy.MaxRetries,
		MaxHandlerDeferrals:  policy.MaxHandlerDeferrals,
		MaxDeliveryDeferrals: policy.MaxDeliveryDeferrals,
		Backoff: BackoffDescription{
			Initial: policy.Backoff.Initial,
			Maximum: policy.Backoff.Maximum,
			Jitter:  policy.Backoff.Jitter,
		},
		Retention:       policy.Retention,
		IntentRetention: policy.IntentRetention,
		MaxPayloadBytes: policy.Payload.MaxBytes,
		MaxDecodedBytes: policy.Payload.MaxDecodedBytes,
		MaxPayloadDepth: policy.Payload.MaxDepth,
		Durability:      policy.Durability,
		Trace:           policy.Trace,
	}
}

func cloneDescriptor(value Descriptor) Descriptor {
	value.Codec.SupportedRevisions = append([]SchemaVersion(nil), value.Codec.SupportedRevisions...)
	value.Codec.Upcasts = append([]UpcastDescription(nil), value.Codec.Upcasts...)
	value.Policy.Overrides = append([]string(nil), value.Policy.Overrides...)
	return value
}
