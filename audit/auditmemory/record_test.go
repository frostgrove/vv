package auditmemory_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/audit/auditmemory"
)

type invoiceSent struct {
	Number string
	At     time.Time
}

type lookupContextKey struct{}

type lookupValueRejectingWriter struct{ audit.Writer }

type staleSwitchWriter struct {
	audit.Writer
	stale atomic.Bool
}

type countingContextResolver struct {
	audit.ContextResolver
	calls atomic.Int32
}

func (w *staleSwitchWriter) Catalogs() audit.StoreCatalogState {
	if w.stale.Load() {
		return audit.NewEmptyStoreCatalogState()
	}
	return w.Writer.Catalogs()
}

func (r *countingContextResolver) ResolveAuditContext(ctx context.Context) (audit.Context, error) {
	r.calls.Add(1)
	return r.ContextResolver.ResolveAuditContext(ctx)
}

func (w lookupValueRejectingWriter) Lookup(ctx context.Context, request audit.LookupRequest) (audit.LookupResult, error) {
	if ctx.Value(lookupContextKey{}) != nil {
		return audit.LookupResult{}, audit.Failure(audit.Refused, errors.New("lookup received caller values"))
	}
	return w.Writer.Lookup(ctx, request)
}

func (w lookupValueRejectingWriter) Append(ctx context.Context, request audit.AppendRequest) (audit.AppendResult, error) {
	if ctx.Value(lookupContextKey{}) != nil {
		return audit.AppendResult{}, audit.Failure(audit.Refused, errors.New("append received caller values"))
	}
	return w.Writer.Append(ctx, request)
}

