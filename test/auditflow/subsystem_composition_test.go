package auditflow_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/app/module"
	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/audit/auditmemory"
	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/jobs/jobsmemory"
	"github.com/frostgrove/vv/storage"
	"github.com/frostgrove/vv/storage/storagefs"
)

type applicationEvidence struct {
	primary   string
	secondary string
	first     uint64
	last      uint64
	count     uint64
	occurred  time.Time
}

type applicationEvidenceTypes struct {
	eventCommit    *audit.EventType[applicationEvidence]
	jobAttempt     *audit.EventType[applicationEvidence]
	storagePut     *audit.EventType[applicationEvidence]
	storagePromote *audit.EventType[applicationEvidence]
	storageDelete  *audit.EventType[applicationEvidence]
	moduleProfile  *audit.EventType[applicationEvidence]
}

type applicationPrivateMaterial struct {
	payload    string
	credential string
	metadata   string
}

type applicationOperationKey struct{}
type applicationPrivateKey struct{}

type observingAuditWriter struct {
	audit.Writer
	mu        sync.Mutex
	revisions []audit.RevisionWireView
}

type observingAuditTokenizer struct {
	audit.Tokenizer
	mu        sync.Mutex
	plaintext [][]byte
}

type applicationAuditFixture struct {
	recorder  *audit.Recorder
	writer    *observingAuditWriter
	tokenizer *observingAuditTokenizer
	events    applicationEvidenceTypes
}

type applicationCapture struct {
	receipt    audit.Receipt
	revision   audit.RevisionWireView
	tokenInput [][]byte
}

type sourcedAccount struct{ applied int }
type sourcedAccountID struct{ tenant, number string }
type sourcedDecision struct{ Detail string }

