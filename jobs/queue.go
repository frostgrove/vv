package jobs

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

var (
	ErrDriver  = errors.New("jobs: driver operation failed")
	ErrEntropy = errors.New("jobs: entropy unavailable")
)

type DefinitionOf[P any] interface {
	Declaration
	Name() Name
	Policy() Policy
	Partition() PartitionMode
	PayloadIdentity() PayloadIdentityDescription
	Encode(P) (EncodedPayload, error)
	Digest(P) (PayloadDigest, error)
	Decode(EncodedPayload) (P, error)
	decodeOwned(EncodedPayload) (P, error)
	preparePayload(P, bool) (EncodedPayload, PayloadDigest, error)
}

type Sender interface {
	Description() BackendDescription
	Place(context.Context, Placement) (PlacementResult, error)
}

type Stager interface {
	Transaction() TransactionContext
	Stage(context.Context, Placement) (Staged, error)
}

type QueueSpec struct {
	Namespace    Namespace
	Catalog      Catalog
	Sender       Sender
	Context      TrustedContextProvider
	Digests      IntentDigestPlan
	Requirements ProducerRequirements
	Durability   DurabilityRequirement
	Entropy      io.Reader
}

func (QueueSpec) String() string { return "[job queue spec]" }
func (s QueueSpec) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, s.String())
}

type entropySource struct {
	mu     sync.Mutex
	reader io.Reader
}

func (source *entropySource) read(destination []byte) error {
	if source == nil || nilInterface(source.reader) || len(destination) == 0 {
		return ErrEntropy
	}
	source.mu.Lock()
	err := readEntropy(source.reader, destination)
	source.mu.Unlock()
	if err != nil {
		return ErrEntropy
	}
	return nil
}

type Queue struct {
	namespace            Namespace
	catalog              Catalog
	sender               Sender
	description          BackendDescription
	contexts             TrustedContextProvider
	digests              IntentDigestPlan
	requirements         ProducerRequirements
	durability           DurabilityRequirement
	definitionDurability map[Name]DurabilityRequirement
	entropy              *entropySource
}

func NewQueue(spec QueueSpec) (*Queue, error) {
	if !spec.Namespace.valid() || spec.Catalog.Len() == 0 || spec.Catalog.Fingerprint() == "" || nilInterface(spec.Sender) || spec.Catalog.RequiresTenantPartition() && nilInterface(spec.Context) {
		return nil, invalid("queue namespace, catalog, or sender")
	}
	description, err := senderDescription(spec.Sender)
	if err != nil {
		return nil, err
	}
	requirements := spec.Requirements
	if requirements.IsZero() {
		requirements = StandardProducerRequirements()
	}
	if !requirements.valid() {
		return nil, invalid("queue producer requirements")
	}
	if !description.Capabilities().satisfies(requirements.Capabilities()) {
		return nil, fmt.Errorf("%w: sender does not satisfy the producer contract", ErrUnsupported)
	}
	durability, err := resolveQueueDurability(spec.Catalog, spec.Durability, description.Durability())
	if err != nil {
		return nil, err
	}
	digests := spec.Digests
	if digests.IsZero() {
		digests = CurrentIntentDigestPlan()
	}
	if !digests.valid() {
		return nil, invalid("queue intent digest plan")
	}
	if nilInterface(spec.Entropy) {
		spec.Entropy = rand.Reader
	}
	return &Queue{
		namespace:            spec.Namespace,
		catalog:              spec.Catalog,
		sender:               spec.Sender,
		description:          description,
		contexts:             spec.Context,
		digests:              digests,
		requirements:         requirements,
		durability:           spec.Durability,
		definitionDurability: durability,
		entropy:              &entropySource{reader: spec.Entropy},
	}, nil
}