func TestStandaloneRecordIsImmediatelyReconcilable(t *testing.T) {
	ctx := context.Background()
	operationPolicy := audit.ContextFacts(audit.OperationFact(audit.ContextRequired, audit.Provenances(audit.ServerDerived), audit.Internal, audit.AsPlaintext))
	event := audit.Declare(audit.EventPolicy[invoiceSent]{
		Semantics: audit.Semantics(1, audit.PolicyGolden("invoice.sent", strings.Repeat("01", 32))),
		Descriptor: audit.Descriptor{
			Resource: "billing.invoice", Action: "invoice.sent", Owner: "billing.team",
			Purpose: "business.audit", Retention: "business.forever", Consequence: audit.Required,
			Context: operationPolicy,
		},
		Target:     audit.NoEventTarget[invoiceSent](),
		Outcome:    audit.EventOutcome(audit.Outcomes("sent"), func(invoiceSent) audit.Outcome { return "sent" }),
		OccurredAt: audit.EventOccurredAt(func(value invoiceSent) time.Time { return value.At }),
		Fields: audit.EventFields(
			audit.EventValue("invoice_number", func(value invoiceSent) string { return value.Number }, audit.Text(), audit.Public),
		),
	})
	operationType := audit.DeclareOperation(audit.OperationPolicy{
		Name: "billing.invoice.send", Semantics: audit.Semantics(1),
		Retention: "business.forever", Consequence: audit.Required,
		Context: operationPolicy, Members: audit.OperationMembers(event),
	})
	semantic, err := audit.HMACSemanticDigester("semantic-2026", bytesOf(1))
	if err != nil {
		t.Fatal(err)
	}
	identities, err := audit.HMACIdentityKeyring(audit.HMACIdentityKey{KeyID: "identity-2026", Key: bytesOf(2), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := audit.Compile(audit.CatalogSpec{
		ID: "billing.audit", Owner: "billing.team", Generation: 1,
		Retention: audit.RetentionRules(audit.KeepForever("business.forever")),
		Semantics: semantic.Description(), Identities: identities.ActiveDescription(), Integrity: audit.IntegrityOnly(),
	}, event, operationType)
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := audit.Lineage(catalog)
	if err != nil {
		t.Fatal(err)
	}
	log, err := auditmemory.NewLog(auditmemory.LogSpec{})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := auditmemory.NewDeployment(log)
	if err != nil {
		t.Fatal(err)
	}
	change, err := audit.NewCatalogChangeRef("deploy.audit", "release-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := deployment.InstallAndActivate(ctx, catalogs, change); err != nil {
		t.Fatal(err)
	}
	if err := deployment.InstallAndActivate(ctx, catalogs, change); err != nil {
		t.Fatalf("rerun deployment: %v", err)
	}
	store, err := auditmemory.New(auditmemory.Spec{Log: log})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := audit.NewContextValue(audit.OperationID{7}, audit.ServerDerived)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := audit.StaticContext(audit.Context{Operation: operation})
	if err != nil {
		t.Fatal(err)
	}
	switching := &staleSwitchWriter{Writer: store}
	writer := lookupValueRejectingWriter{Writer: switching}
	trackedResolver := &countingContextResolver{ContextResolver: resolver}
	recorder, err := audit.New(audit.Config{
		Catalogs: catalogs, Writer: writer, Context: trackedResolver,
		Semantics: semantic, Identities: identities,
		Observer: audit.ObserverFunc(func(audit.AuditObservation) { panic("observer must be isolated") }),
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := event.New(invoiceSent{Number: "INV-42", At: time.Unix(1_789_000_000, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	callerContext := context.WithValue(ctx, lookupContextKey{}, "private")
	result, err := recorder.Record(callerContext, draft, audit.InOperation(operationType), audit.WithIdempotencyKey("invoice-inv-42"))
	if err != nil {
		t.Fatal(err)
	}
	receipt, ok := result.Receipt()
	if !ok || receipt.Disposition() != audit.Inserted || receipt.Settlement() != audit.Committed {
		t.Fatalf("receipt = %#v, %v", receipt, ok)
	}
	key, ok := receipt.ReconcileKey()
	if !ok {
		t.Fatal("missing reconcile key")
	}
	lookup, err := recorder.Lookup(context.WithValue(ctx, lookupContextKey{}, "private"), key)
	if err != nil {
		t.Fatal(err)
	}
	if lookup.State() != audit.Found {
		t.Fatalf("lookup state = %v", lookup.State())
	}
	resolved, ok := lookup.Receipt()
	if !ok || resolved.RevisionID() != receipt.RevisionID() || resolved.OperationID() != receipt.OperationID() {
		t.Fatalf("resolved receipt = %#v, %v", resolved, ok)
	}
	replayed, err := recorder.Record(callerContext, draft, audit.InOperation(operationType), audit.WithIdempotencyKey("invoice-inv-42"))
	if err != nil {
		t.Fatal(err)
	}
	replayReceipt, ok := replayed.Receipt()
	if !ok || replayReceipt.Disposition() != audit.Replayed || replayReceipt.RevisionID() != receipt.RevisionID() || replayReceipt.OperationID() != receipt.OperationID() {
		t.Fatalf("replay receipt = %#v, %v", replayReceipt, ok)
	}
	replayKey, ok := replayReceipt.ReconcileKey()
	if !ok {
		t.Fatal("replay receipt has no reconcile key")
	}
	replayLookup, err := recorder.Lookup(ctx, replayKey)
	if err != nil || replayLookup.State() != audit.Found {
		t.Fatalf("replay lookup = (%v, %v)", replayLookup.State(), err)
	}
	resolvedBeforeStale := trackedResolver.calls.Load()
	switching.stale.Store(true)
	if _, err := recorder.Record(ctx, draft, audit.InOperation(operationType)); !errors.Is(err, audit.ErrStaleCatalog) {
		t.Fatalf("record against stale store = %v", err)
	}
	if trackedResolver.calls.Load() != resolvedBeforeStale {
		t.Fatal("stale record invoked the context resolver")
	}
	callbackCalls := 0
	if _, err := recorder.Within(ctx, audit.GroupSpec{Operation: operationType}, func(context.Context) error {
		callbackCalls++
		return nil
	}); !errors.Is(err, audit.ErrStaleCatalog) {
		t.Fatalf("group against stale store = %v", err)
	}
	if callbackCalls != 0 || trackedResolver.calls.Load() != resolvedBeforeStale {
		t.Fatalf("stale group invoked callback/resolver: %d/%d", callbackCalls, trackedResolver.calls.Load()-resolvedBeforeStale)
	}
}

func bytesOf(value byte) []byte {
	return bytes.Repeat([]byte{value}, 32)
}
