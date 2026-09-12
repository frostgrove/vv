//go:build integration

package eventpg

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/adapter/crudsql"
	"github.com/frostgrove/vv/event/eventtest"
	"github.com/frostgrove/vv/event/projection"
	"github.com/frostgrove/vv/event/receipt"
)

// The three interfaces a consumer implements against its own database, driven
// against live PostgreSQL through the harnesses eventtest publishes. This
// package implements none of them — no store gains a table and no deployment
// that wants none deploys one — so what is certified here are the three
// reference implementations this suite already wrote to prove the framework
// around them, and what that proves is the harness: the statements it certifies
// are the statements the contract describes, against a real database, with real
// row locks and real snapshots.
const applicationDefect = "EVENTPG_APPLICATION_DEFECT"

func TestTheLiveLedgerSatisfiesTheContract(t *testing.T) {
	eventtest.RunLedger(t, liveLedgerFactory(t, "harnessledger"))
}

func TestTheLiveOwnershipRowSatisfiesTheContract(t *testing.T) {
	eventtest.RunGenerations(t, liveGenerationsFactory(t))
}

func TestTheLiveQueueSatisfiesTheContract(t *testing.T) {
	eventtest.RunPark(t, liveParkFactory(t))
}

func liveLedgerFactory(t *testing.T, name string) eventtest.LedgerFactory {
	t.Helper()
	stand := newReceiptStand(t, name)
	switch os.Getenv(applicationDefect) {
	case "select-before-insert":
		stand.ledger.selectFirst = true
	case "horizon-from-the-newest-row":
		stand.ledger.optimistic = true
	case "find-on-the-claiming-connection":
		stand.ledger.dirty = true
	case "echoes-the-fingerprint-it-was-handed":
		stand.ledger.echoesPrint = true
	}
	return eventtest.LedgerFactory{
		New: func(*testing.T) receipt.Ledger { return stand.ledger },
		Begin: func(t *testing.T, ctx context.Context, _ receipt.Ledger) (context.Context, eventtest.Tx) {
			return beginBound(t, ctx, stand.store.db, stand.store.source)
		},
		Window: 30 * time.Second,
	}
}

func liveGenerationsFactory(t *testing.T) eventtest.GenerationsFactory {
	t.Helper()
	held := newProjectionCase(t, 8, 0)
	rows := held.generations(t, "harness_ownership")
	if os.Getenv(applicationDefect) == "plain-ownership-read" {
		rows = rows.unlocked()
	}
	return eventtest.GenerationsFactory{
		New: func(*testing.T) projection.Generations { return rows },
		Begin: func(t *testing.T, ctx context.Context, _ projection.Generations) (context.Context, eventtest.Tx) {
			return beginBound(t, ctx, held.pool, held.source)
		},
		Closes: eventtest.LockingRead,
		Window: 30 * time.Second,
	}
}

func liveParkFactory(t *testing.T) eventtest.ParkFactory {
	t.Helper()
	held := newProjectionCase(t, 8, 0)
	queue := held.park(t, "harness_queue").bounded(3, 2)
	if os.Getenv(applicationDefect) == "queue-on-the-pool" {
		queue.elsewhere = true
	}
	return eventtest.ParkFactory{
		New: func(*testing.T) projection.Park { return queue },
		Begin: func(t *testing.T, ctx context.Context, _ projection.Park) (context.Context, eventtest.Tx) {
			return beginBound(t, ctx, held.pool, held.source)
		},
		Sequences: queue.maxSequences,
		Letters:   queue.maxLetters,
		Window:    30 * time.Second,
	}
}

// A unit of work on the context the harness is running its section under, rather
// than on the T's: a section that refuses leaves through a panic, and a
// transaction begun on a context nothing cancels holds a connection and every
// row lock it took into the next section.
func beginBound(t *testing.T, ctx context.Context, db *sql.DB, source crud.Source) (context.Context, eventtest.Tx) {
	t.Helper()
	beginner, found := crud.BeginnerOf(crudsql.Postgres(db))
	if !found {
		t.Fatal("this data source cannot begin a transaction, so no section below could bind one")
	}
	tx, err := beginner.Begin(ctx)
	if err != nil {
		t.Fatalf("beginning a transaction answered %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.WithoutCancel(ctx)) })
	return crud.BindExecutor(ctx, source, tx), tx
}

// The falsifying half, live. Six defects of the reference implementations, each
// run through the harness that certifies it in a subprocess of this binary, and
// each one must be reported by the section named beside it. A defect the harness
// does not catch is a finding against the harness and never a pass — which is
// the same rule the store suite's own mutation harness holds, one contract over.
type applicationDefectRow struct {
	name    string
	test    string
	section string
}

const applicationDefects = 6

func applicationMutations() []applicationDefectRow {
	return []applicationDefectRow{
		{"select-before-insert", "^TestTheLiveLedgerSatisfiesTheContract$", "claim order"},
		{"echoes-the-fingerprint-it-was-handed", "^TestTheLiveLedgerSatisfiesTheContract$", "repeat"},
		{"horizon-from-the-newest-row", "^TestTheLiveLedgerSatisfiesTheContract$", "horizon"},
		{"find-on-the-claiming-connection", "^TestTheLiveLedgerSatisfiesTheContract$", "unit of work"},
		{"plain-ownership-read", "^TestTheLiveOwnershipRowSatisfiesTheContract$", "locking read"},
		{"queue-on-the-pool", "^TestTheLiveQueueSatisfiesTheContract$", "inside the unit"},
	}
}

func TestTheApplicationHarnessesCatchADefectiveImplementation(t *testing.T) {
	inventory := applicationMutations()
	if err := sized(inventory, applicationDefects, "application defects"); err != nil {
		t.Fatal(err)
	}
	if err := sized(inventory[:applicationDefects-1], applicationDefects, "application defects"); err == nil {
		t.Fatal("an inventory one row shorter passed the assertion that pins the size, so what is pinned is whatever the slice holds")
	}

	caught := 0
	for _, held := range inventory {
		if t.Run(held.name, func(t *testing.T) {
			control, code := runs(t, held.test, os.Environ())
			if code != 0 {
				t.Fatalf("the unmutated implementation fails the harness in a subprocess, so nothing below is about the defect:\n%s", control)
			}
			if !strings.Contains(control, reportedAs+held.section+": passed") {
				t.Fatalf("the unmutated implementation did not pass the %s section, so what fails below is the section rather than the defect:\n%s", held.section, control)
			}

			output, code := runs(t, held.test, append(os.Environ(), applicationDefect+"="+held.name))
			if code == 0 {
				t.Fatalf("an implementation that %s passed the harness, so the harness certifies a defect the %s section is supposed to report:\n%s",
					held.name, held.section, output)
			}
			if !strings.Contains(output, reportedAs+held.section+": failed") {
				t.Fatalf("an implementation that %s failed the harness and the %s section did not report it — the sections that did are %v:\n%s",
					held.name, held.section, sectionsThatFailed(output), output)
			}
		}) {
			caught++
		}
	}
	if caught != applicationDefects {
		t.Fatalf("%d of the %d defects this harness names were driven through their harness and reported by their section, and a loop over an inventory nobody counted runs as many times as the slice is long", caught, applicationDefects)
	}
}