func resolveQueueDurability(catalog Catalog, global DurabilityRequirement, profile DurabilityProfile) (map[Name]DurabilityRequirement, error) {
	if !global.valid() || !profile.valid() {
		return nil, invalid("queue durability")
	}
	descriptions := catalog.Describe().Definitions
	result := make(map[Name]DurabilityRequirement, len(descriptions))
	for _, description := range descriptions {
		requirement, err := combineDurabilityRequirements(global, description.Policy.Durability)
		if err != nil {
			return nil, fmt.Errorf("job %q durability requirement: %w", description.Name, err)
		}
		if !requirement.accepts(profile) {
			return nil, fmt.Errorf("%w: backend durability does not satisfy job %q", ErrUnsupported, description.Name)
		}
		result[description.Name] = requirement
	}
	return result, nil
}

func (q *Queue) Namespace() Namespace {
	if q == nil {
		return Namespace{}
	}
	return q.namespace
}

func (q *Queue) Backend() BackendID {
	if q == nil {
		return BackendID{}
	}
	return q.description.ID()
}

func (q *Queue) Description() BackendDescription {
	if q == nil {
		return BackendDescription{}
	}
	return q.description
}

func (q *Queue) Durability() DurabilityProfile {
	if q == nil {
		return DurabilityProfile{}
	}
	return q.description.Durability()
}

func (q *Queue) Capabilities() Capabilities {
	if q == nil {
		return Capabilities{}
	}
	return q.description.Capabilities()
}

func (q *Queue) Requirements() ProducerRequirements {
	if q == nil {
		return ProducerRequirements{}
	}
	return q.requirements
}

func (q *Queue) GlobalDurabilityRequirement() DurabilityRequirement {
	if q == nil {
		return DurabilityRequirement{}
	}
	return q.durability
}

func (q *Queue) RequiredDurability(definition Name) (DurabilityRequirement, bool) {
	if q == nil {
		return DurabilityRequirement{}, false
	}
	requirement, ok := q.definitionDurability[definition]
	return requirement, ok
}

func (q *Queue) IntentDigestPlan() IntentDigestPlan {
	if q == nil {
		return IntentDigestPlan{}
	}
	return q.digests
}

func (q *Queue) Catalog() Catalog {
	if q == nil {
		return Catalog{}
	}
	return q.catalog
}

func (*Queue) String() string { return "[job queue]" }
func (q *Queue) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, q.String())
}

type PlacementOutcome uint8

const (
	PlacementCreated PlacementOutcome = iota + 1
	PlacementExistingSamePayload
	PlacementConflict
	PlacementCollapsed
)

func (o PlacementOutcome) Valid() bool {
	return o >= PlacementCreated && o <= PlacementCollapsed
}

func (o PlacementOutcome) String() string {
	switch o {
	case PlacementCreated:
		return "created"
	case PlacementExistingSamePayload:
		return "existing_same_payload"
	case PlacementConflict:
		return "conflict"
	case PlacementCollapsed:
		return "collapsed"
	default:
		return "unknown"
	}
}

type EnqueueOnceOutcome uint8

const (
	EnqueueCreated EnqueueOnceOutcome = iota + 1
	EnqueueExistingSamePayload
	EnqueueConflict
)

func (o EnqueueOnceOutcome) Valid() bool {
	return o >= EnqueueCreated && o <= EnqueueConflict
}

func (o EnqueueOnceOutcome) String() string {
	return PlacementOutcome(o).String()
}

type PlacementResult struct {
	id      InvocationID
	outcome PlacementOutcome
}

func NewPlacementResult(id InvocationID, outcome PlacementOutcome) (PlacementResult, error) {
	if !id.valid() || !outcome.Valid() {
		return PlacementResult{}, invalid("placement result")
	}
	return PlacementResult{id: id, outcome: outcome}, nil
}

func (r PlacementResult) InvocationID() InvocationID     { return r.id }
func (r PlacementResult) Outcome() PlacementOutcome      { return r.outcome }
func (r PlacementResult) IsZero() bool                   { return r.id.IsZero() }
func (r PlacementResult) String() string                 { return "[job placement result]" }
func (r PlacementResult) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, r.String()) }

type Staged struct {
	transaction TransactionContext
	result      PlacementResult
}

