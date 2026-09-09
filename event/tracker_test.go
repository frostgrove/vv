package event

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
)

func TestAFreshTrackerAnswersTheZeroCheckpointAndSavesAtAdvanceOne(t *testing.T) {
	ctx := context.Background()
	name := "accounts.balances"
	checkpoints := newRecordingCheckpoints(t)
	tracker, err := Track(checkpoints, name)
	if err != nil {
		t.Fatalf("the tracker this case drives was refused with %v", err)
	}

	held, err := tracker.Load(ctx)
	if err != nil {
		t.Fatalf("a store holding no row for this name answered %v, and absence is a first run rather than an error", err)
	}
	if !held.Fresh() || held != (Checkpoint{}) {
		t.Fatalf("a store holding no row answered %+v, and the zero checkpoint is what says the walk starts at the origin", held)
	}
	if tracker.Projection() != name {
		t.Fatalf("the tracker names %q where it was built for %q", tracker.Projection(), name)
	}

	if _, err := tracker.Save(ctx, "1", Progress{Highest: 1, Applied: 1}); err != nil {
		t.Fatalf("the first save was refused with %v", err)
	}
	if stored := checkpoints.held(name); stored.Advance != 1 {
		t.Fatalf("the first save presented advance %d where a fresh row is admitted at 1 and nothing else", stored.Advance)
	}
	if _, err := tracker.Save(ctx, "2", Progress{Highest: 2, Applied: 2}); err != nil {
		t.Fatalf("the second save was refused with %v", err)
	}
	if stored := checkpoints.held(name); stored.Advance != 2 || stored.Cursor != "2" {
		t.Fatalf("the second save left the row at advance %d and a cursor of %d bytes", stored.Advance, len(stored.Cursor))
	}
	if stored := checkpoints.held(name); stored.Progress.Applied != 2 || stored.Progress.Highest != 2 {
		t.Fatalf("the progress the second save carried arrived as %+v", stored.Progress)
	}
}

func TestASaveBeforeALoadIsRefused(t *testing.T) {
	ctx := context.Background()
	name := "accounts.balances"
	checkpoints := newRecordingCheckpoints(t)
	tracker, err := Track(checkpoints, name)
	if err != nil {
		t.Fatalf("the tracker this case drives was refused with %v", err)
	}

	if _, err := tracker.Save(ctx, "1", Progress{}); !errors.Is(err, ErrWrongStore) {
		t.Fatalf("a save before a load answered %v, and the advance a save is fenced on is the one a load answered", err)
	}
	if checkpoints.count("Save") != 0 {
		t.Fatalf("a save before a load reached the store %d times, so a row was written at an advance nobody loaded", checkpoints.count("Save"))
	}

	if _, err := tracker.Load(ctx); err != nil {
		t.Fatalf("the control's load was refused with %v", err)
	}
	if _, err := tracker.Save(ctx, "1", Progress{}); err != nil {
		t.Fatalf("the same save after a load was refused with %v, so the arm above refuses every save rather than the unloaded one", err)
	}
}

