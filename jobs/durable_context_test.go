package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestDurableContextBindsTenantActorProvenanceEpochAndTrace(t *testing.T) {
	namespace := queueTestNamespace(t, "context")
	definition := testJobName(t, "context.identity")
	requestKey := mustCorrelationKey(t, "request_id")
	operationKey := mustCorrelationKey(t, "operation_id")
	policy, err := NewTracePolicy(requestKey, operationKey)
	if err != nil {
		t.Fatal(err)
	}
	fields := []CorrelationField{
		mustCorrelationField(t, requestKey, "request-secret"),
		mustCorrelationField(t, operationKey, "operation-secret"),
	}
	trace, err := NewUntrustedTraceCarrier(TraceCarrierSpec{
		TraceParent:  "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		TraceState:   "vendor=value",
		Correlations: fields,
	})
	if err != nil {
		t.Fatal(err)
	}
	fields[0] = CorrelationField{}
	provenance := mustIdentityProvenance(t, "auth.jwt")
	epoch, err := NewIdentityEpoch(7)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := NewContextCapture(ContextCaptureSpec{
		Tenant:     Partition("tenant-private"),
		Actor:      Actor("actor-private"),
		Token:      mustIdentityToken(t, []byte("sealed-private-identity")),
		Provenance: provenance,
		Epoch:      epoch,
		Trace:      trace,
	})
	if err != nil {
		t.Fatal(err)
	}
	partition, durable, err := BuildDurableContext(namespace, definition, PartitionTenantRequired, policy, capture)
	if err != nil {
		t.Fatal(err)
	}
	tenant, hasTenant := durable.Tenant()
	actor, hasActor := durable.Actor()
	if partition.Global() || durable.Scope() != ContextTenant || !hasTenant || tenant.Bytes() != partition.Digest().Bytes() || !hasActor || actor.IsZero() || durable.Provenance() != provenance || durable.Epoch() != epoch {
		t.Fatalf("durable context identity = %+v", durable)
	}
	correlations := durable.Trace().Correlations()
	if len(correlations) != 2 || correlations[0].Key() != operationKey || correlations[1].Key() != requestKey {
		t.Fatalf("canonical correlations = %+v", correlations)
	}
	correlations[0] = CorrelationField{}
	if durable.Trace().Correlations()[0].IsZero() {
		t.Fatal("returned correlation slice mutated durable context")
	}
	record := durable.Record()
	restored, err := RestoreDurableContext(namespace, partition, definition, policy, record)
	if err != nil || restored.Record().Binding != record.Binding {
		t.Fatalf("restore = %+v/%v", restored, err)
	}
	record.Trace.Correlations[0].Value = "mutated"
	if durable.Record().Trace.Correlations[0].Value == "mutated" {
		t.Fatal("record mutation changed durable context")
	}
}

