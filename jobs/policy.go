package jobs

import (
	"fmt"
	"sort"
	"time"
)

const (
	MaximumPriority = 1000
)

type JitterMode uint8

const (
	NoJitter JitterMode = iota + 1
	FullJitter
)

type BackoffPolicy struct {
	Initial time.Duration
	Maximum time.Duration
	Jitter  JitterMode
}

func Exponential(initial, maximum time.Duration, jitter JitterMode) BackoffPolicy {
	return BackoffPolicy{Initial: initial, Maximum: maximum, Jitter: jitter}
}

type Policy struct {
	Queue                QueueName
	Priority             int
	AttemptTimeout       time.Duration
	ProgressTimeout      time.Duration
	MaxElapsed           time.Duration
	MaxRetries           int
	MaxHandlerDeferrals  int
	MaxDeliveryDeferrals int
	Backoff              BackoffPolicy
	Retention            time.Duration
	IntentRetention      time.Duration
	Payload              PayloadLimit
	Durability           DurabilityRequirement
	Trace                TracePolicy

	profile   string
	overrides policyOverride
	baseline  *policySnapshot
}

type policySnapshot struct {
	Queue                QueueName
	Priority             int
	AttemptTimeout       time.Duration
	ProgressTimeout      time.Duration
	MaxElapsed           time.Duration
	MaxRetries           int
	MaxHandlerDeferrals  int
	MaxDeliveryDeferrals int
	Backoff              BackoffPolicy
	Retention            time.Duration
	IntentRetention      time.Duration
	Payload              PayloadLimit
	Durability           DurabilityRequirement
	Trace                TracePolicy
}

type policyOverride uint32

const (
	overrideQueue policyOverride = 1 << iota
	overridePriority
	overrideAttemptTimeout
	overrideProgressTimeout
	overrideMaxElapsed
	overrideRetries
	overrideHandlerDeferrals
	overrideDeliveryDeferrals
	overrideBackoff
	overrideRetention
	overrideIntentRetention
	overridePayloadBytes
	overrideDecodedBytes
	overridePayloadDepth
	overrideAcceptedAckModes
	overrideProtectedFailures
	overrideTraceCorrelations
)

type Option interface{ applyPolicy(*Policy) error }

type policyOption func(*Policy) error

func (this policyOption) applyPolicy(policy *Policy) error { return this(policy) }

func OnQueue(value QueueName) Option {
	return policyOption(func(policy *Policy) error {
		if value.IsZero() {
			return fmt.Errorf("%w: queue is required", ErrInvalid)
		}
		policy.Queue = value
		policy.overrides |= overrideQueue
		return nil
	})
}

func Priority(value int) Option {
	return integerPolicyOption(value, 1, MaximumPriority, overridePriority, func(policy *Policy, value int) { policy.Priority = value }, "priority")
}

func AttemptTimeout(value time.Duration) Option {
	return durationPolicyOption(value, MaximumAttemptTimeout, overrideAttemptTimeout, func(policy *Policy, value time.Duration) { policy.AttemptTimeout = value }, "attempt timeout")
}

func ProgressTimeout(value time.Duration) Option {
	return policyOption(func(policy *Policy) error {
		if value < 0 || value > MaximumAttemptTimeout {
			return fmt.Errorf("%w: progress timeout is outside supported bounds", ErrInvalid)
		}
		policy.ProgressTimeout = value
		policy.overrides |= overrideProgressTimeout
		return nil
	})
}

func MaxElapsed(value time.Duration) Option {
	return durationPolicyOption(value, MaximumMaxElapsed, overrideMaxElapsed, func(policy *Policy, value time.Duration) { policy.MaxElapsed = value }, "maximum elapsed time")
}

func Retries(value int) Option {
	return integerPolicyOption(value, 0, MaximumRetries, overrideRetries, func(policy *Policy, value int) { policy.MaxRetries = value }, "retries")
}

func MaxHandlerDeferrals(value int) Option {
	return integerPolicyOption(value, 0, MaximumHandlerDeferrals, overrideHandlerDeferrals, func(policy *Policy, value int) { policy.MaxHandlerDeferrals = value }, "handler deferrals")
}

func MaxDeliveryDeferrals(value int) Option {
	return integerPolicyOption(value, 0, MaximumDeliveryDeferrals, overrideDeliveryDeferrals, func(policy *Policy, value int) { policy.MaxDeliveryDeferrals = value }, "delivery deferrals")
}

func RetryBackoff(value BackoffPolicy) Option {
	return policyOption(func(policy *Policy) error {
		if err := validateBackoff(value, MaximumMaxElapsed); err != nil {
			return err
		}
		policy.Backoff = value
		policy.overrides |= overrideBackoff
		return nil
	})
}

func RetainFor(value time.Duration) Option {
	return durationPolicyOption(value, 0, overrideRetention, func(policy *Policy, value time.Duration) { policy.Retention = value }, "retention")
}