func NewStaged(transaction TransactionContext, result PlacementResult) (Staged, error) {
	if !transaction.valid() || result.IsZero() || !result.Outcome().Valid() {
		return Staged{}, invalid("staged placement")
	}
	return Staged{transaction: transaction, result: result}, nil
}

func (s Staged) Backend() BackendID              { return s.transaction.Backend() }
func (s Staged) Transaction() TransactionContext { return s.transaction }
func (s Staged) InvocationID() InvocationID      { return s.result.InvocationID() }
func (s Staged) Outcome() PlacementOutcome       { return s.result.Outcome() }
func (s Staged) IsZero() bool                    { return s.transaction.IsZero() }
func (s Staged) String() string                  { return "[staged job placement]" }
func (s Staged) Format(state fmt.State, _ rune)  { _, _ = fmt.Fprint(state, s.String()) }

type EnqueueOption interface{ applyEnqueue(*enqueueOptions) error }

type enqueueOption func(*enqueueOptions) error

func (o enqueueOption) applyEnqueue(options *enqueueOptions) error { return o(options) }

type enqueueOptions struct {
	delay          time.Duration
	maxDelay       time.Duration
	startBefore    time.Time
	priority       int
	collapse       ProducerIntent
	mode           PlacementMode
	delaySet       bool
	startBeforeSet bool
	prioritySet    bool
	collapseSet    bool
}

func After(delay time.Duration) EnqueueOption {
	return enqueueOption(func(options *enqueueOptions) error {
		if options.delaySet || delay <= 0 || delay > MaxRetention {
			return invalid("enqueue delay")
		}
		options.delay = delay
		options.delaySet = true
		return nil
	})
}

func StartBefore(deadline time.Time) EnqueueOption {
	return enqueueOption(func(options *enqueueOptions) error {
		if options.startBeforeSet {
			return invalid("enqueue start deadline")
		}
		canonical, err := requiredTime(deadline, "enqueue start deadline")
		if err != nil {
			return err
		}
		options.startBefore = canonical
		options.startBeforeSet = true
		return nil
	})
}

func AtPriority(priority int) EnqueueOption {
	return enqueueOption(func(options *enqueueOptions) error {
		if options.prioritySet || priority <= 0 || priority > MaximumPriority {
			return invalid("enqueue priority")
		}
		options.priority = priority
		options.prioritySet = true
		return nil
	})
}

func Collapse(raw string) EnqueueOption {
	intent := Intent(raw)
	return enqueueOption(func(options *enqueueOptions) error {
		if options.collapseSet || !intent.valid() {
			return invalid("collapse key")
		}
		options.collapse = intent
		options.mode = PlacementCollapse
		options.collapseSet = true
		return nil
	})
}

type DebounceOption interface{ applyDebounce(*debounceOptions) error }

type debounceOption func(*debounceOptions) error

func (o debounceOption) applyDebounce(options *debounceOptions) error { return o(options) }

type debounceOptions struct {
	maxDelay    time.Duration
	maxDelaySet bool
}

func MaxDelay(maximum time.Duration) DebounceOption {
	return debounceOption(func(options *debounceOptions) error {
		if options.maxDelaySet || maximum <= 0 || maximum > MaximumCollapseDelay {
			return invalid("debounce maximum delay")
		}
		options.maxDelay = maximum
		options.maxDelaySet = true
		return nil
	})
}

func Debounce(raw string, option DebounceOption) EnqueueOption {
	intent := Intent(raw)
	return enqueueOption(func(options *enqueueOptions) error {
		if options.collapseSet || !intent.valid() || nilInterface(option) {
			return invalid("debounce key or bound")
		}
		var resolved debounceOptions
		if err := option.applyDebounce(&resolved); err != nil {
			return err
		}
		if !resolved.maxDelaySet {
			return invalid("debounce maximum delay")
		}
		options.collapse = intent
		options.mode = PlacementDebounce
		options.maxDelay = resolved.maxDelay
		options.collapseSet = true
		return nil
	})
}

