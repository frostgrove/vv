package projection_test

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/projection"
)

// A target a caller can write is evidence it invented, which is the door Cutover
// closes by having no barrier field. A wait cannot close it that way — the caller
// is the only party that knows which change it is waiting for — so the value is
// minted instead, by two doors and no third, and the zero one is refused where it
// would otherwise read as position zero and clear every wait at once.
//
// The cross-spec arm is the second half of the same rule and is the one a
// deployment reaches with every value of the right type: a mark carries the
// sequence keys ONE projection's sequencer answered, so the position reaching
// says nothing about the queue of a second projection asked under them.
func TestAMarkIsMintedOnlyFromANumberAStoreProduced(t *testing.T) {
	t.Run("no exported field, no third door, and nothing of the log in its rendering", func(t *testing.T) {
		mark := reflect.TypeFor[projection.Mark]()
		for index := range mark.NumField() {
			if field := mark.Field(index); field.IsExported() {
				t.Errorf("Mark publishes %s, and a caller that can fill in a field of a mark is waiting for a number it wrote itself", field.Name)
			}
		}
		if doors := mintingDoors(t); !slices.Equal(doors, []string{"MarkOf", "WaitSpec.Committed"}) {
			t.Fatalf("%v answer a Mark, and the two doors are MarkOf and WaitSpec.Committed — a third is a number that came from somewhere other than a store", doors)
		}

		minted := projection.MarkOf(projection.Barrier{Projection: "orders", Generation: 2, At: 41719})
		if rendered := fmt.Sprintf("%v", minted); rendered != "[mark]" {
			t.Fatalf("a mark renders %q, where Backing and Authority render their own name and nothing of what they hold", rendered)
		}
		if rendered := fmt.Sprintf("%+v %s", minted, minted); strings.Contains(rendered, "41719") {
			t.Fatalf("a mark rendered the position it holds into %q, and the position of a change is a number an operator's log would let anybody replay a history from", rendered)
		}
		if minted.At() != 41719 || minted.Zero() {
			t.Fatal("a mark minted from a non-zero barrier answers another position or reports itself unminted")
		}
		if !(projection.Mark{}).Zero() || (projection.Mark{}).At() != 0 {
			t.Fatal("the zero Mark does not report itself unminted, and it is the one value a caller can build")
		}
	})

	t.Run("the zero mark is refused at Wait's door and reaches no store", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		of := identityOf(t, "orders", 2, projection.Whole())
		cover := coverOf(t, projection.Whole())
		recorded(t, stand, of, event.Progress{Highest: 90})

		spec := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: stand.points}, cover)
		vis, err := projection.Wait(context.Background(), spec)
		if !errors.Is(err, projection.ErrSpec) {
			t.Fatalf("a wait with the zero Mark answered %v, where a position is drawn from a sequence starting at one and zero is exactly a mark nobody minted", err)
		}
		if vis != (projection.Visibility{}) {
			t.Fatalf("a refusal at the door answered %+v, where no poll was made", vis)
		}
		if loads := stand.points.loads.Load(); loads != 0 {
			t.Fatalf("the zero mark cost %d checkpoint reads, and a refusal at the door is made before any store call", loads)
		}

		spec.Until = projection.MarkOf(observedOver(t, stand.checkpoints, of, cover))
		reaches(t, spec)
	})

	t.Run("a mark minted from one projection is refused on another, at both doors", func(t *testing.T) {
		stand := newStand(t, eventmemory.Spec{})
		repo := waitedRepo(t, stand.store)
		commit := committing(t, repo, "a-17", "one")

		orders := identityOf(t, "orders", 2, projection.Whole())
		invoices := identityOf(t, "invoices", 2, projection.Whole())
		cover := coverOf(t, projection.Whole())
		recorded(t, stand, orders, event.Progress{Highest: 90})
		recorded(t, stand, invoices, event.Progress{Highest: 90})

		over := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: stand.points}, cover)
		beside := waitingOver(t, projection.Spec{Name: "invoices", Generation: 2, Checkpoints: stand.points}, cover)

		mark, err := over.Committed(context.Background(), stand.store, commit)
		if err != nil {
			t.Fatalf("minting a mark from a committed append answered %v", err)
		}
		folded := projection.MarkOf(observedOver(t, stand.checkpoints, orders, cover))

		for _, one := range []struct {
			what string
			mark projection.Mark
		}{
			{"a mark minted through Committed", mark},
			{"a mark folded out of a barrier", folded},
		} {
			before := stand.points.loads.Load()
			beside.Until = one.mark
			if _, err := projection.Wait(context.Background(), beside); !errors.Is(err, projection.ErrSpec) {
				t.Fatalf("%s of another projection was answered %v on this one, where the position is global and would reach while the park is asked under a key no letter of this projection was ever written under", one.what, err)
			}
			if spent := stand.points.loads.Load() - before; spent != 0 {
				t.Fatalf("%s of another projection cost %d checkpoint reads, and the refusal is at the door", one.what, spent)
			}

			over.Until = one.mark
			reaches(t, over)
		}
	})
}

