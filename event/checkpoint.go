package event

import (
	"context"
	"fmt"
	"time"
)

// Written at one point and one only: the save that carries it. So it describes a
// page that was applied or quarantined, never one that was merely delivered, and
// a consumer retrying or halted on the page it just read publishes what it had
// before that page.
type Progress struct {
	// The highest position of a page a consumer finished with, and a completeness
	// watermark rather than a number off the last envelope: once this is saved at
	// P, a read resumed from the cursor beside it answers only positions above P,
	// and no event at or below P is ever delivered to this projection for the
	// first time. It resumes nothing, and it means this much only because the log
	// delivers in position order for every store ([[D-128]]) — read off a store
	// ordering by anything else it would be the page's last position and no
	// promise at all.
	//
	// Delivered, and then applied or recorded elsewhere. At least once, so an
	// event at or below P may certainly arrive again.
	Highest Position

	Applied uint64

	// How many envelopes went somewhere other than the destination. Non-zero
	// means that destination has holes, which is what keeps Highest from reading
	// as a completeness claim.
	Quarantined uint64

	// When the consumer finished the page this checkpoint accounts for. It is the
	// consumer's own observation and a store answers what it was handed: a second
	// clock in the row is an instant nobody observed, and an operator reading it
	// after a restart would be told a halted projection is current. A store keeps
	// it at whatever grain its own backing holds one at — no grain is named here,
	// because a bound this kernel invented would refuse every backing but one.
	At time.Time
}

// Field by field and never with ==, because time.Time's equality carries a
// monotonic reading and a *Location: a store answering a zero instant in another
// location would be refused for a difference that is not one.
func (this Progress) zero() bool {
	return this.Highest == 0 && this.Applied == 0 && this.Quarantined == 0 && this.At.IsZero()
}

// The resume authority is Cursor. Advance is a fence and not a position;
// Progress is an observation and not a resume point.
type Checkpoint struct {
	Projection string
	Cursor     Cursor
	Advance    uint64
	Progress   Progress
}

func (this Checkpoint) Fresh() bool { return this.Advance == 0 }

type CheckpointCapabilities struct {
	Transactions Support
	Persistence  Support
}

// Seven methods, and the shape is Store's on purpose. Save admits a checkpoint
// if and only if the stored advance is one below the one presented, or there is
// no row and the presented advance is one; a losing save is Failure(Conflict,
// ...) and never a read-then-write. Load answers the zero Checkpoint when there
// is none, and Fresh says so. Save refuses an empty cursor with
// Failure(Refused, ...): the empty cursor is the origin of a log, so a row
// carrying one at a non-zero advance is a readable checkpoint that restarts a
// consumer at the beginning. Forget removes a name and is not fenced. None of
// the seven opens, commits or rolls back anything, and a store classifies its
// own failure through the same seven outcomes a Store does.
type Checkpoints interface {
	Capabilities() CheckpointCapabilities
	Backing() Backing
	Transaction(ctx context.Context) (Authority, error)

	Load(ctx context.Context, projection string) (Checkpoint, error)
	Save(ctx context.Context, checkpoint Checkpoint) error
	Forget(ctx context.Context, projection string) error
	Close() error
}

// The one door onto a Checkpoints, as Read is onto a Log: it holds the name,
// owns the fence and re-checks every answer. Nothing calls a Checkpoints raw,
// so a second consumer inherits the re-checks rather than re-deriving them.
func Track(checkpoints Checkpoints, projection string) (*Tracker, error) {
	if nilByAnyRoute(checkpoints) {
		return nil, fmt.Errorf("%w: a tracker records through a checkpoint store and this call names none", ErrWrongStore)
	}
	if broken := checkName(projection); broken != "" {
		return nil, fmt.Errorf("%w: a projection name %s", ErrDeclaration, broken)
	}
	return &Tracker{checkpoints: checkpoints, projection: projection}, nil
}

