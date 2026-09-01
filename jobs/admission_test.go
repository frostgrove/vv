package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHeldReasonIsBoundedAndCanonical(t *testing.T) {
	boundary := strings.Repeat("a", MaxHeldReasonBytes)
	for _, raw := range []string{"dependency", "dependency.unavailable", "rate-limited", "quota_exhausted", boundary} {
		reason, err := ParseHeldReason(raw)
		if err != nil || reason.Value() != raw || reason.String() != raw || reason.IsZero() || !reason.valid() {
			t.Fatalf("expected valid held reason %q, got %#v and %v", raw, reason, err)
		}
	}
	for _, raw := range []string{"", "Dependency", "dependency..down", " dependency", "dependency ", "dependency/down", "dependency\n"} {
		if _, err := ParseHeldReason(raw); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected invalid held reason %q, got %v", raw, err)
		}
	}
	if _, err := ParseHeldReason(boundary + "a"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected oversized held reason, got %v", err)
	}
	if !(HeldReason{}).IsZero() || (HeldReason{}).valid() {
		t.Fatal("zero held reason must be explicit and invalid as a nonzero reason")
	}
}

func TestAdmissionConstructionOwnsCanonicalUTCState(t *testing.T) {
	reason := admissionTestReason(t, "dependency.down")
	location := time.FixedZone("test", 6*60*60)
	observed := time.Now().In(location)
	admission, err := NewAdmission(0, reason, observed)
	if err != nil {
		t.Fatal(err)
	}
	canonical := observed.Round(0).UTC()
	if admission.Limit() != 0 || admission.HeldReason() != reason || admission.ObservedAt() != canonical || admission.ObservedAt().Location() != time.UTC || !admission.IsInitialized() || !admission.valid() {
		t.Fatalf("unexpected admission: limit=%d reason=%q observed=%v initialized=%v", admission.Limit(), admission.HeldReason(), admission.ObservedAt(), admission.IsInitialized())
	}
	if (Admission{}).IsInitialized() || (Admission{}).valid() {
		t.Fatal("zero admission must be uninitialized")
	}
}

func TestAdmissionRejectsIntrinsicInvalidDecisions(t *testing.T) {
	reason := admissionTestReason(t, "dependency.down")
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		limit  int
		reason HeldReason
		at     time.Time
		match  error
	}{
		{"negative", -1, reason, now, ErrInvalid},
		{"over framework bound", MaxWorkerConcurrency + 1, reason, now, ErrTooLarge},
		{"zero without reason", 0, HeldReason{}, now, ErrInvalid},
		{"positive with reason", 1, reason, now, ErrInvalid},
		{"zero timestamp", 1, HeldReason{}, time.Time{}, ErrInvalid},
		{"year below canonical range", 1, HeldReason{}, time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC), ErrInvalid},
		{"year above canonical range", 1, HeldReason{}, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), ErrInvalid},
		{"fabricated reason", 0, HeldReason{value: "Dependency"}, now, ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			admission, err := NewAdmission(test.limit, test.reason, test.at)
			if !errors.Is(err, test.match) || admission.IsInitialized() {
				t.Fatalf("expected %v and zero admission, got %#v and %v", test.match, admission, err)
			}
			if errors.Is(err, ErrInvalid) {
				var typed AdmissionError
				if !errors.As(err, &typed) || typed.Signal() != AdmissionInvalid {
					t.Fatalf("expected typed invalid admission error, got %T %v", err, err)
				}
			}
		})
	}
}

func TestAdmissionSnapshotRequiresFiniteFreshness(t *testing.T) {
	for _, freshness := range []time.Duration{0, -time.Nanosecond} {
		if snapshot, err := NewAdmissionSnapshot(freshness); !errors.Is(err, ErrInvalid) || snapshot != nil {
			t.Fatalf("expected invalid freshness %v, got %#v and %v", freshness, snapshot, err)
		}
	}
	if snapshot, err := NewAdmissionSnapshot(MaximumAdmissionFreshness + time.Nanosecond); !errors.Is(err, ErrTooLarge) || snapshot != nil {
		t.Fatalf("expected oversized freshness, got %#v and %v", snapshot, err)
	}
	for _, freshness := range []time.Duration{time.Nanosecond, DefaultAdmissionFreshness, MaximumAdmissionFreshness} {
		snapshot, err := NewAdmissionSnapshot(freshness)
		if err != nil || snapshot == nil || snapshot.Freshness() != freshness || snapshot.Reader().Freshness() != freshness {
			t.Fatalf("expected freshness %v, got %#v and %v", freshness, snapshot, err)
		}
	}
}

