package jobspg

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/frostgrove/vv/jobs"
)

type recordDTO struct {
	Version       int
	Genesis       genesisDTO
	Payload       payloadDTO
	WireDigest    [32]byte
	PayloadDigest *payloadDigestDTO
	Outcomes      []outcomeDTO
	Attempts      []attemptDTO
}

type genesisDTO struct {
	ID                [16]byte
	Application       string
	Environment       string
	PartitionBinding  [32]byte
	PartitionRevision jobs.DigestRevision
	PartitionDigest   [32]byte
	PartitionGlobal   bool
	Definition        string
	Queue             string
	Mode              jobs.PlacementMode
	IntentScope       [jobs.IntentScopeBytes]byte
	IntentRevision    jobs.DigestRevision
	IntentPurpose     jobs.IntentPurpose
	IntentDigest      [jobs.IntentDigestBytes]byte
	LegacyIntent      string
	Priority          int
	CreatedAt         time.Time
	EligibleAt        time.Time
	StartBefore       time.Time
	Policy            policyDTO
	Context           contextDTO
}

type payloadDTO struct {
	Codec   string
	Version jobs.SchemaVersion
	Data    []byte
}

type payloadDigestDTO struct {
	Identity string
	Version  jobs.SchemaVersion
	Value    [32]byte
}

type policyDTO struct {
	Queue                string
	Priority             int
	AttemptTimeout       time.Duration
	ProgressTimeout      time.Duration
	MaxElapsed           time.Duration
	MaxRetries           uint16
	MaxHandlerDeferrals  uint16
	MaxDeliveryDeferrals uint16
	BackoffInitial       time.Duration
	BackoffMaximum       time.Duration
	BackoffJitter        jobs.JitterMode
	Retention            time.Duration
	IntentRetention      time.Duration
	Payload              jobs.PayloadLimit
	AcceptedAckModes     []jobs.AckMode
	ProtectedFailures    []jobs.Failure
	TraceKeys            []string
}

type contextDTO struct {
	Scope        jobs.ContextScope
	Tenant       [32]byte
	Actor        [32]byte
	Token        []byte
	Provenance   string
	Epoch        uint64
	TraceParent  string
	TraceState   string
	Correlations []correlationDTO
	Binding      [32]byte
}

type correlationDTO struct {
	Key   string
	Value string
}

type failureDTO struct {
	Code    string
	Message string
}

type dispositionDTO struct {
	Kind       jobs.DispositionKind
	Reason     jobs.Reason
	RetryAfter time.Duration
	RetryCost  jobs.RetryCost
	Failure    failureDTO
}

type outcomeDTO struct {
	Kind           jobs.InvocationOutcomeKind
	Attempt        uint16
	Disposition    dispositionDTO
	Reason         jobs.Reason
	Failure        failureDTO
	TerminalReason jobs.Reason
	TerminalState  jobs.InvocationState
	OccurredAt     time.Time
	AvailableAt    time.Time
}

type attemptDTO struct {
	Invocation       [16]byte
	Ordinal          uint16
	Binding          string
	Build            string
	State            jobs.AttemptState
	StartedAt        time.Time
	Deadline         time.Time
	ProgressedAt     time.Time
	ProgressDeadline time.Time
	FinishedAt       time.Time
	Disposition      dispositionDTO
}

func encodeRecord(record jobs.DeliveryRecord) ([]byte, error) {
	if _, err := record.Size(); err != nil {
		return nil, err
	}
	dto := recordDTO{
		Version:    1,
		Genesis:    encodeGenesis(record.Genesis),
		Payload:    payloadDTO{Codec: record.Payload.Codec.Value(), Version: record.Payload.Version, Data: record.Payload.Data},
		WireDigest: record.WireDigest.Bytes(),
		Outcomes:   make([]outcomeDTO, len(record.Outcomes)),
		Attempts:   make([]attemptDTO, len(record.Attempts)),
	}
	if !record.PayloadDigest.IsZero() {
		dto.PayloadDigest = &payloadDigestDTO{Identity: record.PayloadDigest.Identity().Value(), Version: record.PayloadDigest.Version(), Value: record.PayloadDigest.Bytes()}
	}
	for index, outcome := range record.Outcomes {
		dto.Outcomes[index] = encodeOutcome(outcome)
	}
	for index, attempt := range record.Attempts {
		dto.Attempts[index] = encodeAttempt(attempt)
	}
	encoded, err := json.Marshal(dto)
	if err != nil {
		return nil, fmt.Errorf("jobspg: encode delivery record: %w", err)
	}
	return encoded, nil
}

