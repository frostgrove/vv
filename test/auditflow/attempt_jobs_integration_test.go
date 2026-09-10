package auditflow_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/audit"
	"github.com/frostgrove/vv/jobs"
)

var errApplicationJobResponseLost = errors.New("auditflow: job response was lost")

type applicationJobEffectMember struct {
	occurred time.Time
}

type applicationJobEffectStart struct {
	invocation audit.Reference
	job        string
}

type applicationJobEffectCheckpoint struct{}

type applicationJobEffectFinish struct {
	result string
}

func TestJobRedeliveryKeepsDeliveryEvidenceSeparateFromOneLogicalEffect(t *testing.T) {
	runtime := newApplicationJobAuditRuntime(t)
	invocation, err := jobs.ParseInvocationID("00000000-0000-4000-8000-000000000654")
	if err != nil {
		t.Fatal(err)
	}
	first := newApplicationDeliveryMeta(t, invocation, 1, 0, 1)
	second := newApplicationDeliveryMeta(t, invocation, 2, 1, 1)

	var callbackCalls atomic.Int64
	var firstExecution audit.AttemptExecution
	var secondExecution audit.AttemptExecution
	next := jobs.AdapterHandler[string](func(ctx context.Context, payload string, meta jobs.DeliveryMeta, _ jobs.AttemptController) error {
		if payload != "apply-logical-effect" {
			return errors.New("auditflow: unexpected job payload")
		}
		logicalOperation := operationForJobEffect(meta.InvocationID())
		attemptCtx := context.WithValue(ctx, applicationOperationKey{}, logicalOperation)
		execution, runErr := runtime.effectAttempt.Run(
			attemptCtx,
			runtime.attempts,
			applicationJobEffectStart{invocation: audit.Reference(meta.InvocationID().String()), job: meta.Definition().String()},
			audit.AttemptRunSpec{IdempotencyKey: applicationJobEffectKey(meta.InvocationID())},
			func(context.Context, *audit.AttemptProgress[applicationJobEffectCheckpoint]) (audit.AttemptCompletion[applicationJobEffectFinish], error) {
				callbackCalls.Add(1)
				completion := audit.AttemptSucceeded(applicationJobEffectFinish{result: "committed"})
				if meta.AttemptOrdinal().Value() == 1 {
					return completion, errApplicationJobResponseLost
				}
				return completion, nil
			},
		)
		if runErr != nil {
			return runErr
		}
		if meta.AttemptOrdinal().Value() == 1 {
			firstExecution = execution
		} else {
			secondExecution = execution
		}
		return execution.ApplicationError()
	})
	handler := runtime.auditDeliveries(next)

	if err := handler(t.Context(), "apply-logical-effect", first, nil); !errors.Is(err, errApplicationJobResponseLost) {
		t.Fatalf("first delivery = %v", err)
	}
	if err := handler(t.Context(), "apply-logical-effect", second, nil); err != nil {
		t.Fatalf("redelivery = %v", err)
	}
	if callbackCalls.Load() != 1 || !firstExecution.Invoked() || secondExecution.Invoked() {
		t.Fatalf("logical effect executions = callbacks:%d first:%t second:%t", callbackCalls.Load(), firstExecution.Invoked(), secondExecution.Invoked())
	}

	firstReceipt, firstPresent := firstExecution.Attempt().Receipt()
	secondReceipt, secondPresent := secondExecution.Attempt().Receipt()
	if !firstPresent || !secondPresent || firstReceipt.Disposition() != audit.Inserted || secondReceipt.Disposition() != audit.Replayed {
		t.Fatalf("attempt receipts = first:%v/%t second:%v/%t", firstReceipt.Disposition(), firstPresent, secondReceipt.Disposition(), secondPresent)
	}
	firstTransition, firstTransitionPresent := firstExecution.Attempt().Transition()
	secondTransition, secondTransitionPresent := secondExecution.Attempt().Transition()
	logicalOperation := operationForJobEffect(invocation)
	if !firstTransitionPresent || !secondTransitionPresent || firstTransition.Kind != audit.AttemptSucceededTransition || secondTransition.Kind != audit.AttemptStartedTransition {
		t.Fatalf("attempt transitions = first:%v/%t second:%v/%t", firstTransition.Kind, firstTransitionPresent, secondTransition.Kind, secondTransitionPresent)
	}
	if firstTransition.Chain == (audit.AttemptChainID{}) || firstTransition.Chain != secondTransition.Chain || firstTransition.OperationID != logicalOperation || secondTransition.OperationID != logicalOperation {
		t.Fatalf("logical attempt identity = chains:%x/%x operations:%x/%x want:%x", firstTransition.Chain, secondTransition.Chain, firstTransition.OperationID, secondTransition.OperationID, logicalOperation)
	}

	captures := runtime.recordedCaptures()
	if len(captures) != 2 {
		t.Fatalf("delivery evidence count = %d", len(captures))
	}
	assertApplicationJobCapture(t, captures[0], first, "failed", false)
	assertApplicationJobCapture(t, captures[1], second, "succeeded", true)
	if captures[0].receipt.RevisionID() == captures[1].receipt.RevisionID() {
		t.Fatal("two deliveries collapsed into one evidence revision")
	}
	firstDeliveryOperation := operationForJobAttempt(first)
	secondDeliveryOperation := operationForJobAttempt(second)
	if firstDeliveryOperation == secondDeliveryOperation || firstDeliveryOperation == logicalOperation || secondDeliveryOperation == logicalOperation {
		t.Fatalf("delivery and effect identities collided: first=%x second=%x logical=%x", firstDeliveryOperation, secondDeliveryOperation, logicalOperation)
	}
}

