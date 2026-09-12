package eventtest_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/frostgrove/vv/event/eventtest"
	"github.com/frostgrove/vv/event/receipt"
)

// A ledger is the one contract in this extension that nothing in this repository
// implements: the row is the application's, the statements are the
// application's, and what a consumer has to get right is an order no signature
// carries. RunLedger over the reference is what says the harness certifies a
// ledger written the way the contract describes it, and the defect inventory
// below is what says the certificate means anything.
func TestTheReferenceLedgerIsCertified(t *testing.T) {
	eventtest.RunLedger(t, ledgerFactory(t, newMemoryLedger(t), nil))
}

func TestTheLedgerHarnessStillDetectsEveryDefectItWasBuiltToDetect(t *testing.T) {
	named := map[string]bool{}
	sections := map[string]bool{}
	for _, name := range eventtest.LedgerSectionNames() {
		sections[name] = true
	}
	for _, defect := range eventtest.LedgerDefects() {
		if named[defect.Name] {
			t.Fatalf("two ledger defects are named %q, so one of them is proving the other's section", defect.Name)
		}
		named[defect.Name] = true
		if !sections[defect.Section] {
			t.Fatalf("the ledger defect %q names the %s section, which this harness does not have", defect.Name, defect.Section)
		}

		plain, broken := ledgerFactoriesFor(t, defect)
		if word := oneLedgerVerdict(t, broken, defect.Section); word != "failed" {
			t.Errorf("the harness no longer detects a ledger that %s: its %s section was reported %q", defect.Name, defect.Section, word)
		}
		if word := oneLedgerVerdict(t, plain, defect.Section); word != "passed" {
			t.Errorf("the same ledger without the defect that %s was reported %q for the %s section, so the failure above is not the defect's", defect.Name, word, defect.Section)
		}
	}
	if len(named) == 0 {
		t.Fatal("the ledger harness carries no defects at all, so this run asserted nothing")
	}
}

const ledgerInventoried = 11

func TestTheLedgerDefectInventoryIsTheSizeItSaysItIs(t *testing.T) {
	defects := eventtest.LedgerDefects()
	if err := sizedLedger(defects); err != nil {
		t.Fatal(err)
	}
	if err := sizedLedger(defects[:len(defects)-1]); err == nil {
		t.Fatal("an inventory one row shorter passed the assertion that pins the size, so what is pinned is whatever the slice holds")
	}
}

func sizedLedger(defects []eventtest.LedgerDefect) error {
	if len(defects) != ledgerInventoried {
		return fmt.Errorf("the ledger harness carries %d defects where its inventory is %d rows", len(defects), ledgerInventoried)
	}
	return nil
}

// The repeat section's two losers are two assertions and a ledger trips one
// without the other: the one that answers the whole row it was handed is caught
// by the range the completed row carries, and the one that answers the table's
// row with the fingerprint replaced is caught by nothing until a claim is made
// under a fingerprint the row does not hold. Reading which arm reported is what
// says the second loser is doing the work rather than the first — without it
// both rows pass over a section driving one claim, and every collision reads as
// a repeat.
func TestTheEchoedFingerprintIsCaughtByTheFingerprintAndTheEchoedRowByItsRange(t *testing.T) {
	for _, one := range []struct{ defect, says string }{
		{"answers the row it was handed rather than the row its table holds", "answered the range"},
		{"answers the fingerprint it was handed rather than the one its table holds", "answered the fingerprint"},
	} {
		over := ledgerDefectNamed(t, one.defect).Over
		verdicts := eventtest.CertifyLedger(t, ledgerFactory(t, newMemoryLedger(t), over), "repeat")
		if len(verdicts) != 1 {
			t.Fatalf("running the repeat section alone reported %d verdicts", len(verdicts))
		}
		if verdicts[0].Word != "failed" {
			t.Errorf("a ledger that %s was reported %q by the repeat section", one.defect, verdicts[0].Word)
			continue
		}
		if !strings.Contains(verdicts[0].Reason, one.says) {
			t.Errorf("a ledger that %s was refused with %q, which is not the arm that reports it — so one of the section's two claims is answering for the other and either could be removed unnoticed",
				one.defect, verdicts[0].Reason)
		}
	}
}

func ledgerDefectNamed(t *testing.T, name string) eventtest.LedgerDefect {
	t.Helper()
	for _, defect := range eventtest.LedgerDefects() {
		if defect.Name == name {
			return defect
		}
	}
	t.Fatalf("the ledger inventory names no defect %q", name)
	return eventtest.LedgerDefect{}
}