// The fence is the door's: the advance presented is the one this tracker loaded
// plus one, a failed save does not move it, and Forget does not reset it — a
// consumer that forgot its own name finds no row at its next advance and is
// refused, which is the correct and visible outcome of retiring a live one.
func TestTheFenceIsTheDoorsAndNoCallerComputesIt(t *testing.T) {
	ctx := context.Background()
	name := "accounts.balances"

	t.Run("no caller can present an advance at all", func(t *testing.T) {
		save, found := reflect.TypeOf((*Tracker)(nil)).MethodByName("Save")
		if !found {
			t.Fatal("a tracker answers no Save, and the fence is the door's because the door is the only party that can present one")
		}
		for index := range save.Type.NumIn() {
			if kind := save.Type.In(index); kind.Kind() == reflect.Uint64 && kind != reflect.TypeOf(Position(0)) {
				t.Fatalf("Save takes a %s, so an advance is a number a caller computes and two callers can compute it differently", kind)
			}
		}
	})

	// The other half of the same rule, and the half a caller can get wrong on its
	// own: the door answers the number it presented, so nothing downstream has to
	// keep a second copy of the fence to compare a row against. It answers it on
	// a refusal too, because that is exactly when the number is needed.
	t.Run("the door answers the advance it presented, landed or refused", func(t *testing.T) {
		checkpoints := newRecordingCheckpoints(t)
		tracker, err := Track(checkpoints, name)
		if err != nil {
			t.Fatalf("the tracker this case drives was refused with %v", err)
		}
		if _, err := tracker.Load(ctx); err != nil {
			t.Fatalf("the load this case saves after was refused with %v", err)
		}
		presented, err := tracker.Save(ctx, "1", Progress{})
		if err != nil || presented != 1 {
			t.Fatalf("a first save over a fresh row presented advance %d and answered %v, where the origin's next advance is 1", presented, err)
		}

		checkpoints.failSave = Failure(Conflict, nil)
		presented, err = tracker.Save(ctx, "2", Progress{})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("the refused save this case reads the advance off answered %v", err)
		}
		if presented != 2 {
			t.Fatalf("a refused save answered advance %d, and a caller that cannot read what was presented over a row that refused it settles against a number nobody wrote", presented)
		}

		checkpoints.failSave = nil
		if presented, err := tracker.Save(ctx, "2", Progress{}); err != nil || presented != 2 {
			t.Fatalf("the save after a refusal presented advance %d and answered %v, so the refusal moved the fence", presented, err)
		}
	})

	t.Run("a hundred advances in one process meet no refusal", func(t *testing.T) {
		checkpoints := newRecordingCheckpoints(t)
		tracker, err := Track(checkpoints, name)
		if err != nil {
			t.Fatalf("the tracker this case drives was refused with %v", err)
		}
		if _, err := tracker.Load(ctx); err != nil {
			t.Fatalf("the first load was refused with %v", err)
		}
		for advance := 1; advance <= 100; advance++ {
			if _, err := tracker.Save(ctx, Cursor(fmt.Sprint(advance)), Progress{Highest: Position(advance)}); err != nil {
				t.Fatalf("save %d was refused with %v, and one process advancing alone meets no fence", advance, err)
			}
			if stored := checkpoints.held(name); stored.Advance != uint64(advance) {
				t.Fatalf("save %d left the row at advance %d, so the advance presented is not the loaded one plus one", advance, stored.Advance)
			}
		}
	})

	t.Run("a failed save does not move the fence", func(t *testing.T) {
		checkpoints := newRecordingCheckpoints(t)
		tracker, err := Track(checkpoints, name)
		if err != nil {
			t.Fatalf("the tracker this case drives was refused with %v", err)
		}
		if _, err := tracker.Load(ctx); err != nil {
			t.Fatalf("the load this case saves after was refused with %v", err)
		}
		if _, err := tracker.Save(ctx, "1", Progress{}); err != nil {
			t.Fatalf("the save this case fails after was refused with %v", err)
		}

		checkpoints.failSave = Failure(NotWritten, nil)
		if _, err := tracker.Save(ctx, "2", Progress{}); !errors.Is(err, ErrBackend) {
			t.Fatalf("a save the store refused answered %v", err)
		}
		checkpoints.failSave = nil
		if _, err := tracker.Save(ctx, "2", Progress{}); err != nil {
			t.Fatalf("the retry of a failed save was refused with %v, so the fence moved for a save that never landed", err)
		}
		if stored := checkpoints.held(name); stored.Advance != 2 {
			t.Fatalf("two saves landed and the row is at advance %d", stored.Advance)
		}
	})

	t.Run("a second tracker over one store presents one above what it loaded", func(t *testing.T) {
		checkpoints := newRecordingCheckpoints(t)
		checkpoints.write(Checkpoint{Projection: name, Cursor: "9", Advance: 9})
		tracker, err := Track(checkpoints, name)
		if err != nil {
			t.Fatalf("the tracker this case drives was refused with %v", err)
		}
		held, err := tracker.Load(ctx)
		if err != nil || held.Advance != 9 {
			t.Fatalf("the stored row loaded as advance %d (%v)", held.Advance, err)
		}
		if _, err := tracker.Save(ctx, "10", Progress{}); err != nil {
			t.Fatalf("the save after a load at advance 9 was refused with %v", err)
		}
		if stored := checkpoints.held(name); stored.Advance != 10 {
			t.Fatalf("a tracker that loaded advance 9 presented %d", stored.Advance)
		}
	})

	t.Run("Forget does not reset the fence", func(t *testing.T) {
		checkpoints := newRecordingCheckpoints(t)
		tracker, err := Track(checkpoints, name)
		if err != nil {
			t.Fatalf("the tracker this case drives was refused with %v", err)
		}
		if _, err := tracker.Load(ctx); err != nil {
			t.Fatalf("the load this case forgets after was refused with %v", err)
		}
		if _, err := tracker.Save(ctx, "1", Progress{}); err != nil {
			t.Fatalf("the save this case forgets was refused with %v", err)
		}
		if err := tracker.Forget(ctx); err != nil {
			t.Fatalf("forgetting one name answered %v", err)
		}
		if stored := checkpoints.held(name); stored != (Checkpoint{}) {
			t.Fatalf("the row survived being forgotten as %+v", stored)
		}

		if _, err := tracker.Save(ctx, "2", Progress{}); !errors.Is(err, ErrConflict) {
			t.Fatalf("the next save after a forget answered %v; a tracker that reset its own fence would write a fresh row and restart the walk at the origin in silence", err)
		}
		if stored := checkpoints.held(name); stored != (Checkpoint{}) {
			t.Fatalf("the refused save wrote %+v over a name that had been retired", stored)
		}
	})
}

