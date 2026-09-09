package eventtest_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventtest"
)

// The checkpoint store this package's own tests are run against, built out of
// the exported vocabulary alone — the same compile-time obligation the store
// fixtures carry, and the reason it is written here rather than borrowed from
// eventmemory: a conformance suite whose only subject is one shipped
// implementation certifies that implementation and nothing else.

var (
	errCheckpointMoved    = errors.New("eventtest_test: the row is not at the advance this save follows")
	errCheckpointRefused  = errors.New("eventtest_test: this fixture refuses to write what it was handed")
	errCheckpointFinished = errors.New("eventtest_test: this transaction has already been committed or rolled back")
)

type checkpointRows struct {
	mutex sync.Mutex
	kept  map[string]event.Checkpoint
}

type checkpointStore struct {
	rows     *checkpointRows
	backing  event.Backing
	persists bool
	closed   atomic.Bool
}

type checkpointTx struct {
	rows   *checkpointRows
	staged []event.Checkpoint
	done   atomic.Bool
}

type checkpointKey struct{ rows *checkpointRows }

func newCheckpointStore(t *testing.T, persists bool) *checkpointStore {
	t.Helper()
	rows := &checkpointRows{kept: map[string]event.Checkpoint{}}
	backing, err := event.NewBacking(rows)
	if err != nil {
		t.Fatalf("the fixture cannot name what it writes to: %v", err)
	}
	return &checkpointStore{rows: rows, backing: backing, persists: persists}
}

func (this *checkpointStore) beside() *checkpointStore {
	return &checkpointStore{rows: this.rows, backing: this.backing, persists: this.persists}
}

func (this *checkpointStore) Capabilities() event.CheckpointCapabilities {
	return event.CheckpointCapabilities{Transactions: event.Supported, Persistence: support(this.persists)}
}

func (this *checkpointStore) Backing() event.Backing { return this.backing }

func (this *checkpointStore) Transaction(ctx context.Context) (event.Authority, error) {
	tx := this.inside(ctx)
	if tx == nil {
		return event.Authority{}, nil
	}
	if tx.done.Load() {
		return event.Authority{}, errCheckpointFinished
	}
	return event.NewAuthority(this.backing, tx)
}

func (this *checkpointStore) inside(ctx context.Context) *checkpointTx {
	tx, _ := ctx.Value(checkpointKey{rows: this.rows}).(*checkpointTx)
	return tx
}

func (this *checkpointStore) ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if this.closed.Load() {
		return event.Failure(event.Closed, nil)
	}
	return nil
}

func (this *checkpointStore) Load(ctx context.Context, projection string) (event.Checkpoint, error) {
	if err := this.ready(ctx); err != nil {
		return event.Checkpoint{}, err
	}
	this.rows.mutex.Lock()
	defer this.rows.mutex.Unlock()
	return this.held(projection, this.inside(ctx)), nil
}

func (this *checkpointStore) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	if err := this.ready(ctx); err != nil {
		return err
	}
	if checkpoint.Cursor == "" || len(checkpoint.Cursor) > event.MaxCursorBytes || checkpoint.Projection == "" {
		return event.Failure(event.Refused, errCheckpointRefused)
	}
	this.rows.mutex.Lock()
	defer this.rows.mutex.Unlock()
	tx := this.inside(ctx)
	if checkpoint.Advance != this.held(checkpoint.Projection, tx).Advance+1 {
		return event.Failure(event.Conflict, errCheckpointMoved)
	}
	if tx == nil {
		this.rows.kept[checkpoint.Projection] = checkpoint
		return nil
	}
	tx.staged = append(tx.staged, checkpoint)
	return nil
}

func (this *checkpointStore) Forget(ctx context.Context, projection string) error {
	if err := this.ready(ctx); err != nil {
		return err
	}
	this.rows.mutex.Lock()
	defer this.rows.mutex.Unlock()
	if tx := this.inside(ctx); tx != nil {
		tx.staged = append(tx.staged, event.Checkpoint{Projection: projection})
		return nil
	}
	delete(this.rows.kept, projection)
	return nil
}

func (this *checkpointStore) Close() error {
	this.closed.Store(true)
	return nil
}

