package auditpg

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
)

type alphaOccurrence struct {
	Number string
	At     time.Time
}

type failOnceWriter struct {
	audit.Writer
	mu     sync.Mutex
	failed bool
}

func (w *failOnceWriter) Append(ctx context.Context, request audit.AppendRequest) (audit.AppendResult, error) {
	w.mu.Lock()
	if !w.failed {
		w.failed = true
		w.mu.Unlock()
		return audit.AppendResult{}, audit.Failure(audit.NotWritten, errors.New("simulated pre-write refusal"))
	}
	w.mu.Unlock()
	return w.Writer.Append(ctx, request)
}

func TestStandaloneAppendIdempotencyAndReconciliationHappyPath(t *testing.T) {
	ctx := context.Background()
	db, database := newStubDB(t)
	defer db.Close()
	semantic, err := audit.HMACSemanticDigester("semantic-2026", bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	identities, err := audit.HMACIdentityKeyring(audit.HMACIdentityKey{KeyID: "identity-2026", Key: bytes.Repeat([]byte{2}, 32), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	contextPolicy := audit.ContextFacts(audit.OperationFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Internal, audit.AsPlaintext))
	event := audit.Declare(audit.EventPolicy[alphaOccurrence]{
		Semantics: audit.Semantics(1, audit.PolicyGolden("invoice.alpha", strings.Repeat("07", 32))),
		Descriptor: audit.Descriptor{
			Resource: "billing.invoice", Action: "billing.invoice.sent", Owner: "billing.team",
			Purpose: "business.audit", Retention: "business.forever", Consequence: audit.Required, Context: contextPolicy,
		},
		Target:     audit.NoEventTarget[alphaOccurrence](),
		Outcome:    audit.EventOutcome(audit.Outcomes("sent"), func(alphaOccurrence) audit.Outcome { return "sent" }),
		OccurredAt: audit.EventOccurredAt(func(value alphaOccurrence) time.Time { return value.At }),
		Fields: audit.EventFields(
			audit.EventValue("invoice_number", func(value alphaOccurrence) string { return value.Number }, audit.Text(), audit.Public),
		),
	})
	catalog, err := audit.Compile(audit.CatalogSpec{
		ID: "billing.audit", Owner: "billing.team", Generation: 1,
		Retention: audit.RetentionRules(audit.KeepForever("business.forever")),
		Semantics: semantic.Description(), Identities: identities.ActiveDescription(), Integrity: audit.IntegrityOnly(),
	}, event)
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := audit.Lineage(catalog)
	if err != nil {
		t.Fatal(err)
	}
	database.active, database.set = catalog.Ref(), catalogs.Digest()
	store, err := New(Spec{DB: db, Source: crudsql.Postgres(db)})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Check(ctx); err != nil {
		t.Fatal(err)
	}
	operationID := audit.OperationID{9}
	operation, err := audit.NewContextValue(operationID, audit.Verified)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := audit.StaticContext(audit.Context{Operation: operation})
	if err != nil {
		t.Fatal(err)
	}
	writer := &failOnceWriter{Writer: store}
	recorder, err := audit.New(audit.Config{Catalogs: catalogs, Writer: writer, Context: resolver, Semantics: semantic, Identities: identities})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := event.New(alphaOccurrence{Number: "INV-100", At: time.Unix(1_790_000_000, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := recorder.Record(ctx, draft, audit.WithIdempotencyKey("invoice-inv-100"))
	if !errors.Is(err, audit.ErrNotWritten) {
		t.Fatalf("first simulated failure = %v", err)
	}
	retry, ok := failed.RetryToken()
	if !ok {
		t.Fatal("certain pre-write failure did not return a retry token")
	}
	inserted, err := recorder.Retry(ctx, retry)
	if err != nil {
		t.Fatal(err)
	}
	receipt, ok := inserted.Receipt()
	if !ok || receipt.Disposition() != audit.Inserted || receipt.Settlement() != audit.Committed {
		t.Fatalf("insert receipt = (%#v, %v)", receipt, ok)
	}
	replayed, err := recorder.Retry(ctx, retry)
	if err != nil {
		t.Fatal(err)
	}
	replayReceipt, ok := replayed.Receipt()
	if !ok || replayReceipt.Disposition() != audit.Replayed || replayReceipt.RevisionID() != receipt.RevisionID() {
		t.Fatalf("replay receipt = (%#v, %v)", replayReceipt, ok)
	}
	key, ok := receipt.ReconcileKey()
	if !ok {
		t.Fatal("insert receipt has no reconcile key")
	}
	lookup, err := recorder.Lookup(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok := lookup.Receipt()
	if !ok || resolved.RevisionID() != receipt.RevisionID() {
		t.Fatalf("lookup receipt = (%#v, %v)", resolved, ok)
	}
}
