package eventmemory_test

import (
	"context"
	"runtime"
	"slices"
	"sync"
	"testing"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
)

func begin(t *testing.T, ctx context.Context, store *eventmemory.Store) (context.Context, *eventmemory.Tx) {
	t.Helper()
	tx, err := store.Begin(ctx)
	if err != nil {
		t.Fatalf("beginning a transaction was refused: %v", err)
	}
	return eventmemory.WithTransaction(ctx, tx), tx
}

func TestASecondAppendInOneTransactionIsAdmitted(t *testing.T) {
	ctx := context.Background()
	_, store := openStore(t)
	stream := streamOf("orders.order", "acme/A-17")
	inside, tx := begin(t, ctx, store)

	appendTo(t, inside, store, stream, 0, "credited 10")
	appendTo(t, inside, store, stream, 1, "credited 20")

	t.Run("the two operations answer one authority", func(t *testing.T) {
		first, err := store.Transaction(inside)
		if err != nil {
			t.Fatalf("the store refused to name the bound transaction: %v", err)
		}
		second, err := store.Transaction(inside)
		if err != nil {
			t.Fatalf("the store refused to name the bound transaction the second time: %v", err)
		}
		if !first.Same(second) {
			t.Fatalf("two questions about one live transaction answered authorities that are not the same, so nothing can prove two subsystems wrote together")
		}

		beside, other := begin(t, ctx, store)
		second, err = store.Transaction(beside)
		if err != nil {
			t.Fatalf("the store refused to name a second transaction of its own: %v", err)
		}
		if first.Same(second) {
			t.Fatalf("two transactions live on one store at once answered authorities that compare the same, so two subsystems that each opened their own prove they wrote together and the operation commits in two units")
		}
		if err := other.Rollback(ctx); err != nil {
			t.Fatalf("rolling back the second transaction answered %v", err)
		}
	})

	t.Run("a second append at the committed version rather than the staged one is refused", func(t *testing.T) {
		err := store.Append(inside, event.AppendRequest{Stream: stream, Expected: 0, Records: records("credited 30")})
		classifiedAs(t, err, event.Conflict, "a third append at the version the transaction started from")
	})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("committing two appends of one transaction answered %v", err)
	}
	page := readStream(t, ctx, store, stream, 0)
	if got := versionsOf(page); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("the committed stream holds versions %v rather than the dense 1 and 2 two appends in one transaction produce", got)
	}
	if got := payloadsOf(page); got[0] != "credited 10" || got[1] != "credited 20" {
		t.Fatalf("the committed stream holds %v in this order", got)
	}
}

func TestAReadInsideATransactionSeesItsOwnStagedAppends(t *testing.T) {
	ctx := context.Background()
	log, store := openStore(t)
	sibling := newStore(t, eventmemory.Spec{Log: log})
	stream := streamOf("orders.order", "acme/A-17")

	appendTo(t, ctx, store, stream, 0, "committed")
	inside, tx := begin(t, ctx, store)
	appendTo(t, inside, store, stream, 1, "staged")

	t.Run("the transaction reads the committed events and then its own", func(t *testing.T) {
		held := readStream(t, inside, store, stream, 0)
		if got := payloadsOf(held); len(got) != 2 || got[0] != "committed" || got[1] != "staged" {
			t.Fatalf("a read inside the transaction returned %v, so an operation that appends and reloads cannot see its own writes", got)
		}
		if got := positionsOf(held); got[1] != 0 || got[0] == 0 {
			t.Fatalf("the staged event reads back at position %d beside the committed one at %d, and this store's answer to the position the kernel leaves unspecified before a commit is zero", got[1], got[0])
		}
	})

	t.Run("nothing outside it sees the staged event", func(t *testing.T) {
		if got := payloadsOf(readStream(t, ctx, store, stream, 0)); len(got) != 1 || got[0] != "committed" {
			t.Fatalf("a read outside the transaction returned %v, so staged work is visible before it was committed", got)
		}
		if got := payloadsOf(readStream(t, ctx, sibling, stream, 0)); len(got) != 1 {
			t.Fatalf("another store over the same log read %v of a staged event", got)
		}
		page, _ := readAll(t, inside, store, "")
		if got := payloadsOf(page); len(got) != 1 || got[0] != "committed" {
			t.Fatalf("the global read returned %v, and a staged event has no position to be read at", got)
		}
	})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("committing answered %v", err)
	}
	page := readStream(t, ctx, sibling, stream, 0)
	if got := payloadsOf(page); len(got) != 2 || got[1] != "staged" {
		t.Fatalf("after the commit another reader sees %v", got)
	}
	if positionsOf(page)[1] == 0 {
		t.Fatalf("the committed event carries no position, so a global reader can never reach it")
	}
}

