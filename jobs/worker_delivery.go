package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

type claimedDeliveryPreparation struct {
	context       context.Context
	invocation    Invocation
	decoded       any
	payloadDigest PayloadDigest
	command       DeliveryCommand
}

func (p claimedDeliveryPreparation) ready() bool {
	return p.context != nil && !p.invocation.IsZero() && p.decoded != nil && !p.command.kind.Valid()
}

func (p claimedDeliveryPreparation) commanded() bool {
	return p.context == nil && p.invocation.IsZero() && p.decoded == nil && p.payloadDigest.IsZero() && p.command.kind.Valid()
}

func (claimedDeliveryPreparation) String() string { return "[job claimed delivery preparation]" }
func (p claimedDeliveryPreparation) Format(state fmt.State, _ rune) {
	_, _ = fmt.Fprint(state, p.String())
}
func (p claimedDeliveryPreparation) LogValue() slog.Value { return slog.StringValue(p.String()) }
func (claimedDeliveryPreparation) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("%w: claimed delivery preparation cannot be serialized", ErrUnsupported)
}

func prepareClaimedDelivery(ctx context.Context, namespace Namespace, catalog Catalog, binding consumerBinding, expectedBuild BuildID, restorer TrustedIdentityRestorer, delivery ClaimedDelivery) (claimedDeliveryPreparation, error) {
	if nilInterface(ctx) || !namespace.valid() || catalog.Len() == 0 || catalog.Fingerprint() == "" || !expectedBuild.valid() || nilInterface(restorer) || !validPreparationBinding(catalog, binding) || !delivery.target.valid() || !delivery.lease.valid() || delivery.record == nil {
		return claimedDeliveryPreparation{}, invalid("claimed delivery preparation")
	}
	if !claimedDeliveryTargetMatches(binding, expectedBuild, delivery.target) {
		return claimedDeliveryPreparation{}, invalid("claimed delivery target")
	}
	if err := ctx.Err(); err != nil {
		return claimedDeliveryPreparation{}, err
	}
	reject := func() (claimedDeliveryPreparation, error) {
		command, err := RejectCorruptCommand(delivery.lease)
		if err != nil {
			return claimedDeliveryPreparation{}, err
		}
		return claimedDeliveryPreparation{command: command}, nil
	}
	record, ok := delivery.takeRecordValue()
	if !ok {
		return claimedDeliveryPreparation{}, invalid("claimed delivery record ownership")
	}
	restored, err := restoreOwnedDeliveryRecord(catalog, record)
	if contextErr := ctx.Err(); contextErr != nil {
		return claimedDeliveryPreparation{}, contextErr
	}
	if err != nil {
		if !errors.Is(err, ErrCorrupt) {
			return claimedDeliveryPreparation{}, err
		}
		return reject()
	}
	invocation := restored.Invocation()
	if !claimedDeliveryRecordMatches(namespace, delivery, invocation) {
		return reject()
	}
	if invocation.State() != InvocationQueued {
		return claimedDeliveryPreparation{}, driverContractError("claim", invalid("claimed invocation state"))
	}
	payload := restored.payloadValue()
	if !delivery.target.supports(payload.Codec(), payload.Version()) || !restored.Compatible() {
		command, commandErr := ReleaseUnchangedCommand(delivery.lease, delivery.target.binding, delivery.target.build, DefaultRetryDelay)
		if commandErr != nil {
			return claimedDeliveryPreparation{}, commandErr
		}
		return claimedDeliveryPreparation{command: command}, nil
	}
	decoded, err := decodeClaimedPayloadOwned(binding, payload)
	if contextErr := ctx.Err(); contextErr != nil {
		return claimedDeliveryPreparation{}, contextErr
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrCorrupt):
			command, commandErr := FinishDeliveryCommand(delivery.lease, InvocationQuarantined, ReasonPayload, PublicFailure{})
			if commandErr != nil {
				return claimedDeliveryPreparation{}, commandErr
			}
			return claimedDeliveryPreparation{command: command}, nil
		case errors.Is(err, ErrTooLarge), errors.Is(err, ErrUnsupported):
			command, commandErr := ReleaseUnchangedCommand(delivery.lease, delivery.target.binding, delivery.target.build, DefaultRetryDelay)
			if commandErr != nil {
				return claimedDeliveryPreparation{}, commandErr
			}
			return claimedDeliveryPreparation{command: command}, nil
		default:
			return claimedDeliveryPreparation{}, err
		}
	}
	request, err := invocation.Context().IdentityRestoreRequest(namespace, invocation.Partition(), invocation.Definition(), invocation.Policy().Trace())
	if contextErr := ctx.Err(); contextErr != nil {
		return claimedDeliveryPreparation{}, contextErr
	}
	if err != nil {
		return claimedDeliveryPreparation{}, ErrInvalid
	}
	restoredContext, err := RestoreTrustedIdentity(ctx, restorer, request)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return claimedDeliveryPreparation{}, contextErr
		}
		command, commandErr := DeferDeliveryCommand(delivery.lease, ReasonDependency, PublicFailure{}, DefaultRetryDelay)
		if commandErr != nil {
			return claimedDeliveryPreparation{}, commandErr
		}
		return claimedDeliveryPreparation{command: command}, nil
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return claimedDeliveryPreparation{}, contextErr
	}
	return claimedDeliveryPreparation{
		context:       restoredContext,
		invocation:    invocation,
		decoded:       decoded,
		payloadDigest: restored.PayloadDigest(),
	}, nil
}

func validPreparationBinding(catalog Catalog, binding consumerBinding) bool {
	if binding.err != nil || !binding.valid || nilInterface(binding.declaration) || binding.decode == nil || binding.decodeOwned == nil || !binding.binding.valid() || binding.concurrency < 1 || binding.concurrency > MaxBindingConcurrency || !validOptionalAdmissionReader(binding.admission) || !binding.mode.valid() {
		return false
	}
	if binding.mode == consumerHandlerStandard && (binding.handle == nil || binding.handleAdapter != nil) || binding.mode == consumerHandlerAdapter && (binding.handle != nil || binding.handleAdapter == nil) {
		return false
	}
	registered, ok := catalog.Lookup(binding.declaration.declarationName())
	return ok && registered == binding.declaration
}

func claimedDeliveryTargetMatches(binding consumerBinding, expectedBuild BuildID, target ClaimTarget) bool {
	return target.definition == binding.declaration.declarationName() && target.binding == binding.binding && target.build == expectedBuild
}

func claimedDeliveryRecordMatches(namespace Namespace, delivery ClaimedDelivery, invocation Invocation) bool {
	return invocation.Namespace() == namespace &&
		invocation.ID() == delivery.lease.invocation &&
		invocation.Definition() == delivery.target.definition
}

func decodeClaimedPayloadOwned(binding consumerBinding, payload EncodedPayload) (decoded any, err error) {
	defer func() {
		if recover() != nil {
			decoded = nil
			err = ErrInvalid
		}
	}()
	decoded, err = binding.decodeOwned(payload)
	if err == nil {
		if decoded == nil {
			return nil, ErrInvalid
		}
		return decoded, nil
	}
	decoded = nil
	switch {
	case errors.Is(err, ErrTooLarge):
		return nil, ErrTooLarge
	case errors.Is(err, ErrUnsupported):
		return nil, ErrUnsupported
	case errors.Is(err, ErrCorrupt):
		return nil, ErrCorrupt
	default:
		return nil, ErrInvalid
	}
}