func Enqueue[P any](ctx context.Context, queue *Queue, definition DefinitionOf[P], payload P, options ...EnqueueOption) (InvocationID, error) {
	request, err := validateEnqueueRequest(ctx, queue, definition, ProducerIntent{}, false, options)
	if err != nil {
		return InvocationID{}, err
	}
	durable, err := capturePlacementContext(ctx, queue, definition.Name(), definition.Partition(), request.policy.Trace())
	if err != nil {
		return InvocationID{}, err
	}
	placement, err := preparePlacement(ctx, queue, definition, ProducerIntent{}, false, payload, request, durable)
	if err != nil {
		return InvocationID{}, err
	}
	if err := ctx.Err(); err != nil {
		return InvocationID{}, err
	}
	result, err := place(queue.sender, ctx, placement)
	if err != nil {
		return InvocationID{}, err
	}
	if !validPlacementResult(placement, result) {
		return InvocationID{}, ErrAmbiguous
	}
	return result.id, nil
}

func EnqueueOnce[P any](ctx context.Context, queue *Queue, definition DefinitionOf[P], intent ProducerIntent, payload P, options ...EnqueueOption) (InvocationID, EnqueueOnceOutcome, error) {
	request, err := validateEnqueueRequest(ctx, queue, definition, intent, true, options)
	if err != nil {
		return InvocationID{}, 0, err
	}
	durable, err := capturePlacementContext(ctx, queue, definition.Name(), definition.Partition(), request.policy.Trace())
	if err != nil {
		return InvocationID{}, 0, err
	}
	placement, err := preparePlacement(ctx, queue, definition, intent, true, payload, request, durable)
	if err != nil {
		return InvocationID{}, 0, err
	}
	if err := ctx.Err(); err != nil {
		return InvocationID{}, 0, err
	}
	result, err := place(queue.sender, ctx, placement)
	if err != nil {
		return InvocationID{}, 0, err
	}
	if !validPlacementResult(placement, result) {
		return InvocationID{}, 0, ErrAmbiguous
	}
	return result.id, EnqueueOnceOutcome(result.outcome), nil
}

func EnqueueIn[P any](ctx context.Context, queue *Queue, stager Stager, definition DefinitionOf[P], payload P, options ...EnqueueOption) (Staged, error) {
	request, err := validateEnqueueRequest(ctx, queue, definition, ProducerIntent{}, false, options)
	if err != nil {
		return Staged{}, err
	}
	transaction, err := validateStager(queue, stager, definition.Name())
	if err != nil {
		return Staged{}, err
	}
	durable, err := capturePlacementContext(ctx, queue, definition.Name(), definition.Partition(), request.policy.Trace())
	if err != nil {
		return Staged{}, err
	}
	placement, err := preparePlacement(ctx, queue, definition, ProducerIntent{}, false, payload, request, durable)
	if err != nil {
		return Staged{}, err
	}
	if err := ctx.Err(); err != nil {
		return Staged{}, err
	}
	staged, err := stage(stager, ctx, placement)
	if err != nil {
		return Staged{}, err
	}
	if staged.transaction != transaction || !validPlacementResult(placement, staged.result) {
		return Staged{}, ErrAmbiguous
	}
	return staged, nil
}

func EnqueueOnceIn[P any](ctx context.Context, queue *Queue, stager Stager, definition DefinitionOf[P], intent ProducerIntent, payload P, options ...EnqueueOption) (Staged, error) {
	request, err := validateEnqueueRequest(ctx, queue, definition, intent, true, options)
	if err != nil {
		return Staged{}, err
	}
	transaction, err := validateStager(queue, stager, definition.Name())
	if err != nil {
		return Staged{}, err
	}
	durable, err := capturePlacementContext(ctx, queue, definition.Name(), definition.Partition(), request.policy.Trace())
	if err != nil {
		return Staged{}, err
	}
	placement, err := preparePlacement(ctx, queue, definition, intent, true, payload, request, durable)
	if err != nil {
		return Staged{}, err
	}
	if err := ctx.Err(); err != nil {
		return Staged{}, err
	}
	staged, err := stage(stager, ctx, placement)
	if err != nil {
		return Staged{}, err
	}
	if staged.transaction != transaction || !validPlacementResult(placement, staged.result) {
		return Staged{}, ErrAmbiguous
	}
	return staged, nil
}