// A section no defect names has no control that can fail: every assertion in it
// can be deleted and every run stays green. The control is one run of the same
// assertion per section, over an inventory with that section's rows taken out.
func TestEveryLedgerSectionIsNamedByADefectThatBreaksIt(t *testing.T) {
	names := eventtest.LedgerSectionNames()
	defects := eventtest.LedgerDefects()
	if err := guardedLedger(names, defects); err != nil {
		t.Fatal(err)
	}
	for _, unguarded := range names {
		kept := []eventtest.LedgerDefect{}
		for _, defect := range defects {
			if defect.Section != unguarded {
				kept = append(kept, defect)
			}
		}
		if err := guardedLedger(names, kept); err == nil {
			t.Fatalf("an inventory naming no defect of the %s section passed the assertion that every section carries one", unguarded)
		}
	}
}

func guardedLedger(names []string, defects []eventtest.LedgerDefect) error {
	naming := map[string]int{}
	for _, defect := range defects {
		naming[defect.Section]++
	}
	for _, name := range names {
		if naming[name] == 0 {
			return fmt.Errorf("no defect in this harness's inventory breaks the %s section, so nothing here can tell whether that section still asserts anything", name)
		}
	}
	return nil
}

// go test -list cannot see a subtest, so a harness shipped with four of its seven
// sections written prints four passing lines and looks complete. This reads the
// report instead, and its control is a run over an inventory one section shorter.
func TestEveryLedgerSectionInTheInventoryWasReported(t *testing.T) {
	names := eventtest.LedgerSectionNames()
	if len(names) != 7 {
		t.Fatalf("the ledger harness carries %d sections where its inventory names seven", len(names))
	}
	if err := reported(names, eventtest.CertifyLedger(t, ledgerFactory(t, newMemoryLedger(t), nil))); err != nil {
		t.Fatal(err)
	}
	shortened := names[:len(names)-1]
	if err := reported(names, eventtest.CertifyLedger(t, ledgerFactory(t, newMemoryLedger(t), nil), shortened...)); err == nil {
		t.Fatalf("a run that never dispatched %q passed the assertion that every section is reported exactly once", names[len(names)-1])
	}
}

func ledgerFactory(t *testing.T, held *memoryLedger, over func(receipt.Ledger) receipt.Ledger) eventtest.LedgerFactory {
	t.Helper()
	return eventtest.LedgerFactory{
		New: func(*testing.T) receipt.Ledger {
			beside := held.beside()
			if over == nil {
				return beside
			}
			return over(beside)
		},
		Begin: func(_ *testing.T, ctx context.Context, _ receipt.Ledger) (context.Context, eventtest.Tx) {
			return beginUnit(ctx, held.rows)
		},
	}
}

// The four defects that are ledgers rather than decorators, each with the pair
// of values it is built from: one that has the defect and one that does not.
func ledgerShaped() map[string]func(held *memoryLedger, broken bool) {
	return map[string]func(*memoryLedger, bool){
		"writes its claim on a connection of its own rather than in the caller's unit": func(held *memoryLedger, broken bool) { held.outside = broken },
		"answers a lookup on the connection the claim is open on":                      func(held *memoryLedger, broken bool) { held.dirty = broken },
		"takes its horizon from the newest row it holds rather than the oldest":        func(held *memoryLedger, broken bool) { held.newest = broken },
		"makes its completion visible before the unit it was issued in commits":        func(held *memoryLedger, broken bool) { held.completes = broken },
	}
}

func ledgerFactoriesFor(t *testing.T, defect eventtest.LedgerDefect) (plain, broken eventtest.LedgerFactory) {
	t.Helper()
	if defect.Over != nil {
		return ledgerFactory(t, newMemoryLedger(t), nil), ledgerFactory(t, newMemoryLedger(t), defect.Over)
	}
	flag, known := ledgerShaped()[defect.Name]
	if !known {
		t.Fatalf("the ledger defect %q is a ledger rather than a decorator and this test builds no ledger for it", defect.Name)
	}
	sound, wrong := newMemoryLedger(t), newMemoryLedger(t)
	flag(sound, false)
	flag(wrong, true)
	return ledgerFactory(t, sound, nil), ledgerFactory(t, wrong, nil)
}

func oneLedgerVerdict(t *testing.T, factory eventtest.LedgerFactory, section string) string {
	t.Helper()
	verdicts := eventtest.CertifyLedger(t, factory, section)
	if len(verdicts) != 1 {
		t.Fatalf("running the %s section alone reported %d verdicts", section, len(verdicts))
	}
	return verdicts[0].Word
}