// The control for the cross-spec refusal: everything else about that wiring
// reaches, so the refusal is about the projection the mark names and not about a
// wait that refuses whatever it is given.
func reaches(t *testing.T, spec projection.WaitSpec) {
	t.Helper()
	vis, err := projection.Wait(context.Background(), spec)
	if err != nil || !vis.Reached {
		t.Fatalf("the same mark on the spec that minted it answered %+v and %v, where the rows are past it", vis, err)
	}
}

// Every exported function and method of this package whose result carries a
// Mark, rendered as a caller writes it. A third one is what the compile-level
// half of INV-108 exists to report: a door nobody argued for is a number that
// came from somewhere other than a store.
func mintingDoors(t *testing.T) []string {
	t.Helper()
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("reading this package's own files answered %v", err)
	}
	var doors []string
	walked := 0
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		walked++
		held, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("reading %s answered %v", source, err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), source, held, 0)
		if err != nil {
			t.Fatalf("parsing %s answered %v", source, err)
		}
		for _, declaration := range parsed.Decls {
			function, is := declaration.(*ast.FuncDecl)
			if !is || !function.Name.IsExported() || !answersAMark(function.Type.Results) {
				continue
			}
			doors = append(doors, renderedDoor(function))
		}
	}
	if walked < 20 {
		t.Fatalf("%d files of this package were parsed, so this read the wrong directory", walked)
	}
	slices.Sort(doors)
	return doors
}

func answersAMark(results *ast.FieldList) bool {
	if results == nil {
		return false
	}
	for _, field := range results.List {
		if named, is := field.Type.(*ast.Ident); is && named.Name == "Mark" {
			return true
		}
	}
	return false
}

func renderedDoor(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return function.Name.Name
	}
	held := function.Recv.List[0].Type
	if starred, is := held.(*ast.StarExpr); is {
		held = starred.X
	}
	named, is := held.(*ast.Ident)
	if !is {
		return function.Name.Name
	}
	return named.Name + "." + function.Name.Name
}

