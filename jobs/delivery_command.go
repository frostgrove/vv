package jobs

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"time"
)

const MaxLeaseTokenBytes = 2 << 10

type LeaseRef struct {
	backend    BackendID
	invocation InvocationID
	token      []byte
	binding    [32]byte
}

func NewLeaseRef(backend BackendID, invocation InvocationID, driverToken []byte) (LeaseRef, error) {
	if !backend.valid() || !invocation.valid() || len(driverToken) == 0 {
		return LeaseRef{}, invalid("lease reference")
	}
	if len(driverToken) > MaxLeaseTokenBytes {
		return LeaseRef{}, tooLarge("lease driver token")
	}
	token := bytes.Clone(driverToken)
	return LeaseRef{backend: backend, invocation: invocation, token: token, binding: digestLeaseRef(backend, invocation, token)}, nil
}

func (r LeaseRef) Backend() BackendID         { return r.backend }
func (r LeaseRef) InvocationID() InvocationID { return r.invocation }
func (r LeaseRef) DriverToken() []byte        { return bytes.Clone(r.token) }
func (r LeaseRef) IsZero() bool               { return r.binding == [32]byte{} }
func (LeaseRef) String() string               { return "[job lease reference]" }
func (r LeaseRef) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, r.String())
}
func (r LeaseRef) LogValue() slog.Value { return slog.StringValue(r.String()) }
func (LeaseRef) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: lease reference cannot be serialized", ErrUnsupported)
}
func (r LeaseRef) valid() bool {
	return r.backend.valid() && r.invocation.valid() && len(r.token) > 0 && len(r.token) <= MaxLeaseTokenBytes && r.binding == digestLeaseRef(r.backend, r.invocation, r.token)
}

type DeliveryCommandKind uint8

const (
	DeliveryCommandBeginAttempt DeliveryCommandKind = iota + 1
	DeliveryCommandProgress
	DeliveryCommandFinishAttempt
	DeliveryCommandDeferDelivery
	DeliveryCommandFinishDelivery
	DeliveryCommandReleaseUnchanged
	DeliveryCommandRejectCorrupt
	DeliveryCommandArbitrateAttemptDeadline
	DeliveryCommandRevokeAttempt
)

func (k DeliveryCommandKind) Valid() bool {
	return k >= DeliveryCommandBeginAttempt && k <= DeliveryCommandRevokeAttempt
}

func (k DeliveryCommandKind) String() string {
	switch k {
	case DeliveryCommandBeginAttempt:
		return "begin_attempt"
	case DeliveryCommandProgress:
		return "progress"
	case DeliveryCommandFinishAttempt:
		return "finish_attempt"
	case DeliveryCommandDeferDelivery:
		return "defer_delivery"
	case DeliveryCommandFinishDelivery:
		return "finish_delivery"
	case DeliveryCommandReleaseUnchanged:
		return "release_unchanged"
	case DeliveryCommandRejectCorrupt:
		return "reject_corrupt"
	case DeliveryCommandArbitrateAttemptDeadline:
		return "arbitrate_attempt_deadline"
	case DeliveryCommandRevokeAttempt:
		return "revoke_attempt"
	default:
		return "unknown"
	}
}

type DeliveryCommand struct {
	kind          DeliveryCommandKind
	lease         LeaseRef
	binding       BindingName
	build         BuildID
	disposition   Disposition
	delay         time.Duration
	deadlineDelay time.Duration
	reason        Reason
	failure       PublicFailure
	state         InvocationState
}

func BeginAttemptCommand(lease LeaseRef, binding BindingName, build BuildID) (DeliveryCommand, error) {
	command := DeliveryCommand{kind: DeliveryCommandBeginAttempt, lease: cloneLeaseRef(lease), binding: binding, build: build}
	return validateDeliveryCommand(command)
}

func ProgressCommand(lease LeaseRef) (DeliveryCommand, error) {
	return validateDeliveryCommand(DeliveryCommand{kind: DeliveryCommandProgress, lease: cloneLeaseRef(lease)})
}