func TestDurableContextTenantCollisionAndSystemPath(t *testing.T) {
	namespace := queueTestNamespace(t, "scope")
	definition := testJobName(t, "scope.identity")
	policy, _ := NewTracePolicy()
	provenance := mustIdentityProvenance(t, "service.scheduler")
	epoch, _ := NewIdentityEpoch(1)
	leftCapture := mustContextCapture(t, ContextCaptureSpec{Tenant: Partition("tenant-a"), Provenance: provenance, Epoch: epoch})
	rightCapture := mustContextCapture(t, ContextCaptureSpec{Tenant: Partition("tenant-b"), Provenance: provenance, Epoch: epoch})
	leftPartition, left, err := BuildDurableContext(namespace, definition, PartitionTenantRequired, policy, leftCapture)
	if err != nil {
		t.Fatal(err)
	}
	rightPartition, right, err := BuildDurableContext(namespace, definition, PartitionTenantRequired, policy, rightCapture)
	if err != nil {
		t.Fatal(err)
	}
	if leftPartition == rightPartition || left.Binding() == right.Binding() {
		t.Fatal("tenant collision shared partition or durable binding")
	}
	if _, err := RestoreDurableContext(namespace, rightPartition, definition, policy, left.Record()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("cross-tenant restore = %v", err)
	}
	systemCapture := mustContextCapture(t, ContextCaptureSpec{Provenance: provenance, Epoch: epoch})
	globalPartition, system, err := BuildDurableContext(namespace, definition, PartitionGlobal, policy, systemCapture)
	if err != nil {
		t.Fatal(err)
	}
	if !globalPartition.Global() || system.Scope() != ContextSystem {
		t.Fatalf("system context = %+v", system)
	}
	if _, ok := system.Tenant(); ok {
		t.Fatal("system context fabricated a tenant")
	}
	if _, ok := system.Actor(); ok {
		t.Fatal("system context fabricated an actor")
	}
	if _, _, err := BuildDurableContext(namespace, definition, PartitionTenantRequired, policy, systemCapture); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing tenant = %v", err)
	}
	if _, _, err := BuildDurableContext(namespace, definition, PartitionGlobal, policy, leftCapture); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tenant on global context = %v", err)
	}
	actorWithoutToken := mustContextCapture(t, ContextCaptureSpec{Tenant: Partition("tenant-a"), Actor: Actor("actor-a"), Provenance: provenance, Epoch: epoch})
	if _, _, err := BuildDurableContext(namespace, definition, PartitionTenantRequired, policy, actorWithoutToken); !errors.Is(err, ErrInvalid) {
		t.Fatalf("actor without restoration token = %v", err)
	}
	systemToken := mustContextCapture(t, ContextCaptureSpec{Token: mustIdentityToken(t, []byte("detached-identity")), Provenance: provenance, Epoch: epoch})
	if _, _, err := BuildDurableContext(namespace, definition, PartitionGlobal, policy, systemToken); !errors.Is(err, ErrInvalid) {
		t.Fatalf("system token without identity = %v", err)
	}
}

func TestDurableContextBindsActorAndRecordToDefinition(t *testing.T) {
	namespace := queueTestNamespace(t, "definition-binding")
	leftDefinition := testJobName(t, "definition.left")
	rightDefinition := testJobName(t, "definition.right")
	policy, _ := NewTracePolicy()
	capture := mustContextCapture(t, ContextCaptureSpec{
		Tenant:     Partition("tenant-private"),
		Actor:      Actor("actor-private"),
		Token:      mustIdentityToken(t, []byte("sealed-private-identity")),
		Provenance: mustIdentityProvenance(t, "auth.jwt"),
		Epoch:      3,
	})
	partition, left, err := BuildDurableContext(namespace, leftDefinition, PartitionTenantRequired, policy, capture)
	if err != nil {
		t.Fatal(err)
	}
	rightPartition, right, err := BuildDurableContext(namespace, rightDefinition, PartitionTenantRequired, policy, capture)
	if err != nil {
		t.Fatal(err)
	}
	leftActor, _ := left.Actor()
	rightActor, _ := right.Actor()
	if partition != rightPartition || leftActor == rightActor || left.Binding() == right.Binding() {
		t.Fatal("definition did not bind durable actor and context")
	}
	if _, err := RestoreDurableContext(namespace, partition, rightDefinition, policy, left.Record()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("cross-definition restore = %v", err)
	}
}