func TestApplicationTranslatesExactEventCommitAndJobAttemptAtOneBoundary(t *testing.T) {
	auditing := newApplicationAuditFixture(t)

	t.Run("event commit", func(t *testing.T) {
		operation := audit.OperationID{0x31}
		private := applicationPrivateMaterial{payload: "event-payload-secret-3101", credential: "event-bearer-secret-3102"}
		ctx := applicationEvidenceContext(t.Context(), operation, private)
		log, err := eventmemory.NewLog(eventmemory.LogSpec{})
		if err != nil {
			t.Fatal(err)
		}
		store, err := eventmemory.New(eventmemory.Spec{Log: log})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		aggregate := event.Define[sourcedAccount]("auditflow.account", func(id sourcedAccountID) event.Key {
			return event.Compose(id.tenant, id.number)
		})
		decided := event.Declare(aggregate, "auditflow.account.decided", event.From(event.JSON[sourcedDecision]()),
			func(state sourcedAccount, _ sourcedDecision) sourcedAccount {
				state.applied++
				return state
			})
		repository, err := event.Bind(event.Open(store), aggregate)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := store.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		txContext := eventmemory.WithTransaction(ctx, tx)
		id := sourcedAccountID{tenant: "tenant-31", number: "account-7"}
		_, at, err := repository.Load(txContext, id)
		if err != nil {
			t.Fatal(err)
		}
		_, commit, err := repository.Append(txContext, at,
			decided.New(id, sourcedDecision{Detail: private.payload}),
			decided.New(id, sourcedDecision{Detail: "second-event-payload-secret-3103"}),
		)
		if err != nil {
			t.Fatal(err)
		}
		if !commit.Authority().Valid() {
			t.Fatal("event commit did not retain its event-store transaction authority")
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		state, loaded, err := repository.Load(ctx, id)
		if err != nil || state.applied != 2 || loaded.Version() != commit.Last() {
			t.Fatalf("committed event stream = state:%+v version:%d error:%v", state, loaded.Version(), err)
		}

		captured, err := auditing.capture(ctx, auditing.events.eventCommit, applicationEvidence{
			primary: string(commit.Stream().Key), secondary: commit.Stream().Family,
			first: uint64(commit.First()), last: uint64(commit.Last()), count: uint64(commit.Count()), occurred: time.Now().UTC(),
		})
		if err != nil {
			t.Fatal(err)
		}
		assertApplicationCapture(t, captured, operation, "auditflow.event.commit", map[audit.FieldName]string{
			"stream_family": commit.Stream().Family,
			"stream_key":    string(commit.Stream().Key),
		}, map[audit.FieldName]uint64{
			"first": uint64(commit.First()), "last": uint64(commit.Last()), "count": uint64(commit.Count()),
		})
	})

	t.Run("job delivery attempt", func(t *testing.T) {
		name, err := jobs.ParseName("auditflow.deliver")
		if err != nil {
			t.Fatal(err)
		}
		policy, err := jobs.Default.Build()
		if err != nil {
			t.Fatal(err)
		}
		definition := jobs.MustDefine(jobs.DefinitionSpec[string]{Name: name, Codec: jobs.String(1), Policy: policy})
		catalog := jobs.MustCatalog(definition)
		namespace, err := jobs.NamespaceOf("auditflow", "composition")
		if err != nil {
			t.Fatal(err)
		}
		backend, err := jobsmemory.NewDefault()
		if err != nil {
			t.Fatal(err)
		}
		queue, err := jobs.NewQueue(jobs.QueueSpec{Namespace: namespace, Catalog: catalog, Sender: backend})
		if err != nil {
			t.Fatal(err)
		}
		payload := "job-payload-secret-3201"
		invocation, err := jobs.Enqueue(t.Context(), queue, definition, payload)
		if err != nil {
			t.Fatal(err)
		}
		build, err := jobs.ParseBuildID("auditflow:composition")
		if err != nil {
			t.Fatal(err)
		}
		type handledAttempt struct {
			capture applicationCapture
			meta    jobs.DeliveryMeta
			payload string
			err     error
		}
		handled := make(chan handledAttempt, 1)
		consumer := jobs.OnAdapter(definition, jobs.AdapterHandler[string](func(ctx context.Context, delivered string, meta jobs.DeliveryMeta, _ jobs.AttemptController) error {
			operation := operationForJobAttempt(meta)
			ctx = applicationEvidenceContext(ctx, operation, applicationPrivateMaterial{
				payload: delivered, credential: "worker-credential-secret-3202",
			})
			captured, captureErr := auditing.capture(ctx, auditing.events.jobAttempt, applicationEvidence{
				primary: meta.InvocationID().String(), count: uint64(meta.AttemptOrdinal().Value()), occurred: meta.StartedAt(),
			})
			handled <- handledAttempt{capture: captured, meta: meta, payload: delivered, err: captureErr}
			return captureErr
		}), jobs.Concurrency(1))
		restorer := jobs.TrustedIdentityRestorerFunc(func(ctx context.Context, _ jobs.IdentityRestoreRequest) (jobs.RestoredIdentity, error) {
			return jobs.NewRestoredIdentity(ctx, jobs.ProducerPartition{}, jobs.ProducerActor{})
		})
		workers, err := jobs.NewWorkers(jobs.WorkersSpec{
			Namespace: namespace, Catalog: catalog, Driver: backend, Build: build,
			Identity: restorer, PollInterval: jobs.MinimumPollInterval,
		}, consumer)
		if err != nil {
			t.Fatal(err)
		}
		runContext, stop := context.WithCancel(context.Background())
		runResult := make(chan error, 1)
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			runResult <- workers.Run(runContext)
		}()
		t.Cleanup(func() {
			stop()
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Error("job workers did not stop")
			}
			_ = backend.Close()
		})

		var result handledAttempt
		select {
		case result = <-handled:
		case <-finished:
			t.Fatalf("job workers stopped before delivery: %v", <-runResult)
		case <-time.After(5 * time.Second):
			t.Fatal("job attempt was not delivered")
		}
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.payload != payload || result.meta.InvocationID() != invocation || result.meta.AttemptOrdinal().Value() != 1 {
			t.Fatalf("handled attempt = payload:%q invocation:%v ordinal:%d", result.payload, result.meta.InvocationID(), result.meta.AttemptOrdinal().Value())
		}
		operation := operationForJobAttempt(result.meta)
		assertApplicationCapture(t, result.capture, operation, "auditflow.job.attempt", map[audit.FieldName]string{
			"invocation_id": invocation.String(),
		}, map[audit.FieldName]uint64{"attempt_ordinal": 1})
		drainContext, cancelDrain := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelDrain()
		if err := workers.Drain(drainContext); err != nil {
			t.Fatal(err)
		}
		stop()
		select {
		case err := <-runResult:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("workers run: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("job workers did not return")
		}
	})
}

