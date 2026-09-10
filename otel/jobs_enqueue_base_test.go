package vvotel_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
	"github.com/frostgrove/vv/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type jobsEnqueueContextKey struct{}

type jobsEnqueueSender struct {
	description jobs.BackendDescription
	outcome     jobs.PlacementOutcome
	failAt      int
	failure     error
	calls       int
	contexts    []context.Context
	placements  []jobs.Placement
}

func (s *jobsEnqueueSender) Description() jobs.BackendDescription { return s.description }

func (s *jobsEnqueueSender) Place(ctx context.Context, placement jobs.Placement) (jobs.PlacementResult, error) {
	s.calls++
	s.contexts = append(s.contexts, ctx)
	s.placements = append(s.placements, placement)
	if s.calls == s.failAt {
		return jobs.PlacementResult{}, s.failure
	}
	return jobs.NewPlacementResult(placement.Candidate(), s.outcome)
}

type jobsEnqueueStager struct {
	transaction jobs.TransactionContext
	calls       int
	contexts    []context.Context
	placements  []jobs.Placement
}

func (s *jobsEnqueueStager) Transaction() jobs.TransactionContext { return s.transaction }

func (s *jobsEnqueueStager) Stage(ctx context.Context, placement jobs.Placement) (jobs.Staged, error) {
	s.calls++
	s.contexts = append(s.contexts, ctx)
	s.placements = append(s.placements, placement)
	result, err := jobs.NewPlacementResult(placement.Candidate(), jobs.PlacementCreated)
	if err != nil {
		return jobs.Staged{}, err
	}
	return jobs.NewStaged(s.transaction, result)
}

func TestJobsEnqueueWrappersPreserveFourCallContractsAndSpanSemantics(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel := vvotel.Must(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	queue, definition, sender, stager := jobsEnqueueTestFixture(t)
	base := context.WithValue(context.Background(), jobsEnqueueContextKey{}, "application-value")

	id, err := vvotel.Enqueue(base, tel, queue, definition, "secret-payload", jobs.After(time.Second))
	if err != nil || id != sender.placements[0].Candidate() {
		t.Fatalf("Enqueue = %v, %v", id, err)
	}
	sender.outcome = jobs.PlacementExistingSamePayload
	onceID, onceOutcome, err := vvotel.EnqueueOnce(base, tel, queue, definition, jobs.Intent("secret-intent"), "secret-once")
	if err != nil || onceID != sender.placements[1].Candidate() || onceOutcome != jobs.EnqueueExistingSamePayload {
		t.Fatalf("EnqueueOnce = %v, %v, %v", onceID, onceOutcome, err)
	}
	staged, err := vvotel.EnqueueIn(base, tel, queue, stager, definition, "secret-staged")
	if err != nil || staged.IsZero() || staged.Transaction() != stager.transaction {
		t.Fatalf("EnqueueIn = %v, %v", staged, err)
	}
	onceStaged, err := vvotel.EnqueueOnceIn(base, tel, queue, stager, definition, jobs.Intent("secret-staged-intent"), "secret-once-staged")
	if err != nil || onceStaged.IsZero() || onceStaged.Transaction() != stager.transaction {
		t.Fatalf("EnqueueOnceIn = %v, %v", onceStaged, err)
	}

	if sender.calls != 2 || stager.calls != 2 || sender.placements[0].Delay() != time.Second {
		t.Fatalf("calls/forwarded option = %d/%d/%s", sender.calls, stager.calls, sender.placements[0].Delay())
	}
	for _, ctx := range append(append([]context.Context(nil), sender.contexts...), stager.contexts...) {
		if ctx.Value(jobsEnqueueContextKey{}) != "application-value" || ctx.Value(spanKey{}) == nil {
			t.Fatal("enqueue boundary did not receive the derived span context with application values")
		}
	}

	wantNames := []string{"vv.jobs enqueue", "vv.jobs enqueue_once", "vv.jobs enqueue_in", "vv.jobs enqueue_once_in"}
	wantKinds := []trace.SpanKind{trace.SpanKindProducer, trace.SpanKindProducer, trace.SpanKindInternal, trace.SpanKindInternal}
	wantOperations := []string{vvotel.OpJobsEnqueueEnqueue, vvotel.OpJobsEnqueueEnqueueOnce, vvotel.OpJobsEnqueueEnqueueIn, vvotel.OpJobsEnqueueEnqueueOnceIn}
	wantOutcomes := []string{vvotel.OutcomeOk, "existing_same_payload", "staged", "staged"}
	if len(tp.spans) != len(wantNames) || mp.metricCount() != len(wantNames) {
		t.Fatalf("spans/metrics = %d/%d", len(tp.spans), mp.metricCount())
	}
	for i, span := range tp.spans {
		if span.name != wantNames[i] || span.kind != wantKinds[i] || !span.ended || span.status != codes.Unset {
			t.Fatalf("span %d = %+v", i, span)
		}
		if span.attributes[vvotel.AttrComponent].AsString() != vvotel.ComponentJobsEnqueue || span.attributes[vvotel.AttrOperationName].AsString() != wantOperations[i] || span.attributes[vvotel.AttrOperationOutcome].AsString() != wantOutcomes[i] {
			t.Fatalf("span %d attributes = %v", i, span.attributes)
		}
	}
	for i, metric := range mp.metrics {
		if metric.name != vvotel.MetricJobsEnqueueDuration || metric.attributes[vvotel.AttrOperationName].AsString() != wantOperations[i] || metric.attributes[vvotel.AttrOperationOutcome].AsString() != wantOutcomes[i] {
			t.Fatalf("metric %d = %+v", i, metric)
		}
	}
	assertJobsEnqueueTelemetryExcludes(t, tp.spans, mp.metrics, "secret-payload", "secret-once", "secret-intent", "secret-staged", "secret-staged-intent", "committed", "published")
}

func TestJobsEnqueueWrapperClassifiesBaseFailureWithoutReplacingIt(t *testing.T) {
	tp := newTestTracerProvider()
	mp := newTestMeterProvider()
	tel := vvotel.Must(vvotel.Config{TracerProvider: tp, MeterProvider: mp})
	_, definition, _, _ := jobsEnqueueTestFixture(t)

	_, err := vvotel.Enqueue(context.Background(), tel, nil, definition, "secret-error-payload")
	if !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("error = %v", err)
	}
	if len(tp.spans) != 1 || mp.metricCount() != 1 || tp.spans[0].status != codes.Error {
		t.Fatalf("spans/metrics/status = %d/%d/%v", len(tp.spans), mp.metricCount(), tp.spans[0].status)
	}
	if tp.spans[0].attributes[vvotel.AttrErrorType].AsString() != vvotel.ErrorTypeInvalid || mp.metrics[0].attributes[vvotel.AttrErrorType].AsString() != vvotel.ErrorTypeInvalid {
		t.Fatalf("failure attributes = %v / %v", tp.spans[0].attributes, mp.metrics[0].attributes)
	}
	assertJobsEnqueueTelemetryExcludes(t, tp.spans, mp.metrics, "secret-error-payload", err.Error())
}