func RetainIntentsFor(value time.Duration) Option {
	return durationPolicyOption(value, 0, overrideIntentRetention, func(policy *Policy, value time.Duration) { policy.IntentRetention = value }, "intent retention")
}

func PayloadBytes(value int) Option {
	return integerPolicyOption(value, 1, MaxPayloadBytes, overridePayloadBytes, func(policy *Policy, value int) { policy.Payload.MaxBytes = value }, "payload bytes")
}

func MaxDecodedPayloadBytes(value int) Option {
	return integerPolicyOption(value, 1, MaxDecodedBytes, overrideDecodedBytes, func(policy *Policy, value int) { policy.Payload.MaxDecodedBytes = value }, "decoded payload bytes")
}

func PayloadDepth(value int) Option {
	return integerPolicyOption(value, 1, MaxPayloadDepth, overridePayloadDepth, func(policy *Policy, value int) { policy.Payload.MaxDepth = value }, "payload depth")
}

func AcceptAckModes(values ...AckMode) Option {
	return policyOption(func(policy *Policy) error {
		if len(values) == 0 {
			return invalid("accepted ack modes")
		}
		modes, err := AckModes(values...)
		if err != nil {
			return err
		}
		policy.Durability.acceptedAckModes = modes
		policy.overrides |= overrideAcceptedAckModes
		return nil
	})
}

func AcceptAnyAckMode() Option {
	return policyOption(func(policy *Policy) error {
		policy.Durability.acceptedAckModes = AckModeSet{}
		policy.overrides |= overrideAcceptedAckModes
		return nil
	})
}

func ProtectAcknowledgedEnqueuesFrom(values ...Failure) Option {
	return policyOption(func(policy *Policy) error {
		if len(values) == 0 {
			return invalid("protected enqueue failures")
		}
		failures, err := Failures(values...)
		if err != nil {
			return err
		}
		policy.Durability.protectedFailures = failures
		policy.overrides |= overrideProtectedFailures
		return nil
	})
}

func AllowAcknowledgedLoss() Option {
	return policyOption(func(policy *Policy) error {
		policy.Durability.protectedFailures = FailureSet{}
		policy.overrides |= overrideProtectedFailures
		return nil
	})
}

func AllowTraceCorrelations(values ...CorrelationKey) Option {
	return policyOption(func(policy *Policy) error {
		if len(values) == 0 {
			return invalid("trace correlation allowlist")
		}
		trace, err := NewTracePolicy(values...)
		if err != nil {
			return err
		}
		policy.Trace = trace
		policy.overrides |= overrideTraceCorrelations
		return nil
	})
}

func NoTraceCorrelations() Option {
	return policyOption(func(policy *Policy) error {
		policy.Trace = TracePolicy{}
		policy.overrides |= overrideTraceCorrelations
		return nil
	})
}

func integerPolicyOption(value, minimum, maximum int, flag policyOverride, assign func(*Policy, int), field string) Option {
	return policyOption(func(policy *Policy) error {
		if value < minimum || value > maximum {
			return fmt.Errorf("%w: %s is outside supported bounds", ErrInvalid, field)
		}
		assign(policy, value)
		policy.overrides |= flag
		return nil
	})
}

func durationPolicyOption(value, maximum time.Duration, flag policyOverride, assign func(*Policy, time.Duration), field string) Option {
	return policyOption(func(policy *Policy) error {
		if value <= 0 || maximum > 0 && value > maximum {
			return fmt.Errorf("%w: %s is outside supported bounds", ErrInvalid, field)
		}
		assign(policy, value)
		policy.overrides |= flag
		return nil
	})
}

func validatePolicy(policy Policy) error {
	if policy.Queue.IsZero() || policy.Priority <= 0 || policy.Priority > MaximumPriority {
		return fmt.Errorf("%w: queue or priority is invalid", ErrInvalid)
	}
	if policy.AttemptTimeout <= 0 || policy.AttemptTimeout > MaximumAttemptTimeout || policy.MaxElapsed <= 0 || policy.MaxElapsed > MaximumMaxElapsed || policy.AttemptTimeout > policy.MaxElapsed {
		return fmt.Errorf("%w: attempt and elapsed limits are invalid", ErrInvalid)
	}
	if policy.ProgressTimeout < 0 || policy.ProgressTimeout > policy.AttemptTimeout {
		return fmt.Errorf("%w: progress timeout is invalid", ErrInvalid)
	}
	if policy.MaxRetries < 0 || policy.MaxRetries > MaximumRetries || policy.MaxHandlerDeferrals < 0 || policy.MaxHandlerDeferrals > MaximumHandlerDeferrals || policy.MaxDeliveryDeferrals < 0 || policy.MaxDeliveryDeferrals > MaximumDeliveryDeferrals {
		return fmt.Errorf("%w: retry or deferral limit is invalid", ErrInvalid)
	}
	if err := validateBackoff(policy.Backoff, policy.MaxElapsed); err != nil {
		return err
	}
	if policy.Retention <= 0 || policy.Retention > MaxRetention || policy.IntentRetention < policy.Retention || policy.IntentRetention > MaxRetention {
		return fmt.Errorf("%w: retention limits are invalid", ErrInvalid)
	}
	if !policy.Durability.valid() {
		return fmt.Errorf("%w: durability requirement is invalid", ErrInvalid)
	}
	if !policy.Trace.valid() {
		return fmt.Errorf("%w: trace policy is invalid", ErrInvalid)
	}
	return validatePayloadLimit(policy.Payload)
}