func TestATransactionThatStagesToTwoStreamsNeverCrossesThem(t *testing.T) {
	ctx := context.Background()
	_, store := openStore(t)
	account := streamOf("accounts.account", "acme/A-17")
	audit := streamOf("audit.entry", "acme/A-17")
	untouched := streamOf("audit.entry", "acme/A-18")

	appendTo(t, ctx, store, account, 0, "credited 10")
	appendTo(t, ctx, store, audit, 0, "opened")

	inside, tx := begin(t, ctx, store)
	appendTo(t, inside, store, account, 1, "credited 20", "credited 30")
	appendTo(t, inside, store, audit, 1, "credited twice")

	t.Run("each read inside the transaction answers its own stream and nothing of the other", func(t *testing.T) {
		held := readStream(t, inside, store, account, 0)
		if got := payloadsOf(held); !slices.Equal(got, []string{"credited 10", "credited 20", "credited 30"}) {
			t.Fatalf("the account read inside the transaction returned %v, so an aggregate reloaded inside the unit of work that wrote it folds another stream's events", got)
		}
		if got := versionsOf(held); !slices.Equal(got, []event.Version{1, 2, 3}) {
			t.Fatalf("the account read inside the transaction holds versions %v, which is not the committed history continued densely", got)
		}
		beside := readStream(t, inside, store, audit, 0)
		if got := payloadsOf(beside); !slices.Equal(got, []string{"opened", "credited twice"}) {
			t.Fatalf("the audit stream read inside the transaction returned %v", got)
		}
		if got := versionsOf(beside); !slices.Equal(got, []event.Version{1, 2}) {
			t.Fatalf("the audit stream read inside the transaction holds versions %v", got)
		}
		if page := readStream(t, inside, store, untouched, 0); len(page) != 0 {
			t.Fatalf("a stream the transaction never wrote to reads %v inside it, so the two assertions above hold of a read that returns everything staged", payloadsOf(page))
		}
	})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("committing a transaction that staged to two streams answered %v", err)
	}

	t.Run("the commit publishes both, and each stream keeps its own order in the log", func(t *testing.T) {
		global := drainLog(t, ctx, store)
		if len(global) != 5 {
			t.Fatalf("the log holds %d events where two committed and three were staged across two streams", len(global))
		}
		for _, stream := range []event.Stream{account, audit} {
			var last event.Version
			for _, envelope := range global {
				if envelope.Stream != stream {
					continue
				}
				if envelope.Version != last+1 {
					t.Fatalf("%v appears in the log at version %d after version %d, so the commit published one stream out of the order it staged it",
						stream, envelope.Version, last)
				}
				last = envelope.Version
			}
			if last == 0 {
				t.Fatalf("%v appears in the log not at all, so the ordering above was asserted over nothing", stream)
			}
		}
	})
}