// One goroutine at a time, like the Reader: it holds the advance its own Load
// answered and presents one above it.
//
// What it holds is a window rather than that one number, because the two ends
// answer different questions. advance is the fence — a save presents one above
// it — and floor is the lowest advance the row can still be at, which is the
// last one this tracker watched a store commit for itself. They part when a save
// leaves the row in one of two states: an unresolved one, where the store did
// not say whether it wrote, and a staged one, where it wrote inside a
// transaction this tracker does not commit and the caller may still roll back.
type Tracker struct {
	checkpoints Checkpoints
	projection  string
	floor       uint64
	advance     uint64
	loaded      bool
	unresolved  bool
}

func (this *Tracker) Projection() string { return this.projection }

func (this *Tracker) Capabilities() CheckpointCapabilities { return this.checkpoints.Capabilities() }

func (this *Tracker) Backing() Backing { return this.checkpoints.Backing() }

func (this *Tracker) Transaction(ctx context.Context) (Authority, error) {
	authority, err := this.checkpoints.Transaction(ctx)
	if err != nil {
		return Authority{}, refuseTransaction(err)
	}
	return authority, nil
}

// The read door, because nothing was written and an unclassified failure here is
// a backend that failed rather than a write whose fate is unknown. On any
// refusal the fence does not move and nothing is written over the row: an
// operator's only repair is the row that is there.
func (this *Tracker) Load(ctx context.Context) (Checkpoint, error) {
	held, err := this.checkpoints.Load(ctx, this.projection)
	if err != nil {
		return Checkpoint{}, refuseRead(err)
	}
	if err := this.admit(held); err != nil {
		return Checkpoint{}, err
	}
	this.floor, this.advance, this.loaded, this.unresolved = held.Advance, held.Advance, true, false
	return held, nil
}

// Five re-checks, each ErrWrongStore, and the order is fixed.
//
// Absence is total: a row that is half-absent is not a fresh start with some
// extra fields, it is a store that did not answer the question.
//
// Presence is total in the one field that resumes: the empty cursor IS the
// origin of the log, refused by nothing else on any path, so a row at a non-zero
// advance carrying one resumes a consumer at the beginning of the log against a
// live destination with no error anywhere. It is not total in the other three —
// Progress is an observation nobody resumes from, and a Highest at a non-zero
// advance is a number this door has no independent account of, so refusing a
// store over either would refuse correct stores.
//
// The answer is about the name that was asked for: a Load that ignores its
// argument, or whose statement is mis-parameterised, answers another
// projection's well-formed cursor — of the right log, in the right format,
// refused by nothing — and the walk resumes hundreds of thousands of positions
// ahead, skipping everything in between, forever.
//
// The fifth is about this tracker rather than about the answer's shape, and it
// runs last for that reason: the four above name a defect an operator can read
// off the row, and this one names a row that moved while a tracker was live.
func (this *Tracker) admit(held Checkpoint) error {
	if held.Advance == 0 {
		if held.Cursor != "" || held.Projection != "" || !held.Progress.zero() {
			return fmt.Errorf("%w: %q was answered no advance beside a cursor, a name or a progress, and a row that is half-absent is not a fresh start",
				ErrWrongStore, this.projection)
		}
		return this.established(held.Advance)
	}
	if held.Cursor == "" {
		return fmt.Errorf("%w: %q was answered advance %d with no cursor, and the empty cursor is the origin of the log rather than a point to resume past",
			ErrWrongStore, this.projection, held.Advance)
	}
	if held.Projection != this.projection {
		return fmt.Errorf("%w: %q was asked for and this checkpoint store answered another name's row", ErrWrongStore, this.projection)
	}
	if len(held.Cursor) > MaxCursorBytes {
		return fmt.Errorf("%w: %q was answered a cursor of %d bytes where the kernel publishes a ceiling of %d",
			ErrWrongStore, this.projection, len(held.Cursor), MaxCursorBytes)
	}
	return this.established(held.Advance)
}