type enqueueRequest struct {
	options enqueueOptions
	policy  PolicySnapshot
}

type placementContext struct {
	partition PartitionKey
	durable   DurableContext
}

func validateEnqueueRequest[P any](ctx context.Context, queue *Queue, definition DefinitionOf[P], intent ProducerIntent, once bool, optionValues []EnqueueOption) (enqueueRequest, error) {
	if nilInterface(ctx) || queue == nil || !queue.namespace.valid() || queue.catalog.Len() == 0 || nilInterface(queue.sender) || !queue.description.valid() || !queue.digests.valid() || queue.entropy == nil || nilInterface(definition) {
		return enqueueRequest{}, invalid("enqueue context, queue, or definition")
	}
	if err := ctx.Err(); err != nil {
		return enqueueRequest{}, err
	}
	registered, ok := queue.catalog.Lookup(definition.Name())
	if !ok || registered != declarationOf(definition) {
		return enqueueRequest{}, invalid("definition is not an exact catalog member")
	}
	if once != intent.valid() {
		return enqueueRequest{}, invalid("producer intent")
	}
	if once && !definition.PayloadIdentity().Available {
		return enqueueRequest{}, ErrUnsupported
	}
	resolved, err := resolveEnqueueOptions(optionValues)
	if err != nil {
		return enqueueRequest{}, err
	}
	if once && resolved.collapseSet {
		return enqueueRequest{}, invalid("once enqueue cannot collapse")
	}
	if resolved.prioritySet && !queue.description.Capabilities().Priority {
		return enqueueRequest{}, fmt.Errorf("%w: sender does not support priority", ErrUnsupported)
	}
	if resolved.delaySet && !queue.description.Capabilities().Scheduled {
		return enqueueRequest{}, fmt.Errorf("%w: sender does not support scheduling", ErrUnsupported)
	}
	if resolved.collapseSet && !queue.description.Capabilities().Debounce {
		return enqueueRequest{}, fmt.Errorf("%w: sender does not support collapse or debounce", ErrUnsupported)
	}
	policy, err := NewPolicySnapshot(definition.Policy())
	if err != nil {
		return enqueueRequest{}, err
	}
	return enqueueRequest{options: resolved, policy: policy}, nil
}

func preparePlacement[P any](ctx context.Context, queue *Queue, definition DefinitionOf[P], intent ProducerIntent, once bool, payload P, request enqueueRequest, captured placementContext) (Placement, error) {
	if err := ctx.Err(); err != nil {
		return Placement{}, err
	}
	if !request.policy.valid() || !captured.partition.validFor(queue.namespace) || !captured.durable.validFor(queue.namespace, captured.partition, definition.Name(), request.policy.Trace()) {
		return Placement{}, invalid("enqueue request or durable context")
	}
	resolved := request.options
	policySnapshot := request.policy
	partition := captured.partition
	encoded, payloadDigest, err := definition.preparePayload(payload, once)
	if err != nil {
		return Placement{}, err
	}
	if encoded.IsZero() || encoded.encodedLength() > policySnapshot.payload.MaxBytes {
		return Placement{}, ErrTooLarge
	}
	if err := ctx.Err(); err != nil {
		return Placement{}, err
	}
	candidate, err := queue.nextInvocationID()
	if err != nil {
		return Placement{}, err
	}
	intentDigests, err := digestRegularIntents(queue.digests, queue.namespace, partition, definition.Name(), candidate)
	if err != nil {
		return Placement{}, err
	}
	mode := PlacementRegular
	legacyIntent := LegacyIntent{}
	if once {
		intentDigests, err = digestProducerIntents(queue.digests, queue.namespace, partition, definition.Name(), IntentOnce, intent)
		if err != nil {
			return Placement{}, err
		}
		mode = PlacementOnce
		if queue.digests.LegacyCompatibility() {
			legacyIntent = protectLegacyIntent(intent)
		}
	} else if resolved.collapseSet {
		intentDigests, err = digestProducerIntents(queue.digests, queue.namespace, partition, definition.Name(), IntentCollapse, resolved.collapse)
		if err != nil {
			return Placement{}, err
		}
		mode = resolved.mode
		if queue.digests.LegacyCompatibility() {
			legacyIntent = protectLegacyIntent(resolved.collapse)
		}
	}
	priority := policySnapshot.Priority()
	if resolved.prioritySet {
		priority = resolved.priority
	}
	return NewPlacement(PlacementSpec{
		Namespace:     queue.namespace,
		Partition:     partition,
		Candidate:     candidate,
		Definition:    definition.Name(),
		Queue:         policySnapshot.Queue(),
		Mode:          mode,
		Payload:       encoded,
		PayloadDigest: payloadDigest,
		WireDigest:    digestWirePayload(encoded),
		IntentDigests: intentDigests,
		LegacyIntent:  legacyIntent,
		Priority:      priority,
		Delay:         resolved.delay,
		MaxDelay:      resolved.maxDelay,
		StartBefore:   resolved.startBefore,
		Policy:        policySnapshot,
		Context:       captured.durable,
	})
}