func FinishAttemptCommand(lease LeaseRef, disposition Disposition, delay, deadlineRetryDelay time.Duration) (DeliveryCommand, error) {
	command := DeliveryCommand{kind: DeliveryCommandFinishAttempt, lease: cloneLeaseRef(lease), disposition: disposition, delay: delay, deadlineDelay: deadlineRetryDelay}
	return validateDeliveryCommand(command)
}

func ArbitrateAttemptDeadlineCommand(lease LeaseRef, deadlineRetryDelay time.Duration) (DeliveryCommand, error) {
	return validateDeliveryCommand(DeliveryCommand{kind: DeliveryCommandArbitrateAttemptDeadline, lease: cloneLeaseRef(lease), deadlineDelay: deadlineRetryDelay})
}

func RevokeAttemptCommand(lease LeaseRef, reason Reason, retryDelay time.Duration) (DeliveryCommand, error) {
	return validateDeliveryCommand(DeliveryCommand{kind: DeliveryCommandRevokeAttempt, lease: cloneLeaseRef(lease), delay: retryDelay, deadlineDelay: retryDelay, reason: reason})
}

func DeferDeliveryCommand(lease LeaseRef, reason Reason, failure PublicFailure, delay time.Duration) (DeliveryCommand, error) {
	command := DeliveryCommand{kind: DeliveryCommandDeferDelivery, lease: cloneLeaseRef(lease), reason: reason, failure: failure, delay: delay}
	return validateDeliveryCommand(command)
}

func FinishDeliveryCommand(lease LeaseRef, state InvocationState, reason Reason, failure PublicFailure) (DeliveryCommand, error) {
	command := DeliveryCommand{kind: DeliveryCommandFinishDelivery, lease: cloneLeaseRef(lease), state: state, reason: reason, failure: failure}
	return validateDeliveryCommand(command)
}

func ReleaseUnchangedCommand(lease LeaseRef, binding BindingName, build BuildID, delay time.Duration) (DeliveryCommand, error) {
	return validateDeliveryCommand(DeliveryCommand{kind: DeliveryCommandReleaseUnchanged, lease: cloneLeaseRef(lease), binding: binding, build: build, reason: ReasonCompatibility, delay: delay})
}

func ReleaseForShutdownCommand(lease LeaseRef, delay time.Duration) (DeliveryCommand, error) {
	return validateDeliveryCommand(DeliveryCommand{kind: DeliveryCommandReleaseUnchanged, lease: cloneLeaseRef(lease), reason: ReasonShutdown, delay: delay})
}

func RejectCorruptCommand(lease LeaseRef) (DeliveryCommand, error) {
	return validateDeliveryCommand(DeliveryCommand{kind: DeliveryCommandRejectCorrupt, lease: cloneLeaseRef(lease)})
}

func (c DeliveryCommand) Kind() DeliveryCommandKind         { return c.kind }
func (c DeliveryCommand) Lease() LeaseRef                   { return cloneLeaseRef(c.lease) }
func (c DeliveryCommand) Binding() BindingName              { return c.binding }
func (c DeliveryCommand) Build() BuildID                    { return c.build }
func (c DeliveryCommand) Disposition() Disposition          { return c.disposition }
func (c DeliveryCommand) Delay() time.Duration              { return c.delay }
func (c DeliveryCommand) DeadlineRetryDelay() time.Duration { return c.deadlineDelay }
func (c DeliveryCommand) Reason() Reason                    { return c.reason }
func (c DeliveryCommand) Failure() PublicFailure            { return c.failure }
func (c DeliveryCommand) State() InvocationState            { return c.state }
func (DeliveryCommand) String() string                      { return "[job delivery command]" }
func (c DeliveryCommand) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, c.String())
}
func (c DeliveryCommand) LogValue() slog.Value { return slog.StringValue(c.String()) }
func (DeliveryCommand) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: delivery command cannot be serialized", ErrUnsupported)
}

