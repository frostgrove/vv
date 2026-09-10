package jobs

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type compositionContextKey string

func TestContextCaptureWithTracePreservesIdentityAndTheOriginal(t *testing.T) {
	originalTrace, err := NewUntrustedTraceCarrier(TraceCarrierSpec{
		TraceParent: "00-11111111111111111111111111111111-2222222222222222-01",
	})
	if err != nil {
		t.Fatal(err)
	}
	nextTrace, err := NewUntrustedTraceCarrier(TraceCarrierSpec{
		TraceParent: "00-33333333333333333333333333333333-4444444444444444-00",
		TraceState:  "vendor=value",
	})
	if err != nil {
		t.Fatal(err)
	}
	original := mustContextCapture(t, ContextCaptureSpec{
		Tenant:     Partition("tenant-private"),
		Actor:      Actor("actor-private"),
		Token:      mustIdentityToken(t, []byte("protected-token")),
		Provenance: mustIdentityProvenance(t, "auth.jwt"),
		Epoch:      7,
		Trace:      originalTrace,
	})

	updated, err := original.WithTrace(nextTrace)
	if err != nil {
		t.Fatal(err)
	}
	if updated.tenant != original.tenant || updated.actor != original.actor ||
		!reflect.DeepEqual(updated.token.Bytes(), original.token.Bytes()) ||
		updated.provenance != original.provenance || updated.epoch != original.epoch {
		t.Fatal("WithTrace changed protected identity fields")
	}
	if !reflect.DeepEqual(updated.Trace(), nextTrace) {
		t.Fatal("WithTrace did not install the requested carrier")
	}
	if !reflect.DeepEqual(original.Trace(), originalTrace) {
		t.Fatal("WithTrace mutated the original capture")
	}
}

func TestRestoredIdentityWithContextPreservesLineageAndLifetime(t *testing.T) {
	base, cancel := context.WithCancelCause(context.WithValue(t.Context(), compositionContextKey("base"), "kept"))
	identity, err := NewRestoredIdentity(base, ProducerPartition{}, ProducerActor{})
	if err != nil {
		t.Fatal(err)
	}
	derived := context.WithValue(identity.Context(), compositionContextKey("span"), "attached")
	updated, err := identity.WithContext(derived)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Context().Value(compositionContextKey("base")) != "kept" ||
		updated.Context().Value(compositionContextKey("span")) != "attached" ||
		updated.Context().Done() != identity.Context().Done() {
		t.Fatal("WithContext lost values or changed lifetime")
	}
	wantCause := errors.New("shutdown cause")
	cancel(wantCause)
	<-updated.Context().Done()
	if context.Cause(updated.Context()) != wantCause {
		t.Fatalf("cause=%v, want exact delayed cause", context.Cause(updated.Context()))
	}
	if _, err := identity.WithContext(context.Background()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unrelated lineage error=%v, want ErrInvalid", err)
	}
}

func TestSystemContextAndIdentityBuildingBlocksMatchTheDefault(t *testing.T) {
	namespace := queueTestNamespace(t, "system-context")
	definition := testJobName(t, "system.context")
	request := ContextCaptureRequest{
		namespace:  namespace,
		definition: definition,
		partition:  PartitionGlobal,
		candidate:  queueTestInvocationID(t, 17),
		wire:       testWireDigest(t, 19),
	}
	ctx := context.WithValue(t.Context(), compositionContextKey("request"), "kept")
	capture, err := SystemContextProvider().Capture(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(capture, defaultSystemContextCapture()) {
		t.Fatalf("system capture=%+v, want queue default", capture)
	}

	policy, err := NewTracePolicy()
	if err != nil {
		t.Fatal(err)
	}
	partition, durable, err := BuildDurableContext(namespace, definition, PartitionGlobal, policy, capture)
	if err != nil {
		t.Fatal(err)
	}
	restoreRequest, err := durable.IdentityRestoreRequest(namespace, partition, definition, request.candidate, request.wire, policy)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := SystemIdentityRestorer().RestoreIdentity(ctx, restoreRequest)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Context().Value(compositionContextKey("request")) != "kept" {
		t.Fatal("system restorer did not preserve the invocation context")
	}
	_, _, _, _, _, tenantRequest := identityRestoreFixture(t)
	if _, err := SystemIdentityRestorer().RestoreIdentity(ctx, tenantRequest); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tenant restore error=%v, want ErrInvalid", err)
	}
}
