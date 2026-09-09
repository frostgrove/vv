package event

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var errEmptyCursorStored = errors.New("recording checkpoints: the empty cursor is the origin and is not a resume point")

// The checkpoint store the door cases drive: the real fence, a row per name, a
// count per method so a case that says no statement was issued asserts it, and
// one hook — answer — that lets an honest store be turned into each of the
// defective ones §UC-129 names without a second fixture.
type recordingCheckpoints struct {
	backing      Backing
	capabilities CheckpointCapabilities

	answer     func(held Checkpoint, asked string) (Checkpoint, error)
	failLoad   error
	failSave   error
	failForget error

	// What Transaction answers an authority over. A store on a caller's
	// transaction stages a save the caller commits or rolls back; one on nothing
	// commits its own.
	bound any

	mutex sync.Mutex
	rows  map[string]Checkpoint
	calls map[string]int
}

func newRecordingCheckpoints(t *testing.T) *recordingCheckpoints {
	t.Helper()
	checkpoints := &recordingCheckpoints{
		capabilities: CheckpointCapabilities{Transactions: Supported, Persistence: Unsupported},
		rows:         map[string]Checkpoint{},
		calls:        map[string]int{},
	}
	backing, err := NewBacking(checkpoints)
	if err != nil {
		t.Fatalf("the fixture checkpoint store cannot name what it writes to, so no case below is about the door: %v", err)
	}
	checkpoints.backing = backing
	return checkpoints
}

func (this *recordingCheckpoints) Capabilities() CheckpointCapabilities {
	this.record("Capabilities")
	return this.capabilities
}

func (this *recordingCheckpoints) Backing() Backing {
	this.record("Backing")
	return this.backing
}

func (this *recordingCheckpoints) Transaction(context.Context) (Authority, error) {
	this.record("Transaction")
	if this.bound == nil {
		return Authority{}, nil
	}
	return NewAuthority(this.backing, this.bound)
}

func (this *recordingCheckpoints) Load(ctx context.Context, projection string) (Checkpoint, error) {
	this.record("Load")
	if err := ctx.Err(); err != nil {
		return Checkpoint{}, err
	}
	if this.failLoad != nil {
		return Checkpoint{}, this.failLoad
	}
	this.mutex.Lock()
	held := this.rows[projection]
	this.mutex.Unlock()
	if this.answer != nil {
		return this.answer(held, projection)
	}
	return held, nil
}

func (this *recordingCheckpoints) Save(ctx context.Context, checkpoint Checkpoint) error {
	this.record("Save")
	if err := ctx.Err(); err != nil {
		return err
	}
	if this.failSave != nil {
		return this.failSave
	}
	if checkpoint.Cursor == "" {
		return Failure(Refused, errEmptyCursorStored)
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	held, found := this.rows[checkpoint.Projection]
	if found != (checkpoint.Advance > 1) || (found && held.Advance != checkpoint.Advance-1) {
		return Failure(Conflict, nil)
	}
	this.rows[checkpoint.Projection] = checkpoint
	return nil
}

func (this *recordingCheckpoints) Forget(ctx context.Context, projection string) error {
	this.record("Forget")
	if err := ctx.Err(); err != nil {
		return err
	}
	if this.failForget != nil {
		return this.failForget
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	delete(this.rows, projection)
	return nil
}

func (this *recordingCheckpoints) Close() error {
	this.record("Close")
	return nil
}

func (this *recordingCheckpoints) record(method string) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.calls[method]++
}

func (this *recordingCheckpoints) count(method string) int {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.calls[method]
}

func (this *recordingCheckpoints) held(projection string) Checkpoint {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.rows[projection]
}

// A row no tracker wrote: another deploy's, a restored dump, a foreign writer.
func (this *recordingCheckpoints) write(row Checkpoint) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.rows[row.Projection] = row
}

// What a consumer does with a checkpoint — load it, then read the log from the
// cursor it answered — so "no read is issued from a refused cursor" is asserted
// against the log's own ReadAll count rather than by the case declining to call
// it.
func resumeFrom(ctx context.Context, tracker *Tracker, log Log) error {
	held, err := tracker.Load(ctx)
	if err != nil {
		return err
	}
	reader, err := Read(log, held.Cursor)
	if err != nil {
		return err
	}
	_, err = reader.Next(ctx)
	return err
}