type DeliveryMutationStatus uint8

const (
	DeliveryMutationApplied DeliveryMutationStatus = iota + 1
	DeliveryMutationLeaseLost
	DeliveryMutationAmbiguous
)

func (s DeliveryMutationStatus) Valid() bool {
	return s >= DeliveryMutationApplied && s <= DeliveryMutationAmbiguous
}

func (s DeliveryMutationStatus) String() string {
	switch s {
	case DeliveryMutationApplied:
		return "applied"
	case DeliveryMutationLeaseLost:
		return "lease_lost"
	case DeliveryMutationAmbiguous:
		return "ambiguous"
	default:
		return "unknown"
	}
}

type DeliveryControlStatus uint8

const (
	DeliveryControlNone DeliveryControlStatus = iota
	DeliveryControlCancelRequested
	DeliveryControlTerminated
)

func (s DeliveryControlStatus) Valid() bool {
	return s >= DeliveryControlNone && s <= DeliveryControlTerminated
}

func (s DeliveryControlStatus) String() string {
	switch s {
	case DeliveryControlNone:
		return "none"
	case DeliveryControlCancelRequested:
		return "cancel_requested"
	case DeliveryControlTerminated:
		return "terminated"
	default:
		return "unknown"
	}
}

type DeliveryCommandResult struct {
	mutation DeliveryMutationStatus
	control  DeliveryControlStatus
}

func NewDeliveryCommandResult(mutation DeliveryMutationStatus, control DeliveryControlStatus) (DeliveryCommandResult, error) {
	if !mutation.Valid() || !control.Valid() {
		return DeliveryCommandResult{}, invalid("delivery command result")
	}
	return DeliveryCommandResult{mutation: mutation, control: control}, nil
}

func (r DeliveryCommandResult) Mutation() DeliveryMutationStatus { return r.mutation }
func (r DeliveryCommandResult) Control() DeliveryControlStatus   { return r.control }
func (r DeliveryCommandResult) IsZero() bool                     { return r.mutation == 0 }
func (DeliveryCommandResult) String() string                     { return "[job delivery command result]" }
func (r DeliveryCommandResult) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, r.String())
}
func (r DeliveryCommandResult) LogValue() slog.Value { return slog.StringValue(r.String()) }
func (DeliveryCommandResult) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: delivery command result cannot be serialized", ErrUnsupported)
}

type DeliveryApplication struct {
	kind       DeliveryCommandKind
	invocation Invocation
	attempt    Attempt
	changed    bool
	release    DeliveryRelease
	proof      [32]byte
}

func (a DeliveryApplication) Kind() DeliveryCommandKind { return a.kind }
func (a DeliveryApplication) Invocation() Invocation    { return a.invocation }
func (a DeliveryApplication) Attempt() (Attempt, bool)  { return a.attempt, !a.attempt.IsZero() }
func (a DeliveryApplication) Changed() bool             { return a.changed }
func (a DeliveryApplication) RequiresFence() bool {
	return a.kind.Valid() && a.proof != [32]byte{}
}
func (a DeliveryApplication) Release() (DeliveryRelease, bool) {
	return a.release, !a.release.IsZero()
}
func (DeliveryApplication) String() string { return "[job delivery application]" }
func (a DeliveryApplication) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, a.String())
}
func (a DeliveryApplication) LogValue() slog.Value { return slog.StringValue(a.String()) }
func (DeliveryApplication) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: delivery application cannot be serialized", ErrUnsupported)
}