func TestRestoreTrustedIdentityVerifiesDecodedIdentityAndLifetime(t *testing.T) {
	namespace, partition, definition, policy, durable, request := identityRestoreFixture(t)
	key := new(byte)
	base, cancel := context.WithTimeout(context.Background(), time.Minute)
	restorer := TrustedIdentityRestorerFunc(func(ctx context.Context, got IdentityRestoreRequest) (RestoredIdentity, error) {
		if got.Namespace() != namespace || got.Partition() != partition || got.Definition() != definition || got.Scope() != ContextTenant || got.Provenance() != durable.Provenance() || got.Epoch() != durable.Epoch() {
			t.Fatal("restorer received the wrong durable identity request")
		}
		token, ok := got.Token()
		if !ok || string(token.Bytes()) != "sealed-private-identity" {
			t.Fatal("restorer did not receive the protected token")
		}
		return NewRestoredIdentity(context.WithValue(ctx, key, "verified"), Partition("tenant-private"), Actor("actor-private"))
	})
	restored, err := RestoreTrustedIdentity(base, restorer, request)
	if err != nil || restored.Value(key) != "verified" {
		t.Fatalf("restore = %v/%v", restored, err)
	}
	wantDeadline, wantDeadlineSet := base.Deadline()
	gotDeadline, gotDeadlineSet := restored.Deadline()
	if !wantDeadlineSet || !gotDeadlineSet || gotDeadline != wantDeadline {
		t.Fatalf("deadline = %v/%t, want %v/%t", gotDeadline, gotDeadlineSet, wantDeadline, wantDeadlineSet)
	}
	cancel()
	<-restored.Done()
	if !errors.Is(restored.Err(), context.Canceled) {
		t.Fatalf("restored cancellation = %v", restored.Err())
	}

	secret := errors.New("identity backend password=private")
	cases := []struct {
		name    string
		restore func(context.Context) (RestoredIdentity, error)
	}{
		{"wrong tenant", func(ctx context.Context) (RestoredIdentity, error) {
			return NewRestoredIdentity(ctx, Partition("tenant-other"), Actor("actor-private"))
		}},
		{"wrong actor", func(ctx context.Context) (RestoredIdentity, error) {
			return NewRestoredIdentity(ctx, Partition("tenant-private"), Actor("actor-other"))
		}},
		{"missing actor", func(ctx context.Context) (RestoredIdentity, error) {
			return NewRestoredIdentity(ctx, Partition("tenant-private"), ProducerActor{})
		}},
		{"unrelated context", func(context.Context) (RestoredIdentity, error) {
			return NewRestoredIdentity(context.Background(), Partition("tenant-private"), Actor("actor-private"))
		}},
		{"detached cancellation", func(ctx context.Context) (RestoredIdentity, error) {
			return NewRestoredIdentity(context.WithoutCancel(ctx), Partition("tenant-private"), Actor("actor-private"))
		}},
		{"error", func(ctx context.Context) (RestoredIdentity, error) {
			identity, _ := NewRestoredIdentity(ctx, Partition("tenant-private"), Actor("actor-private"))
			return identity, secret
		}},
		{"zero result", func(context.Context) (RestoredIdentity, error) { return RestoredIdentity{}, nil }},
		{"panic", func(context.Context) (RestoredIdentity, error) { panic("identity private panic") }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			_, err := RestoreTrustedIdentity(ctx, TrustedIdentityRestorerFunc(func(ctx context.Context, _ IdentityRestoreRequest) (RestoredIdentity, error) {
				return test.restore(ctx)
			}), request)
			if err != ErrDriver || errors.Is(err, secret) || strings.Contains(fmt.Sprint(err), "private") {
				t.Fatalf("restore error = %v", err)
			}
		})
	}

	cancelled, stop := context.WithCancel(context.Background())
	_, err = RestoreTrustedIdentity(cancelled, TrustedIdentityRestorerFunc(func(ctx context.Context, _ IdentityRestoreRequest) (RestoredIdentity, error) {
		stop()
		identity, _ := NewRestoredIdentity(ctx, Partition("tenant-private"), Actor("actor-private"))
		return identity, secret
	}), request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation precedence = %v", err)
	}
	panicContext, cancelPanic := context.WithCancel(context.Background())
	_, err = RestoreTrustedIdentity(panicContext, TrustedIdentityRestorerFunc(func(context.Context, IdentityRestoreRequest) (RestoredIdentity, error) {
		cancelPanic()
		panic("identity private panic")
	}), request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("panic cancellation precedence = %v", err)
	}
	preCancelled, cancelBefore := context.WithCancel(context.Background())
	cancelBefore()
	called := false
	_, err = RestoreTrustedIdentity(preCancelled, TrustedIdentityRestorerFunc(func(context.Context, IdentityRestoreRequest) (RestoredIdentity, error) {
		called = true
		return RestoredIdentity{}, nil
	}), request)
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("pre-cancelled restore = %v, called=%t", err, called)
	}

	systemCapture := mustContextCapture(t, ContextCaptureSpec{Provenance: mustIdentityProvenance(t, "service.scheduler"), Epoch: 1})
	systemPartition, system, err := BuildDurableContext(namespace, definition, PartitionGlobal, policy, systemCapture)
	if err != nil {
		t.Fatal(err)
	}
	systemRequest, err := system.IdentityRestoreRequest(namespace, systemPartition, definition, policy)
	if err != nil {
		t.Fatal(err)
	}
	_, err = RestoreTrustedIdentity(context.Background(), TrustedIdentityRestorerFunc(func(ctx context.Context, _ IdentityRestoreRequest) (RestoredIdentity, error) {
		return NewRestoredIdentity(ctx, ProducerPartition{}, Actor("fabricated-principal"))
	}), systemRequest)
	if err != ErrDriver {
		t.Fatalf("fabricated system principal = %v", err)
	}
}