func logWithAHistory(t *testing.T) *recordingStore {
	t.Helper()
	store := newRecordingStore(t)
	repo, declared := bindAccounts(t, store)
	acme := accountID{tenant: "acme", number: "A-17"}
	ctx := context.Background()
	_, at, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the stream the cases below walk was refused with %v", err)
	}
	if _, _, err := repo.Append(ctx, at,
		declared.opened.New(acme, opened{Owner: "acme"}),
		declared.credited.New(acme, creditedV2{Minor: 1, Reason: "one"}),
	); err != nil {
		t.Fatalf("the events the cases below walk could not be written: %v", err)
	}
	store.forget()
	return store
}

// The check the whole door exists for. A Load that ignores its argument answers
// another projection's well-formed row — of this log, in this store's format,
// refused by nothing else on any path — and a consumer that took it resumes
// hundreds of thousands of positions ahead and skips everything in between, for
// ever, with no error anywhere. Beside it the two other answers a row can be
// wrong in without being unreadable.
func TestTheDoorRefusesAnAnswerAboutAnotherName(t *testing.T) {
	ctx := context.Background()
	ours := Checkpoint{Projection: "accounts.balances", Cursor: "1", Advance: 4}
	foreign := Checkpoint{Projection: "accounts.audit", Cursor: Cursor(strconv.FormatUint(400000, 10)), Advance: 91}

	for _, one := range []struct {
		what   string
		answer func(held Checkpoint, asked string) (Checkpoint, error)
	}{
		{"a Load that ignores its argument and answers another name's row", func(Checkpoint, string) (Checkpoint, error) {
			return foreign, nil
		}},
		{"a Load answering no advance beside a cursor that is set", func(Checkpoint, string) (Checkpoint, error) {
			return Checkpoint{Cursor: "1"}, nil
		}},
		{"a Load answering a cursor over the published ceiling", func(held Checkpoint, _ string) (Checkpoint, error) {
			held.Cursor = Cursor(strings.Repeat("c", MaxCursorBytes+1))
			return held, nil
		}},
	} {
		store := logWithAHistory(t)
		checkpoints := newRecordingCheckpoints(t)
		checkpoints.write(ours)
		checkpoints.answer = one.answer
		tracker, err := Track(checkpoints, ours.Projection)
		if err != nil {
			t.Fatalf("%s: the tracker this case drives was refused with %v", one.what, err)
		}

		if err := resumeFrom(ctx, tracker, ReadOnly(store)); !errors.Is(err, ErrWrongStore) {
			t.Fatalf("%s answered %v, and a consumer that took that row would skip every event between the two positions with no error on any path", one.what, err)
		}
		if store.count("ReadAll") != 0 {
			t.Fatalf("%s was refused and the log was read %d times, so the cursor the door refused reached a walk", one.what, store.count("ReadAll"))
		}
		if checkpoints.count("Save") != 0 {
			t.Fatalf("%s was refused and %d saves were issued; a row a consumer would not resume from is a row it does not write over, because an operator's only repair is the row that is there", one.what, checkpoints.count("Save"))
		}
		if checkpoints.held(ours.Projection) != ours {
			t.Fatalf("%s was refused and the stored row is now %+v rather than the one that was there", one.what, checkpoints.held(ours.Projection))
		}
	}

	store := logWithAHistory(t)
	checkpoints := newRecordingCheckpoints(t)
	checkpoints.write(ours)
	tracker, err := Track(checkpoints, ours.Projection)
	if err != nil {
		t.Fatalf("the control tracker was refused with %v", err)
	}
	if err := resumeFrom(ctx, tracker, ReadOnly(store)); err != nil {
		t.Fatalf("the honest store's own row was refused at the same door with %v, so every row above passes against a door that refuses everything", err)
	}
	if store.count("ReadAll") != 1 {
		t.Fatalf("the control resumed and read the log %d times, so the count the rows above assert is zero is a count nothing ever raises", store.count("ReadAll"))
	}
}