// What a tracker holds is a window and not the number its last Load happened to
// answer. A row that moved outside it moved under a live tracker, and a tracker
// that re-seated itself to it would go on writing: downwards that is a fresh row
// at advance 1 and a walk that resumes from the origin at the next pass;
// upwards it is two processes taking turns over one name, each re-reading the
// other's advance and saving one above it. Nothing else on any path fires on
// either.
func TestALoadNeverReSeatsAFenceThisTrackerEstablished(t *testing.T) {
	ctx := context.Background()
	name := "accounts.balances"

	t.Run("a forgotten row is not a fresh start for the tracker that forgot it", func(t *testing.T) {
		checkpoints := newRecordingCheckpoints(t)
		tracker, err := Track(checkpoints, name)
		if err != nil {
			t.Fatalf("the tracker this case drives was refused with %v", err)
		}
		if _, err := tracker.Load(ctx); err != nil {
			t.Fatalf("the load this case advances from was refused with %v", err)
		}
		for advance := 1; advance <= 5; advance++ {
			if _, err := tracker.Save(ctx, Cursor(fmt.Sprint(advance)), Progress{Highest: Position(advance)}); err != nil {
				t.Fatalf("save %d was refused with %v", advance, err)
			}
		}
		if err := tracker.Forget(ctx); err != nil {
			t.Fatalf("forgetting one name answered %v", err)
		}

		if _, err := tracker.Load(ctx); !errors.Is(err, ErrWrongStore) {
			t.Fatalf("a load over the row this tracker forgot answered %v, and a tracker that took the absence would write a fresh row at advance 1 and resume the walk from the origin", err)
		}
		if _, err := tracker.Save(ctx, "6", Progress{}); !errors.Is(err, ErrConflict) {
			t.Fatalf("the save after that load answered %v, where a retired name is the store's conflict and a visible halt", err)
		}
		if stored := checkpoints.held(name); stored != (Checkpoint{}) {
			t.Fatalf("the row was written again as %+v, and the read model now holds five pages no checkpoint accounts for", stored)
		}
	})

	t.Run("the loser of a fenced save does not re-read the winner's advance", func(t *testing.T) {
		checkpoints := newRecordingCheckpoints(t)
		winner, err := Track(checkpoints, name)
		if err != nil {
			t.Fatalf("the winning tracker was refused with %v", err)
		}
		loser, err := Track(checkpoints, name)
		if err != nil {
			t.Fatalf("the losing tracker was refused with %v", err)
		}
		for _, tracker := range []*Tracker{winner, loser} {
			if _, err := tracker.Load(ctx); err != nil {
				t.Fatalf("a load of the row both replicas start from was refused with %v", err)
			}
		}
		if _, err := winner.Save(ctx, "a", Progress{}); err != nil {
			t.Fatalf("the winning save was refused with %v", err)
		}
		if _, err := loser.Save(ctx, "b", Progress{}); !errors.Is(err, ErrConflict) {
			t.Fatalf("the losing save answered %v, so the fence this case is about is not there at all", err)
		}

		if _, err := loser.Load(ctx); !errors.Is(err, ErrWrongStore) {
			t.Fatalf("the loser re-read the row and was answered %v, and a save at the advance it read is what makes two processes take turns over one name", err)
		}
		if _, err := loser.Save(ctx, "c", Progress{}); !errors.Is(err, ErrConflict) {
			t.Fatalf("the loser's second save answered %v", err)
		}
		if stored := checkpoints.held(name); stored.Cursor != "a" || stored.Advance != 1 {
			t.Fatalf("the loser advanced the row the winner owns to %+v", stored)
		}
	})

	t.Run("a save the store did not confirm is settled by a load, either way", func(t *testing.T) {
		for _, one := range []struct {
			what   string
			landed bool
		}{
			{"the statement committed and the answer was lost", true},
			{"nothing was written", false},
		} {
			checkpoints := newRecordingCheckpoints(t)
			checkpoints.write(Checkpoint{Projection: name, Cursor: "3", Advance: 3})
			tracker, err := Track(checkpoints, name)
			if err != nil {
				t.Fatalf("%s: the tracker this row drives was refused with %v", one.what, err)
			}
			if _, err := tracker.Load(ctx); err != nil {
				t.Fatalf("%s: the load this row saves after was refused with %v", one.what, err)
			}

			checkpoints.failSave = Failure(Unconfirmed, nil)
			if _, err := tracker.Save(ctx, "4", Progress{}); !errors.Is(err, ErrUncertain) {
				t.Fatalf("%s: the save this row resolves answered %v", one.what, err)
			}
			checkpoints.failSave = nil
			if one.landed {
				checkpoints.write(Checkpoint{Projection: name, Cursor: "4", Advance: 4})
			}

			held, err := tracker.Load(ctx)
			if err != nil {
				t.Fatalf("%s: the one load that resolves an unconfirmed save was refused with %v, and the resolution the framework prescribes is unreachable", one.what, err)
			}
			if landed := held.Advance == 4; landed != one.landed {
				t.Fatalf("%s: the resolving load answered advance %d", one.what, held.Advance)
			}
			if _, err := tracker.Save(ctx, "5", Progress{}); err != nil {
				t.Fatalf("%s: the save after the resolution was refused with %v", one.what, err)
			}
		}
	})

	t.Run("a save over one the store did not confirm never reaches the store", func(t *testing.T) {
		checkpoints := newRecordingCheckpoints(t)
		tracker, err := Track(checkpoints, name)
		if err != nil {
			t.Fatalf("the tracker this case drives was refused with %v", err)
		}
		if _, err := tracker.Load(ctx); err != nil {
			t.Fatalf("the load this case saves after was refused with %v", err)
		}
		checkpoints.failSave = Failure(Unconfirmed, nil)
		if _, err := tracker.Save(ctx, "1", Progress{}); !errors.Is(err, ErrUncertain) {
			t.Fatalf("the unconfirmed save answered %v", err)
		}
		checkpoints.failSave = nil
		issued := checkpoints.count("Save")

		if _, err := tracker.Save(ctx, "1", Progress{}); !errors.Is(err, ErrWrongStore) {
			t.Fatalf("a second save over an unresolved one answered %v, and either advance it presents is a guess about a row nobody read", err)
		}
		if checkpoints.count("Save") != issued {
			t.Fatalf("the blind re-save reached the store, which issued %d statements against %d", checkpoints.count("Save"), issued)
		}
		if _, err := tracker.Load(ctx); err != nil {
			t.Fatalf("the load that resolves it was refused with %v", err)
		}
		if _, err := tracker.Save(ctx, "1", Progress{}); err != nil {
			t.Fatalf("the save after the resolution was refused with %v, so the arm above refuses every save rather than the unresolved one", err)
		}
	})

	// The one legitimate step back, and what tells it apart from the two above:
	// a save staged in a transaction this tracker does not commit may still be
	// rolled back under it, so the row it read may legitimately be the one that
	// was there before. A store that commits its own saves leaves no such room,
	// and the pair is what keeps the window from being either always open or
	// always shut.
	t.Run("a save this tracker did not commit may be rolled back under it", func(t *testing.T) {
		for _, one := range []struct {
			what     string
			bound    any
			rollback bool
		}{
			{"a save staged in the caller's transaction, rolled back", "the caller's transaction", true},
			{"a save staged in the caller's transaction, committed", "the caller's transaction", false},
			{"a save the store committed itself", nil, true},
		} {
			checkpoints := newRecordingCheckpoints(t)
			checkpoints.bound = one.bound
			checkpoints.write(Checkpoint{Projection: name, Cursor: "3", Advance: 3})
			tracker, err := Track(checkpoints, name)
			if err != nil {
				t.Fatalf("%s: the tracker this row drives was refused with %v", one.what, err)
			}
			if _, err := tracker.Load(ctx); err != nil {
				t.Fatalf("%s: the load this row saves after was refused with %v", one.what, err)
			}
			if _, err := tracker.Save(ctx, "4", Progress{}); err != nil {
				t.Fatalf("%s: the staged save was refused with %v", one.what, err)
			}
			if one.rollback {
				checkpoints.write(Checkpoint{Projection: name, Cursor: "3", Advance: 3})
			}

			held, err := tracker.Load(ctx)
			switch {
			case one.bound == nil && one.rollback:
				if !errors.Is(err, ErrWrongStore) {
					t.Fatalf("%s: a row that went back under a tracker whose save the store committed answered %v", one.what, err)
				}
			case err != nil:
				t.Fatalf("%s: the load after the caller's own transaction settled was refused with %v", one.what, err)
			case one.rollback && held.Advance != 3:
				t.Fatalf("%s: the rolled-back row loaded as advance %d", one.what, held.Advance)
			case !one.rollback && held.Advance != 4:
				t.Fatalf("%s: the committed row loaded as advance %d", one.what, held.Advance)
			}
		}
	})

	t.Run("what a tracker has established nothing about it still admits", func(t *testing.T) {
		checkpoints := newRecordingCheckpoints(t)
		checkpoints.write(Checkpoint{Projection: name, Cursor: "900", Advance: 900})
		ahead, err := Track(checkpoints, name)
		if err != nil {
			t.Fatalf("the tracker this case drives was refused with %v", err)
		}
		if held, err := ahead.Load(ctx); err != nil || held.Advance != 900 {
			t.Fatalf("a first load answered advance %d (%v), and a process that restarts has established nothing to compare it against", held.Advance, err)
		}
		if _, err := ahead.Save(ctx, "901", Progress{}); err != nil {
			t.Fatalf("the save after that first load was refused with %v", err)
		}
		if held, err := ahead.Load(ctx); err != nil || held.Advance != 901 {
			t.Fatalf("a load between two saves answered advance %d (%v), and nothing moved between them", held.Advance, err)
		}

		fresh, err := Track(newRecordingCheckpoints(t), name)
		if err != nil {
			t.Fatalf("the tracker over an empty store was refused with %v", err)
		}
		held, err := fresh.Load(ctx)
		if err != nil || !held.Fresh() {
			t.Fatalf("a store holding no row answered %+v (%v), and absence is still a first run at the origin", held, err)
		}
		if _, err := fresh.Save(ctx, "1", Progress{}); err != nil {
			t.Fatalf("the first save of a first run was refused with %v", err)
		}
		if stored := checkpoints.held(name); stored.Advance != 901 {
			t.Fatalf("the two stores are one and this case proves nothing: the first holds %+v", stored)
		}
	})
}