func decodeRecord(encoded []byte) (jobs.DeliveryRecord, error) {
	var dto recordDTO
	if err := json.Unmarshal(encoded, &dto); err != nil {
		return jobs.DeliveryRecord{}, fmt.Errorf("jobspg: decode delivery record: %w", err)
	}
	if dto.Version != 1 {
		return jobs.DeliveryRecord{}, fmt.Errorf("jobspg: unsupported delivery record version %d", dto.Version)
	}
	genesis, err := decodeGenesis(dto.Genesis)
	if err != nil {
		return jobs.DeliveryRecord{}, err
	}
	codec, err := jobs.ParseCodecID(dto.Payload.Codec)
	if err != nil {
		return jobs.DeliveryRecord{}, err
	}
	wireDigest, err := jobs.WireDigestFromBytes(dto.WireDigest)
	if err != nil {
		return jobs.DeliveryRecord{}, err
	}
	var payloadDigest jobs.PayloadDigest
	if dto.PayloadDigest != nil {
		identity, parseErr := jobs.ParseCodecID(dto.PayloadDigest.Identity)
		if parseErr != nil {
			return jobs.DeliveryRecord{}, parseErr
		}
		payloadDigest, err = jobs.NewPayloadDigest(identity, dto.PayloadDigest.Version, dto.PayloadDigest.Value)
		if err != nil {
			return jobs.DeliveryRecord{}, err
		}
	}
	outcomes := make([]jobs.InvocationOutcome, len(dto.Outcomes))
	for index, stored := range dto.Outcomes {
		outcomes[index], err = decodeOutcome(stored)
		if err != nil {
			return jobs.DeliveryRecord{}, err
		}
	}
	attempts := make([]jobs.AttemptRecord, len(dto.Attempts))
	for index, stored := range dto.Attempts {
		attempts[index], err = decodeAttempt(stored)
		if err != nil {
			return jobs.DeliveryRecord{}, err
		}
	}
	record := jobs.DeliveryRecord{
		Genesis:       genesis,
		Payload:       jobs.EncodedPayloadRecord{Codec: codec, Version: dto.Payload.Version, Data: dto.Payload.Data},
		WireDigest:    wireDigest,
		PayloadDigest: payloadDigest,
		Outcomes:      outcomes,
		Attempts:      attempts,
	}
	if _, err := record.Size(); err != nil {
		return jobs.DeliveryRecord{}, err
	}
	return record, nil
}

func encodeGenesis(genesis jobs.InvocationGenesisRecord) genesisDTO {
	partitionBinding := genesis.Partition.NamespaceBinding().Bytes()
	partitionDigest := genesis.Partition.Digest().Bytes()
	intentScope := genesis.Intent.Scope().Bytes()
	intentDigest := genesis.Intent.Digest().Bytes()
	return genesisDTO{
		ID:                genesis.ID.Bytes(),
		Application:       genesis.Namespace.Application().Value(),
		Environment:       genesis.Namespace.Environment().Value(),
		PartitionBinding:  partitionBinding,
		PartitionRevision: genesis.Partition.Revision(),
		PartitionDigest:   partitionDigest,
		PartitionGlobal:   genesis.Partition.Global(),
		Definition:        genesis.Definition.Value(),
		Queue:             genesis.Queue.Value(),
		Mode:              genesis.Mode,
		IntentScope:       intentScope,
		IntentRevision:    genesis.Intent.Revision(),
		IntentPurpose:     genesis.Intent.Purpose(),
		IntentDigest:      intentDigest,
		LegacyIntent:      genesis.LegacyIntent.Value(),
		Priority:          genesis.Priority,
		CreatedAt:         genesis.CreatedAt,
		EligibleAt:        genesis.EligibleAt,
		StartBefore:       genesis.StartBefore,
		Policy:            encodePolicy(genesis.Policy),
		Context:           encodeContext(genesis.Context),
	}
}

