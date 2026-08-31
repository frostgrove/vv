package jobs

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestAttemptAccountingTypesHaveIndependentBounds(t *testing.T) {
	if reflect.TypeOf(AttemptOrdinal{}) == reflect.TypeOf(RetrySpent{}) || reflect.TypeOf(AttemptOrdinal{}) == reflect.TypeOf(HandlerDeferrals{}) || reflect.TypeOf(RetrySpent{}) == reflect.TypeOf(HandlerDeferrals{}) {
		t.Fatal("attempt counters share a type")
	}
	ordinal, err := NewAttemptOrdinal(MaxAttemptOrdinal)
	if err != nil || ordinal.Value() != MaxAttemptOrdinal || ordinal.IsZero() || !ordinal.valid() {
		t.Fatalf("ordinal = (%d, %v)", ordinal.Value(), err)
	}
	if _, err := NewAttemptOrdinal(MaxAttemptOrdinal + 1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("ordinal bound = %v", err)
	}
	retries, err := NewRetrySpent(MaximumRetries)
	if err != nil || retries.Value() != MaximumRetries || retries.IsZero() || !retries.valid() {
		t.Fatalf("retry spent = (%d, %v)", retries.Value(), err)
	}
	if _, err := NewRetrySpent(MaximumRetries + 1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("retry bound = %v", err)
	}
	deferrals, err := NewHandlerDeferrals(MaximumHandlerDeferrals)
	if err != nil || deferrals.Value() != MaximumHandlerDeferrals || deferrals.IsZero() || !deferrals.valid() {
		t.Fatalf("handler deferrals = (%d, %v)", deferrals.Value(), err)
	}
	if _, err := NewHandlerDeferrals(MaximumHandlerDeferrals + 1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("deferral bound = %v", err)
	}
}

func TestAttemptRecordsAreMintedByInvocationAndRedacted(t *testing.T) {
	invocation := testGenesisInvocation(t)
	started := invocation.EligibleAt().Add(timeSecond)
	running, attempt, err := invocation.BeginAttempt(BeginAttemptSpec{Binding: testBindingName(t), Build: testBuildID(t), StartedAt: started})
	if err != nil {
		t.Fatal(err)
	}
	record := attempt.Record()
	if record.Invocation != invocation.ID() || record.Ordinal.Value() != 1 || record.State != AttemptRunning || !record.Deadline.Equal(started.Add(invocation.Policy().AttemptTimeout())) || !record.ProgressedAt.IsZero() || !record.ProgressDeadline.IsZero() {
		t.Fatalf("running record = %+v", record)
	}
	finishedInvocation, finished, err := running.FinishAttempt(attempt, FinishAttemptSpec{FinishedAt: started.Add(timeSecond), Disposition: SuccessDisposition()})
	if err != nil {
		t.Fatal(err)
	}
	if finished.State() != AttemptFinished || finishedInvocation.AttemptRecords()[0] != finished.Record() || attempt.State() != AttemptRunning {
		t.Fatalf("finished ledger = (%v, %v)", finished.State(), finishedInvocation.AttemptRecords())
	}
	for _, value := range []any{attempt, record, BeginAttemptSpec{Binding: record.Binding, Build: record.Build, StartedAt: record.StartedAt}, FinishAttemptSpec{Disposition: testSecretDisposition(t)}} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			formatted := fmt.Sprintf(format, value)
			if strings.Contains(formatted, "private operator detail") {
				t.Fatalf("format %q leaked: %q", format, formatted)
			}
		}
	}
}

func TestAttemptStateSetIsClosed(t *testing.T) {
	if !AttemptRunning.Valid() || AttemptRunning.String() != "running" || !AttemptFinished.Valid() || AttemptFinished.String() != "finished" || AttemptState(0).Valid() || AttemptState(255).Valid() || AttemptState(255).String() != "unknown" {
		t.Fatal("attempt state set is not closed")
	}
}

const timeSecond = 1_000_000_000

func testModelInvocationID(t *testing.T) InvocationID {
	t.Helper()
	id, err := ParseInvocationID("00112233-4455-4677-8899-aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func testBindingName(t *testing.T) BindingName {
	t.Helper()
	value, err := ParseBindingName("worker.primary")
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func testBuildID(t *testing.T) BuildID {
	t.Helper()
	value, err := ParseBuildID("git:ABC123")
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func testSecretDisposition(t *testing.T) Disposition {
	t.Helper()
	code, err := ParseFailureCode("private.failure")
	if err != nil {
		t.Fatal(err)
	}
	failure, err := NewPublicFailure(code, "private operator detail")
	if err != nil {
		t.Fatal(err)
	}
	disposition, err := PermanentFailureDisposition(ReasonHandlerFailure, failure)
	if err != nil {
		t.Fatal(err)
	}
	return disposition
}