// A Sequencer is any total function of an envelope, so one commit can belong to
// several sequences: a mark that carried only the last envelope's key would ask
// the park about one third of what the caller is waiting for and report the rest
// applied. The keys are computed once, at the mint, and the envelopes are not
// retained — there is nothing here to run an application's sequencer on at every
// poll.
func TestCommittedReadsTheCommitsOwnRangeAndNothingElse(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	repo := waitedRepo(t, stand.store)
	of := identityOf(t, "orders", 2, projection.Whole())
	cover := coverOf(t, projection.Whole())
	ctx := context.Background()

	// A stream that already holds three events before the commit and one after
	// it, so a mint that read the whole stream rather than the commit's own range
	// answers a different set of keys and a different position.
	committing(t, repo, "a-17", "before", "before", "before")
	commit := committing(t, repo, "a-17", "A", "B", "A")
	committing(t, repo, "a-17", "after")

	watched := watchingStore(stand.store)
	spec := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: stand.points, Sequence: byTag()}, cover)
	mark, err := spec.Committed(ctx, watched, commit)
	if err != nil {
		t.Fatalf("minting a mark from a committed append answered %v", err)
	}
	if reads := watched.reads.Load(); reads != 1 {
		t.Fatalf("a commit of %d events that fits one page cost %d reads, where the range is one ReadStream and one more per page beyond it", commit.Count(), reads)
	}
	at := positionOf(t, stand.store, commit)
	if mark.At() != at {
		t.Fatalf("the mark is at %d where the commit's last event went in at %d", mark.At(), at)
	}

	t.Run("one more read per page beyond the first, and no read of the rest of the stream", func(t *testing.T) {
		paged := watchingStore(stand.store)
		paged.page = 2
		beside, err := spec.Committed(ctx, paged, commit)
		if err != nil {
			t.Fatalf("minting a mark over a two-envelope page answered %v", err)
		}
		if reads := paged.reads.Load(); reads != 2 {
			t.Fatalf("a commit of three events over pages of two cost %d reads, where two pages are two reads and the fourth event of the stream is nobody's business here", reads)
		}
		if beside.At() != mark.At() {
			t.Fatalf("the paged mint answered position %d where the single-page one answered %d, and a page boundary is not a fact about the log", beside.At(), mark.At())
		}
	})

	t.Run("the control: the park is asked once per distinct key and not once per envelope", func(t *testing.T) {
		held := newPark()
		// A sequence of neither of the commit's keys, so the queue is non-empty
		// and the count-first gate lets the Holds questions through.
		parkOne(t, held, of, "Z")
		watched := watchingPark(held)

		asked := waitingOver(t, projection.Spec{
			Name: "orders", Generation: 2, Checkpoints: stand.points,
			Sequence: byTag(), Park: watched, OnPermanentFailure: projection.ParkSequence,
		}, cover)
		asked.Until = mark
		asked.Ticks = neverBeating().Ticks

		recorded(t, stand, of, event.Progress{Highest: mark.At()})
		vis, err := projection.Wait(ctx, asked)
		if err != nil || !vis.Reached {
			t.Fatalf("a wait over a queue holding another sequence answered %+v and %v, where neither of the mark's own keys is parked", vis, err)
		}
		if vis.Polls != 1 {
			t.Fatalf("the wait made %d polls where one was enough, so the counts below are over more polls than the case set up", vis.Polls)
		}
		watched.counted(t, "Holds", 0, 2)
		watched.counted(t, "Sequences", 0, 1)
	})
}