func decodeGenesis(dto genesisDTO) (jobs.InvocationGenesisRecord, error) {
	id, err := jobs.InvocationIDFromBytes(dto.ID)
	if err != nil {
		return jobs.InvocationGenesisRecord{}, err
	}
	namespace, err := jobs.NamespaceOf(dto.Application, dto.Environment)
	if err != nil {
		return jobs.InvocationGenesisRecord{}, err
	}
	binding, err := jobs.PartitionNamespaceBindingFromBytes(dto.PartitionBinding)
	if err != nil {
		return jobs.InvocationGenesisRecord{}, err
	}
	digest, err := jobs.PartitionDigestFromBytes(dto.PartitionDigest)
	if err != nil {
		return jobs.InvocationGenesisRecord{}, err
	}
	partition, err := jobs.RestorePartitionKey(namespace, binding, dto.PartitionRevision, digest, dto.PartitionGlobal)
	if err != nil {
		return jobs.InvocationGenesisRecord{}, err
	}
	definition, err := jobs.ParseName(dto.Definition)
	if err != nil {
		return jobs.InvocationGenesisRecord{}, err
	}
	queue, err := jobs.ParseQueueName(dto.Queue)
	if err != nil {
		return jobs.InvocationGenesisRecord{}, err
	}
	intentScope, err := jobs.IntentScopeBindingFromBytes(dto.IntentScope)
	if err != nil {
		return jobs.InvocationGenesisRecord{}, err
	}
	intentDigest, err := jobs.IntentDigestFromBytes(dto.IntentDigest)
	if err != nil {
		return jobs.InvocationGenesisRecord{}, err
	}
	intent, err := jobs.NewIntentKey(intentScope, dto.IntentRevision, dto.IntentPurpose, intentDigest)
	if err != nil {
		return jobs.InvocationGenesisRecord{}, err
	}
	var legacy jobs.LegacyIntent
	if dto.LegacyIntent != "" {
		legacy, err = jobs.RestoreLegacyIntent(dto.LegacyIntent)
		if err != nil {
			return jobs.InvocationGenesisRecord{}, err
		}
	}
	policy, err := decodePolicy(dto.Policy)
	if err != nil {
		return jobs.InvocationGenesisRecord{}, err
	}
	contextRecord := decodeContext(dto.Context)
	return jobs.InvocationGenesisRecord{
		ID:           id,
		Namespace:    namespace,
		Partition:    partition,
		Definition:   definition,
		Queue:        queue,
		Mode:         dto.Mode,
		Intent:       intent,
		LegacyIntent: legacy,
		Priority:     dto.Priority,
		CreatedAt:    dto.CreatedAt,
		EligibleAt:   dto.EligibleAt,
		StartBefore:  dto.StartBefore,
		Policy:       policy,
		Context:      contextRecord,
	}, nil
}

func encodePolicy(policy jobs.PolicySnapshot) policyDTO {
	traceKeys := policy.Trace().Keys()
	storedKeys := make([]string, len(traceKeys))
	for index, key := range traceKeys {
		storedKeys[index] = key.Value()
	}
	return policyDTO{
		Queue:                policy.Queue().Value(),
		Priority:             policy.Priority(),
		AttemptTimeout:       policy.AttemptTimeout(),
		ProgressTimeout:      policy.ProgressTimeout(),
		MaxElapsed:           policy.MaxElapsed(),
		MaxRetries:           policy.RetryLimit().Value(),
		MaxHandlerDeferrals:  policy.HandlerDeferralLimit().Value(),
		MaxDeliveryDeferrals: policy.DeliveryDeferralLimit().Value(),
		BackoffInitial:       policy.Backoff().Initial,
		BackoffMaximum:       policy.Backoff().Maximum,
		BackoffJitter:        policy.Backoff().Jitter,
		Retention:            policy.Retention(),
		IntentRetention:      policy.IntentRetention(),
		Payload:              policy.Payload(),
		AcceptedAckModes:     policy.Durability().AcceptedAckModes().Values(),
		ProtectedFailures:    policy.Durability().ProtectedFailures().Values(),
		TraceKeys:            storedKeys,
	}
}