// A save at advance zero is how a staged Forget travels, so one staging area
// carries both and a rolled-back unit discards them alike. What a staged removal
// would leave behind is no row rather than that entry, because absence is total.
func (this *checkpointStore) held(projection string, tx *checkpointTx) event.Checkpoint {
	if tx != nil {
		for index := len(tx.staged) - 1; index >= 0; index-- {
			if tx.staged[index].Projection != projection {
				continue
			}
			if tx.staged[index].Advance == 0 {
				return event.Checkpoint{}
			}
			return tx.staged[index]
		}
	}
	return this.rows.kept[projection]
}

func (this *checkpointTx) Commit(context.Context) error {
	this.rows.mutex.Lock()
	defer this.rows.mutex.Unlock()
	if this.done.Load() {
		return errCheckpointFinished
	}
	for _, held := range this.staged {
		if held.Advance == 0 {
			delete(this.rows.kept, held.Projection)
			continue
		}
		this.rows.kept[held.Projection] = held
	}
	this.staged = nil
	this.done.Store(true)
	return nil
}

func (this *checkpointTx) Rollback(context.Context) error {
	this.rows.mutex.Lock()
	defer this.rows.mutex.Unlock()
	this.staged = nil
	this.done.Store(true)
	return nil
}

var minted atomic.Uint64

func checkpointFactory(persists bool, over func(event.Checkpoints) event.Checkpoints) eventtest.CheckpointFactory {
	built := &checkpointsBuilt{over: over, of: map[event.Checkpoints]*checkpointStore{}}
	return eventtest.CheckpointFactory{
		New: func(t *testing.T) event.Checkpoints {
			return built.wrap(newCheckpointStore(t, persists))
		},
		Begin: func(t *testing.T, ctx context.Context, c event.Checkpoints) (context.Context, eventtest.Tx) {
			store := built.held(t, c)
			tx := &checkpointTx{rows: store.rows}
			return context.WithValue(ctx, checkpointKey{rows: store.rows}, tx), tx
		},
		Sibling: func(t *testing.T, c event.Checkpoints) event.Checkpoints {
			return built.wrap(built.held(t, c).beside())
		},
		Cursor: func(*testing.T) event.Cursor {
			return event.Cursor("eventtest_test:" + strconv.FormatUint(minted.Add(1), 10))
		},
	}
}

// The decorator a defect wraps a store in makes the value the suite hands back
// something other than the value this factory built, so the factory keeps the
// pairing rather than type-asserting through a wrapper it does not know.
type checkpointsBuilt struct {
	over  func(event.Checkpoints) event.Checkpoints
	mutex sync.Mutex
	of    map[event.Checkpoints]*checkpointStore
}