func TestRestoreDurableContextRejectsTamperedRecord(t *testing.T) {
	namespace := queueTestNamespace(t, "tamper")
	definition := testJobName(t, "tamper.identity")
	key := mustCorrelationKey(t, "request_id")
	policy, _ := NewTracePolicy(key)
	trace, _ := NewUntrustedTraceCarrier(TraceCarrierSpec{
		TraceParent:  "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		TraceState:   "vendor=value",
		Correlations: []CorrelationField{mustCorrelationField(t, key, "request-private")},
	})
	capture := mustContextCapture(t, ContextCaptureSpec{
		Tenant:     Partition("tenant-private"),
		Actor:      Actor("actor-private"),
		Token:      mustIdentityToken(t, []byte("sealed-private-identity")),
		Provenance: mustIdentityProvenance(t, "auth.session"),
		Epoch:      IdentityEpoch(9),
		Trace:      trace,
	})
	partition, durable, err := BuildDurableContext(namespace, definition, PartitionTenantRequired, policy, capture)
	if err != nil {
		t.Fatal(err)
	}
	base := durable.Record()
	tests := map[string]func(*DurableContextRecord){
		"scope":      func(record *DurableContextRecord) { record.Scope = ContextSystem },
		"tenant":     func(record *DurableContextRecord) { record.Tenant[0] ^= 1 },
		"actor":      func(record *DurableContextRecord) { record.Actor[0] ^= 1 },
		"token":      func(record *DurableContextRecord) { record.Token[0] ^= 1 },
		"provenance": func(record *DurableContextRecord) { record.Provenance = "auth.other" },
		"epoch":      func(record *DurableContextRecord) { record.Epoch++ },
		"trace parent": func(record *DurableContextRecord) {
			record.Trace.TraceParent = "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01"
		},
		"trace state":       func(record *DurableContextRecord) { record.Trace.TraceState = "other=value" },
		"correlation key":   func(record *DurableContextRecord) { record.Trace.Correlations[0].Key = "other_id" },
		"correlation value": func(record *DurableContextRecord) { record.Trace.Correlations[0].Value = "spoofed" },
		"binding":           func(record *DurableContextRecord) { record.Binding[0] ^= 1 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			record := base
			record.Token = append([]byte(nil), base.Token...)
			record.Trace.Correlations = append([]CorrelationRecord(nil), base.Trace.Correlations...)
			mutate(&record)
			if _, err := RestoreDurableContext(namespace, partition, definition, policy, record); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("restore = %v", err)
			}
		})
	}
	otherPolicy, _ := NewTracePolicy(mustCorrelationKey(t, "other_id"))
	if _, err := RestoreDurableContext(namespace, partition, definition, otherPolicy, base); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("unallowed restored correlation = %v", err)
	}
	withoutToken := base
	withoutToken.Token = nil
	if _, err := RestoreDurableContext(namespace, partition, definition, policy, withoutToken); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("missing tenant token = %v", err)
	}
}