// Absence is total: a row that is half-absent is not a fresh start with some
// extra fields. And the comparison is field by field, never Progress{} ==
// held.Progress: a time.Time carries a monotonic reading and a *Location, so an
// equality test would refuse a store answering a zero instant in another zone —
// a difference that is not one.
func TestAbsenceIsTotalAndProgressIsComparedFieldByField(t *testing.T) {
	ctx := context.Background()
	elsewhere := time.FixedZone("elsewhere", 3600)

	t.Run("a zero instant in another location is still absence", func(t *testing.T) {
		checkpoints := newRecordingCheckpoints(t)
		checkpoints.answer = func(Checkpoint, string) (Checkpoint, error) {
			return Checkpoint{Progress: Progress{At: time.Time{}.In(elsewhere)}}, nil
		}
		tracker, err := Track(checkpoints, "accounts.balances")
		if err != nil {
			t.Fatalf("the tracker this case drives was refused with %v", err)
		}

		held, err := tracker.Load(ctx)
		if err != nil {
			t.Fatalf("a store answering a zero instant in another zone was refused with %v, and a door comparing the whole struct would refuse a correct store for a difference that is not one", err)
		}
		if !held.Fresh() {
			t.Fatalf("the answer reads as advance %d rather than as absence, so the walk starts somewhere other than the origin", held.Advance)
		}
	})

	for _, one := range []struct {
		what     string
		progress Progress
	}{
		{"a highest position", Progress{Highest: 91}},
		{"a count of applied envelopes", Progress{Applied: 12}},
		{"a count of quarantined envelopes", Progress{Quarantined: 3}},
		{"an instant that is not the zero one", Progress{At: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)}},
	} {
		checkpoints := newRecordingCheckpoints(t)
		checkpoints.answer = func(Checkpoint, string) (Checkpoint, error) {
			return Checkpoint{Progress: one.progress}, nil
		}
		tracker, err := Track(checkpoints, "accounts.balances")
		if err != nil {
			t.Fatalf("%s: the tracker this case drives was refused with %v", one.what, err)
		}

		if _, err := tracker.Load(ctx); !errors.Is(err, ErrWrongStore) {
			t.Fatalf("no advance beside %s answered %v, and a row that is half-absent is a store that did not answer the question", one.what, err)
		}
	}

	for _, one := range []struct {
		what   string
		answer Checkpoint
	}{
		{"a name", Checkpoint{Projection: "accounts.balances"}},
		{"a cursor", Checkpoint{Cursor: "1"}},
	} {
		checkpoints := newRecordingCheckpoints(t)
		checkpoints.answer = func(Checkpoint, string) (Checkpoint, error) { return one.answer, nil }
		tracker, err := Track(checkpoints, "accounts.balances")
		if err != nil {
			t.Fatalf("%s: the tracker this case drives was refused with %v", one.what, err)
		}

		if _, err := tracker.Load(ctx); !errors.Is(err, ErrWrongStore) {
			t.Fatalf("no advance beside %s answered %v", one.what, err)
		}
	}
}