func (this *checkpointsBuilt) wrap(store *checkpointStore) event.Checkpoints {
	held := event.Checkpoints(store)
	if this.over != nil {
		held = this.over(store)
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.of[held] = store
	return held
}

func (this *checkpointsBuilt) held(t *testing.T, c event.Checkpoints) *checkpointStore {
	t.Helper()
	this.mutex.Lock()
	defer this.mutex.Unlock()
	store, built := this.of[c]
	if !built {
		t.Fatalf("the suite handed back a checkpoint store this factory did not build: %T", c)
	}
	return store
}

// A conformance suite is the one artifact whose failure mode is silent by
// construction: one that tests nothing passes everything. This runs every defect
// the checkpoint suite was built to detect against the section named for it and
// fails when that section passes, with the same store minus the defect as the
// control.
func TestEveryCheckpointDefectIsReportedByItsOwnSection(t *testing.T) {
	sections := map[string]bool{}
	for _, name := range eventtest.CheckpointSectionNames() {
		sections[name] = true
	}
	held := eventtest.CheckpointDefects()
	if len(held) != checkpointDefects {
		t.Fatalf("the checkpoint suite carries %d defects where its inventory is %d rows", len(held), checkpointDefects)
	}
	named := map[string]bool{}
	for _, defect := range held {
		if named[defect.Name] {
			t.Fatalf("two checkpoint defects are named %q, so one of them is proving the other's section", defect.Name)
		}
		named[defect.Name] = true
		if !sections[defect.Section] {
			t.Fatalf("the defect %q names the %s section, which this suite does not have", defect.Name, defect.Section)
		}
		if word := oneCheckpointVerdict(t, checkpointFactory(true, defect.Over), defect.Section); word != "failed" {
			t.Errorf("the suite no longer detects a checkpoint store that %s: its %s section was reported %q", defect.Name, defect.Section, word)
		}
		if word := oneCheckpointVerdict(t, checkpointFactory(true, nil), defect.Section); word != "passed" {
			t.Errorf("the same store without the defect that %s was reported %q for the %s section, so the failure above is not the defect's", defect.Name, word, defect.Section)
		}
	}
}

const checkpointDefects = 5

func oneCheckpointVerdict(t *testing.T, factory eventtest.CheckpointFactory, section string) string {
	t.Helper()
	verdicts := eventtest.CertifyCheckpoints(t, factory, section)
	if len(verdicts) != 1 {
		t.Fatalf("running the %s section alone reported %d verdicts", section, len(verdicts))
	}
	return verdicts[0].Word
}

// A store that keeps nothing past its process declines the one section that is
// about surviving one, and certifies the other eleven — which is the opposite of
// what an ungated durability section would have reported for it. The control is
// the same fixture claiming persistence, which certifies twelve: without it this
// test passes against a suite that refuses everything.
func TestAStoreThatDoesNotPersistDeclinesDurabilityAndCertifiesTheOtherEleven(t *testing.T) {
	verdicts := eventtest.CertifyCheckpoints(t, checkpointFactory(false, nil))
	if len(verdicts) != len(eventtest.CheckpointSectionNames()) {
		t.Fatalf("a whole run reported %d verdicts where the suite has %d sections", len(verdicts), len(eventtest.CheckpointSectionNames()))
	}
	for _, given := range verdicts {
		if given.Section != "durability" {
			if given.Word != "passed" {
				t.Errorf("the %s section was reported %q for a store that satisfies the contract: %s", given.Section, given.Word, given.Reason)
			}
			continue
		}
		if given.Word != "not certified" {
			t.Errorf("the durability section was reported %q for a store nothing of which survives its process", given.Word)
		}
		if !strings.Contains(given.Reason, "does not claim persistence") {
			t.Errorf("the durability section was declined with the reason %q, which does not say what was not asked", given.Reason)
		}
	}
	if certified := eventtest.Certified(verdicts); certified != 11 {
		t.Fatalf("a store that declines one section of twelve certified %d", certified)
	}
	if certified := eventtest.Certified(eventtest.CertifyCheckpoints(t, checkpointFactory(true, nil))); certified != 12 {
		t.Fatalf("the same fixture claiming persistence certified %d of twelve, so the count above is not the declined section's", certified)
	}
}

// The instant a row keeps is the width of whatever column keeps it, and
// microsecond is PostgreSQL's timestamptz and nothing else's. A store over a
// coarser one declares its grain and is measured on the twelve properties it has;
// the same store declaring nothing is measured against an exactness it never
// claimed and is reported broken on most of them, which is what the declaration
// buys. The control is the fixture at the finest grain of all, which certifies
// twelve with no declaration at all — so the failure below is the column's and
// not the suite's.
func TestACoarserInstantIsCertifiedWhenTheFactoryDeclaresItsGrainAndNotWhenItDoesNot(t *testing.T) {
	for _, grain := range []time.Duration{time.Millisecond, time.Second} {
		t.Run(grain.String(), func(t *testing.T) {
			declared := checkpointFactory(true, roundsTheInstant(grain))
			declared.Instant = func(minted time.Time) time.Time { return minted.Truncate(grain) }
			if certified := eventtest.Certified(eventtest.CertifyCheckpoints(t, declared)); certified != 12 {
				t.Errorf("a store whose instant is a %v and whose factory says so certified %d of twelve, so a backing this framework does not ship is reported broken on properties it does not get wrong", grain, certified)
			}
			undeclared := checkpointFactory(true, roundsTheInstant(grain))
			if certified := eventtest.Certified(eventtest.CertifyCheckpoints(t, undeclared)); certified == 12 {
				t.Errorf("the same store declaring no grain certified twelve, so the suite no longer asks whether the instant it handed in is the one it gets back")
			}
		})
	}
	if certified := eventtest.Certified(eventtest.CertifyCheckpoints(t, checkpointFactory(true, nil))); certified != 12 {
		t.Fatalf("the fixture that keeps the instant it was handed certified %d of twelve with no grain declared, so the counts above are not the suite's own", certified)
	}
}

// A column that holds an instant to a coarser grain than a Go nanosecond, which
// is every column that holds one.
func roundsTheInstant(grain time.Duration) func(event.Checkpoints) event.Checkpoints {
	return func(held event.Checkpoints) event.Checkpoints {
		return rounding{Checkpoints: held, grain: grain}
	}
}

type rounding struct {
	event.Checkpoints
	grain time.Duration
}

func (this rounding) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	checkpoint.Progress.At = checkpoint.Progress.At.Truncate(this.grain)
	return this.Checkpoints.Save(ctx, checkpoint)
}