func TestTwoTransactionsOnOneStreamLeaveOneWinnerAndAConflictFromAppend(t *testing.T) {
	ctx := context.Background()
	_, store := openStore(t)
	stream := streamOf("orders.order", "acme/A-17")

	t.Run("one writer alone is admitted", func(t *testing.T) {
		inside, tx := begin(t, ctx, store)
		appendTo(t, inside, store, streamOf("orders.order", "acme/alone"), 0, "credited 10")
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("the only writer's commit answered %v, so the conflict below would pass against a store that refuses everything", err)
		}
	})

	t.Run("the loser is refused at the append and its commit reports nothing", func(t *testing.T) {
		winner, winning := begin(t, ctx, store)
		loser, losing := begin(t, ctx, store)

		appendTo(t, winner, store, stream, 0, "the winner")
		err := store.Append(loser, event.AppendRequest{Stream: stream, Expected: 0, Records: records("the loser")})
		classifiedAs(t, err, event.Conflict, "a second transaction appending to a stream another one holds")

		if err := losing.Commit(ctx); err != nil {
			t.Fatalf("the loser's commit answered %v, and a conflict is reported from the append rather than from a commit", err)
		}
		if err := winning.Commit(ctx); err != nil {
			t.Fatalf("the winner's commit answered %v", err)
		}
		if got := payloadsOf(readStream(t, ctx, store, stream, 0)); len(got) != 1 || got[0] != "the winner" {
			t.Fatalf("the stream holds %v, so both writers landed or the wrong one did", got)
		}
	})

	t.Run("two concurrent writers produce one winner and one conflict", func(t *testing.T) {
		contested := streamOf("orders.order", "acme/contested")
		refusals := make([]error, 2)
		var writers sync.WaitGroup
		var released sync.WaitGroup
		released.Add(1)
		for writer := range refusals {
			writers.Add(1)
			go func() {
				defer writers.Done()
				released.Wait()
				refusals[writer] = store.Append(ctx, event.AppendRequest{
					Stream:   contested,
					Expected: 0,
					Records:  records("credited by writer"),
				})
			}()
		}
		released.Done()
		writers.Wait()

		admitted := 0
		for _, err := range refusals {
			if err == nil {
				admitted++
				continue
			}
			classifiedAs(t, err, event.Conflict, "the loser of two concurrent appends at one version")
		}
		if admitted != 1 {
			t.Fatalf("%d of two concurrent appends at version 0 were admitted", admitted)
		}
		if got := versionsOf(readStream(t, ctx, store, contested, 0)); len(got) != 1 || got[0] != 1 {
			t.Fatalf("the contested stream holds versions %v after two concurrent appends at version 0", got)
		}
	})
}

func TestARolledBackAppendBurnsItsPositions(t *testing.T) {
	ctx := context.Background()
	_, store := openStore(t)
	first, discarded, later := streamOf("orders.order", "acme/first"), streamOf("orders.order", "acme/discarded"), streamOf("orders.order", "acme/later")

	appendTo(t, ctx, store, first, 0, "the committed one")

	inside, tx := begin(t, ctx, store)
	appendTo(t, inside, store, discarded, 0, "rolled back", "rolled back too")
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rolling back answered %v", err)
	}

	appendTo(t, ctx, store, later, 0, "the one after the rollback")

	page, _ := readAll(t, ctx, store, "")
	if got := payloadsOf(page); len(got) != 2 || got[0] != "the committed one" || got[1] != "the one after the rollback" {
		t.Fatalf("the log holds %v, so a rolled-back append survived somewhere", got)
	}
	positions := positionsOf(page)
	if positions[1] != positions[0]+3 {
		t.Fatalf("the append after the rollback took position %d against the earlier %d, so the two positions the rollback staged were reissued rather than burned",
			positions[1], positions[0])
	}
	if page := readStream(t, ctx, store, discarded, 0); len(page) != 0 {
		t.Fatalf("the rolled-back stream still holds %v", payloadsOf(page))
	}

	t.Run("the same appends committed leave no gap", func(t *testing.T) {
		_, control := openStore(t)
		appendTo(t, ctx, control, first, 0, "the committed one")
		inside, tx := begin(t, ctx, control)
		appendTo(t, inside, control, discarded, 0, "kept", "kept too")
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("committing answered %v", err)
		}
		appendTo(t, ctx, control, later, 0, "the one after the commit")
		page, _ := readAll(t, ctx, control, "")
		if got := positionsOf(page); len(got) != 4 || got[3] != got[0]+3 {
			t.Fatalf("four committed events took positions %v, so the gap the rollback produced is not the rollback's doing", got)
		}
	})

	t.Run("a transaction that was rolled back is finished", func(t *testing.T) {
		if err := tx.Rollback(ctx); err == nil {
			t.Fatalf("rolling back a finished transaction answered nothing, so a second rollback burns another round of positions")
		}
		if err := tx.Commit(ctx); err == nil {
			t.Fatalf("committing a rolled-back transaction answered nothing")
		}
		if err := store.Append(inside, event.AppendRequest{Stream: discarded, Expected: 0, Records: records("after the rollback")}); err == nil {
			t.Fatalf("an append through a context carrying a finished transaction was admitted, so it landed on autocommit while the caller believed it was inside a transaction")
		}
	})
}