func ApplyDeliveryCommand(current Invocation, command DeliveryCommand, now time.Time) (DeliveryApplication, error) {
	command, err := validateDeliveryCommand(command)
	if err != nil {
		return DeliveryApplication{}, err
	}
	now, err = requiredTime(now, "delivery command time")
	if err != nil {
		return DeliveryApplication{}, err
	}
	if command.kind == DeliveryCommandRejectCorrupt {
		if !current.IsZero() {
			return DeliveryApplication{}, transitionConflict("restored invocation cannot be rejected as corrupt")
		}
		return DeliveryApplication{kind: command.kind, changed: true, proof: digestDeliveryCommand(command)}, nil
	}
	if current.IsZero() || current.ID() != command.lease.invocation {
		return DeliveryApplication{}, ErrLeaseLost
	}
	application := DeliveryApplication{kind: command.kind, invocation: current, proof: digestDeliveryCommand(command)}
	switch command.kind {
	case DeliveryCommandBeginAttempt:
		application.invocation, application.attempt, err = current.beginAttemptOrExpire(BeginAttemptSpec{Binding: command.binding, Build: command.build, StartedAt: now})
		application.changed = err == nil
	case DeliveryCommandProgress:
		if current.attempts == nil {
			return DeliveryApplication{}, transitionConflict("invocation has no active attempt")
		}
		application.invocation, application.attempt, err = current.RecordProgress(current.attempts.value, now)
		application.changed = err == nil && application.attempt.Record() != current.attempts.value.Record()
	case DeliveryCommandFinishAttempt:
		if current.attempts == nil {
			return DeliveryApplication{}, transitionConflict("invocation has no active attempt")
		}
		application.invocation, application.attempt, err = current.finishAttemptAuthoritatively(current.attempts.value, command.disposition, now, command.delay, command.deadlineDelay)
		application.changed = err == nil
	case DeliveryCommandArbitrateAttemptDeadline:
		if current.attempts == nil {
			return DeliveryApplication{}, transitionConflict("invocation has no active attempt")
		}
		application.invocation, application.attempt, application.changed, err = current.arbitrateAttemptDeadline(current.attempts.value, now, command.deadlineDelay)
	case DeliveryCommandRevokeAttempt:
		if current.attempts == nil {
			return DeliveryApplication{}, transitionConflict("invocation has no active attempt")
		}
		application.invocation, application.attempt, err = current.revokeAttempt(current.attempts.value, command.reason, now, command.delay, command.deadlineDelay)
		application.changed = err == nil
	case DeliveryCommandDeferDelivery:
		availableAt, timeErr := relativeDeliveryTime(now, command.delay)
		if timeErr != nil {
			return DeliveryApplication{}, timeErr
		}
		application.invocation, err = current.DeferDelivery(DeferDeliverySpec{Reason: command.reason, Failure: command.failure, ObservedAt: now, AvailableAt: availableAt})
		application.changed = err == nil
	case DeliveryCommandFinishDelivery:
		application.invocation, err = current.FinishDelivery(FinishDeliverySpec{State: command.state, Reason: command.reason, Failure: command.failure, ObservedAt: now})
		application.changed = err == nil
	case DeliveryCommandReleaseUnchanged:
		var availableAt time.Time
		if current.state == InvocationQueued && !now.Before(current.readyAt()) && current.deadlineReason(now) == ReasonNone {
			availableAt, err = releaseAvailability(current, now, command.delay)
			if err != nil {
				return DeliveryApplication{}, err
			}
		}
		validated, validationErr := current.releaseUnchanged(command.reason, now, availableAt)
		if validationErr != nil {
			return DeliveryApplication{}, validationErr
		}
		if validated.State() != InvocationQueued {
			application.invocation = validated
			application.changed = true
			break
		}
		application.release = DeliveryRelease{availableAt: availableAt, binding: command.binding, build: command.build, reason: command.reason}
		application.changed = false
	default:
		err = invalid("delivery command kind")
	}
	if err != nil {
		return DeliveryApplication{}, err
	}
	return application, nil
}