func TestRestoreDurableContextRejectsPersistedBoundsBeforeMaterialization(t *testing.T) {
	namespace := queueTestNamespace(t, "persisted-bounds")
	definition := testJobName(t, "persisted-bounds.identity")
	keys := make([]CorrelationKey, MaxCorrelationFields)
	for index := range keys {
		keys[index] = mustCorrelationKey(t, fmt.Sprintf("field_%d", index))
	}
	policy, err := NewTracePolicy(keys...)
	if err != nil {
		t.Fatal(err)
	}
	capture := mustContextCapture(t, ContextCaptureSpec{Provenance: mustIdentityProvenance(t, "service.test"), Epoch: 1})
	partition, durable, err := BuildDurableContext(namespace, definition, PartitionGlobal, policy, capture)
	if err != nil {
		t.Fatal(err)
	}
	base := durable.Record()
	tests := map[string]func(*DurableContextRecord){
		"token": func(record *DurableContextRecord) {
			record.Token = make([]byte, MaxIdentityTokenBytes+1)
		},
		"trace parent": func(record *DurableContextRecord) {
			record.Trace.TraceParent = strings.Repeat("a", MaxTraceParentBytes+1)
		},
		"trace state": func(record *DurableContextRecord) {
			record.Trace.TraceState = strings.Repeat("a", MaxTraceStateBytes+1)
		},
		"correlation count": func(record *DurableContextRecord) {
			record.Trace.Correlations = make([]CorrelationRecord, MaxCorrelationFields+1)
		},
		"correlation key": func(record *DurableContextRecord) {
			record.Trace.Correlations = []CorrelationRecord{{Key: strings.Repeat("a", MaxCorrelationKeyBytes+1), Value: "value"}}
		},
		"correlation value": func(record *DurableContextRecord) {
			record.Trace.Correlations = []CorrelationRecord{{Key: "field_0", Value: strings.Repeat("a", MaxCorrelationValueBytes+1)}}
		},
		"carrier total": func(record *DurableContextRecord) {
			record.Trace.Correlations = make([]CorrelationRecord, MaxCorrelationFields)
			for index := range record.Trace.Correlations {
				record.Trace.Correlations[index] = CorrelationRecord{Key: fmt.Sprintf("field_%d", index), Value: strings.Repeat("a", MaxCorrelationValueBytes)}
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			record := base
			mutate(&record)
			if _, err := RestoreDurableContext(namespace, partition, definition, policy, record); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("restore = %v", err)
			}
		})
	}
}