// The transaction is begun, staged into, and left behind here rather than in the
// caller's frame: nothing it returns names the *Tx or the context that carries
// it, so from the return on there is no route to it and it can never be
// committed. The authority is what event.Repo.Append puts in every non-empty
// commit receipt, so retaining it is what an audit buffer or an outbox row does.
func abandon(t *testing.T, ctx context.Context, store *eventmemory.Store, stream event.Stream) event.Authority {
	t.Helper()
	inside, _ := begin(t, ctx, store)
	appendTo(t, inside, store, stream, 0, "staged and never finished")
	authority, err := store.Transaction(inside)
	if err != nil {
		t.Fatalf("naming the transaction bound in its own context answered %v", err)
	}
	return authority
}

const collections = 8

func TestAStreamIsClaimedOnlyWhileSomethingCanStillCommitIt(t *testing.T) {
	ctx := context.Background()
	stream := streamOf("orders.order", "acme/A-17")

	t.Run("a transaction the caller still holds keeps its claim across a collection", func(t *testing.T) {
		_, store := openStore(t)
		inside, tx := begin(t, ctx, store)
		appendTo(t, inside, store, stream, 0, "staged and still reachable")
		for range collections {
			runtime.GC()
		}
		refusal := store.Append(ctx, event.AppendRequest{Stream: stream, Expected: 0, Records: records("outside")})
		classifiedAs(t, refusal, event.Conflict, "appending to a stream a live transaction has claimed")
		if err := tx.Rollback(ctx); err != nil {
			t.Fatalf("rolling back answered %v", err)
		}
		runtime.KeepAlive(tx)
	})

	t.Run("a transaction nobody can reach any more claims nothing", func(t *testing.T) {
		_, store := openStore(t)
		abandon(t, ctx, store, stream)

		admitted := appendUntilAdmitted(ctx, store, stream)
		if admitted != nil {
			t.Fatalf("appending to a stream claimed by a transaction nobody can reach any more answered %v after %d collections, so one aggregate refuses every write for the rest of the process with a conflict that will never clear",
				admitted, collections)
		}
		if got := payloadsOf(drainStream(t, ctx, store, stream)); len(got) != 1 || got[0] != "after" {
			t.Fatalf("the stream holds %v, so records nobody could commit were published by dropping the claim", got)
		}
	})

	t.Run("the commit receipt of an abandoned transaction is not a route back to it", func(t *testing.T) {
		_, store := openStore(t)
		receipt := abandon(t, ctx, store, stream)

		admitted := appendUntilAdmitted(ctx, store, stream)
		if !receipt.Valid() {
			t.Fatalf("the retained authority stopped being valid, so this ran without holding the value the caller holds")
		}
		runtime.KeepAlive(receipt)
		if admitted != nil {
			t.Fatalf("appending to a stream claimed by an abandoned transaction whose commit receipt the caller still holds answered %v after %d collections, so keeping the receipt every append hands back — an audit buffer, an outbox row — bricks that aggregate for the life of the process",
				admitted, collections)
		}
	})
}

func appendUntilAdmitted(ctx context.Context, store *eventmemory.Store, stream event.Stream) error {
	var admitted error
	for range collections {
		runtime.GC()
		admitted = store.Append(ctx, event.AppendRequest{Stream: stream, Expected: 0, Records: records("after")})
		if admitted == nil {
			return nil
		}
	}
	return admitted
}