// A tracker that has never loaded has established nothing and takes whatever the
// store holds — a process that restarts is the ordinary way a projection begins.
// Afterwards the answer must be inside the window, and a load that re-seated the
// fence outside it would go on writing over a row that moved under a live
// tracker. Below the floor is the row retired, reset or restored from a dump,
// and the next save lands at advance 1 over a live read model — a fresh row and
// a walk that resumes from the origin, with no error on any path. Above the
// fence is a second writer at the same name, and taking its advance is what
// makes two processes take turns over one checkpoint, each re-reading the
// other's number and saving one above it. Neither is reached by any other
// refusal: the store's own fence admits both, because both present exactly one
// above what the row holds.
func (this *Tracker) established(advance uint64) error {
	ceiling := this.advance
	if this.unresolved {
		ceiling++
	}
	if !this.loaded || (advance >= this.floor && advance <= ceiling) {
		return nil
	}
	return fmt.Errorf("%w: %q is fenced between advance %d and %d and this checkpoint store answered %d, and a row that moved under a live tracker is not one it can go on writing",
		ErrWrongStore, this.projection, this.floor, ceiling, advance)
}

// Whether the save just issued was the whole of the write. A checkpoint store on
// a caller's transaction stages one the caller commits or rolls back, so the row
// can still go back to what it held before; a store on nothing has committed it
// and the row cannot. The question is the one Transaction exists to answer, it
// issues no statement, and guessing it wrong in the cautious direction only
// widens the window.
func (this *Tracker) autocommits(ctx context.Context) bool {
	authority, err := this.checkpoints.Transaction(ctx)
	return err == nil && !authority.Valid()
}

// The fence is the door's: the advance presented is the one this tracker loaded
// plus one, so no caller can present another and Checkpoint.Advance is a field
// the store reads rather than one a caller computes. It is also the number this
// answers, whatever the store said, because a caller that re-derived it would
// hold a second copy of one fence — and two copies part the moment a pass issues
// two saves, after which the settlement that reads the row is decided on a number
// no store ever saw. Zero is the answer to a refusal made before an advance was
// presented at all.
//
// The append door, because a save that was issued and never confirmed is a write
// whose fate is unknown — and that is what the two ends of the window record. The
// fence moves when a save lands; the floor follows it only for a save the store
// committed itself; and a save the store never confirmed moves neither, opens the
// window by one and is settled by a load rather than by a second save, which
// could only guess which of the two advances the row holds.
func (this *Tracker) Save(ctx context.Context, cursor Cursor, progress Progress) (uint64, error) {
	if !this.loaded {
		return 0, fmt.Errorf("%w: %q presented a checkpoint before it loaded one, and a save before a load is a loop assembled wrong", ErrWrongStore, this.projection)
	}
	if this.unresolved {
		return 0, fmt.Errorf("%w: %q presented a checkpoint over a save the store never confirmed, and one load settles which of the two advances the row holds where a second save guesses at it", ErrWrongStore, this.projection)
	}
	if cursor == "" {
		return 0, fmt.Errorf("%w: %q presented the empty cursor, which is the origin of the log rather than a point to resume past", ErrWrongStore, this.projection)
	}
	if len(cursor) > MaxCursorBytes {
		return 0, tooLarge("the cursor this checkpoint carries", len(cursor), MaxCursorBytes)
	}
	presented := Checkpoint{Projection: this.projection, Cursor: cursor, Advance: this.advance + 1, Progress: progress}
	err := refuseAppend(this.checkpoints.Save(ctx, presented))
	switch {
	case err == nil:
		this.advance = presented.Advance
		if this.autocommits(ctx) {
			this.floor = presented.Advance
		}
	case matches(err, ErrUncertain), matches(err, context.Canceled), matches(err, context.DeadlineExceeded):
		this.unresolved = true
	}
	return presented.Advance, err
}

// It does not reset the fence, and neither does the load that follows it: a
// running consumer that forgets its own name finds no row at its next advance,
// is refused ErrConflict and stops, and a consumer that re-reads first is
// refused the absence at the door. Both are the correct and visible outcome of
// retiring a live one, and either of them re-seating the fence would turn it
// into a silent restart at the origin.
func (this *Tracker) Forget(ctx context.Context) error {
	return refuseAppend(this.checkpoints.Forget(ctx, this.projection))
}