func validateBackoff(backoff BackoffPolicy, maxElapsed time.Duration) error {
	if backoff.Initial < MinRetryDelay || backoff.Maximum < backoff.Initial || backoff.Maximum > MaxRetryDelay || maxElapsed <= 0 || backoff.Maximum > maxElapsed || backoff.Jitter < NoJitter || backoff.Jitter > FullJitter {
		return fmt.Errorf("%w: retry backoff is invalid", ErrInvalid)
	}
	return nil
}

func policyOverrideNames(value policyOverride) []string {
	fields := []struct {
		flag policyOverride
		name string
	}{
		{overrideQueue, "queue"},
		{overridePriority, "priority"},
		{overrideAttemptTimeout, "attempt_timeout"},
		{overrideProgressTimeout, "progress_timeout"},
		{overrideMaxElapsed, "max_elapsed"},
		{overrideRetries, "max_retries"},
		{overrideHandlerDeferrals, "max_handler_deferrals"},
		{overrideDeliveryDeferrals, "max_delivery_deferrals"},
		{overrideBackoff, "backoff"},
		{overrideRetention, "retention"},
		{overrideIntentRetention, "intent_retention"},
		{overridePayloadBytes, "max_payload_bytes"},
		{overrideDecodedBytes, "max_decoded_bytes"},
		{overridePayloadDepth, "max_payload_depth"},
		{overrideAcceptedAckModes, "accepted_ack_modes"},
		{overrideProtectedFailures, "protected_enqueue_failures"},
		{overrideTraceCorrelations, "trace_correlations"},
	}
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		if value&field.flag != 0 {
			result = append(result, field.name)
		}
	}
	sort.Strings(result)
	return result
}

func effectivePolicyOverrides(policy Policy) policyOverride {
	result := policy.overrides
	if policy.baseline == nil {
		return result
	}
	baseline := policy.baseline
	if policy.Queue != baseline.Queue {
		result |= overrideQueue
	}
	if policy.Priority != baseline.Priority {
		result |= overridePriority
	}
	if policy.AttemptTimeout != baseline.AttemptTimeout {
		result |= overrideAttemptTimeout
	}
	if policy.ProgressTimeout != baseline.ProgressTimeout {
		result |= overrideProgressTimeout
	}
	if policy.MaxElapsed != baseline.MaxElapsed {
		result |= overrideMaxElapsed
	}
	if policy.MaxRetries != baseline.MaxRetries {
		result |= overrideRetries
	}
	if policy.MaxHandlerDeferrals != baseline.MaxHandlerDeferrals {
		result |= overrideHandlerDeferrals
	}
	if policy.MaxDeliveryDeferrals != baseline.MaxDeliveryDeferrals {
		result |= overrideDeliveryDeferrals
	}
	if policy.Backoff != baseline.Backoff {
		result |= overrideBackoff
	}
	if policy.Retention != baseline.Retention {
		result |= overrideRetention
	}
	if policy.IntentRetention != baseline.IntentRetention {
		result |= overrideIntentRetention
	}
	if policy.Payload.MaxBytes != baseline.Payload.MaxBytes {
		result |= overridePayloadBytes
	}
	if policy.Payload.MaxDecodedBytes != baseline.Payload.MaxDecodedBytes {
		result |= overrideDecodedBytes
	}
	if policy.Payload.MaxDepth != baseline.Payload.MaxDepth {
		result |= overridePayloadDepth
	}
	if policy.Durability.acceptedAckModes != baseline.Durability.acceptedAckModes {
		result |= overrideAcceptedAckModes
	}
	if policy.Durability.protectedFailures != baseline.Durability.protectedFailures {
		result |= overrideProtectedFailures
	}
	if policy.Trace != baseline.Trace {
		result |= overrideTraceCorrelations
	}
	return result
}

func snapshotPolicy(policy Policy) policySnapshot {
	return policySnapshot{
		Queue:                policy.Queue,
		Priority:             policy.Priority,
		AttemptTimeout:       policy.AttemptTimeout,
		ProgressTimeout:      policy.ProgressTimeout,
		MaxElapsed:           policy.MaxElapsed,
		MaxRetries:           policy.MaxRetries,
		MaxHandlerDeferrals:  policy.MaxHandlerDeferrals,
		MaxDeliveryDeferrals: policy.MaxDeliveryDeferrals,
		Backoff:              policy.Backoff,
		Retention:            policy.Retention,
		IntentRetention:      policy.IntentRetention,
		Payload:              policy.Payload,
		Durability:           policy.Durability,
		Trace:                policy.Trace,
	}
}