func jobsEnqueueTestFixture(t *testing.T) (*jobs.Queue, *jobs.Definition[string], *jobsEnqueueSender, *jobsEnqueueStager) {
	t.Helper()
	name, err := jobs.ParseName("otel.secret.definition")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := jobs.Default.Build()
	if err != nil {
		t.Fatal(err)
	}
	definition := jobs.MustDefine(jobs.DefinitionSpec[string]{
		Name:      name,
		Codec:     jobs.String(jobs.SchemaVersion(1)),
		Policy:    policy,
		Partition: jobs.PartitionGlobal,
	})
	namespace, err := jobs.NamespaceOf("otel", "enqueue-tests")
	if err != nil {
		t.Fatal(err)
	}
	var backendBytes [jobs.BackendIDBytes]byte
	backendBytes[0] = 1
	backend, err := jobs.BackendIDFromBytes(backendBytes)
	if err != nil {
		t.Fatal(err)
	}
	failures, err := jobs.Failures()
	if err != nil {
		t.Fatal(err)
	}
	durability, err := jobs.NewDurabilityProfile(jobs.AckBeforePersistence, jobs.AcknowledgedLossPossible, failures)
	if err != nil {
		t.Fatal(err)
	}
	description, err := jobs.NewBackendDescription(backend, durability, jobs.Capabilities{Priority: true, Debounce: true, Unique: true, Scheduled: true, AttemptTrace: true})
	if err != nil {
		t.Fatal(err)
	}
	sender := &jobsEnqueueSender{description: description, outcome: jobs.PlacementCreated}
	queue, err := jobs.NewQueue(jobs.QueueSpec{
		Namespace: namespace,
		Catalog:   jobs.MustCatalog(definition),
		Sender:    sender,
		Entropy:   bytes.NewReader(bytes.Repeat([]byte{7}, 256)),
	})
	if err != nil {
		t.Fatal(err)
	}
	var bindingBytes [32]byte
	bindingBytes[0] = 2
	binding, err := jobs.TransactionBindingFromBytes(bindingBytes)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := jobs.NewTransactionContext(backend, binding, durability)
	if err != nil {
		t.Fatal(err)
	}
	return queue, definition, sender, &jobsEnqueueStager{transaction: transaction}
}

func assertJobsEnqueueTelemetryExcludes(t *testing.T, spans []*recordedSpan, metrics []*recordedMetric, forbidden ...string) {
	t.Helper()
	for _, span := range spans {
		for _, value := range span.attributes {
			for _, secret := range forbidden {
				if value.AsString() == secret {
					t.Fatalf("span exported %q: %v", secret, span.attributes)
				}
			}
		}
	}
	for _, metric := range metrics {
		for _, value := range metric.attributes {
			for _, secret := range forbidden {
				if value.AsString() == secret {
					t.Fatalf("metric exported %q: %v", secret, metric.attributes)
				}
			}
		}
	}
}