func validateDeliveryCommand(command DeliveryCommand) (DeliveryCommand, error) {
	if !command.kind.Valid() || !command.lease.valid() {
		return DeliveryCommand{}, invalid("delivery command")
	}
	switch command.kind {
	case DeliveryCommandBeginAttempt:
		if !command.binding.valid() || !command.build.valid() || !command.disposition.IsZero() || command.delay != 0 || command.deadlineDelay != 0 || command.reason != ReasonNone || !command.failure.IsZero() || command.state != 0 {
			return DeliveryCommand{}, invalid("begin attempt command")
		}
	case DeliveryCommandProgress, DeliveryCommandRejectCorrupt:
		if !command.binding.IsZero() || !command.build.IsZero() || !command.disposition.IsZero() || command.delay != 0 || command.deadlineDelay != 0 || command.reason != ReasonNone || !command.failure.IsZero() || command.state != 0 {
			return DeliveryCommand{}, invalid("empty delivery command")
		}
	case DeliveryCommandReleaseUnchanged:
		compatibility := command.reason == ReasonCompatibility && command.binding.valid() && command.build.valid()
		shutdown := command.reason == ReasonShutdown && command.binding.IsZero() && command.build.IsZero()
		if !compatibility && !shutdown || !command.disposition.IsZero() || !command.failure.IsZero() || command.state != 0 || command.deadlineDelay != 0 || !validCommandDelay(true, command.delay) {
			return DeliveryCommand{}, invalid("release unchanged command")
		}
	case DeliveryCommandFinishAttempt:
		rescheduled := command.disposition.kind == DispositionRetry || command.disposition.kind == DispositionDeferred
		timeout := command.disposition.reason == ReasonAttemptTimeout || command.disposition.reason == ReasonProgressTimeout
		if !command.binding.IsZero() || !command.build.IsZero() || !command.disposition.valid() || !command.disposition.allowedForAttempt() || command.disposition.kind == DispositionTerminated || timeout || command.reason != ReasonNone || !command.failure.IsZero() || command.state != 0 || !validCommandDelay(rescheduled, command.delay) || !validCommandDelay(true, command.deadlineDelay) || rescheduled && command.delay < command.disposition.retryAfter || command.disposition.kind == DispositionDeferred && command.delay != command.disposition.retryAfter {
			return DeliveryCommand{}, invalid("finish attempt command")
		}
	case DeliveryCommandDeferDelivery:
		if !command.binding.IsZero() || !command.build.IsZero() || !command.disposition.IsZero() || !deliveryDeferralReason(command.reason) || !validOptionalFailure(command.failure) || command.state != 0 || command.deadlineDelay != 0 || !validCommandDelay(true, command.delay) {
			return DeliveryCommand{}, invalid("defer delivery command")
		}
	case DeliveryCommandFinishDelivery:
		if !command.binding.IsZero() || !command.build.IsZero() || !command.disposition.IsZero() || command.delay != 0 || command.deadlineDelay != 0 || command.state != InvocationDiscarded && command.state != InvocationQuarantined || command.reason != ReasonPayload && command.reason != ReasonCompatibility || !validOptionalFailure(command.failure) {
			return DeliveryCommand{}, invalid("finish delivery command")
		}
	case DeliveryCommandArbitrateAttemptDeadline:
		if !command.binding.IsZero() || !command.build.IsZero() || !command.disposition.IsZero() || command.delay != 0 || !validCommandDelay(true, command.deadlineDelay) || command.reason != ReasonNone || !command.failure.IsZero() || command.state != 0 {
			return DeliveryCommand{}, invalid("arbitrate attempt deadline command")
		}
	case DeliveryCommandRevokeAttempt:
		if !command.binding.IsZero() || !command.build.IsZero() || !command.disposition.IsZero() || !validCommandDelay(true, command.delay) || command.deadlineDelay != command.delay || command.reason != ReasonShutdown && command.reason != ReasonLeaseLost || !command.failure.IsZero() || command.state != 0 {
			return DeliveryCommand{}, invalid("revoke attempt command")
		}
	default:
		return DeliveryCommand{}, invalid("delivery command kind")
	}
	command.lease = cloneLeaseRef(command.lease)
	return command, nil
}

