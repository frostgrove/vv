package auditflow_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/app/module"
	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/audit/auditmemory"
	"github.com/frostgrove/vv/jobs"
)

var errApplicationAuditUnready = errors.New("auditflow: audit runtime is not ready")

const applicationJobDiagnosticGolden = "34720fb39a1f49627dfe2eae998f697fbb8d556796325db0ff5260719de8f209"

type applicationJobDiagnostic struct {
	invocation string
	ordinal    uint64
	final      bool
	outcome    audit.Outcome
	occurred   time.Time
}

type applicationJobAuditRuntime struct {
	recorder      *audit.Recorder
	history       *audit.History
	attempts      *audit.Attempts
	store         *auditmemory.Store
	deployment    *auditmemory.Deployment
	catalogs      *audit.CatalogSet
	event         *audit.EventType[applicationJobDiagnostic]
	effectAttempt *audit.AttemptType[applicationJobEffectStart, applicationJobEffectCheckpoint, applicationJobEffectFinish]
	writer        *observingAuditWriter
	tokenizer     *observingAuditTokenizer
	accessCalls   atomic.Uint32
	mu            sync.Mutex
	captures      []applicationCapture
}

type applicationAuditServing struct {
	recorder *audit.Recorder
	history  *audit.History
	attempts *audit.Attempts
	health   applicationAuditHealth
}

func (s *applicationAuditServing) Recorder() *audit.Recorder { return s.recorder }
func (s *applicationAuditServing) History() *audit.History   { return s.history }
func (s *applicationAuditServing) Attempts() *audit.Attempts { return s.attempts }
func (s *applicationAuditServing) Health() applicationAuditHealth {
	return s.health
}

type applicationAuditHealth struct {
	store  audit.StoreInfo
	active audit.CatalogRef
}

