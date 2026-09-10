package auditmemory_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/audit/auditmemory"
)

func TestGroupStagesOneRevisionAndSettlesWithTheCallerTransaction(t *testing.T) {
	ctx := context.Background()
	operationContext := audit.ContextFacts(audit.GeneratedOperationFact(audit.Internal, audit.AsPlaintext))
	event := audit.Declare(audit.EventPolicy[invoiceSent]{
		Semantics: audit.Semantics(1, audit.PolicyGolden("invoice.group", strings.Repeat("02", 32))),
		Descriptor: audit.Descriptor{
			Resource: "billing.invoice", Action: "invoice.sent", Owner: "billing.team",
			Purpose: "business.audit", Retention: "business.forever", Consequence: audit.Required,
			Context: operationContext,
		},
		Target:     audit.NoEventTarget[invoiceSent](),
		Outcome:    audit.EventOutcome(audit.Outcomes("sent"), func(invoiceSent) audit.Outcome { return "sent" }),
		OccurredAt: audit.EventOccurredAt(func(value invoiceSent) time.Time { return value.At }),
		Fields: audit.EventFields(
			audit.EventValue("invoice_number", func(value invoiceSent) string { return value.Number }, audit.Text(), audit.Internal),
		),
	})
	operation := audit.DeclareOperation(audit.OperationPolicy{
		Name: "billing.invoice.send", Semantics: audit.Semantics(1),
		Retention: "business.forever", Consequence: audit.Required,
		Context: operationContext, Members: audit.OperationMembers(event),
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
	}, event, operation)
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
	change, err := audit.NewCatalogChangeRef("deploy.audit", "release-group")
	if err != nil {
		t.Fatal(err)
	}
	if err := deployment.InstallAndActivate(ctx, catalogs, change); err != nil {
		t.Fatal(err)
	}
	store, err := auditmemory.New(auditmemory.Spec{Log: log})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := audit.StaticContext(audit.Context{})
	if err != nil {
		t.Fatal(err)
	}
	var observations []audit.AuditObservation
	recorder, err := audit.New(audit.Config{
		Catalogs: catalogs, Writer: store, Context: resolver, Semantics: semantic, Identities: identities,
		Observer: audit.ObserverFunc(func(observation audit.AuditObservation) { observations = append(observations, observation) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := event.New(invoiceSent{Number: "INV-43", At: time.Unix(1_789_000_001, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	txContext := auditmemory.WithTransaction(ctx, tx)
	group, err := recorder.Within(txContext, audit.GroupSpec{Operation: operation}, func(groupContext context.Context) error {
		if _, recordErr := recorder.Record(groupContext, draft); !errors.Is(recordErr, audit.ErrTransaction) {
			t.Fatalf("standalone record in group error = %v", recordErr)
		}
		capture, captureErr := recorder.Capture(groupContext, draft)
		if captureErr != nil {
			return captureErr
		}
		if !capture.Staged() {
			t.Fatal("capture was not staged")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, ok := group.Receipt()
	if !ok || receipt.Settlement() != audit.InCallerTransaction {
		t.Fatalf("group receipt = %#v, %v", receipt, ok)
	}
	key, ok := receipt.ReconcileKey()
	if !ok {
		t.Fatal("missing reconcile key")
	}
	before, err := recorder.Lookup(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if before.State() != audit.AbsentNow {
		t.Fatalf("uncommitted revision leaked with state %v", before.State())
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := recorder.Lookup(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if after.State() != audit.Found {
		t.Fatalf("committed revision state = %v", after.State())
	}
	if len(observations) != 1 || observations[0].Kind != audit.ObservationGroup || observations[0].Phase != audit.ObservationStore || observations[0].Items != 1 || observations[0].Work.StoreCalls != 1 || observations[0].Settlement != audit.InCallerTransaction {
		t.Fatalf("group observations = %#v", observations)
	}

	concurrentTx, err := store.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	concurrentContext := auditmemory.WithTransaction(ctx, concurrentTx)
	started := make(chan struct{})
	release := make(chan struct{})
	joined := make(chan error, 1)
	type groupOutcome struct {
		result audit.GroupResult
		err    error
	}
	completed := make(chan groupOutcome, 1)
	go func() {
		result, groupErr := recorder.Within(concurrentContext, audit.GroupSpec{Operation: operation}, func(groupContext context.Context) error {
			go func() {
				_, nestedErr := recorder.Within(groupContext, audit.GroupSpec{Operation: operation}, func(context.Context) error {
					close(started)
					<-release
					return nil
				})
				joined <- nestedErr
			}()
			<-started
			return nil
		})
		completed <- groupOutcome{result: result, err: groupErr}
	}()
	<-started
	select {
	case outcome := <-completed:
		t.Fatalf("concurrent group settled before admitted callback: %v", outcome.err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	outcome := <-completed
	result, err := outcome.result, outcome.err
	if !errors.Is(err, audit.ErrGroupPoisoned) {
		t.Fatalf("concurrent group seal error = %v", err)
	}
	if _, ok := result.Receipt(); ok {
		t.Fatal("poisoned concurrent group returned a receipt")
	}
	if nestedErr := <-joined; nestedErr != nil {
		t.Fatalf("admitted nested group returned %v", nestedErr)
	}
	if err := concurrentTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}