func TestAdmissionSnapshotStartsExplicitlyUninitialized(t *testing.T) {
	snapshot, err := NewAdmissionSnapshot(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	decision := snapshot.Reader().Evaluate(4, now)
	assertAdmissionDecision(t, decision, 0, AdmissionUninitialized, HeldReason{}, time.Time{}, ErrAdmissionUninitialized)

	var zeroReader AdmissionReader
	decision = zeroReader.Evaluate(4, now)
	assertAdmissionDecision(t, decision, 0, AdmissionUninitialized, HeldReason{}, time.Time{}, ErrAdmissionUninitialized)
	if zeroReader.Freshness() != 0 {
		t.Fatalf("zero reader reported freshness %v", zeroReader.Freshness())
	}

	admission, err := NewAdmission(1, HeldReason{}, now)
	if err != nil {
		t.Fatal(err)
	}
	var zeroPublisher AdmissionPublisher
	if err := zeroPublisher.Publish(admission); !errors.Is(err, ErrAdmissionUninitialized) {
		t.Fatalf("expected zero publisher to fail closed, got %v", err)
	}
	var nilSnapshot *AdmissionSnapshot
	if nilSnapshot.Freshness() != 0 || nilSnapshot.Publisher().initialized || nilSnapshot.Reader().initialized {
		t.Fatal("nil snapshot must produce zero endpoints")
	}
}

func TestAdmissionReaderEvaluatesReadyHeldAndConcurrency(t *testing.T) {
	snapshot, err := NewAdmissionSnapshot(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	publisher := snapshot.Publisher()
	reader := snapshot.Reader()
	observed := time.Date(2026, 9, 1, 12, 0, 0, 123, time.FixedZone("source", -4*60*60))
	canonical := observed.UTC()

	if err := publisher.Update(3, HeldReason{}, observed); err != nil {
		t.Fatal(err)
	}
	decision := reader.Evaluate(3, canonical.Add(time.Minute))
	assertAdmissionDecision(t, decision, 3, AdmissionReady, HeldReason{}, canonical, nil)

	decision = reader.Evaluate(2, canonical.Add(time.Second))
	assertAdmissionDecision(t, decision, 0, AdmissionInvalid, HeldReason{}, canonical, ErrInvalid)

	reason := admissionTestReason(t, "dependency.down")
	if err := publisher.Update(0, reason, canonical.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	decision = reader.Evaluate(MaxBindingConcurrency, canonical.Add(2*time.Second))
	assertAdmissionDecision(t, decision, 0, AdmissionHeld, reason, canonical.Add(time.Second), ErrAdmissionHeld)
	var typed AdmissionError
	if !errors.As(decision.Err(), &typed) || typed.Signal() != AdmissionHeld || typed.HeldReason() != reason {
		t.Fatalf("expected typed held error, got %T %v", decision.Err(), decision.Err())
	}
}

func TestAdmissionReaderEvaluatesUnrestrictedAtRequestedConcurrency(t *testing.T) {
	snapshot, err := NewAdmissionSnapshot(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	observed := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err = snapshot.Publisher().Unrestricted(observed); err != nil {
		t.Fatal(err)
	}
	for _, concurrency := range []int{1, 4, MaxBindingConcurrency} {
		decision := snapshot.Reader().Evaluate(concurrency, observed.Add(time.Second))
		assertAdmissionDecision(t, decision, concurrency, AdmissionUnrestricted, HeldReason{}, observed, nil)
	}
	decision := snapshot.Reader().Evaluate(4, observed.Add(time.Minute+time.Nanosecond))
	assertAdmissionDecision(t, decision, 0, AdmissionStale, HeldReason{}, observed, ErrAdmissionStale)
	var zeroPublisher AdmissionPublisher
	if err = zeroPublisher.Unrestricted(observed); !errors.Is(err, ErrAdmissionUninitialized) {
		t.Fatalf("zero publisher unrestricted = %v", err)
	}
}

func TestAdmissionReaderRejectsInvalidConcurrencyAndClock(t *testing.T) {
	snapshot, err := NewAdmissionSnapshot(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := snapshot.Publisher().Update(1, HeldReason{}, now); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		concurrency int
		now         time.Time
	}{
		{0, now},
		{-1, now},
		{MaxWorkerConcurrency + 1, now},
		{1, time.Time{}},
		{1, now.Add(-time.Nanosecond)},
	} {
		decision := snapshot.Reader().Evaluate(test.concurrency, test.now)
		if decision.Signal() != AdmissionInvalid || decision.Limit() != 0 || !errors.Is(decision.Err(), ErrInvalid) {
			t.Fatalf("expected invalid decision for concurrency=%d now=%v, got signal=%v limit=%d err=%v", test.concurrency, test.now, decision.Signal(), decision.Limit(), decision.Err())
		}
	}
}

func TestAdmissionFreshnessBoundaryIsDeterministic(t *testing.T) {
	snapshot, err := NewAdmissionSnapshot(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	reason := admissionTestReason(t, "dependency.down")
	observed := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := snapshot.Publisher().Update(0, reason, observed); err != nil {
		t.Fatal(err)
	}

	decision := snapshot.Reader().Evaluate(4, observed.Add(time.Minute))
	assertAdmissionDecision(t, decision, 0, AdmissionHeld, reason, observed, ErrAdmissionHeld)

	decision = snapshot.Reader().Evaluate(4, observed.Add(time.Minute+time.Nanosecond))
	assertAdmissionDecision(t, decision, 0, AdmissionStale, reason, observed, ErrAdmissionStale)
	var typed AdmissionError
	if !errors.As(decision.Err(), &typed) || typed.Signal() != AdmissionStale || typed.HeldReason() != reason {
		t.Fatalf("expected typed stale error, got %T %v", decision.Err(), decision.Err())
	}
}

func TestAdmissionPublisherRejectsRegressionAndInvalidReplacement(t *testing.T) {
	snapshot, err := NewAdmissionSnapshot(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	publisher := snapshot.Publisher()
	reader := snapshot.Reader()
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := publisher.Update(2, HeldReason{}, now); err != nil {
		t.Fatal(err)
	}
	if err := publisher.Update(1, HeldReason{}, now.Add(-time.Nanosecond)); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected timestamp regression conflict, got %v", err)
	}
	if err := publisher.Publish(Admission{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid replacement rejection, got %v", err)
	}
	decision := reader.Evaluate(2, now)
	assertAdmissionDecision(t, decision, 2, AdmissionReady, HeldReason{}, now, nil)
}

func TestAdmissionPublisherFailsClosedOnInvalidUpdate(t *testing.T) {
	reason := admissionTestReason(t, "dependency.invalid")
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		limit      int
		reason     HeldReason
		match      error
		wantReason HeldReason
	}{
		{"negative", -1, reason, ErrInvalid, reason},
		{"zero without reason", 0, HeldReason{}, ErrInvalid, HeldReason{}},
		{"positive with reason", 1, reason, ErrInvalid, reason},
		{"over framework bound", MaxWorkerConcurrency + 1, HeldReason{}, ErrTooLarge, HeldReason{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot, err := NewAdmissionSnapshot(time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			publisher := snapshot.Publisher()
			if err := publisher.Update(2, HeldReason{}, now); err != nil {
				t.Fatal(err)
			}
			if err := publisher.Update(test.limit, test.reason, now.Add(time.Second)); !errors.Is(err, test.match) {
				t.Fatalf("expected invalid update error %v, got %v", test.match, err)
			}
			decision := snapshot.Reader().Evaluate(2, now.Add(time.Second))
			assertAdmissionDecision(t, decision, 0, AdmissionInvalid, test.wantReason, now.Add(time.Second), ErrInvalid)
			if err := publisher.Update(2, HeldReason{}, now.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			decision = snapshot.Reader().Evaluate(2, now.Add(2*time.Second))
			assertAdmissionDecision(t, decision, 2, AdmissionReady, HeldReason{}, now.Add(2*time.Second), nil)
		})
	}
}

func TestAdmissionPublisherRequiresStrictTimestampOrdering(t *testing.T) {
	snapshot, err := NewAdmissionSnapshot(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	publisher := snapshot.Publisher()
	reader := snapshot.Reader()
	reason := admissionTestReason(t, "dependency.down")
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	ready, err := NewAdmission(2, HeldReason{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(ready); err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(ready); err != nil {
		t.Fatalf("idempotent publication failed: %v", err)
	}
	if err := publisher.Update(0, reason, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected equal-time disagreement conflict, got %v", err)
	}
	decision := reader.Evaluate(2, now)
	assertAdmissionDecision(t, decision, 2, AdmissionReady, HeldReason{}, now, nil)
	if err := publisher.Update(-1, reason, now); !errors.Is(err, ErrInvalid) || !errors.Is(err, ErrConflict) {
		t.Fatalf("expected invalid equal-time update conflict, got %v", err)
	}
	decision = reader.Evaluate(2, now)
	assertAdmissionDecision(t, decision, 2, AdmissionReady, HeldReason{}, now, nil)
	if err := publisher.Update(0, reason, now.Add(time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	decision = reader.Evaluate(2, now.Add(time.Nanosecond))
	assertAdmissionDecision(t, decision, 0, AdmissionHeld, reason, now.Add(time.Nanosecond), ErrAdmissionHeld)
}

func TestAdmissionDecisionAvailableUsesLimitAsAnAbsoluteCap(t *testing.T) {
	decision := AdmissionDecision{limit: 3, signal: AdmissionReady}
	for _, test := range []struct {
		inFlight int
		want     int
	}{
		{0, 3},
		{1, 2},
		{3, 0},
		{4, 0},
	} {
		available, err := decision.Available(test.inFlight)
		if err != nil || available != test.want {
			t.Fatalf("in-flight %d: expected %d available, got %d and %v", test.inFlight, test.want, available, err)
		}
	}
	if available, err := decision.Available(-1); available != 0 || !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected invalid negative in-flight count, got %d and %v", available, err)
	}
	held := AdmissionDecision{signal: AdmissionHeld, heldReason: admissionTestReason(t, "dependency.down")}
	if available, err := held.Available(0); available != 0 || !errors.Is(err, ErrAdmissionHeld) {
		t.Fatalf("expected held decision to admit nothing, got %d and %v", available, err)
	}
}

func TestAdmissionValuesRefuseSerializationAndRedactFormatting(t *testing.T) {
	snapshot, err := NewAdmissionSnapshot(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	reason := admissionTestReason(t, "private.dependency")
	observed := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	admission, err := NewAdmission(0, reason, observed)
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Publisher().Publish(admission); err != nil {
		t.Fatal(err)
	}
	decision := snapshot.Reader().Evaluate(4, observed)
	values := []any{admission, snapshot, snapshot.Publisher(), snapshot.Reader(), decision, decision.Err()}
	for _, value := range values {
		for _, formatted := range []string{fmt.Sprint(value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			if strings.Contains(formatted, reason.Value()) || strings.Contains(formatted, observed.Format(time.RFC3339Nano)) {
				t.Fatalf("formatted %T leaked admission details: %q", value, formatted)
			}
		}
		if _, err := json.Marshal(value); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("expected %T to refuse JSON, got %v", value, err)
		}
	}
	for _, value := range []interface{ LogValue() slog.Value }{admission, snapshot, snapshot.Publisher(), snapshot.Reader(), decision} {
		if rendered := value.LogValue().String(); strings.Contains(rendered, reason.Value()) || strings.Contains(rendered, observed.Format(time.RFC3339Nano)) {
			t.Fatalf("slog value leaked admission details: %q", rendered)
		}
	}
}

func TestAdmissionSnapshotConcurrentPublishAndRead(t *testing.T) {
	snapshot, err := NewAdmissionSnapshot(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	publisher := snapshot.Publisher()
	reader := snapshot.Reader()
	reason := admissionTestReason(t, "dependency.busy")
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if err := publisher.Update(1, HeldReason{}, base); err != nil {
		t.Fatal(err)
	}
	var invalid atomic.Int32
	var wait sync.WaitGroup
	start := make(chan struct{})
	for worker := range 8 {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			<-start
			for iteration := range 5000 {
				sequence := worker*5000 + iteration + 1
				limit := 1
				currentReason := HeldReason{}
				if sequence%2 == 0 {
					limit = 0
					currentReason = reason
				}
				if err := publisher.Update(limit, currentReason, base.Add(time.Duration(sequence)*time.Nanosecond)); err != nil && !errors.Is(err, ErrConflict) {
					invalid.Add(1)
				}
			}
		}(worker)
	}
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			for range 10000 {
				decision := reader.Evaluate(4, base.Add(time.Minute))
				switch decision.Signal() {
				case AdmissionReady:
					if decision.Limit() != 1 || decision.Err() != nil {
						invalid.Add(1)
					}
				case AdmissionHeld:
					if decision.Limit() != 0 || decision.HeldReason() != reason || !errors.Is(decision.Err(), ErrAdmissionHeld) {
						invalid.Add(1)
					}
				default:
					invalid.Add(1)
				}
			}
		}()
	}
	close(start)
	wait.Wait()
	if invalid.Load() != 0 {
		t.Fatalf("observed %d invalid concurrent results", invalid.Load())
	}
}

func assertAdmissionDecision(t *testing.T, decision AdmissionDecision, limit int, signal AdmissionSignal, reason HeldReason, observed time.Time, match error) {
	t.Helper()
	if decision.Limit() != limit || decision.Signal() != signal || decision.HeldReason() != reason || decision.ObservedAt() != observed {
		t.Fatalf("unexpected admission decision: limit=%d signal=%s reason=%q observed=%v", decision.Limit(), decision.Signal(), decision.HeldReason(), decision.ObservedAt())
	}
	if match == nil {
		if decision.Err() != nil {
			t.Fatalf("expected no admission error, got %v", decision.Err())
		}
		return
	}
	if !errors.Is(decision.Err(), match) {
		t.Fatalf("expected admission error %v, got %v", match, decision.Err())
	}
}

func admissionTestReason(t *testing.T, raw string) HeldReason {
	t.Helper()
	reason, err := ParseHeldReason(raw)
	if err != nil {
		t.Fatal(err)
	}
	return reason
}
