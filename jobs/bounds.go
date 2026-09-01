package jobs

import "time"

const (
	MaxDefinitions        = 4096
	MaxNameBytes          = 128
	MaxQueueNameBytes     = 64
	MaxBindingNameBytes   = 128
	MaxBuildIDBytes       = 128
	MaxCodecIDBytes       = 64
	MaxIntentBytes        = 512
	IntentDigestBytes     = 32
	InvocationIDBytes     = 16
	MaxFailureCodeBytes   = 64
	MaxPublicFailureBytes = 2 << 10

	DefaultPayloadBytes   = 64 << 10
	MaxPayloadBytes       = 1 << 20
	DefaultDecodedBytes   = 256 << 10
	MaxDecodedBytes       = 4 << 20
	MaxPayloadDepth       = 64
	MaxSupportedRevisions = 8
	MaxUpcastHops         = MaxSupportedRevisions - 1

	MaxTraceCarrierBytes       = 1 << 10
	MaxTraceParentBytes        = 256
	MaxTraceStateBytes         = 512
	MaxCorrelationFields       = 8
	MaxCorrelationKeyBytes     = 32
	MaxCorrelationValueBytes   = 128
	MaxActorIdentityBytes      = 512
	MaxIdentityTokenBytes      = 2 << 10
	MaxIdentityProvenanceBytes = 64
	DefaultAttemptTraceEvents  = 32
	MaxAttemptTraceEvents      = 128
	MaxTraceEventNameBytes     = 64

	DefaultRetries           = 5
	MaximumRetries           = 32
	DefaultHandlerDeferrals  = 256
	MaximumHandlerDeferrals  = 4096
	DefaultDeliveryDeferrals = 256
	MaximumDeliveryDeferrals = 4096
	MaxAttemptOrdinal        = MaximumRetries + MaximumHandlerDeferrals + 1
	MaxInvocationOutcomes    = 1 + 2*MaxAttemptOrdinal + MaximumDeliveryDeferrals + 1

	MaxBindingConcurrency = 256
	MaxWorkerConcurrency  = 4096
	DefaultClaimItems     = 64
	MaxClaimItems         = 256
	DefaultClaimBytes     = MaxDeliveryRecordBytes
	MaxClaimBytes         = 64 << 20

	DefaultReclaimBatch        = 100
	MaxReclaimBatch            = 1000
	DefaultTransientBytes      = 16 << 20
	DefaultTransientWaiters    = 256
	MaxTransientWaiters        = 4096
	DefaultWorkerInFlightBytes = 64 << 20
)

const (
	DefaultAttemptTimeout = 10 * time.Minute
	MaximumAttemptTimeout = 24 * time.Hour
	DefaultMaxElapsed     = 24 * time.Hour
	MaximumMaxElapsed     = 30 * 24 * time.Hour

	MinRetryDelay        = 100 * time.Millisecond
	DefaultRetryDelay    = 5 * time.Second
	DefaultMaxRetryDelay = 5 * time.Minute
	MaxRetryDelay        = time.Hour

	DefaultTerminalRetention = 7 * 24 * time.Hour
	DefaultIntentRetention   = 30 * 24 * time.Hour
	MaxRetention             = 365 * 24 * time.Hour

	DefaultPollInterval    = time.Second
	MinimumLeaseTTL        = time.Second
	DefaultLeaseTTL        = time.Minute
	MaximumLeaseTTL        = 24 * time.Hour
	DefaultHeartbeat       = 15 * time.Second
	DefaultReclaimInterval = 15 * time.Second
	DefaultShutdownGrace   = 20 * time.Second
	MaxShutdownGrace       = 10 * time.Minute
	DefaultTransientWait   = 250 * time.Millisecond
)