func TestTrackRefusesWhatItCannotHold(t *testing.T) {
	var unset Checkpoints
	var typed *recordingCheckpoints
	var wrapped Checkpoints = typed

	for _, one := range []struct {
		what  string
		store Checkpoints
	}{
		{"an untyped nil", nil},
		{"an interface nobody set", unset},
		{"a typed nil pointer behind the interface", wrapped},
	} {
		if _, err := Track(one.store, "accounts.balances"); !errors.Is(err, ErrWrongStore) {
			t.Fatalf("%s answered %v, and a tracker over nothing is a wiring refusal rather than a panic on the first load", one.what, err)
		}
	}

	for _, one := range []struct {
		what string
		name string
	}{
		{"a name that is empty", ""},
		{"a name over the identifier cap", strings.Repeat("f", MaxNameBytes+1)},
		{"a name carrying a control character", "accounts.\nbalances"},
		{"a name that is not valid UTF-8", "accounts.\xffbalances"},
		{"a name carrying a bracket", "accounts.balances] admin logged in [x"},
	} {
		_, err := Track(newRecordingCheckpoints(t), one.name)
		if !errors.Is(err, ErrDeclaration) {
			t.Fatalf("%s answered %v, and a projection name is a declared identifier held to the rule a family and a wire type name are held to", one.what, err)
		}
		if strings.Contains(err.Error(), one.name) && one.name != "" {
			t.Fatalf("%s rendered the name that broke the rule in %q", one.what, err)
		}
	}

	tracker, err := Track(newRecordingCheckpoints(t), "accounts.balances")
	if err != nil || tracker == nil {
		t.Fatalf("a legal store and a legal name answered %v, so every row above passes against a door that refuses everything", err)
	}
}

