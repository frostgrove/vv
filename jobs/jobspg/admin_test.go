package jobspg

import (
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
)

func TestNormalizeListSpecDefaultsClonesAndBoundsFilters(t *testing.T) {
	definition, err := jobs.ParseName("jobspg.admin")
	if err != nil {
		t.Fatal(err)
	}
	spec := ListSpec{Definitions: []jobs.Name{definition}, States: []jobs.InvocationState{jobs.InvocationQueued}, Offset: 7}
	normalized, err := normalizeListSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.limit != DefaultListLimit || normalized.offset != 7 || !slices.Equal(normalized.definitions, spec.Definitions) || !slices.Equal(normalized.states, spec.States) {
		t.Fatalf("normalized = %+v", normalized)
	}
	spec.Definitions[0] = jobs.Name{}
	spec.States[0] = 0
	if normalized.definitions[0] != definition || normalized.states[0] != jobs.InvocationQueued {
		t.Fatal("normalized filters alias caller memory")
	}
	tests := []struct {
		spec ListSpec
		want error
	}{
		{ListSpec{Limit: -1}, jobs.ErrInvalid},
		{ListSpec{Offset: -1}, jobs.ErrInvalid},
		{ListSpec{Limit: MaxListLimit + 1}, jobs.ErrTooLarge},
		{ListSpec{Offset: MaxListOffset + 1}, jobs.ErrTooLarge},
		{ListSpec{Definitions: make([]jobs.Name, MaxListDefinitions+1)}, jobs.ErrTooLarge},
		{ListSpec{States: make([]jobs.InvocationState, int(jobs.InvocationTerminated)+1)}, jobs.ErrTooLarge},
		{ListSpec{Definitions: []jobs.Name{{}}}, jobs.ErrInvalid},
		{ListSpec{States: []jobs.InvocationState{0}}, jobs.ErrInvalid},
		{ListSpec{Definitions: []jobs.Name{definition, definition}}, jobs.ErrConflict},
		{ListSpec{States: []jobs.InvocationState{jobs.InvocationDead, jobs.InvocationDead}}, jobs.ErrConflict},
	}
	for index, test := range tests {
		if _, err := normalizeListSpec(test.spec); !errors.Is(err, test.want) {
			t.Fatalf("case %d = %v, want %v", index, err, test.want)
		}
	}
}

func TestRedriveRecordPreservesPayloadAndProducesRestorableGenesis(t *testing.T) {
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
	terminal, err := restored.Invocation().Terminate(restored.Invocation().EligibleAt())
	if err != nil {
		t.Fatal(err)
	}
	terminalRecord, err := jobs.NewDeliveryRecord(terminal, restored.Payload(), restored.WireDigest(), restored.PayloadDigest())
	if err != nil {
		t.Fatal(err)
	}
	terminalEncoded, err := encodeRecord(terminalRecord)
	if err != nil {
		t.Fatal(err)
	}
	now := terminal.FinishedAt().Add(time.Hour)
	redrive, err := driver.redriveRecord(terminalEncoded, now)
	if err != nil {
		t.Fatal(err)
	}
	if redrive.createdAt != now || redrive.mode != terminal.Mode() || redrive.intent != terminal.Intent() || redrive.size <= 0 {
		t.Fatalf("redrive metadata = %+v", redrive)
	}
	decoded, err := decodeRecord(redrive.encoded)
	if err != nil {
		t.Fatal(err)
	}
	again, err := jobs.RestoreDeliveryRecord(catalog, decoded)
	if err != nil {
		t.Fatal(err)
	}
	if again.Invocation().State() != jobs.InvocationQueued || again.Invocation().CreatedAt() != now || len(again.Invocation().History()) != 1 || len(again.Invocation().Attempts()) != 0 || !slices.Equal(again.Payload().Bytes(), restored.Payload().Bytes()) {
		t.Fatal("redriven delivery did not restore as fresh queued work")
	}
	if viewPayload := redrive.view.Payload().Bytes(); !slices.Equal(viewPayload, restored.Payload().Bytes()) || redrive.view.Invocation().ID() != terminal.ID() {
		t.Fatal("redrive view does not match durable record")
	}
	if _, err := driver.redriveRecord(insert.record, now); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("nonterminal record redrive = %v", err)
	}
}

func TestAdminMethodsRequireReadinessAndValidateWithoutDatabaseAccess(t *testing.T) {
	namespace, catalog, placement := testPlacement(t)
	driver, err := New(Spec{DB: &sql.DB{}, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if _, err := driver.Get(ctx, placement.Candidate()); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Get before readiness = %v", err)
	}
	if _, err := driver.List(ctx, ListSpec{}); !errors.Is(err, ErrNotReady) {
		t.Fatalf("List before readiness = %v", err)
	}
	if _, err := driver.Redrive(ctx, placement.Candidate()); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Redrive before readiness = %v", err)
	}
	if _, err := driver.PurgeTerminal(ctx, time.Now(), 1); !errors.Is(err, ErrNotReady) {
		t.Fatalf("PurgeTerminal before readiness = %v", err)
	}
}
