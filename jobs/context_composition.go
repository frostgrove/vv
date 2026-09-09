package jobs

import "context"

func (capture ContextCapture) Trace() UntrustedTraceCarrier {
	return capture.trace
}

func (capture ContextCapture) WithTrace(carrier UntrustedTraceCarrier) (ContextCapture, error) {
	if !capture.valid() || !carrier.valid() {
		return ContextCapture{}, invalid("context capture trace")
	}
	capture.trace = carrier
	return capture, nil
}

func (request IdentityRestoreRequest) Trace() UntrustedTraceCarrier {
	return request.durable.trace
}

func (identity RestoredIdentity) WithContext(ctx context.Context) (RestoredIdentity, error) {
	if nilInterface(ctx) || nilInterface(identity.context) || identity.lineage == nil ||
		ctx.Value(restoredIdentityLineageKey{}) != identity.lineage || !sameContextLifetime(identity.context, ctx) {
		return RestoredIdentity{}, invalid("restored identity context")
	}
	identity.context = ctx
	return identity, nil
}

type systemContextProvider struct{}

func SystemContextProvider() TrustedContextProvider {
	return systemContextProvider{}
}

func (systemContextProvider) Capture(ctx context.Context, request ContextCaptureRequest) (ContextCapture, error) {
	if nilInterface(ctx) || !request.valid() || request.Partition() != PartitionGlobal {
		return ContextCapture{}, invalid("system context capture")
	}
	return defaultSystemContextCapture(), nil
}

type systemIdentityRestorer struct{}

func SystemIdentityRestorer() TrustedIdentityRestorer {
	return systemIdentityRestorer{}
}

func (systemIdentityRestorer) RestoreIdentity(ctx context.Context, request IdentityRestoreRequest) (RestoredIdentity, error) {
	_, hasTenant := request.Tenant()
	_, hasActor := request.Actor()
	_, hasToken := request.Token()
	if nilInterface(ctx) || !request.valid() || request.Scope() != ContextSystem || hasTenant || hasActor || hasToken ||
		request.Provenance() != defaultSystemIdentityProvenance() || request.Epoch() != IdentityEpoch(1) {
		return RestoredIdentity{}, invalid("system identity restoration")
	}
	return NewRestoredIdentity(ctx, ProducerPartition{}, ProducerActor{})
}

func defaultSystemContextCapture() ContextCapture {
	return ContextCapture{provenance: defaultSystemIdentityProvenance(), epoch: IdentityEpoch(1)}
}

func defaultSystemIdentityProvenance() IdentityProvenance {
	return IdentityProvenance{value: "framework.system"}
}