func (h applicationAuditHealth) Check(ctx context.Context) error {
	if ctx == nil {
		return errApplicationAuditUnready
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if h.store == nil || h.store.BackingID() == (audit.BackingID{}) || h.store.LogID() == (audit.LogID{}) {
		return errApplicationAuditUnready
	}
	state := h.store.Catalogs()
	if !state.HasActive() || state.Active() != h.active {
		return errApplicationAuditUnready
	}
	return nil
}

type applicationAuditDeployment struct {
	audit.CatalogAdmin
}

func TestAuditServingProfileExposesRuntimeWithoutDeploymentAuthority(t *testing.T) {
	runtime := newApplicationJobAuditRuntime(t)
	serving := &applicationAuditServing{
		recorder: runtime.recorder,
		history:  runtime.history,
		attempts: runtime.attempts,
		health: applicationAuditHealth{
			store: runtime.store, active: runtime.catalogs.Active(),
		},
	}
	deployment := &applicationAuditDeployment{CatalogAdmin: runtime.deployment}

	var provideCalls atomic.Uint32
	var healthCalls atomic.Uint32
	servingDefinition := module.New("audit").
		Provide(func() *applicationAuditServing {
			provideCalls.Add(1)
			return serving
		}).
		Checks(func() applicationAuditHealth {
			healthCalls.Add(1)
			return serving.Health()
		}).
		MustBuild()
	deploymentDefinition := module.New("audit-deployment").
		Provide(func() *applicationAuditDeployment { return deployment }).
		MustBuild()

	servingCatalog := module.MustCatalog(servingDefinition)
	if err := servingCatalog.Check(module.Serving); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(servingCatalog.Names(), []string{"audit"}) {
		t.Fatalf("serving catalog modules = %v", servingCatalog.Names())
	}
	if provideCalls.Load() != 0 || healthCalls.Load() != 0 {
		t.Fatal("module declaration invoked a runtime constructor")
	}
	for _, contribution := range servingDefinition.Contributions() {
		if contribution.Kind == module.WorkerKind || contribution.Kind == module.SeederKind {
			t.Fatalf("serving audit module exposed %s contribution", contribution.Kind)
		}
	}

	active := servingDefinition.Active(module.Serving)
	if len(active) != 2 {
		t.Fatalf("serving audit constructors = %d, want provide and health", len(active))
	}
	provided, ok := active[0].(func() *applicationAuditServing)
	if !ok || provided() != serving {
		t.Fatalf("serving provider = %T", active[0])
	}
	healthProvider, ok := active[1].(func() applicationAuditHealth)
	if !ok {
		t.Fatalf("serving health provider = %T", active[1])
	}
	health := healthProvider()
	if provideCalls.Load() != 1 || healthCalls.Load() != 1 {
		t.Fatalf("constructor calls = provide:%d health:%d", provideCalls.Load(), healthCalls.Load())
	}
	if serving.Recorder() != runtime.recorder || serving.History() != runtime.history || serving.Attempts() != runtime.attempts || serving.History().Profile() != audit.PublicOnePageDevelopmentAlpha {
		t.Fatal("serving graph did not expose the selected recorder, history and attempts")
	}
	if err := health.Check(t.Context()); err != nil {
		t.Fatalf("selected audit health contribution: %v", err)
	}
	wrongCatalog := health
	wrongCatalog.active = audit.CatalogRef{}
	if err := wrongCatalog.Check(t.Context()); !errors.Is(err, errApplicationAuditUnready) {
		t.Fatalf("health with wrong active catalog = %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := health.Check(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("health with canceled context = %v", err)
	}

	if _, exposed := any(serving).(audit.CatalogAdmin); exposed {
		t.Fatal("serving graph dynamically exposed catalog administration")
	}
	if _, exposed := any(runtime.store).(audit.CatalogAdmin); exposed {
		t.Fatal("serving store dynamically exposed catalog administration")
	}
	if _, exposed := any(health).(audit.CatalogAdmin); exposed {
		t.Fatal("serving health dynamically exposed catalog administration")
	}
	if _, exposed := any(deployment).(audit.CatalogAdmin); !exposed {
		t.Fatal("separate deployment graph did not expose catalog administration")
	}
	if names := module.MustCatalog(deploymentDefinition).Names(); !slices.Equal(names, []string{"audit-deployment"}) {
		t.Fatalf("deployment catalog modules = %v", names)
	}
}

func TestJobRedeliveryCreatesDistinctSafePerAttemptEvidence(t *testing.T) {
	runtime := newApplicationJobAuditRuntime(t)
	invocation, err := jobs.ParseInvocationID("00000000-0000-4000-8000-000000000321")
	if err != nil {
		t.Fatal(err)
	}
	first := newApplicationDeliveryMeta(t, invocation, 1, 0, 1)
	last := newApplicationDeliveryMeta(t, invocation, 2, 1, 1)
	if first.LastChargedAttempt() || !last.LastChargedAttempt() {
		t.Fatalf("attempt finality = first:%t last:%t", first.LastChargedAttempt(), last.LastChargedAttempt())
	}

	leaseSecret := "lease-owner-secret-7201"
	lastFailureSecret := "downstream-failure-secret-7202"
	payloadSecret := "job-payload-secret-7203"
	var handled []uint16
	next := jobs.AdapterHandler[string](func(_ context.Context, payload string, meta jobs.DeliveryMeta, _ jobs.AttemptController) error {
		if payload != payloadSecret {
			return errors.New("unexpected payload")
		}
		handled = append(handled, meta.AttemptOrdinal().Value())
		if meta.AttemptOrdinal().Value() == 1 {
			return fmt.Errorf("%w: %s", jobs.ErrLeaseLost, leaseSecret)
		}
		return errors.New(lastFailureSecret)
	})
	handler := runtime.auditDeliveries(next)

	firstErr := handler(t.Context(), payloadSecret, first, nil)
	if !errors.Is(firstErr, jobs.ErrLeaseLost) || !bytes.Contains([]byte(firstErr.Error()), []byte(leaseSecret)) {
		t.Fatalf("first delivery result = %v", firstErr)
	}
	lastErr := handler(t.Context(), payloadSecret, last, nil)
	if lastErr == nil || lastErr.Error() != lastFailureSecret {
		t.Fatalf("last delivery result = %v", lastErr)
	}
	if !slices.Equal(handled, []uint16{1, 2}) {
		t.Fatalf("handled attempt ordinals = %v", handled)
	}

	captures := runtime.recordedCaptures()
	if len(captures) != 2 {
		t.Fatalf("job diagnostic captures = %d", len(captures))
	}
	firstOperation := operationForJobAttempt(first)
	lastOperation := operationForJobAttempt(last)
	if firstOperation == (audit.OperationID{}) || lastOperation == (audit.OperationID{}) || firstOperation == lastOperation {
		t.Fatalf("per-delivery operation IDs = %x and %x", firstOperation, lastOperation)
	}
	assertApplicationJobCapture(t, captures[0], first, "lease_lost", false)
	assertApplicationJobCapture(t, captures[1], last, "failed", true)
	if captures[0].receipt.RevisionID() == captures[1].receipt.RevisionID() {
		t.Fatal("redelivery was collapsed into the first attempt revision")
	}
	for _, captured := range captures {
		assertApplicationJobSecretsAbsent(t, captured, payloadSecret, leaseSecret, lastFailureSecret,
			"worker-credential-secret-7204", "delivery-metadata-secret-7205",
			first.Definition().String(), first.Binding().String(), first.Build().String())
	}

	historyContext := applicationEvidenceContext(t.Context(), audit.OperationID{0x73}, applicationPrivateMaterial{
		payload: "history-payload-secret-7301", credential: "history-credential-secret-7302",
	})
	page, err := runtime.event.History(runtime.history).Events(historyContext, audit.Query{
		Purpose: "history.read", Role: "job-auditor",
		Fields: audit.OnlyFields("attempt_ordinal", "final_attempt"), Context: audit.NoContext(),
		Direction: audit.OldestFirst, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if runtime.accessCalls.Load() != 1 {
		t.Fatalf("history authority calls = %d", runtime.accessCalls.Load())
	}
	assertApplicationJobHistory(t, page)
}

func newApplicationJobAuditRuntime(t *testing.T) *applicationJobAuditRuntime {
	t.Helper()
	contextPolicy := audit.ContextFacts(
		audit.OperationFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Public, audit.AsPlaintext),
	)
	event := audit.Declare(audit.EventPolicy[applicationJobDiagnostic]{
		Semantics: audit.Semantics(1, audit.PolicyGolden("job.delivery.diagnostic", applicationJobDiagnosticGolden)),
		Descriptor: audit.Descriptor{
			Resource: "auditflow.job_delivery", Action: "auditflow.job.delivery", Owner: "auditflow.application",
			Purpose: "operations.audit", Retention: "operations.forever", Consequence: audit.Required, Context: contextPolicy,
		},
		Target: audit.NoEventTarget[applicationJobDiagnostic](),
		Outcome: audit.EventOutcome(audit.Outcomes("failed", "lease_lost", "succeeded"), func(value applicationJobDiagnostic) audit.Outcome {
			return value.outcome
		}),
		OccurredAt: audit.EventOccurredAt(func(value applicationJobDiagnostic) time.Time { return value.occurred }),
		Fields: audit.EventFields(
			audit.EventTokenized("invocation_id", func(value applicationJobDiagnostic) string { return value.invocation }, audit.Text(), audit.Public),
			audit.EventValue("attempt_ordinal", func(value applicationJobDiagnostic) uint64 { return value.ordinal }, audit.Uint64(), audit.Public),
			audit.EventValue("final_attempt", func(value applicationJobDiagnostic) bool { return value.final }, audit.Bool(), audit.Public),
		),
	})
	effectMember, effectOperation, effectAttempt := applicationJobEffectDeclarations()
	fingerprint, err := audit.ComputeEventFixtureFingerprint(event, "job.delivery.diagnostic", applicationJobDiagnostic{
		invocation: "00000000-0000-4000-8000-000000000321", ordinal: 2, final: true,
		outcome: "failed", occurred: time.Date(2026, 9, 9, 12, 2, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", fingerprint); got != applicationJobDiagnosticGolden {
		t.Fatalf("job diagnostic semantic golden = %s", got)
	}
	semantic, err := audit.HMACSemanticDigester("auditflow-job-semantic", bytes.Repeat([]byte{0x71}, 32))
	if err != nil {
		t.Fatal(err)
	}
	identities, err := audit.HMACIdentityKeyring(audit.HMACIdentityKey{
		KeyID: "auditflow-job-identity", Key: bytes.Repeat([]byte{0x72}, 32), Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	baseTokenizer, err := audit.HMACTokenizer(audit.HMACTokenKey{
		KeyID: "auditflow-job-token", Key: bytes.Repeat([]byte{0x73}, 32), Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := audit.HMACSigner(audit.HMACSigningKey{KeyID: "auditflow-job-signature", Key: bytes.Repeat([]byte{0x74}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := audit.HMACVerifier(audit.HMACVerificationKey{KeyID: "auditflow-job-signature", Key: bytes.Repeat([]byte{0x74}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := audit.Compile(audit.CatalogSpec{
		ID: "auditflow.job.redelivery", Owner: "auditflow.application", Generation: 1,
		Retention: audit.RetentionRules(audit.KeepForever("operations.forever")),
		Semantics: semantic.Description(), Identities: identities.ActiveDescription(),
		Tokens: baseTokenizer.ActiveDescription(), Integrity: audit.RequireSignature(signer.Description()),
		Control: audit.ControlPolicy{
			Resource: "auditflow.audit_control", Semantics: audit.Semantics(1), Purpose: "operations.audit",
			Retention: "operations.forever", Consequence: audit.Required,
			Context: audit.ContextFacts(audit.ScopeFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Public, audit.AsPlaintext)),
			Actions: audit.ControlActions(audit.AttemptContinuationAuthorized, audit.AttemptAccessDenied),
			Reasons: audit.ControlReasons(audit.ReasonsFor(audit.AttemptAccessDenied, audit.Reasons("attempt.access.denied"))),
		},
	}, event, effectMember, effectOperation, effectAttempt)
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
	change, err := audit.NewCatalogChangeRef("auditflow.deploy", "job-redelivery-tests")
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
		return audit.Context{
			Actors:    []audit.Actor{{Kind: audit.HumanActor, Reference: "job-worker:auditflow", Provenance: audit.Verified}},
			Operation: value,
		}, nil
	})
	recorder, err := audit.New(audit.Config{
		Catalogs: catalogs, Writer: writer, Context: resolver,
		Semantics: semantic, Identities: identities, Tokenizer: tokenizer, Signer: signer,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := &applicationJobAuditRuntime{
		recorder: recorder, store: store, deployment: deployment, catalogs: catalogs,
		event: event, writer: writer, tokenizer: tokenizer,
	}
	authority := audit.AccessAuthorityFunc(func(ctx context.Context, request audit.AccessRequest) (audit.AccessDecision, error) {
		runtime.accessCalls.Add(1)
		if ctx.Value(applicationOperationKey{}) != nil || ctx.Value(applicationPrivateKey{}) != nil {
			return audit.AccessDecision{}, errors.New("auditflow: caller values crossed the history authority boundary")
		}
		view := request.View()
		return audit.AllowAccess(request, audit.AccessGrantSpec{
			Roles: []audit.Reference{view.Query.Role}, Catalogs: view.Catalogs,
			Resources: view.Target.Resources, Actions: view.Query.Actions,
			Fields: view.Query.Fields.Fields, Context: view.Query.Context.Facts,
			Classifications: []audit.Classification{audit.Public}, Direction: view.Query.Direction,
			ExpiresAt:    time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
			MaxRevisions: view.Query.Limit, MaxPages: 1, MaxBytes: 1 << 20,
		})
	})
	history, err := audit.NewHistory(audit.HistoryConfig{
		Profile: audit.PublicOnePageDevelopmentAlpha, Recorder: recorder, Log: store, Exact: store, Access: authority, Verifier: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	attempts, err := audit.NewAttempts(audit.AttemptsConfig{
		Profile: audit.RunOnlyAlpha, Recorder: recorder, State: store, Types: store,
		History: history, SettlementTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime.history = history
	runtime.attempts = attempts
	runtime.effectAttempt = effectAttempt
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close job audit store: %v", err)
		}
		if err := deployment.Close(); err != nil {
			t.Errorf("close job audit deployment: %v", err)
		}
	})
	return runtime
}

func newApplicationDeliveryMeta(t *testing.T, invocation jobs.InvocationID, ordinal, spent, limit uint16) jobs.DeliveryMeta {
	t.Helper()
	name, err := jobs.ParseName("auditflow.redelivery")
	if err != nil {
		t.Fatal(err)
	}
	binding, err := jobs.ParseBindingName("auditflow.redelivery.consumer")
	if err != nil {
		t.Fatal(err)
	}
	build, err := jobs.ParseBuildID("auditflow:redelivery@2026-09-09")
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := jobs.NewAttemptOrdinal(ordinal)
	if err != nil {
		t.Fatal(err)
	}
	retrySpent, err := jobs.NewRetrySpent(spent)
	if err != nil {
		t.Fatal(err)
	}
	retryLimit, err := jobs.NewRetryLimit(limit)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	started := base.Add(time.Duration(ordinal) * time.Minute)
	meta, err := jobs.NewDeliveryMeta(jobs.DeliveryMetaSpec{
		Invocation: invocation, Definition: name, Binding: binding, Build: build,
		Attempt: attempt, RetrySpent: retrySpent, RetryLimit: retryLimit,
		CreatedAt: base, EligibleAt: base, StartedAt: started,
		AttemptDeadline: started.Add(time.Minute), MaxElapsedAt: base.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	return meta
}

func (r *applicationJobAuditRuntime) auditDeliveries(next jobs.AdapterHandler[string]) jobs.AdapterHandler[string] {
	return func(ctx context.Context, payload string, meta jobs.DeliveryMeta, controller jobs.AttemptController) error {
		result := next(ctx, payload, meta, controller)
		outcome := audit.Outcome("failed")
		if result == nil {
			outcome = "succeeded"
		} else if errors.Is(result, jobs.ErrLeaseLost) {
			outcome = "lease_lost"
		}
		operation := operationForJobAttempt(meta)
		auditContext := applicationEvidenceContext(ctx, operation, applicationPrivateMaterial{
			payload: payload, credential: "worker-credential-secret-7204", metadata: "delivery-metadata-secret-7205",
		})
		captured, captureErr := r.capture(auditContext, applicationJobDiagnostic{
			invocation: meta.InvocationID().String(), ordinal: uint64(meta.AttemptOrdinal().Value()),
			final: meta.LastChargedAttempt(), outcome: outcome, occurred: meta.StartedAt(),
		})
		if captureErr != nil {
			return errors.Join(result, captureErr)
		}
		r.mu.Lock()
		r.captures = append(r.captures, captured)
		r.mu.Unlock()
		return result
	}
}

func (r *applicationJobAuditRuntime) capture(ctx context.Context, value applicationJobDiagnostic) (applicationCapture, error) {
	draft, err := r.event.New(value)
	if err != nil {
		return applicationCapture{}, err
	}
	before := r.tokenizer.count()
	result, err := r.recorder.Capture(ctx, draft)
	if err != nil {
		return applicationCapture{}, err
	}
	if result.Staged() {
		return applicationCapture{}, errors.New("auditflow: job diagnostic was staged")
	}
	receipt, ok := result.Receipt()
	if !ok || receipt.Disposition() != audit.Inserted || receipt.Settlement() != audit.Committed {
		return applicationCapture{}, fmt.Errorf("auditflow: job diagnostic receipt is %#v", receipt)
	}
	revision, ok := r.writer.revision(receipt.RevisionID())
	if !ok {
		return applicationCapture{}, errors.New("auditflow: job diagnostic did not reach the writer")
	}
	return applicationCapture{receipt: receipt, revision: revision, tokenInput: r.tokenizer.since(before)}, nil
}

func (r *applicationJobAuditRuntime) recordedCaptures() []applicationCapture {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.captures)
}

func assertApplicationJobCapture(t *testing.T, captured applicationCapture, meta jobs.DeliveryMeta, outcome audit.Outcome, final bool) {
	t.Helper()
	operation := operationForJobAttempt(meta)
	if captured.receipt.OperationID() != operation || captured.revision.Header.OperationID != operation {
		t.Fatalf("job diagnostic operation = receipt:%x header:%x want:%x", captured.receipt.OperationID(), captured.revision.Header.OperationID, operation)
	}
	if len(captured.revision.Actors) != 0 || len(captured.revision.Context) != 1 || len(captured.revision.Items) != 1 {
		t.Fatalf("job diagnostic shape = actors:%d context:%d items:%d", len(captured.revision.Actors), len(captured.revision.Context), len(captured.revision.Items))
	}
	item := captured.revision.Items[0]
	if item.Kind != audit.EventItem || item.Resource != "auditflow.job_delivery" || item.Action != "auditflow.job.delivery" || item.Outcome != outcome || item.Reason != "" || len(item.Values) != 3 {
		t.Fatalf("job diagnostic item = %+v", item)
	}
	seen := make(map[audit.FieldName]struct{}, len(item.Values))
	for _, value := range item.Values {
		seen[value.Field] = struct{}{}
		switch value.Field {
		case "invocation_id":
			if value.Classification != audit.Public || value.Mode != audit.AsToken || len(value.Plaintext) != 0 || value.Token.Algorithm() == "" {
				t.Fatalf("invocation field = %+v", value)
			}
		case "attempt_ordinal":
			if value.Classification != audit.Public || value.Mode != audit.AsPlaintext || len(value.Plaintext) != 8 || binary.BigEndian.Uint64(value.Plaintext) != uint64(meta.AttemptOrdinal().Value()) {
				t.Fatalf("ordinal field = %+v", value)
			}
		case "final_attempt":
			want := byte(0)
			if final {
				want = 1
			}
			if value.Classification != audit.Public || value.Mode != audit.AsPlaintext || !bytes.Equal(value.Plaintext, []byte{want}) {
				t.Fatalf("final field = %+v", value)
			}
		default:
			t.Fatalf("unexpected job diagnostic field %q", value.Field)
		}
	}
	if len(seen) != 3 || len(captured.tokenInput) != 1 || string(captured.tokenInput[0]) != meta.InvocationID().String() {
		t.Fatalf("job diagnostic fields/tokens = fields:%v tokens:%q", seen, captured.tokenInput)
	}
}

func assertApplicationJobSecretsAbsent(t *testing.T, captured applicationCapture, secrets ...string) {
	t.Helper()
	var exposed bytes.Buffer
	for _, fact := range captured.revision.Context {
		exposed.Write(fact.Plaintext)
		exposed.Write(fact.Protected.Ciphertext())
	}
	for _, item := range captured.revision.Items {
		exposed.Write(item.Subject.Plaintext)
		exposed.Write(item.Target.Plaintext)
		for _, value := range item.Values {
			exposed.Write(value.Plaintext)
			exposed.Write(value.Protected.Ciphertext())
		}
	}
	for _, tokenInput := range captured.tokenInput {
		exposed.Write(tokenInput)
	}
	for _, secret := range secrets {
		if secret != "" && bytes.Contains(exposed.Bytes(), []byte(secret)) {
			t.Fatalf("job diagnostic exposed %q", secret)
		}
	}
}

func assertApplicationJobHistory(t *testing.T, page audit.Page) {
	t.Helper()
	revisions := page.Revisions()
	if len(revisions) != 2 || page.HasMore() {
		t.Fatalf("job history page = revisions:%d more:%t", len(revisions), page.HasMore())
	}
	wantOutcomes := []audit.Outcome{"lease_lost", "failed"}
	for index, revision := range revisions {
		if len(revision.Context) != 0 || len(revision.Items) != 1 || revision.Items[0].Outcome != wantOutcomes[index] {
			t.Fatalf("job history revision %d = %+v", index, revision)
		}
		values := revision.Items[0].Values
		if len(values) != 3 {
			t.Fatalf("job history values %d = %d", index, len(values))
		}
		for _, value := range values {
			switch value.Field {
			case "invocation_id":
				if value.Knowledge != audit.FieldUnprojected || len(value.Canonical) != 0 {
					t.Fatalf("history disclosed invocation at %d: %+v", index, value)
				}
			case "attempt_ordinal":
				if value.Knowledge != audit.FieldKnown || len(value.Canonical) != 8 || binary.BigEndian.Uint64(value.Canonical) != uint64(index+1) {
					t.Fatalf("history ordinal at %d = %+v", index, value)
				}
			case "final_attempt":
				if value.Knowledge != audit.FieldKnown || !bytes.Equal(value.Canonical, []byte{byte(index)}) {
					t.Fatalf("history finality at %d = %+v", index, value)
				}
			default:
				t.Fatalf("unexpected history field %q", value.Field)
			}
		}
	}
}

var _ audit.CatalogAdmin = (*applicationAuditDeployment)(nil)