func declarationOf[P any](definition DefinitionOf[P]) Declaration {
	return definition
}

func resolveEnqueueOptions(values []EnqueueOption) (enqueueOptions, error) {
	result := enqueueOptions{mode: PlacementRegular}
	for _, value := range values {
		if nilInterface(value) {
			return enqueueOptions{}, invalid("enqueue option")
		}
		if err := value.applyEnqueue(&result); err != nil {
			return enqueueOptions{}, err
		}
	}
	if result.mode == PlacementDebounce && (!result.delaySet || result.delay > result.maxDelay) {
		return enqueueOptions{}, invalid("debounce delay")
	}
	return result, nil
}

func capturePlacementContext(ctx context.Context, queue *Queue, definition Name, mode PartitionMode, policy TracePolicy) (placementContext, error) {
	if nilInterface(ctx) || queue == nil || !queue.namespace.valid() || !definition.valid() || !mode.Valid() || !policy.valid() {
		return placementContext{}, invalid("context capture")
	}
	if err := ctx.Err(); err != nil {
		return placementContext{}, err
	}
	capture := ContextCapture{provenance: IdentityProvenance{value: "framework.system"}, epoch: 1}
	if !nilInterface(queue.contexts) {
		request := ContextCaptureRequest{namespace: queue.namespace, definition: definition, partition: mode}
		var err error
		capture, err = invokeContextProvider(queue.contexts, ctx, request)
		if contextErr := ctx.Err(); contextErr != nil {
			return placementContext{}, contextErr
		}
		if err != nil {
			return placementContext{}, err
		}
	}
	partition, durable, err := BuildDurableContext(queue.namespace, definition, mode, policy, capture)
	if err != nil {
		return placementContext{}, err
	}
	return placementContext{partition: partition, durable: durable}, nil
}

func invokeContextProvider(provider TrustedContextProvider, ctx context.Context, request ContextCaptureRequest) (capture ContextCapture, err error) {
	defer func() {
		if recover() != nil {
			capture = ContextCapture{}
			err = ErrDriver
		}
	}()
	capture, err = provider.Capture(ctx, request)
	if err != nil {
		return ContextCapture{}, ErrDriver
	}
	if !capture.valid() {
		return ContextCapture{}, invalid("trusted context provider result")
	}
	return capture, nil
}

func (q *Queue) nextInvocationID() (InvocationID, error) {
	var value [InvocationIDBytes]byte
	if err := q.entropy.read(value[:]); err != nil {
		return InvocationID{}, ErrEntropy
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return InvocationIDFromBytes(value)
}

func readEntropy(reader io.Reader, destination []byte) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrEntropy
		}
	}()
	_, err = io.ReadFull(reader, destination)
	return err
}

