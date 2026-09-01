package jobspg

import (
	"bytes"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
)

func TestControlDeliveryCancelsQueuedAndPreservesRecord(t *testing.T) {
	namespace, catalog, placement := testPlacement(t)
	driver, err := New(Spec{DB: &sql.DB{}, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	insert, err := driver.newPlacement(placement, placement.Candidate(), createdAt)
	if err != nil {
		t.Fatal(err)
	}
	controlled, err := driver.controlDelivery(insert.record, jobs.InvocationQueued, controlCancel, createdAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if controlled.state != jobs.InvocationCancelled || controlled.view.Invocation().FinishedAt() != createdAt.Add(time.Second) || string(controlled.view.Payload().Bytes()) != "payload" || controlled.recordSize <= 0 || controlled.recordExpiresAt == nil || controlled.intentExpiresAt == nil {
		t.Fatalf("controlled delivery = %+v", controlled)
	}
	record, err := decodeRecord(controlled.record)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := jobs.RestoreDeliveryRecord(catalog, record)
	if err != nil || restored.Invocation().State() != jobs.InvocationCancelled || string(restored.Payload().Bytes()) != "payload" {
		t.Fatalf("restored cancellation = (%v, %q, %v)", restored.Invocation().State(), restored.Payload().Bytes(), err)
	}
	if _, err := driver.controlDelivery(controlled.record, jobs.InvocationCancelled, controlCancel, createdAt.Add(2*time.Second)); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("repeated cancellation = %v", err)
	}
	if _, err := driver.controlDelivery(insert.record, jobs.InvocationRunning, controlCancel, createdAt.Add(time.Second)); !errors.Is(err, jobs.ErrCorrupt) {
		t.Fatalf("mismatched stored state = %v", err)
	}
}

func TestControlDeliveryTerminatesRunningAttempt(t *testing.T) {
	namespace, catalog, placement := testPlacement(t)
	driver, err := New(Spec{DB: &sql.DB{}, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	insert, err := driver.newPlacement(placement, placement.Candidate(), createdAt)
	if err != nil {
		t.Fatal(err)
	}
	record, err := decodeRecord(insert.record)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := jobs.RestoreDeliveryRecord(catalog, record)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := jobs.NewLeaseRef(driver.Description().ID(), restored.Invocation().ID(), bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	binding, _ := jobs.ParseBindingName("tests.controller")
	build, _ := jobs.ParseBuildID("tests-controller")
	begin, err := jobs.BeginAttemptCommand(lease, binding, build)
	if err != nil {
		t.Fatal(err)
	}
	application, err := jobs.ApplyDeliveryCommand(restored.Invocation(), begin, createdAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	running, err := jobs.NewDeliveryRecord(application.Invocation(), restored.Payload(), restored.WireDigest(), restored.PayloadDigest())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeRecord(running)
	if err != nil {
		t.Fatal(err)
	}
	controlled, err := driver.controlDelivery(encoded, jobs.InvocationRunning, controlTerminate, createdAt.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	attempts := controlled.view.Invocation().Attempts()
	if controlled.state != jobs.InvocationTerminated || len(attempts) != 1 || attempts[0].Disposition().Kind() != jobs.DispositionTerminated || attempts[0].Disposition().Reason() != jobs.ReasonOperatorTerminated {
		t.Fatalf("terminated delivery = state %v attempts %+v", controlled.state, attempts)
	}
}

func TestDeliveryControlMapsDurableControlStates(t *testing.T) {
	tests := []struct {
		state jobs.InvocationState
		want  jobs.DeliveryControlStatus
	}{
		{jobs.InvocationQueued, jobs.DeliveryControlNone},
		{jobs.InvocationRunning, jobs.DeliveryControlNone},
		{jobs.InvocationCancelRequested, jobs.DeliveryControlCancelRequested},
		{jobs.InvocationCancelled, jobs.DeliveryControlCancelRequested},
		{jobs.InvocationTerminated, jobs.DeliveryControlTerminated},
	}
	for _, test := range tests {
		if got := deliveryControl(test.state); got != test.want {
			t.Fatalf("state %v = %v, want %v", test.state, got, test.want)
		}
	}
}

func TestControllerRejectsNilContextBeforeDatabaseAccess(t *testing.T) {
	namespace, catalog, placement := testPlacement(t)
	driver, err := New(Spec{DB: &sql.DB{}, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	driver.ready.Store(true)
	if _, err := driver.Cancel(nil, placement.Candidate()); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("nil cancellation context = %v", err)
	}
	if _, err := driver.Terminate(nil, placement.Candidate()); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("nil termination context = %v", err)
	}
}