// The door decides exactly one thing — what an error the store did not classify
// means — and the safe guess differs between a read, which wrote nothing there
// is anything to be uncertain about, and a write, where guessing "not written"
// is a silent second attempt at a fenced row.
func TestEachCheckpointDoorMapsItsUnclassifiedFailureItsOwnWay(t *testing.T) {
	ctx := context.Background()
	name := "accounts.balances"
	unclassified := errors.New("the connection went away")

	doors := []struct {
		what     string
		fail     func(*recordingCheckpoints, error)
		drive    func(*Tracker) error
		unstated error
	}{
		{"a load", func(c *recordingCheckpoints, err error) { c.failLoad = err },
			func(tracker *Tracker) error { _, err := tracker.Load(ctx); return err }, ErrBackend},
		{"a save", func(c *recordingCheckpoints, err error) { c.failSave = err },
			func(tracker *Tracker) error { _, err := tracker.Save(ctx, "1", Progress{}); return err }, ErrUncertain},
		{"a forget", func(c *recordingCheckpoints, err error) { c.failForget = err },
			func(tracker *Tracker) error { return tracker.Forget(ctx) }, ErrUncertain},
	}

	for _, door := range doors {
		for _, one := range []struct {
			what     string
			answered error
			want     error
		}{
			{"a conflict", Failure(Conflict, nil), ErrConflict},
			{"a store that certainly did not write", Failure(NotWritten, nil), ErrBackend},
			{"a write that was never confirmed", Failure(Unconfirmed, nil), ErrUncertain},
			{"a closed store", Failure(Closed, nil), ErrClosed},
			{"a cursor this store cannot read", Failure(BadCursor, nil), ErrCursor},
			{"a policy refusal", Failure(Refused, errQuota), ErrRefused},
			{"an outcome outside the vocabulary", Failure(Outcome(200), nil), door.unstated},
			{"an error the store did not classify", unclassified, door.unstated},
		} {
			checkpoints := newRecordingCheckpoints(t)
			tracker, err := Track(checkpoints, name)
			if err != nil {
				t.Fatalf("%s: the tracker this row drives was refused with %v", one.what, err)
			}
			if _, err := tracker.Load(ctx); err != nil {
				t.Fatalf("%s: the load every row below a save needs was refused with %v", one.what, err)
			}
			door.fail(checkpoints, one.answered)

			if err := door.drive(tracker); !errors.Is(err, one.want) {
				t.Fatalf("%s from %s reached the caller as %v where %v is what the store selected", one.what, door.what, err, one.want)
			}
		}
	}

	for _, door := range doors {
		checkpoints := newRecordingCheckpoints(t)
		tracker, err := Track(checkpoints, name)
		if err != nil {
			t.Fatalf("the control tracker was refused with %v", err)
		}
		if _, err := tracker.Load(ctx); err != nil {
			t.Fatalf("the control's load was refused with %v", err)
		}
		if err := door.drive(tracker); err != nil {
			t.Fatalf("%s against an honest store answered %v, so every row above passes against a door that refuses everything", door.what, err)
		}
	}

	checkpoints := newRecordingCheckpoints(t)
	tracker, err := Track(checkpoints, name)
	if err != nil {
		t.Fatalf("the retryable row's tracker was refused with %v", err)
	}
	checkpoints.failLoad = fmt.Errorf("%w: the connection went away", crud.ErrUnavailable)
	if _, err := tracker.Load(ctx); !errors.Is(err, crud.ErrUnavailable) {
		t.Fatalf("a retryable cause the store did not classify reached the caller as %v, and a loop keyed on retryability stops retrying a failure that would have cleared", err)
	}
	if _, err := tracker.Load(ctx); errors.Is(err, ErrUncertain) {
		t.Fatalf("a read that wrote nothing reached the caller as %v, so a consumer takes a write-side recovery for a call that wrote nothing", err)
	}
}
