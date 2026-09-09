package vvotel

import (
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
	"go.opentelemetry.io/otel/attribute"
)

func TestJobsWorkerOuterAttributesRejectUnknownAndInvalidOperationOutcomes(t *testing.T) {
	tests := []jobsWorkerMetricEvent{
		{},
		{operation: jobs.WorkerOperation(255), outcome: jobs.WorkerOutcomeComplete},
		{operation: jobs.WorkerOperationRun, outcome: jobs.WorkerOutcome(255)},
		{operation: jobs.WorkerOperationRun, outcome: jobs.WorkerOutcomeEmpty},
		{operation: jobs.WorkerOperationRun, outcome: jobs.WorkerOutcomeFailed},
		{operation: jobs.WorkerOperationRun, outcome: jobs.WorkerOutcomeComplete, failure: jobs.WorkerFailureRuntime},
	}
	for _, event := range tests {
		if attributes, ok := jobsWorkerOuterAttributes(event); ok || attributes.attributes != nil {
			t.Fatalf("invalid worker outer event was admitted: %#v", event)
		}
	}

	valid := jobsWorkerMetricEvent{
		operation: jobs.WorkerOperationClaim,
		outcome:   jobs.WorkerOutcomeFailed,
		failure:   jobs.WorkerFailureDriver,
	}
	outer, ok := jobsWorkerOuterAttributes(valid)
	if !ok || outer.operation != OpJobsWorkerClaim || outer.outcome != "failed" {
		t.Fatalf("valid worker failure was rejected: %#v, %t", outer, ok)
	}
	if got := workerAttributeValue(outer.attributes, AttrFailure); got != "driver" {
		t.Fatalf("failure attribute = %q, want driver", got)
	}
}

func TestJobsWorkerNarrowAttributesCoverEveryMetricProjection(t *testing.T) {
	recoverEvent := jobsWorkerMetricEvent{
		operation: jobs.WorkerOperationRecover,
		outcome:   jobs.WorkerOutcomeComplete,
		items:     3,
		bytes:     512,
		released:  2,
		more:      true,
		elapsed:   25 * time.Millisecond,
	}
	recoverOuter, ok := jobsWorkerOuterAttributes(recoverEvent)
	if !ok {
		t.Fatal("recover outer attributes were rejected")
	}
	if _, ok = jobsWorkerDurationAttributes(recoverEvent, recoverOuter); !ok {
		t.Fatal("recover duration attributes were rejected")
	}
	if _, ok = jobsWorkerItemsAttributes(recoverOuter); !ok {
		t.Fatal("recover item attributes were rejected")
	}
	if _, ok = jobsWorkerBytesAttributes(recoverOuter); !ok {
		t.Fatal("recover byte attributes were rejected")
	}
	if _, ok = jobsWorkerReleasedAttributes(recoverEvent, recoverOuter); !ok {
		t.Fatal("recover release attributes were rejected")
	}

	admissionEvent := jobsWorkerMetricEvent{
		operation:       jobs.WorkerOperationAdmission,
		outcome:         jobs.WorkerOutcomeReady,
		admissionSignal: jobs.AdmissionReady,
	}
	admissionOuter, ok := jobsWorkerOuterAttributes(admissionEvent)
	if !ok {
		t.Fatal("admission outer attributes were rejected")
	}
	if _, ok = jobsWorkerAdmissionAttributes(admissionEvent, admissionOuter); !ok {
		t.Fatal("admission attributes were rejected")
	}

	applyEvent := jobsWorkerMetricEvent{
		operation:   jobs.WorkerOperationApply,
		outcome:     jobs.WorkerOutcomeComplete,
		commandKind: jobs.DeliveryCommandFinishAttempt,
		disposition: jobs.DispositionPermanentFailure,
		reason:      jobs.ReasonHandlerFailure,
	}
	applyOuter, ok := jobsWorkerOuterAttributes(applyEvent)
	if !ok {
		t.Fatal("apply outer attributes were rejected")
	}
	if _, ok = jobsWorkerDispositionAttributes(applyEvent, applyOuter); !ok {
		t.Fatal("apply disposition attributes were rejected")
	}
	if _, ok = jobsWorkerDeliveryResultAttributes(applyOuter, jobsWorkerMetricResult{
		mutation: jobs.DeliveryMutationLeaseLost,
		control:  jobs.DeliveryControlTerminated,
		items:    1,
	}); !ok {
		t.Fatal("apply delivery-result attributes were rejected")
	}
}

func TestJobsWorkerMetricValueBoundsMatchTheRootContracts(t *testing.T) {
	for _, test := range []struct {
		name     string
		value    int
		maximum  int
		positive bool
		want     bool
	}{
		{name: "items zero", value: 0, maximum: jobs.MaxReclaimBatch, want: true},
		{name: "items maximum", value: jobs.MaxReclaimBatch, maximum: jobs.MaxReclaimBatch, want: true},
		{name: "items negative", value: -1, maximum: jobs.MaxReclaimBatch},
		{name: "items overflow", value: jobs.MaxReclaimBatch + 1, maximum: jobs.MaxReclaimBatch},
		{name: "bytes maximum", value: jobs.MaxClaimBytes, maximum: jobs.MaxClaimBytes, want: true},
		{name: "bytes overflow", value: jobs.MaxClaimBytes + 1, maximum: jobs.MaxClaimBytes},
		{name: "result minimum", value: 1, maximum: jobs.MaxClaimItems, positive: true, want: true},
		{name: "result maximum", value: jobs.MaxClaimItems, maximum: jobs.MaxClaimItems, positive: true, want: true},
		{name: "result zero", value: 0, maximum: jobs.MaxClaimItems, positive: true},
		{name: "result overflow", value: jobs.MaxClaimItems + 1, maximum: jobs.MaxClaimItems, positive: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := jobsWorkerMetricValue(test.value, test.maximum)
			if test.positive {
				got = jobsWorkerPositiveMetricValue(test.value, test.maximum)
			}
			if got != test.want {
				t.Fatalf("admitted = %t, want %t", got, test.want)
			}
		})
	}
}

func workerAttributeValue(attributes []attribute.KeyValue, key attribute.Key) string {
	for _, current := range attributes {
		if current.Key == key {
			return current.Value.AsString()
		}
	}
	return ""
}