func decodePolicy(dto policyDTO) (jobs.PolicySnapshot, error) {
	queue, err := jobs.ParseQueueName(dto.Queue)
	if err != nil {
		return jobs.PolicySnapshot{}, err
	}
	ackModes, err := jobs.AckModes(dto.AcceptedAckModes...)
	if err != nil {
		return jobs.PolicySnapshot{}, err
	}
	failures, err := jobs.Failures(dto.ProtectedFailures...)
	if err != nil {
		return jobs.PolicySnapshot{}, err
	}
	durability, err := jobs.NewDurabilityRequirement(ackModes, failures)
	if err != nil {
		return jobs.PolicySnapshot{}, err
	}
	keys := make([]jobs.CorrelationKey, len(dto.TraceKeys))
	for index, raw := range dto.TraceKeys {
		keys[index], err = jobs.ParseCorrelationKey(raw)
		if err != nil {
			return jobs.PolicySnapshot{}, err
		}
	}
	trace, err := jobs.NewTracePolicy(keys...)
	if err != nil {
		return jobs.PolicySnapshot{}, err
	}
	return jobs.NewPolicySnapshot(jobs.Policy{
		Queue:                queue,
		Priority:             dto.Priority,
		AttemptTimeout:       dto.AttemptTimeout,
		ProgressTimeout:      dto.ProgressTimeout,
		MaxElapsed:           dto.MaxElapsed,
		MaxRetries:           int(dto.MaxRetries),
		MaxHandlerDeferrals:  int(dto.MaxHandlerDeferrals),
		MaxDeliveryDeferrals: int(dto.MaxDeliveryDeferrals),
		Backoff:              jobs.Exponential(dto.BackoffInitial, dto.BackoffMaximum, dto.BackoffJitter),
		Retention:            dto.Retention,
		IntentRetention:      dto.IntentRetention,
		Payload:              dto.Payload,
		Durability:           durability,
		Trace:                trace,
	})
}

func encodeContext(record jobs.DurableContextRecord) contextDTO {
	correlations := make([]correlationDTO, len(record.Trace.Correlations))
	for index, field := range record.Trace.Correlations {
		correlations[index] = correlationDTO{Key: field.Key, Value: field.Value}
	}
	return contextDTO{
		Scope:        record.Scope,
		Tenant:       record.Tenant,
		Actor:        record.Actor,
		Token:        record.Token,
		Provenance:   record.Provenance,
		Epoch:        record.Epoch,
		TraceParent:  record.Trace.TraceParent,
		TraceState:   record.Trace.TraceState,
		Correlations: correlations,
		Binding:      record.Binding,
	}
}

func decodeContext(dto contextDTO) jobs.DurableContextRecord {
	correlations := make([]jobs.CorrelationRecord, len(dto.Correlations))
	for index, field := range dto.Correlations {
		correlations[index] = jobs.CorrelationRecord{Key: field.Key, Value: field.Value}
	}
	return jobs.DurableContextRecord{
		Scope:      dto.Scope,
		Tenant:     dto.Tenant,
		Actor:      dto.Actor,
		Token:      dto.Token,
		Provenance: dto.Provenance,
		Epoch:      dto.Epoch,
		Trace:      jobs.TraceCarrierRecord{TraceParent: dto.TraceParent, TraceState: dto.TraceState, Correlations: correlations},
		Binding:    dto.Binding,
	}
}

func encodeFailure(failure jobs.PublicFailure) failureDTO {
	return failureDTO{Code: failure.Code().Value(), Message: failure.Message()}
}

func decodeFailure(dto failureDTO) (jobs.PublicFailure, error) {
	if dto.Code == "" && dto.Message == "" {
		return jobs.PublicFailure{}, nil
	}
	code, err := jobs.ParseFailureCode(dto.Code)
	if err != nil {
		return jobs.PublicFailure{}, err
	}
	return jobs.NewPublicFailure(code, dto.Message)
}

func encodeDisposition(disposition jobs.Disposition) dispositionDTO {
	return dispositionDTO{
		Kind:       disposition.Kind(),
		Reason:     disposition.Reason(),
		RetryAfter: disposition.RetryAfter(),
		RetryCost:  disposition.RetryCost(),
		Failure:    encodeFailure(disposition.Failure()),
	}
}

func decodeDisposition(dto dispositionDTO) (jobs.Disposition, error) {
	if dto.Kind == 0 {
		return jobs.Disposition{}, nil
	}
	failure, err := decodeFailure(dto.Failure)
	if err != nil {
		return jobs.Disposition{}, err
	}
	return jobs.NewDisposition(jobs.DispositionSpec{Kind: dto.Kind, Reason: dto.Reason, RetryAfter: dto.RetryAfter, RetryCost: dto.RetryCost, Failure: failure})
}