type DeliveryRelease struct {
	availableAt time.Time
	binding     BindingName
	build       BuildID
	reason      Reason
}

func (r DeliveryRelease) AvailableAt() time.Time { return r.availableAt }
func (r DeliveryRelease) ExcludedBinding() BindingName {
	return r.binding
}
func (r DeliveryRelease) ExcludedBuild() BuildID { return r.build }
func (r DeliveryRelease) Reason() Reason         { return r.reason }
func (r DeliveryRelease) IsZero() bool {
	return r.availableAt.IsZero() && r.binding.IsZero() && r.build.IsZero() && r.reason == ReasonNone
}
func (DeliveryRelease) String() string { return "[job delivery release]" }
func (r DeliveryRelease) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, r.String())
}
func (r DeliveryRelease) LogValue() slog.Value { return slog.StringValue(r.String()) }
func (DeliveryRelease) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: delivery release cannot be serialized", ErrUnsupported)
}

func validCommandDelay(required bool, delay time.Duration) bool {
	if !required {
		return delay == 0
	}
	return delay >= MinRetryDelay && delay <= MaxRetryDelay
}

func relativeDeliveryTime(now time.Time, delay time.Duration) (time.Time, error) {
	if delay == 0 {
		return time.Time{}, nil
	}
	return requiredTime(now.Add(delay), "delivery command availability")
}

func releaseAvailability(invocation Invocation, observedAt time.Time, delay time.Duration) (time.Time, error) {
	availableAt, err := relativeDeliveryTime(observedAt, delay)
	if err == nil {
		return availableAt, nil
	}
	deadline, _ := invocation.deliveryDeadline()
	if observedAt.Before(deadline) && delay >= deadline.Sub(observedAt) {
		return deadline, nil
	}
	return time.Time{}, err
}

func cloneLeaseRef(reference LeaseRef) LeaseRef {
	reference.token = bytes.Clone(reference.token)
	return reference
}

func (a DeliveryApplication) matches(command DeliveryCommand) bool {
	return a.RequiresFence() && a.proof == digestDeliveryCommand(command)
}

func digestDeliveryCommand(command DeliveryCommand) [32]byte {
	digest := sha256.New()
	writePlacementString(digest, "frostgrove.jobs.delivery-command.v2")
	writePlacementUint(digest, uint64(command.kind))
	writePlacementBytes(digest, command.lease.binding[:])
	writePlacementString(digest, command.binding.value)
	writePlacementString(digest, command.build.value)
	writePlacementUint(digest, uint64(command.disposition.kind))
	writePlacementUint(digest, uint64(command.disposition.reason))
	writePlacementUint(digest, uint64(command.disposition.retryAfter))
	writePlacementUint(digest, uint64(command.disposition.retryCost))
	writePlacementString(digest, command.disposition.failure.code.value)
	writePlacementString(digest, command.disposition.failure.message)
	writePlacementUint(digest, uint64(command.delay))
	writePlacementUint(digest, uint64(command.deadlineDelay))
	writePlacementUint(digest, uint64(command.reason))
	writePlacementString(digest, command.failure.code.value)
	writePlacementString(digest, command.failure.message)
	writePlacementUint(digest, uint64(command.state))
	var result [32]byte
	copy(result[:], digest.Sum(nil))
	return result
}

func digestLeaseRef(backend BackendID, invocation InvocationID, token []byte) [32]byte {
	digest := sha256.New()
	writePlacementString(digest, "frostgrove.jobs.lease-reference.v1")
	backendValue := backend.Bytes()
	writePlacementBytes(digest, backendValue[:])
	invocationValue := invocation.Bytes()
	writePlacementBytes(digest, invocationValue[:])
	writePlacementBytes(digest, token)
	var binding [32]byte
	copy(binding[:], digest.Sum(nil))
	return binding
}