func applicationJobEffectDeclarations() (*audit.EventType[applicationJobEffectMember], *audit.OperationType, *audit.AttemptType[applicationJobEffectStart, applicationJobEffectCheckpoint, applicationJobEffectFinish]) {
	contextPolicy := applicationJobEffectContextPolicy()
	member := audit.Declare(audit.EventPolicy[applicationJobEffectMember]{
		Semantics: audit.Semantics(1, audit.PolicyGolden("job.effect.member", strings.Repeat("81", 32))),
		Descriptor: audit.Descriptor{
			Resource: "auditflow.job_effect", Action: "auditflow.job.effect.member", Owner: "auditflow.application",
			Purpose: "operations.audit", Retention: "operations.forever", Consequence: audit.BestEffort, Context: contextPolicy,
		},
		Target:     audit.NoEventTarget[applicationJobEffectMember](),
		Outcome:    audit.EventOutcome(audit.Outcomes("applied"), func(applicationJobEffectMember) audit.Outcome { return "applied" }),
		OccurredAt: audit.EventOccurredAt(func(value applicationJobEffectMember) time.Time { return value.occurred }),
	})
	operation := audit.DeclareOperation(audit.OperationPolicy{
		Name: "auditflow.job.effect", Semantics: audit.Semantics(1), Retention: "operations.forever",
		Consequence: audit.BestEffort, Context: contextPolicy, Members: audit.OperationMembers(member),
	})
	attempt := audit.DeclareAttempt(audit.AttemptPolicy[applicationJobEffectStart, applicationJobEffectCheckpoint, applicationJobEffectFinish]{
		Operation: operation,
		Semantics: audit.Semantics(1,
			audit.AttemptStartGolden("job.effect.started", strings.Repeat("82", 32)),
			audit.AttemptFinishGolden("job.effect.succeeded", audit.AttemptSucceededTransition, "", strings.Repeat("83", 32)),
			audit.AttemptFinishGolden("job.effect.failed", audit.AttemptFailedTransition, "job.effect.failed", strings.Repeat("84", 32)),
			audit.AttemptFinishGolden("job.effect.cancelled", audit.AttemptCancelledTransition, "job.effect.cancelled", strings.Repeat("85", 32)),
		),
		Descriptor: audit.AttemptDescriptor{
			Resource: "auditflow.job_effect_attempt", Owner: "auditflow.application", Purpose: "operations.audit",
			Retention: "operations.forever", Consequence: audit.BestEffort, Context: contextPolicy,
		},
		MaxOpen:       time.Hour,
		MaxStateBytes: audit.MaxAttemptStateBytes,
		Continuity:    audit.AttemptOwnedBy(audit.AttemptEffectiveActorOwner),
		Start: audit.AttemptStart(
			audit.AttemptTarget(func(value applicationJobEffectStart) audit.Reference { return value.invocation }, audit.Public, audit.AsToken),
			audit.AttemptFields(audit.AttemptValue("job_name", func(value applicationJobEffectStart) string { return value.job }, audit.Text(), audit.Public)),
		),
		Checkpoints: audit.NoAttemptCheckpoints[applicationJobEffectCheckpoint](),
		Finish: audit.AttemptFinish(
			audit.AttemptReasons(
				audit.AttemptReasonsFor(audit.AttemptFailedTransition, audit.Reasons("job.effect.failed")),
				audit.AttemptReasonsFor(audit.AttemptCancelledTransition, audit.Reasons("job.effect.cancelled")),
			),
			audit.AttemptFinishFields(
				audit.AttemptFieldsFor(audit.AttemptSucceededTransition, audit.AttemptValue("result", func(value applicationJobEffectFinish) string { return value.result }, audit.Text(), audit.Public)),
				audit.AttemptFieldsFor(audit.AttemptFailedTransition, audit.AttemptValue("result", func(value applicationJobEffectFinish) string { return value.result }, audit.Text(), audit.Public)),
				audit.AttemptFieldsFor(audit.AttemptCancelledTransition, audit.AttemptValue("result", func(value applicationJobEffectFinish) string { return value.result }, audit.Text(), audit.Public)),
			),
		),
	})
	return member, operation, attempt
}

func applicationJobEffectContextPolicy() audit.ContextPolicy {
	return audit.ContextFacts(
		audit.ActorChain(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Public, audit.AsPlaintext),
		audit.OperationFact(audit.ContextRequired, audit.Provenances(audit.Verified), audit.Public, audit.AsPlaintext),
	)
}

func operationForJobEffect(invocation jobs.InvocationID) audit.OperationID {
	wire := invocation.Bytes()
	digest := sha256.New()
	_, _ = digest.Write([]byte("frostgrove.auditflow/job-effect/v1"))
	_, _ = digest.Write(wire[:])
	var operation audit.OperationID
	copy(operation[:], digest.Sum(nil))
	return operation
}

func applicationJobEffectKey(invocation jobs.InvocationID) audit.IdempotencyKey {
	return audit.IdempotencyKey("job-effect." + invocation.String())
}