func TestApplicationCapturesLogicalStorageAndActivatedProfileAsIndependentEvidence(t *testing.T) {
	auditing := newApplicationAuditFixture(t)
	operation := audit.OperationID{0x41}
	private := applicationPrivateMaterial{
		payload: "storage-body-secret-4101", credential: "storage-credential-secret-4102", metadata: "storage-metadata-secret-4103",
	}
	ctx := applicationEvidenceContext(t.Context(), operation, private)
	namespace, err := storage.ParseNamespace("documents")
	if err != nil {
		t.Fatal(err)
	}
	backend, err := storagefs.New(&storagefs.Config{Root: t.TempDir(), Sync: true})
	if err != nil {
		t.Fatal(err)
	}
	depot, err := storage.New(&storage.Config{Namespace: namespace.Value(), Backend: backend})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })

	putKey, err := storage.ParseKey("invoices/2026/lease.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := depot.Put(ctx, putKey, bytes.NewBufferString(private.payload), storage.PutOptions{
		Mode: storage.CreateOnly, Metadata: storage.Metadata{"authorization": private.metadata},
	}); err != nil {
		t.Fatal(err)
	}
	put := captureStorageEvidence(t, auditing, ctx, auditing.events.storagePut, namespace, putKey)
	assertApplicationCapture(t, put, operation, "auditflow.storage.put", map[audit.FieldName]string{
		"namespace": namespace.Value(), "key": putKey.Value(),
	}, nil)

	staged, err := depot.Stage(ctx, bytes.NewBufferString("staged-body-secret-4104"), storage.StageOptions{
		Metadata: storage.Metadata{"credential": private.credential},
	})
	if err != nil {
		t.Fatal(err)
	}
	promotedKey, err := storage.ParseKey("invoices/2026/promoted.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := depot.Promote(ctx, staged.ID, promotedKey, storage.PromoteOptions{Mode: storage.CreateOnly}); err != nil {
		t.Fatal(err)
	}
	promoted := captureStorageEvidence(t, auditing, ctx, auditing.events.storagePromote, namespace, promotedKey)
	assertApplicationCapture(t, promoted, operation, "auditflow.storage.promote", map[audit.FieldName]string{
		"namespace": namespace.Value(), "key": promotedKey.Value(),
	}, nil)

	if err := depot.Delete(ctx, putKey, storage.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	deleted := captureStorageEvidence(t, auditing, ctx, auditing.events.storageDelete, namespace, putKey)
	assertApplicationCapture(t, deleted, operation, "auditflow.storage.delete", map[audit.FieldName]string{
		"namespace": namespace.Value(), "key": putKey.Value(),
	}, nil)
	if _, err := depot.Head(ctx, putKey); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("deleted object head = %v", err)
	}

	started := make([]string, 0, 1)
	definition := module.New("documents").
		Routes(func() { started = append(started, "route") }).
		Workers(func() { started = append(started, "worker") }).
		MustBuild()
	catalog := module.MustCatalog(definition)
	profile := module.Serving
	if err := catalog.Check(profile); err != nil {
		t.Fatal(err)
	}
	for _, constructor := range definition.Active(profile) {
		constructor.(func())()
	}
	if !slices.Equal(started, []string{"route"}) {
		t.Fatalf("serving profile activated %v", started)
	}
	profileCapture, err := auditing.capture(ctx, auditing.events.moduleProfile, applicationEvidence{
		primary: definition.Name(), secondary: profile.Name, count: uint64(len(started)), occurred: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertApplicationCapture(t, profileCapture, operation, "auditflow.module.profile", map[audit.FieldName]string{
		"module": definition.Name(), "profile": profile.Name,
	}, map[audit.FieldName]uint64{"active_constructors": 1})

	revisions := []audit.RevisionID{put.receipt.RevisionID(), promoted.receipt.RevisionID(), deleted.receipt.RevisionID(), profileCapture.receipt.RevisionID()}
	for index, revision := range revisions {
		if revision == (audit.RevisionID{}) || slices.Contains(revisions[:index], revision) {
			t.Fatalf("independent capture %d has duplicate or empty revision ID", index)
		}
	}
}

func newApplicationAuditFixture(t *testing.T) *applicationAuditFixture {
	t.Helper()
	contextPolicy := audit.ContextFacts(
		audit.OperationFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Internal, audit.AsPlaintext),
	)
	goldens := map[audit.Action]string{
		"auditflow.event.commit":    "1b3043e8683e80d0c93757e2fe9b407787160edb2b3306b03eedca76ebeb1ee8",
		"auditflow.job.attempt":     "7756b19263bbbc192fe9e9313c45bd4f37ad346ba590408b5fd389987f4a31b8",
		"auditflow.storage.put":     "2c3ac2b5f9c6e87cacb2ac51182faf5c99e1ee369112f2481c2811328ea8a587",
		"auditflow.storage.promote": "5bc8a0651258301a1f479fdee9c83ae6400d7a5042431130347636d8a08857f6",
		"auditflow.storage.delete":  "257f152bd50306a420ca9d8716b1c2deb67b3a861de7503dbc40d58ac0be7c03",
		"auditflow.module.profile":  "3224467a00c5692e227b68400d1a7006d72bb0d0ab80fcaa54c83ca75b975746",
	}
	declare := func(resource audit.Resource, action audit.Action, outcome audit.Outcome, fields ...audit.EventField[applicationEvidence]) *audit.EventType[applicationEvidence] {
		return audit.Declare(audit.EventPolicy[applicationEvidence]{
			Semantics: audit.Semantics(1, audit.PolicyGolden(audit.FixtureName(action), goldens[action])),
			Descriptor: audit.Descriptor{
				Resource: resource, Action: action, Owner: "auditflow.application", Purpose: "business.audit",
				Retention: "business.forever", Consequence: audit.Required, Context: contextPolicy,
			},
			Target: audit.NoEventTarget[applicationEvidence](),
			Outcome: audit.EventOutcome(audit.Outcomes(outcome), func(applicationEvidence) audit.Outcome {
				return outcome
			}),
			OccurredAt: audit.EventOccurredAt(func(value applicationEvidence) time.Time { return value.occurred }),
			Fields:     audit.EventFields(fields...),
		})
	}
	textPrimary := func(name audit.FieldName) audit.EventField[applicationEvidence] {
		return audit.EventTokenized(name, func(value applicationEvidence) string { return value.primary }, audit.Text(), audit.Internal)
	}
	textSecondary := func(name audit.FieldName) audit.EventField[applicationEvidence] {
		return audit.EventTokenized(name, func(value applicationEvidence) string { return value.secondary }, audit.Text(), audit.Internal)
	}
	number := func(name audit.FieldName, extract func(applicationEvidence) uint64) audit.EventField[applicationEvidence] {
		return audit.EventValue(name, extract, audit.Uint64(), audit.Internal)
	}
	events := applicationEvidenceTypes{
		eventCommit: declare("auditflow.event_stream", "auditflow.event.commit", "committed",
			textSecondary("stream_family"), textPrimary("stream_key"),
			number("first", func(value applicationEvidence) uint64 { return value.first }),
			number("last", func(value applicationEvidence) uint64 { return value.last }),
			number("count", func(value applicationEvidence) uint64 { return value.count })),
		jobAttempt: declare("auditflow.job_delivery", "auditflow.job.attempt", "invoked",
			textPrimary("invocation_id"), number("attempt_ordinal", func(value applicationEvidence) uint64 { return value.count })),
		storagePut: declare("auditflow.storage_object", "auditflow.storage.put", "stored",
			textPrimary("namespace"), textSecondary("key")),
		storagePromote: declare("auditflow.storage_object", "auditflow.storage.promote", "promoted",
			textPrimary("namespace"), textSecondary("key")),
		storageDelete: declare("auditflow.storage_object", "auditflow.storage.delete", "deleted",
			textPrimary("namespace"), textSecondary("key")),
		moduleProfile: declare("auditflow.module", "auditflow.module.profile", "activated",
			textPrimary("module"), textSecondary("profile"),
			number("active_constructors", func(value applicationEvidence) uint64 { return value.count })),
	}
	verifyApplicationEvidenceGoldens(t, events)
	semantic, err := audit.HMACSemanticDigester("auditflow-composition-semantic", bytes.Repeat([]byte{0x51}, 32))
	if err != nil {
		t.Fatal(err)
	}
	identities, err := audit.HMACIdentityKeyring(audit.HMACIdentityKey{
		KeyID: "auditflow-composition-identity", Key: bytes.Repeat([]byte{0x52}, 32), Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	baseTokenizer, err := audit.HMACTokenizer(audit.HMACTokenKey{
		KeyID: "auditflow-composition-token", Key: bytes.Repeat([]byte{0x53}, 32), Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	declarations := []audit.Declaration{
		events.eventCommit, events.jobAttempt, events.storagePut, events.storagePromote, events.storageDelete, events.moduleProfile,
	}
	catalog, err := audit.Compile(audit.CatalogSpec{
		ID: "auditflow.composition", Owner: "auditflow.application", Generation: 1,
		Retention: audit.RetentionRules(audit.KeepForever("business.forever")),
		Semantics: semantic.Description(), Identities: identities.ActiveDescription(),
		Tokens: baseTokenizer.ActiveDescription(), Integrity: audit.IntegrityOnly(),
	}, declarations...)
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
	change, err := audit.NewCatalogChangeRef("auditflow.deploy", "composition-tests")
	if err != nil {
		t.Fatal(err)
	}
	if err := deployment.InstallAndActivate(t.Context(), catalogs, change); err != nil {
		t.Fatal(err)
	}
	store, err := auditmemory.New(auditmemory.Spec{Log: log})
	if err != nil {
		t.Fatal(err)
	}
	writer := &observingAuditWriter{Writer: store}
	tokenizer := &observingAuditTokenizer{Tokenizer: baseTokenizer}
	resolver := audit.ContextResolverFunc(func(ctx context.Context) (audit.Context, error) {
		operation, ok := ctx.Value(applicationOperationKey{}).(audit.OperationID)
		if !ok {
			return audit.Context{}, errors.New("auditflow: application operation is missing")
		}
		value, err := audit.NewContextValue(operation, audit.Verified)
		if err != nil {
			return audit.Context{}, err
		}
		return audit.Context{Operation: value}, nil
	})
	recorder, err := audit.New(audit.Config{
		Catalogs: catalogs, Writer: writer, Context: resolver,
		Semantics: semantic, Identities: identities, Tokenizer: tokenizer,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
		_ = deployment.Close()
	})
	return &applicationAuditFixture{recorder: recorder, writer: writer, tokenizer: tokenizer, events: events}
}

func verifyApplicationEvidenceGoldens(t *testing.T, events applicationEvidenceTypes) {
	t.Helper()
	at := time.Date(2026, 9, 9, 1, 2, 3, 0, time.UTC)
	fixtures := []struct {
		declared *audit.EventType[applicationEvidence]
		value    applicationEvidence
	}{
		{events.eventCommit, applicationEvidence{primary: "tenant-31/account-7", secondary: "auditflow.account", first: 1, last: 2, count: 2, occurred: at}},
		{events.jobAttempt, applicationEvidence{primary: "00000000-0000-4000-8000-000000000001", count: 1, occurred: at}},
		{events.storagePut, applicationEvidence{primary: "documents", secondary: "invoices/2026/lease.pdf", occurred: at}},
		{events.storagePromote, applicationEvidence{primary: "documents", secondary: "invoices/2026/promoted.pdf", occurred: at}},
		{events.storageDelete, applicationEvidence{primary: "documents", secondary: "invoices/2026/lease.pdf", occurred: at}},
		{events.moduleProfile, applicationEvidence{primary: "documents", secondary: "serving", count: 1, occurred: at}},
	}
	for _, fixture := range fixtures {
		description := fixture.declared.Description()
		fingerprint, err := audit.ComputeEventFixtureFingerprint(fixture.declared, audit.FixtureName(description.Action), fixture.value)
		if err != nil {
			t.Fatal(err)
		}
		if fingerprint != description.Semantics.Fixtures[0].Fingerprint {
			t.Errorf("%s fixture fingerprint = %x", description.Action, fingerprint)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
}

func (fixture *applicationAuditFixture) capture(ctx context.Context, declared *audit.EventType[applicationEvidence], value applicationEvidence) (applicationCapture, error) {
	draft, err := declared.New(value)
	if err != nil {
		return applicationCapture{}, err
	}
	before := fixture.tokenizer.count()
	result, err := fixture.recorder.Capture(ctx, draft)
	if err != nil {
		return applicationCapture{}, err
	}
	if result.Staged() {
		return applicationCapture{}, errors.New("auditflow: independent evidence was staged")
	}
	receipt, ok := result.Receipt()
	if !ok || receipt.Disposition() != audit.Inserted || receipt.Settlement() != audit.Committed {
		return applicationCapture{}, fmt.Errorf("auditflow: standalone capture receipt is %#v", receipt)
	}
	revision, ok := fixture.writer.revision(receipt.RevisionID())
	if !ok {
		return applicationCapture{}, errors.New("auditflow: committed revision did not reach the audit writer")
	}
	return applicationCapture{receipt: receipt, revision: revision, tokenInput: fixture.tokenizer.since(before)}, nil
}

func captureStorageEvidence(t *testing.T, fixture *applicationAuditFixture, ctx context.Context, declared *audit.EventType[applicationEvidence], namespace storage.Namespace, key storage.Key) applicationCapture {
	t.Helper()
	captured, err := fixture.capture(ctx, declared, applicationEvidence{
		primary: namespace.Value(), secondary: key.Value(), occurred: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return captured
}

func assertApplicationCapture(t *testing.T, captured applicationCapture, operation audit.OperationID, action audit.Action, tokenized map[audit.FieldName]string, numbers map[audit.FieldName]uint64) {
	t.Helper()
	revision := captured.revision
	if captured.receipt.OperationID() != operation || revision.Header.OperationID != operation || captured.receipt.Settlement() != audit.Committed {
		t.Fatalf("capture operation/settlement = receipt:%x header:%x settlement:%v", captured.receipt.OperationID(), revision.Header.OperationID, captured.receipt.Settlement())
	}
	if len(revision.Actors) != 0 || len(revision.Context) != 1 || len(revision.Items) != 1 {
		t.Fatalf("capture shape = actors:%d context:%d items:%d", len(revision.Actors), len(revision.Context), len(revision.Items))
	}
	fact := revision.Context[0]
	operationWire := make([]byte, hex.EncodedLen(len(operation)))
	hex.Encode(operationWire, operation[:])
	if fact.Kind != audit.OperationContext || fact.Provenance != audit.Verified || fact.Mode != audit.AsPlaintext || !bytes.Equal(fact.Plaintext, operationWire) {
		t.Fatalf("operation fact = %+v", fact)
	}
	item := revision.Items[0]
	if item.Kind != audit.EventItem || item.Action != action || item.Target.State != 0 || len(item.Values) != len(tokenized)+len(numbers) {
		t.Fatalf("captured item = kind:%v action:%s target:%+v values:%d", item.Kind, item.Action, item.Target, len(item.Values))
	}
	tokenPosition := 0
	seen := make(map[audit.FieldName]struct{}, len(item.Values))
	for _, value := range item.Values {
		if _, duplicate := seen[value.Field]; duplicate {
			t.Fatalf("duplicate captured field %q", value.Field)
		}
		seen[value.Field] = struct{}{}
		if expected, ok := tokenized[value.Field]; ok {
			if value.State != audit.ValuePresent || value.Mode != audit.AsToken || len(value.Plaintext) != 0 || value.Token.Algorithm() == "" || len(value.Token.Bytes()) != sha256.Size || len(value.Protected.Ciphertext()) != 0 {
				t.Fatalf("tokenized field %q = %+v", value.Field, value)
			}
			if tokenPosition >= len(captured.tokenInput) || string(captured.tokenInput[tokenPosition]) != expected {
				t.Fatalf("tokenized field %q input = %q, want %q", value.Field, tokenAt(captured.tokenInput, tokenPosition), expected)
			}
			tokenPosition++
			continue
		}
		expected, ok := numbers[value.Field]
		if !ok || value.State != audit.ValuePresent || value.Mode != audit.AsPlaintext || len(value.Plaintext) != 8 || binary.BigEndian.Uint64(value.Plaintext) != expected || value.Token.Algorithm() != "" || len(value.Protected.Ciphertext()) != 0 {
			t.Fatalf("numeric field %q = %+v, want %d", value.Field, value, expected)
		}
	}
	if tokenPosition != len(captured.tokenInput) || len(seen) != len(tokenized)+len(numbers) {
		t.Fatalf("captured token/field counts = %d/%d fields:%v", tokenPosition, len(captured.tokenInput), seen)
	}
}

func tokenAt(values [][]byte, index int) string {
	if index < 0 || index >= len(values) {
		return ""
	}
	return string(values[index])
}

func applicationEvidenceContext(ctx context.Context, operation audit.OperationID, private applicationPrivateMaterial) context.Context {
	ctx = context.WithValue(ctx, applicationOperationKey{}, operation)
	return context.WithValue(ctx, applicationPrivateKey{}, private)
}

func operationForJobAttempt(meta jobs.DeliveryMeta) audit.OperationID {
	invocation := meta.InvocationID().Bytes()
	var ordinal [2]byte
	binary.BigEndian.PutUint16(ordinal[:], meta.AttemptOrdinal().Value())
	digest := sha256.Sum256(append(invocation[:], ordinal[:]...))
	var operation audit.OperationID
	copy(operation[:], digest[:len(operation)])
	return operation
}

func (writer *observingAuditWriter) Append(ctx context.Context, request audit.AppendRequest) (audit.AppendResult, error) {
	if ctx.Value(applicationOperationKey{}) != nil || ctx.Value(applicationPrivateKey{}) != nil {
		return audit.AppendResult{}, audit.Failure(audit.Refused, errors.New("auditflow: caller values crossed the audit writer boundary"))
	}
	result, err := writer.Writer.Append(ctx, request)
	if err != nil {
		return audit.AppendResult{}, err
	}
	writer.mu.Lock()
	writer.revisions = append(writer.revisions, request.View().Revision.View())
	writer.mu.Unlock()
	return result, nil
}

func (writer *observingAuditWriter) revision(id audit.RevisionID) (audit.RevisionWireView, bool) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	for _, revision := range writer.revisions {
		if revision.Header.RevisionID == id {
			return revision, true
		}
	}
	return audit.RevisionWireView{}, false
}

func (tokenizer *observingAuditTokenizer) Tokenize(ctx context.Context, request audit.TokenizeRequest) (audit.Token, error) {
	plaintext := request.Plaintext()
	tokenizer.mu.Lock()
	tokenizer.plaintext = append(tokenizer.plaintext, bytes.Clone(plaintext))
	tokenizer.mu.Unlock()
	return tokenizer.Tokenizer.Tokenize(ctx, request)
}

func (tokenizer *observingAuditTokenizer) count() int {
	tokenizer.mu.Lock()
	defer tokenizer.mu.Unlock()
	return len(tokenizer.plaintext)
}

func (tokenizer *observingAuditTokenizer) since(offset int) [][]byte {
	tokenizer.mu.Lock()
	defer tokenizer.mu.Unlock()
	result := make([][]byte, len(tokenizer.plaintext)-offset)
	for index, value := range tokenizer.plaintext[offset:] {
		result[index] = bytes.Clone(value)
	}
	return result
}

var _ audit.Writer = (*observingAuditWriter)(nil)
var _ audit.Tokenizer = (*observingAuditTokenizer)(nil)