func place(sender Sender, ctx context.Context, placement Placement) (result PlacementResult, err error) {
	defer func() {
		if recover() != nil {
			result = PlacementResult{}
			err = ErrAmbiguous
		}
	}()
	result, err = sender.Place(ctx, placement)
	if err != nil {
		if !result.IsZero() {
			return PlacementResult{}, ErrAmbiguous
		}
		return PlacementResult{}, normalizeSenderError(err)
	}
	return result, nil
}

func stage(stager Stager, ctx context.Context, placement Placement) (result Staged, err error) {
	defer func() {
		if recover() != nil {
			result = Staged{}
			err = ErrAmbiguous
		}
	}()
	result, err = stager.Stage(ctx, placement)
	if err != nil {
		if !result.IsZero() {
			return Staged{}, ErrAmbiguous
		}
		return Staged{}, normalizeSenderError(err)
	}
	return result, nil
}

type rejectedPlacement struct{ reason error }

func (e rejectedPlacement) Error() string { return e.reason.Error() }
func (e rejectedPlacement) Is(target error) bool {
	return target == e.reason

}

func RejectPlacement(reason error) error {
	switch reason {
	case ErrInvalid, ErrTooLarge, ErrConflict, ErrSaturated, ErrUnsupported, ErrNotActivated:
		return rejectedPlacement{reason: reason}
	default:
		return ErrInvalid
	}
}

func normalizeSenderError(err error) error {
	if errors.Is(err, ErrAmbiguous) {
		return ErrAmbiguous
	}
	if rejected, ok := err.(rejectedPlacement); ok {
		return rejected.reason
	}
	return ErrAmbiguous
}

func validPlacementResult(placement Placement, result PlacementResult) bool {
	if result.IsZero() || !result.Outcome().Valid() {
		return false
	}
	switch placement.Mode() {
	case PlacementRegular:
		return result.Outcome() == PlacementCreated && result.InvocationID() == placement.Candidate()
	case PlacementOnce:
		return result.Outcome() == PlacementCreated && result.InvocationID() == placement.Candidate() || result.Outcome() == PlacementExistingSamePayload || result.Outcome() == PlacementConflict
	case PlacementCollapse, PlacementDebounce:
		return result.Outcome() == PlacementCreated && result.InvocationID() == placement.Candidate() || result.Outcome() == PlacementCollapsed
	default:
		return false
	}
}

func senderDescription(sender Sender) (description BackendDescription, err error) {
	defer func() {
		if recover() != nil {
			description = BackendDescription{}
			err = invalid("sender description")
		}
	}()
	description = sender.Description()
	if !description.valid() {
		return BackendDescription{}, invalid("sender description")
	}
	return description, nil
}

func stagerTransaction(stager Stager) (transaction TransactionContext, err error) {
	defer func() {
		if recover() != nil {
			transaction = TransactionContext{}
			err = invalid("stager transaction")
		}
	}()
	transaction = stager.Transaction()
	if !transaction.valid() {
		return TransactionContext{}, invalid("stager transaction")
	}
	return transaction, nil
}

func validateStager(queue *Queue, stager Stager, definition Name) (TransactionContext, error) {
	if queue == nil || !queue.description.valid() || !definition.valid() || nilInterface(stager) {
		return TransactionContext{}, invalid("queue or stager")
	}
	requirement, ok := queue.definitionDurability[definition]
	if !ok || !requirement.valid() {
		return TransactionContext{}, invalid("stager durability requirement")
	}
	transaction, err := stagerTransaction(stager)
	if err != nil {
		return TransactionContext{}, err
	}
	if transaction.Backend() != queue.description.ID() {
		return TransactionContext{}, invalid("stager backend does not match queue")
	}
	if !requirement.accepts(transaction.Durability()) {
		return TransactionContext{}, fmt.Errorf("%w: transaction durability does not satisfy the job definition", ErrUnsupported)
	}
	return transaction, nil
}