// The six the mint cannot make, each reached on its own and each leaving the
// caller a mark it never minted rather than one it cannot trust. The seventh is
// the store this call reads through being absent, which every door of this
// package refuses rather than dereferences.
func TestCommittedRefusesTheSixItCannotMint(t *testing.T) {
	stand := newStand(t, eventmemory.Spec{})
	repo := waitedRepo(t, stand.store)
	cover := coverOf(t, projection.Whole())
	ctx := context.Background()
	commit := committing(t, repo, "a-17", "A", "B")

	spec := waitingOver(t, projection.Spec{Name: "orders", Generation: 2, Checkpoints: stand.points, Sequence: byTag()}, cover)

	t.Run("an empty commit", func(t *testing.T) {
		_, at, err := repo.Load(ctx, "a-99")
		if err != nil {
			t.Fatalf("loading a stream nothing was appended to answered %v", err)
		}
		_, empty, err := repo.Append(ctx, at)
		if err != nil || !empty.Empty() {
			t.Fatalf("an append of no changes answered %v and empty=%v", err, empty.Empty())
		}
		if _, err := spec.Committed(ctx, stand.store, empty); !errors.Is(err, projection.ErrSpec) {
			t.Fatalf("a mint over a commit that wrote nothing answered %v, where there is no change for a read model to make visible", err)
		}
	})

	t.Run("a transaction of this store's bound to the context, before any read", func(t *testing.T) {
		tx, err := stand.store.Begin(ctx)
		if err != nil {
			t.Fatalf("opening a transaction of the store answered %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		watched := watchingStore(stand.store)
		if _, err := spec.Committed(eventmemory.WithTransaction(ctx, tx), watched, commit); !errors.Is(err, projection.ErrSpec) {
			t.Fatalf("a mint inside the transaction that wrote the append answered %v, where that append can still roll back and no projection ever delivers one that did", err)
		}
		if reads := watched.reads.Load(); reads != 0 {
			t.Fatalf("the bound transaction cost %d reads, and the refusal is made before any of them", reads)
		}
	})

	t.Run("a store that does not show the version the commit reports", func(t *testing.T) {
		watched := watchingStore(stand.store)
		watched.answer = func(page []event.Envelope, _ int64) ([]event.Envelope, error) {
			return page[:len(page)-1], nil
		}
		_, err := spec.Committed(ctx, watched, commit)
		if !errors.Is(err, projection.ErrUncommitted) {
			t.Fatalf("a mint over a stream that ends below the commit's last version answered %v, where the writer has not committed or it rolled back and a second connection tells the two apart no better", err)
		}
	})

	t.Run("a zero position on the commit's last event", func(t *testing.T) {
		watched := watchingStore(stand.store)
		watched.answer = func(page []event.Envelope, _ int64) ([]event.Envelope, error) {
			held := slices.Clone(page)
			held[len(held)-1].Position = 0
			return held, nil
		}
		if _, err := spec.Committed(ctx, watched, commit); !errors.Is(err, projection.ErrUncommitted) {
			t.Fatalf("a mint over a store answering position zero for the commit's last event answered %v, where that is what a store assigning positions at commit answers before one", err)
		}
	})

	t.Run("a page whose first envelope is another stream's", func(t *testing.T) {
		elsewhere := committing(t, repo, "b-42", "B")
		watched := watchingStore(stand.store)
		watched.answer = func(page []event.Envelope, _ int64) ([]event.Envelope, error) {
			held, err := stand.store.ReadStream(ctx, elsewhere.Stream(), 0)
			if err != nil {
				return nil, err
			}
			return append(slices.Clone(held), page...), nil
		}
		if _, err := spec.Committed(ctx, watched, commit); !errors.Is(err, event.ErrBackend) {
			t.Fatalf("a mint over a page whose first envelope is another stream's answered %v, where a mark minted from it is a mark for somebody else's event", err)
		}
	})

	t.Run("a page whose second envelope is another stream's", func(t *testing.T) {
		elsewhere := committing(t, repo, "c-11", "ZZZ")
		watched := watchingStore(stand.store)
		watched.answer = func(page []event.Envelope, _ int64) ([]event.Envelope, error) {
			other, err := stand.store.ReadStream(ctx, elsewhere.Stream(), 0)
			if err != nil {
				return nil, err
			}
			held := slices.Clone(page)
			foreign := other[0]
			foreign.Version = held[1].Version
			held[1] = foreign
			return held, nil
		}
		if _, err := spec.Committed(ctx, watched, commit); !errors.Is(err, event.ErrBackend) {
			t.Fatalf("a mint over a page whose second envelope is another stream's answered %v, where the kernel's own read door walks every envelope of a page for the two properties this one walked the first for", err)
		}

		// The consequence, and it is the whole of ES-05 rather than a store's
		// honesty: a foreign envelope carries a foreign sequence key, so the mark
		// that page would have minted asks the queue about somebody else's event,
		// is answered no, and reports a parked change applied. There is no such
		// mark to wait on now, and the mark this commit does mint is asked about
		// the key that page would have replaced.
		t.Run("the consequence: the mark this commit mints is asked about the key the foreign page would have dropped", func(t *testing.T) {
			of := identityOf(t, "orders", 2, projection.Whole())
			queue := newPark()
			parkOne(t, queue, of, "B")
			asked := waitingOver(t, projection.Spec{
				Name: "orders", Generation: 2, Checkpoints: stand.points,
				Sequence: byTag(), Park: queue, OnPermanentFailure: projection.ParkSequence,
			}, cover)

			mark, err := asked.Committed(ctx, stand.store, commit)
			if err != nil {
				t.Fatalf("minting over the store that answers its own rows answered %v", err)
			}
			asked.Until = mark
			asked.Ticks = neverBeating().Ticks
			recorded(t, stand, of, event.Progress{Highest: mark.At() + 10})

			vis, err := projection.Wait(ctx, asked)
			if !errors.Is(err, projection.ErrParked) {
				t.Fatalf("a wait over a queue holding this commit's second key answered %+v and %v, and a mark that lost that key answers Reached with a nil error, which is nothing for a caller to branch on", vis, err)
			}
		})
	})

	t.Run("a page whose second envelope repeats the version of the first", func(t *testing.T) {
		watched := watchingStore(stand.store)
		watched.answer = func(page []event.Envelope, _ int64) ([]event.Envelope, error) {
			held := slices.Clone(page)
			held[1].Version = held[0].Version
			return held, nil
		}
		if _, err := spec.Committed(ctx, watched, commit); !errors.Is(err, event.ErrBackend) {
			t.Fatalf("a mint over a page that does not rise by one answered %v, where the range read back is the range the commit reports or it is nobody's range", err)
		}
	})

	t.Run("a page longer than the stream page this store publishes", func(t *testing.T) {
		watched := watchingStore(stand.store)
		watched.page = 1
		watched.answer = func([]event.Envelope, int64) ([]event.Envelope, error) {
			return stand.store.ReadStream(ctx, commit.Stream(), commit.First()-1)
		}
		if _, err := spec.Committed(ctx, watched, commit); !errors.Is(err, event.ErrBackend) {
			t.Fatalf("a mint over a page of two envelopes from a store publishing a page of one answered %v, where a bound a store overruns is a bound it does not keep and the page is dense and this stream's", err)
		}
	})

	t.Run("a nil Sequence beside a non-nil Park", func(t *testing.T) {
		unkeyed := waitingOver(t, projection.Spec{
			Name: "orders", Generation: 2, Checkpoints: stand.points,
			Park: newPark(), OnPermanentFailure: projection.ParkSequence,
		}, cover)
		unkeyed.Sequence = nil
		if _, err := unkeyed.Committed(ctx, stand.store, commit); !errors.Is(err, projection.ErrSpec) {
			t.Fatalf("a mint with no sequencer beside a queue answered %v, where the mark would carry no key the queue could be asked about", err)
		}
	})

	t.Run("a store this call names none of", func(t *testing.T) {
		if _, err := spec.Committed(ctx, nil, commit); !errors.Is(err, projection.ErrSpec) {
			t.Fatalf("a mint through no store answered %v, where the position a commit went in at is read back through the one the append rode through", err)
		}
	})

	t.Run("the control: the same call on a fresh context mints the mark", func(t *testing.T) {
		mark, err := spec.Committed(ctx, stand.store, commit)
		if err != nil {
			t.Fatalf("the well-formed mint every row above differs from in one thing answered %v", err)
		}
		if mark.Zero() || mark.At() != positionOf(t, stand.store, commit) {
			t.Fatalf("the mark is at %d where the commit's last event went in elsewhere", mark.At())
		}
	})
}

// What the store itself says the commit's last event went in at, read the way an
// operator would and not through the value under test.
func positionOf(t *testing.T, store event.Store, commit event.Commit) event.Position {
	t.Helper()
	page, err := store.ReadStream(context.Background(), commit.Stream(), commit.Last()-1)
	if err != nil || len(page) == 0 {
		t.Fatalf("reading the commit's last event answered %v and %d envelopes", err, len(page))
	}
	return page[0].Position
}

// A letter written straight into the queue, outside any unit of work, which is
// what a park holds after a pass that parked a sequence has committed.
func parkOne(t *testing.T, held *park, of projection.Identity, sequence string) {
	t.Helper()
	letter := projection.Letter{
		Identity:  of.Whole(),
		Sequencer: "by-tag",
		Sequence:  sequence,
		Envelope: event.Envelope{
			Stream:  event.Stream{Family: "waits.order", Key: event.Compose("a-17")},
			Version: 1,
			Type:    "waits.tagged",
			Payload: []byte(`{"Tag":"` + sequence + `"}`),
		},
	}
	if err := held.Park(context.Background(), letter); err != nil {
		t.Fatalf("parking the sequence %q answered %v", sequence, err)
	}
	if holds, err := held.Holds(context.Background(), of.Whole(), sequence); err != nil || !holds {
		t.Fatalf("the queue does not hold the sequence the case just parked: %v, %v", holds, err)
	}
}