// The empty cursor IS the origin of the log, in every store that ships, by
// design. So a row at a non-zero advance carrying one is a readable checkpoint
// that restarts a consumer at the beginning of the log against a live
// destination — reached with no error on any path, because nothing else fires on
// it: not the store, not Read, not the reader, not the fence.
func TestPresenceIsTotalAndAnEmptyCursorIsNeverAResumePoint(t *testing.T) {
	ctx := context.Background()
	name := "accounts.balances"

	t.Run("a stored row at a non-zero advance with no cursor", func(t *testing.T) {
		store := logWithAHistory(t)
		checkpoints := newRecordingCheckpoints(t)
		stored := Checkpoint{Projection: name, Cursor: "1", Advance: 7}
		checkpoints.write(stored)
		checkpoints.answer = func(held Checkpoint, _ string) (Checkpoint, error) {
			held.Cursor = ""
			return held, nil
		}
		tracker, err := Track(checkpoints, name)
		if err != nil {
			t.Fatalf("the tracker this case drives was refused with %v", err)
		}

		if err := resumeFrom(ctx, tracker, ReadOnly(store)); !errors.Is(err, ErrWrongStore) {
			t.Fatalf("a row at advance 7 with no cursor answered %v, and a consumer that took it re-applies the whole log against a live destination", err)
		}
		if store.count("ReadAll") != 0 {
			t.Fatalf("the refused row still reached a walk, which read the log %d times from the origin", store.count("ReadAll"))
		}
		if checkpoints.held(name) != stored {
			t.Fatalf("the row the door refused is now %+v rather than the one that was there", checkpoints.held(name))
		}
	})

	t.Run("a caller presenting the empty cursor at a save", func(t *testing.T) {
		checkpoints := newRecordingCheckpoints(t)
		checkpoints.write(Checkpoint{Projection: name, Cursor: "1", Advance: 2})
		tracker, err := Track(checkpoints, name)
		if err != nil {
			t.Fatalf("the tracker this case drives was refused with %v", err)
		}
		if _, err := tracker.Load(ctx); err != nil {
			t.Fatalf("the row this case saves over was refused at the door with %v", err)
		}

		if _, err := tracker.Save(ctx, "", Progress{}); !errors.Is(err, ErrWrongStore) {
			t.Fatalf("a save of the empty cursor answered %v", err)
		}
		if checkpoints.count("Save") != 0 {
			t.Fatalf("the empty cursor was refused after %d calls to the store, and a cursor that is not one never reaches a column", checkpoints.count("Save"))
		}
		if _, err := tracker.Save(ctx, "2", Progress{Highest: 2}); err != nil {
			t.Fatalf("the same tracker at a real cursor was refused with %v, so the arm above refuses every save rather than the empty one", err)
		}
		if held := checkpoints.held(name); held.Advance != 3 || held.Cursor != "2" {
			t.Fatalf("the save that was to land left the row at advance %d and cursor of %d bytes", held.Advance, len(held.Cursor))
		}
	})

	// The other refusal a cursor earns at this door, and it is a different one:
	// an over-ceiling cursor is a store minting something too big for a column
	// that is exactly that wide, while the empty one can only be a caller
	// presenting a cursor that is not one.
	t.Run("a cursor over the ceiling is a different refusal at the same door", func(t *testing.T) {
		checkpoints := newRecordingCheckpoints(t)
		tracker, err := Track(checkpoints, name)
		if err != nil {
			t.Fatalf("the tracker this case drives was refused with %v", err)
		}
		if _, err := tracker.Load(ctx); err != nil {
			t.Fatalf("the load this case saves after was refused with %v", err)
		}

		_, err = tracker.Save(ctx, Cursor(strings.Repeat("c", MaxCursorBytes+1)), Progress{})
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("a cursor of %d bytes answered %v where the checkpoint column is %d wide and the alternative is a constraint violation the store cannot classify", MaxCursorBytes+1, err, MaxCursorBytes)
		}
		if errors.Is(err, ErrWrongStore) {
			t.Fatalf("an over-long cursor also answers for the wiring class in %v, and the two are different defects reached by different parties", err)
		}
		if checkpoints.count("Save") != 0 {
			t.Fatalf("the over-long cursor reached the store %d times", checkpoints.count("Save"))
		}
		if _, err := tracker.Save(ctx, Cursor(strings.Repeat("c", MaxCursorBytes)), Progress{}); err != nil {
			t.Fatalf("a cursor of exactly %d bytes was refused with %v, and the ceiling is the last cursor a store may hold rather than the first it may not", MaxCursorBytes, err)
		}
	})

	t.Run("the honest pair the refusals must not have swallowed", func(t *testing.T) {
		store := logWithAHistory(t)
		checkpoints := newRecordingCheckpoints(t)
		checkpoints.write(Checkpoint{Projection: name, Cursor: "1", Advance: 7})
		present, err := Track(checkpoints, name)
		if err != nil {
			t.Fatalf("the tracker this case drives was refused with %v", err)
		}
		if err := resumeFrom(ctx, present, ReadOnly(store)); err != nil {
			t.Fatalf("a non-empty cursor at advance 7 was refused with %v, so the arm above refuses every stored row", err)
		}

		absent, err := Track(newRecordingCheckpoints(t), name)
		if err != nil {
			t.Fatalf("the tracker over an empty store was refused with %v", err)
		}
		held, err := absent.Load(ctx)
		if err != nil || !held.Fresh() || held.Cursor != "" {
			t.Fatalf("the zero checkpoint answered %v and reads as advance %d, and absence is still the origin", err, held.Advance)
		}
	})
}