func TestTwoLogsCarryTheirOwnTransactionsInOneContext(t *testing.T) {
	ctx := context.Background()
	_, here := openStore(t)
	_, elsewhere := openStore(t)
	stream := streamOf("orders.order", "acme/A-17")

	alone, ours := begin(t, ctx, here)
	theirs, err := elsewhere.Begin(ctx)
	if err != nil {
		t.Fatalf("beginning a transaction on the second store was refused: %v", err)
	}
	both := eventmemory.WithTransaction(alone, theirs)

	t.Run("each store names its own transaction and never the other's", func(t *testing.T) {
		named, err := here.Transaction(both)
		if err != nil {
			t.Fatalf("the first store refused to name its transaction while another log's was bound: %v", err)
		}
		if !named.Valid() {
			t.Fatalf("the first store answered that nothing of its own was bound while its own transaction was, so every write it admits runs on autocommit inside a transaction the caller opened")
		}
		expected, err := here.Transaction(alone)
		if err != nil {
			t.Fatalf("the first store refused to name its transaction with nothing else bound: %v", err)
		}
		if !named.Same(expected) {
			t.Fatalf("the first store named a transaction that is not the one it was given")
		}
		other, err := elsewhere.Transaction(both)
		if err != nil || !other.Valid() {
			t.Fatalf("the second store answered %v and %v for the transaction bound for it", other, err)
		}
		if other.Same(named) {
			t.Fatalf("the two stores named one transaction, so the comparison above passes whichever binding either of them found")
		}
	})

	t.Run("each append lands in the transaction of its own log", func(t *testing.T) {
		appendTo(t, both, here, stream, 0, "ours")
		appendTo(t, both, elsewhere, stream, 0, "theirs")

		if err := ours.Rollback(ctx); err != nil {
			t.Fatalf("rolling back the first log's transaction answered %v", err)
		}
		if page := readStream(t, ctx, here, stream, 0); len(page) != 0 {
			t.Fatalf("the first log kept %v after the transaction it was appended in was rolled back, so that append was committed outside it", payloadsOf(page))
		}
		if err := theirs.Commit(ctx); err != nil {
			t.Fatalf("committing the second log's transaction answered %v", err)
		}
		if got := payloadsOf(readStream(t, ctx, elsewhere, stream, 0)); len(got) != 1 || got[0] != "theirs" {
			t.Fatalf("the second log holds %v after the transaction it was appended in committed", got)
		}
	})

	t.Run("one log alone answers the same, so nothing above passed because two logs were involved", func(t *testing.T) {
		_, only := openStore(t)
		inside, tx := begin(t, ctx, only)
		appendTo(t, inside, only, stream, 0, "alone")
		if err := tx.Rollback(ctx); err != nil {
			t.Fatalf("rolling back answered %v", err)
		}
		if page := readStream(t, ctx, only, stream, 0); len(page) != 0 {
			t.Fatalf("with one log and one transaction the log kept %v after the rollback", payloadsOf(page))
		}
	})
}

func TestOneTransactionUsedFromManyGoroutinesRefusesRatherThanCrashes(t *testing.T) {
	ctx := context.Background()
	stream := streamOf("orders.order", "acme/A-17")

	_, settled := openStore(t)
	beforehand, finished := begin(t, ctx, settled)
	if err := finished.Rollback(ctx); err != nil {
		t.Fatalf("rolling back answered %v", err)
	}
	refusal := settled.Append(beforehand, event.AppendRequest{Stream: stream, Expected: 0, Records: records("after")})
	classifiedAs(t, refusal, event.Refused, "appending through a context carrying a transaction that was finished before the call")

	for attempt := range 200 {
		_, store := openStore(t)
		inside, tx := begin(t, ctx, store)
		answers := make([]error, 3)

		var released, racing sync.WaitGroup
		released.Add(1)
		racing.Add(len(answers))
		go func() {
			defer racing.Done()
			released.Wait()
			answers[0] = store.Append(inside, event.AppendRequest{Stream: stream, Expected: 0, Records: records("racing")})
		}()
		go func() {
			defer racing.Done()
			released.Wait()
			_, answers[1] = store.ReadStream(inside, stream, 0)
		}()
		go func() {
			defer racing.Done()
			released.Wait()
			if attempt%2 == 0 {
				answers[2] = tx.Commit(ctx)
				return
			}
			answers[2] = tx.Rollback(ctx)
		}()
		released.Done()
		racing.Wait()

		if answers[2] != nil {
			t.Fatalf("finishing a transaction another goroutine was using answered %v", answers[2])
		}
		for _, answer := range answers[:2] {
			if answer == nil {
				continue
			}
			if answer.Error() != refusal.Error() {
				t.Fatalf("an operation on a transaction finished by another goroutine answered %q, where one finished beforehand answers %q",
					answer, refusal)
			}
		}
	}
}