// The admission rules are a t.Fatal before any section runs, so the only place
// they can be watched from is one process out: a failure anywhere under a T
// fails the T that started it. The child runs the case the environment names and
// the parent reads the exit status, what was said, and — the half the rule is
// for — that no section reported a verdict at all.
const checkpointAdmissionCase = "EVENTTEST_CHECKPOINT_ADMISSION_CASE"

func TestACheckpointFactoryClaimingPersistenceWithNoSiblingFailsBeforeASectionRuns(t *testing.T) {
	if name := os.Getenv(checkpointAdmissionCase); name != "" {
		eventtest.RunCheckpoints(t, checkpointAdmission(t, name))
		return
	}
	for _, one := range []struct {
		what string
		says string
	}{
		{"a store that claims persistence and a factory with no second value", "Factory.Sibling"},
		{"a store that states nothing about its transactions", "states nothing about Transactions"},
		{"a factory that answers no cursor of its log", eventtest.MintsNoCursor},
		{"a factory that answers one cursor to two calls", eventtest.RepeatsItsCursor},
		{"a factory whose grain invents an instant rather than rounding one", eventtest.InventsItsInstant},
		{"a factory whose grain is coarser than a second", eventtest.CollapsesItsInstant},
	} {
		t.Run(one.what, func(t *testing.T) {
			output, code := ranTheAdmissionCase(t, one.what)
			if code == 0 {
				t.Fatalf("a run over %s exited 0, and a store that claims a capability and then avoids being tested on it is what the section it would skip exists to catch:\n%s", one.what, output)
			}
			if !strings.Contains(output, one.says) {
				t.Errorf("a run over %s never named %q, so an implementer is told a run failed and not which hook is missing:\n%s", one.what, one.says, output)
			}
			for _, section := range eventtest.CheckpointSectionNames() {
				if strings.Contains(output, "eventtest: "+section+": ") {
					t.Errorf("a run over %s reported a verdict for the %s section, so the refusal is not before any section runs:\n%s", one.what, section, output)
				}
			}
		})
	}

	t.Run("the honest factory certifies twelve", func(t *testing.T) {
		if certified := eventtest.Certified(eventtest.CertifyCheckpoints(t, checkpointFactory(true, nil))); certified != 12 {
			t.Fatalf("the factory claiming persistence with a Sibling certified %d of twelve, so the refusals above are not the missing hooks'", certified)
		}
	})
}

func checkpointAdmission(t *testing.T, what string) eventtest.CheckpointFactory {
	t.Helper()
	factory := checkpointFactory(true, nil)
	switch what {
	case "a store that claims persistence and a factory with no second value":
		factory.Sibling = nil
	case "a store that states nothing about its transactions":
		factory.New = func(t *testing.T) event.Checkpoints {
			return unstatedCheckpoints{newCheckpointStore(t, true)}
		}
	case "a factory that answers no cursor of its log":
		factory.Cursor = nil
	case "a factory that answers one cursor to two calls":
		factory.Cursor = func(*testing.T) event.Cursor { return "eventtest_test:one" }
	case "a factory whose grain invents an instant rather than rounding one":
		factory.Instant = func(minted time.Time) time.Time { return minted.Add(time.Millisecond) }
	case "a factory whose grain is coarser than a second":
		factory.Instant = func(minted time.Time) time.Time { return minted.Truncate(time.Hour) }
	default:
		t.Fatalf("%q names no admission case of this test", what)
	}
	return factory
}

type unstatedCheckpoints struct{ *checkpointStore }

func (this unstatedCheckpoints) Capabilities() event.CheckpointCapabilities {
	return event.CheckpointCapabilities{Persistence: event.Unsupported}
}

func ranTheAdmissionCase(t *testing.T, what string) (string, int) {
	t.Helper()
	child := exec.Command(os.Args[0], "-test.run", "^TestACheckpointFactoryClaimingPersistenceWithNoSiblingFailsBeforeASectionRuns$", "-test.v", "-test.count=1")
	child.Env = append(os.Environ(), checkpointAdmissionCase+"="+what)
	output, err := child.CombinedOutput()
	if err == nil {
		return string(output), 0
	}
	exit, is := err.(*exec.ExitError)
	if !is {
		t.Fatalf("this binary could not be run as a child of itself: %v\n%s", err, output)
	}
	return string(output), exit.ExitCode()
}