func encodeOutcome(outcome jobs.InvocationOutcome) outcomeDTO {
	return outcomeDTO{
		Kind:           outcome.Kind(),
		Attempt:        outcome.AttemptOrdinal().Value(),
		Disposition:    encodeDisposition(outcome.Disposition()),
		Reason:         outcome.Reason(),
		Failure:        encodeFailure(outcome.Failure()),
		TerminalReason: outcome.TerminalReason(),
		TerminalState:  outcome.TerminalState(),
		OccurredAt:     outcome.OccurredAt(),
		AvailableAt:    outcome.AvailableAt(),
	}
}

func decodeOutcome(dto outcomeDTO) (jobs.InvocationOutcome, error) {
	ordinal, err := jobs.NewAttemptOrdinal(dto.Attempt)
	if err != nil {
		return jobs.InvocationOutcome{}, err
	}
	switch dto.Kind {
	case jobs.InvocationOutcomeInitial:
		return jobs.InitialInvocationOutcome(), nil
	case jobs.InvocationOutcomeAttemptActive:
		return jobs.ActiveAttemptOutcome(ordinal, dto.OccurredAt)
	case jobs.InvocationOutcomeAttemptFinished:
		disposition, decodeErr := decodeDisposition(dto.Disposition)
		if decodeErr != nil {
			return jobs.InvocationOutcome{}, decodeErr
		}
		return jobs.FinishedAttemptOutcome(ordinal, disposition, dto.TerminalReason, dto.OccurredAt, dto.AvailableAt)
	case jobs.InvocationOutcomeDeliveryDeferred:
		failure, decodeErr := decodeFailure(dto.Failure)
		if decodeErr != nil {
			return jobs.InvocationOutcome{}, decodeErr
		}
		return jobs.DeferredDeliveryOutcome(dto.Reason, failure, dto.OccurredAt, dto.AvailableAt)
	case jobs.InvocationOutcomeCancelRequested:
		return jobs.CancelRequestedOutcome(ordinal, dto.OccurredAt)
	case jobs.InvocationOutcomeDeliveryTerminal:
		failure, decodeErr := decodeFailure(dto.Failure)
		if decodeErr != nil {
			return jobs.InvocationOutcome{}, decodeErr
		}
		return jobs.TerminalDeliveryOutcome(dto.TerminalState, dto.Reason, dto.TerminalReason, failure, dto.OccurredAt, dto.AvailableAt)
	default:
		return jobs.InvocationOutcome{}, fmt.Errorf("jobspg: invalid invocation outcome kind %d", dto.Kind)
	}
}

func encodeAttempt(attempt jobs.AttemptRecord) attemptDTO {
	return attemptDTO{
		Invocation:       attempt.Invocation.Bytes(),
		Ordinal:          attempt.Ordinal.Value(),
		Binding:          attempt.Binding.Value(),
		Build:            attempt.Build.Value(),
		State:            attempt.State,
		StartedAt:        attempt.StartedAt,
		Deadline:         attempt.Deadline,
		ProgressedAt:     attempt.ProgressedAt,
		ProgressDeadline: attempt.ProgressDeadline,
		FinishedAt:       attempt.FinishedAt,
		Disposition:      encodeDisposition(attempt.Disposition),
	}
}

func decodeAttempt(dto attemptDTO) (jobs.AttemptRecord, error) {
	invocation, err := jobs.InvocationIDFromBytes(dto.Invocation)
	if err != nil {
		return jobs.AttemptRecord{}, err
	}
	ordinal, err := jobs.NewAttemptOrdinal(dto.Ordinal)
	if err != nil {
		return jobs.AttemptRecord{}, err
	}
	binding, err := jobs.ParseBindingName(dto.Binding)
	if err != nil {
		return jobs.AttemptRecord{}, err
	}
	build, err := jobs.ParseBuildID(dto.Build)
	if err != nil {
		return jobs.AttemptRecord{}, err
	}
	disposition, err := decodeDisposition(dto.Disposition)
	if err != nil {
		return jobs.AttemptRecord{}, err
	}
	return jobs.AttemptRecord{
		Invocation:       invocation,
		Ordinal:          ordinal,
		Binding:          binding,
		Build:            build,
		State:            dto.State,
		StartedAt:        dto.StartedAt,
		Deadline:         dto.Deadline,
		ProgressedAt:     dto.ProgressedAt,
		ProgressDeadline: dto.ProgressDeadline,
		FinishedAt:       dto.FinishedAt,
		Disposition:      disposition,
	}, nil
}