func TestTransactionAnswersWhatTheContextCarriesAfterTheStoreIsClosed(t *testing.T) {
	ctx := context.Background()
	_, store := openStore(t)
	stream := streamOf("orders.order", "acme/A-17")
	inside, tx := begin(t, ctx, store)

	open, err := store.Transaction(inside)
	if err != nil {
		t.Fatalf("an open store refused to name the transaction bound for it: %v", err)
	}
	if !open.Valid() {
		t.Fatalf("an open store answered that nothing of its own was bound while its own transaction was")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("closing the store answered %v", err)
	}

	closed, err := store.Transaction(inside)
	if err != nil {
		t.Fatalf("a closed store answered %v to a question that writes and reads nothing", err)
	}
	if !closed.Same(open) {
		t.Fatalf("a closed store named a different transaction than it named while open, so a caller loses the proof that two subsystems wrote together the moment either store is closed")
	}
	if nothing, err := store.Transaction(ctx); err != nil || nothing.Valid() {
		t.Fatalf("a closed store answered %v and %v with nothing of its own bound", nothing, err)
	}

	refusal := store.Append(inside, event.AppendRequest{Stream: stream, Expected: 0, Records: records("after the close")})
	classifiedAs(t, refusal, event.Closed, "appending to a closed store inside a transaction it named")
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rolling back a transaction of a closed store answered %v", err)
	}
}

const foreignCursor = event.Cursor("some other log:3")

func TestATransactionThisStoreCannotUseIsRefusedAtEveryDoor(t *testing.T) {
	ctx := context.Background()
	_, store := openStore(t)
	stream := streamOf("orders.order", "acme/A-17")
	appendTo(t, ctx, store, stream, 0, "committed")

	finished, tx := begin(t, ctx, store)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rolling back answered %v", err)
	}

	for _, carried := range []struct {
		what string
		ctx  context.Context
	}{
		{"a transaction that was already finished", finished},
		{"a transaction that is nil", eventmemory.WithTransaction(ctx, nil)},
	} {
		t.Run("the two reads and the append refuse "+carried.what, func(t *testing.T) {
			_, err := store.ReadStream(carried.ctx, stream, 0)
			classifiedAs(t, err, event.Refused, "reading a stream through a context carrying "+carried.what)

			_, _, err = store.ReadAll(carried.ctx, "")
			classifiedAs(t, err, event.Refused, "reading the log through a context carrying "+carried.what)

			_, _, err = store.ReadAll(carried.ctx, foreignCursor)
			classifiedAs(t, err, event.Refused, "reading the log from an unreadable cursor through a context carrying "+carried.what)

			err = store.Append(carried.ctx, event.AppendRequest{Stream: stream, Expected: 1, Records: records("refused")})
			classifiedAs(t, err, event.Refused, "appending through a context carrying "+carried.what)
		})
	}

	t.Run("a live transaction is served at all three, so the refusals above are what the context carries", func(t *testing.T) {
		live, second := begin(t, ctx, store)
		if got := payloadsOf(readStream(t, live, store, stream, 0)); len(got) != 1 || got[0] != "committed" {
			t.Fatalf("a stream read inside a live transaction returned %v", got)
		}
		if page, _ := readAll(t, live, store, ""); len(page) != 1 {
			t.Fatalf("a log read inside a live transaction returned %d envelopes", len(page))
		}
		_, _, err := store.ReadAll(live, foreignCursor)
		classifiedAs(t, err, event.BadCursor, "reading the log from that same cursor inside a live transaction")
		appendTo(t, live, store, stream, 1, "admitted")
		if err := second.Rollback(ctx); err != nil {
			t.Fatalf("rolling back answered %v", err)
		}
	})
}
