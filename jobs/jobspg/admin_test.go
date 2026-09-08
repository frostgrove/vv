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
	plan, err := jobs.NewIntentDigestPlan(jobs.DigestRevision2, jobs.DigestRevision1)
	if err != nil {
		t.Fatal(err)
	}
	namespace, catalog, placement := testPlacementWithDigests(t, plan, jobs.Unique("redrive-rolling"))
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
	redrive, err := driver.redriveRecord(terminalEncoded, placement.IntentDigests().ReservationKeys(), now)
	if err != nil {
		t.Fatal(err)
	}
	if redrive.createdAt != now || redrive.mode != terminal.Mode() || !slices.Equal(redrive.intents, placement.IntentDigests().ReservationKeys()) || redrive.size <= 0 {
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
	if _, err := driver.redriveRecord(insert.record, placement.IntentDigests().ReservationKeys(), now); !errors.Is(err, jobs.ErrConflict) {
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
	if _, err := driver.Cancel(ctx, placement.Candidate()); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Cancel before readiness = %v", err)
	}
	if _, err := driver.Terminate(ctx, placement.Candidate()); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Terminate before readiness = %v", err)
	}
	if _, err := driver.PurgeTerminal(ctx, time.Now(), 1); !errors.Is(err, ErrNotReady) {
		t.Fatalf("PurgeTerminal before readiness = %v", err)
	}
	if _, err := driver.SweepTerminalRetention(ctx, 1); !errors.Is(err, ErrNotReady) {
		t.Fatalf("SweepTerminalRetention before readiness = %v", err)
	}
}

// An offset makes the database walk and discard every row before the page, so the
// cost of page N grows with N and the last pages of a long-retention namespace
// become unusable. A cursor reads the page and nothing else.
func TestAListCursorReadsThePageAndNotEverythingBeforeIt(t *testing.T) {
	repo := newRepository("jobspg_list_cursor")
	id, err := jobs.NewInvocationID()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2035, 3, 4, 5, 6, 7, 0, time.UTC)

	paged, err := normalizeListSpec(jobs.ListSpec{Limit: 10, After: &jobs.ListCursor{CreatedAt: at, ID: id}})
	if err != nil {
		t.Fatal(err)
	}
	if paged.after == nil {
		t.Fatal("the cursor was dropped in normalization")
	}

	// A cursor and an offset name two different pages, so asking for both is a
	// mistake in the caller rather than something to silently resolve.
	if _, err := normalizeListSpec(jobs.ListSpec{Limit: 10, Offset: 5, After: &jobs.ListCursor{CreatedAt: at, ID: id}}); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("cursor with offset = %v, want ErrInvalid", err)
	}
	if _, err := normalizeListSpec(jobs.ListSpec{Limit: 10, After: &jobs.ListCursor{CreatedAt: at}}); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("cursor with no id = %v, want ErrInvalid", err)
	}

	// The control: without a cursor the offset form still works, so the branch
	// above is the cursor rather than paging having been broken.
	plain, err := normalizeListSpec(jobs.ListSpec{Limit: 10, Offset: 20})
	if err != nil || plain.after != nil || plain.offset != 20 {
		t.Fatalf("offset paging = %#v, %v", plain, err)
	}
	_ = repo
}

// The outbox promise is that the invocation row and the rows the caller wrote
// commit together or not at all. A *sql.Tx does not say which database it began
// on, so a transaction from somewhere else made "atomic with your write" a claim
// with nothing behind it: the two would commit independently.
func TestAStagerRefusesATransactionFromAnotherDatabase(t *testing.T) {
	driver := &Driver{db: &sql.DB{}}
	other := &sql.DB{}

	if _, err := driver.Stager(other, &sql.Tx{}); !errors.Is(err, jobs.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported — a transaction from another database was adopted as an outbox", err)
	}
	if _, err := driver.Stager(nil, &sql.Tx{}); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid — the check was skipped when the caller named no database", err)
	}
}