func TestTraceCarrierRejectsDuplicatesUnallowedFieldsAndBounds(t *testing.T) {
	key := mustCorrelationKey(t, "request_id")
	field := mustCorrelationField(t, key, "request")
	if _, err := NewTracePolicy(key, key); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate allowlist = %v", err)
	}
	if _, err := NewUntrustedTraceCarrier(TraceCarrierSpec{Correlations: []CorrelationField{field, field}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate correlations = %v", err)
	}
	trace, err := NewUntrustedTraceCarrier(TraceCarrierSpec{Correlations: []CorrelationField{field}})
	if err != nil {
		t.Fatal(err)
	}
	capture := mustContextCapture(t, ContextCaptureSpec{Provenance: mustIdentityProvenance(t, "service.test"), Epoch: 1, Trace: trace})
	if _, _, err := BuildDurableContext(queueTestNamespace(t, "unallowed"), testJobName(t, "unallowed.identity"), PartitionGlobal, TracePolicy{}, capture); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unallowed correlation = %v", err)
	}
	if _, err := ParseCorrelationKey(strings.Repeat("a", MaxCorrelationKeyBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("key bound = %v", err)
	}
	if _, err := NewCorrelationField(key, strings.Repeat("v", MaxCorrelationValueBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("value bound = %v", err)
	}
	tooMany := make([]CorrelationField, MaxCorrelationFields+1)
	if _, err := NewUntrustedTraceCarrier(TraceCarrierSpec{Correlations: tooMany}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("field count = %v", err)
	}
	large := make([]CorrelationField, MaxCorrelationFields)
	for index := range large {
		large[index] = mustCorrelationField(t, mustCorrelationKey(t, fmt.Sprintf("field_%d", index)), strings.Repeat("v", MaxCorrelationValueBytes))
	}
	if _, err := NewUntrustedTraceCarrier(TraceCarrierSpec{Correlations: large}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("carrier total bound = %v", err)
	}
	if _, err := NewUntrustedTraceCarrier(TraceCarrierSpec{TraceParent: "00-00000000000000000000000000000000-00f067aa0ba902b7-01"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero trace id = %v", err)
	}
	if _, err := NewUntrustedTraceCarrier(TraceCarrierSpec{TraceState: "vendor=value"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tracestate without parent = %v", err)
	}
	if _, err := ParseActor(strings.Repeat("a", MaxActorIdentityBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("actor bound = %v", err)
	}
	if _, err := ParseIdentityProvenance(strings.Repeat("a", MaxIdentityProvenanceBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("provenance bound = %v", err)
	}
}

func TestTraceStateAcceptsLevelTwoGrammarAndRejectsAmbiguity(t *testing.T) {
	parent := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	valid := []string{
		"1vendor=value",
		"1@vendor@region=value",
		"vendor= leading value",
		" \t,1vendor=value,,vendor@region=other,\t",
		" , ",
	}
	for _, state := range valid {
		if _, err := NewUntrustedTraceCarrier(TraceCarrierSpec{TraceParent: parent, TraceState: state}); err != nil {
			t.Fatalf("valid tracestate %q = %v", state, err)
		}
	}
	invalid := []string{
		"Vendor=value",
		"vendor=value,vendor=other",
		"vendor=value\u00a0",
		"\u00a0vendor=value",
		"vendor=value=other",
		strings.Repeat("a", 257) + "=value",
	}
	for _, state := range invalid {
		if _, err := NewUntrustedTraceCarrier(TraceCarrierSpec{TraceParent: parent, TraceState: state}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid tracestate %q = %v", state, err)
		}
	}
	members := make([]string, 33)
	for index := range members {
		members[index] = fmt.Sprintf("k%d=v", index)
	}
	if _, err := NewUntrustedTraceCarrier(TraceCarrierSpec{TraceParent: parent, TraceState: strings.Join(members, ",")}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("too many tracestate members = %v", err)
	}
}

func TestDurableContextSurfacesAreRedacted(t *testing.T) {
	namespace := queueTestNamespace(t, "redaction")
	definition := testJobName(t, "redaction.identity")
	key := mustCorrelationKey(t, "request_id")
	field := mustCorrelationField(t, key, "correlation-private")
	policy, _ := NewTracePolicy(key)
	trace, _ := NewUntrustedTraceCarrier(TraceCarrierSpec{
		TraceParent:  "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		TraceState:   "private=value",
		Correlations: []CorrelationField{field},
	})
	capture := mustContextCapture(t, ContextCaptureSpec{Tenant: Partition("tenant-private"), Actor: Actor("actor-private"), Token: mustIdentityToken(t, []byte("sealed-private-identity")), Provenance: mustIdentityProvenance(t, "auth.jwt"), Epoch: 1, Trace: trace})
	partition, durable, err := BuildDurableContext(namespace, definition, PartitionTenantRequired, policy, capture)
	if err != nil {
		t.Fatal(err)
	}
	record := durable.Record()
	token, _ := durable.Token()
	request, err := durable.IdentityRestoreRequest(namespace, partition, definition, policy)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := NewRestoredIdentity(context.WithValue(context.Background(), new(byte), "context-private"), Partition("tenant-private"), Actor("actor-private"))
	if err != nil {
		t.Fatal(err)
	}
	values := []any{Actor("actor-private"), token, field, trace, capture, durable, request, restored, record.Trace.Correlations[0], record.Trace, record}
	for _, value := range values {
		for _, formatted := range []string{fmt.Sprint(value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			for _, secret := range []string{"actor-private", "tenant-private", "correlation-private", "4bf92f3577b34da6a3ce929d0e0e4736", "private=value"} {
				if strings.Contains(formatted, secret) {
					t.Fatalf("%T exposed %q in %q", value, secret, formatted)
				}
			}
		}
		if _, err := json.Marshal(value); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("%T JSON error = %v", value, err)
		}
	}
	for _, value := range []interface{ LogValue() slog.Value }{Actor("actor-private"), token, trace, capture, durable, restored} {
		if strings.Contains(value.LogValue().String(), "private") {
			t.Fatalf("%T log value leaked", value)
		}
	}
}

func mustCorrelationKey(t *testing.T, raw string) CorrelationKey {
	t.Helper()
	key, err := ParseCorrelationKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func mustCorrelationField(t *testing.T, key CorrelationKey, value string) CorrelationField {
	t.Helper()
	field, err := NewCorrelationField(key, value)
	if err != nil {
		t.Fatal(err)
	}
	return field
}

func mustIdentityProvenance(t *testing.T, raw string) IdentityProvenance {
	t.Helper()
	provenance, err := ParseIdentityProvenance(raw)
	if err != nil {
		t.Fatal(err)
	}
	return provenance
}

func mustContextCapture(t *testing.T, spec ContextCaptureSpec) ContextCapture {
	t.Helper()
	capture, err := NewContextCapture(spec)
	if err != nil {
		t.Fatal(err)
	}
	return capture
}

func mustIdentityToken(t *testing.T, value []byte) ProtectedIdentityToken {
	t.Helper()
	token, err := NewProtectedIdentityToken(value)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func mustTestDurableContext(t *testing.T, namespace Namespace, partition PartitionKey, definition Name, policy TracePolicy) DurableContext {
	t.Helper()
	capture := mustContextCapture(t, ContextCaptureSpec{Provenance: mustIdentityProvenance(t, "framework.test"), Epoch: 1})
	resolved, durable, err := BuildDurableContext(namespace, definition, PartitionGlobal, policy, capture)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != partition {
		t.Fatal("test durable context partition mismatch")
	}
	return durable
}

func mustTenantContextProvider(t *testing.T, partitioner TenantPartitioner) TrustedContextProvider {
	t.Helper()
	provider, err := TrustTenantPartitioner(partitioner, mustIdentityProvenance(t, "framework.tenant"), 1)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func identityRestoreFixture(t *testing.T) (Namespace, PartitionKey, Name, TracePolicy, DurableContext, IdentityRestoreRequest) {
	t.Helper()
	namespace := queueTestNamespace(t, "identity-restore")
	definition := testJobName(t, "identity-restore.handler")
	policy, err := NewTracePolicy()
	if err != nil {
		t.Fatal(err)
	}
	capture := mustContextCapture(t, ContextCaptureSpec{
		Tenant:     Partition("tenant-private"),
		Actor:      Actor("actor-private"),
		Token:      mustIdentityToken(t, []byte("sealed-private-identity")),
		Provenance: mustIdentityProvenance(t, "auth.jwt"),
		Epoch:      7,
	})
	partition, durable, err := BuildDurableContext(namespace, definition, PartitionTenantRequired, policy, capture)
	if err != nil {
		t.Fatal(err)
	}
	request, err := durable.IdentityRestoreRequest(namespace, partition, definition, policy)
	if err != nil {
		t.Fatal(err)
	}
	return namespace, partition, definition, policy, durable, request
}
