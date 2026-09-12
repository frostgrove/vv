package eventtest

import (
	"context"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/receipt"
)

// One inventory of the defects the ledger harness is built to detect, in code,
// because a count kept in prose is what drifts. Every section is named by at
// least one row and a test computes that rather than remembering it: a section
// no row names has no control that can fail, so its assertions can be deleted one
// at a time with every run still green.
//
// Four of the ten are ledgers rather than decorators, and that is not a
// preference. Writing a claim outside the caller's unit, making a completion
// visible before that unit commits, answering a lookup on the connection the
// claim is open on and taking a horizon from the newest row are all decisions
// inside an implementation's own statements, which a decorator that forwards
// cannot reach without holding the connection the ledger opened.
type ledgerDefect struct {
	name    string
	section string
	over    func(receipt.Ledger) receipt.Ledger
}

func ledgerDefects() []ledgerDefect {
	return []ledgerDefect{
		{"reads the key before it inserts it and decides from the read", "claim order",
			func(held receipt.Ledger) receipt.Ledger { return selectsFirst{overLedger{held}} }},
		{"answers the row it was handed rather than the row its table holds", "repeat",
			func(held receipt.Ledger) receipt.Ledger { return echoes{overLedger{held}} }},
		{"reports that every claim took the key", "repeat",
			func(held receipt.Ledger) receipt.Ledger { return alwaysWon{overLedger{held}} }},
		{"answers a claim with a row carrying no instant", "claim",
			func(held receipt.Ledger) receipt.Ledger { return undated{overLedger{held}} }},
		{"completes nothing and answers nil", "completion",
			func(held receipt.Ledger) receipt.Ledger { return completesNothing{overLedger{held}} }},
		{"answers one transaction for every context it is asked about", "transaction",
			func(held receipt.Ledger) receipt.Ledger { return &oneAuthority{overLedger: overLedger{held}} }},
		{"writes its claim on a connection of its own rather than in the caller's unit", "unit of work", nil},
		{"makes its completion visible before the unit it was issued in commits", "completion", nil},
		{"answers a lookup on the connection the claim is open on", "unit of work", nil},
		{"takes its horizon from the newest row it holds rather than the oldest", "horizon", nil},
	}
}

// The ledger the harness builds around the ledger under test. Every method of
// the contract is required, so an embedded receipt.Ledger forwards all five and a
// decorator that overrides one is still the exact value a section asks
// everything of.
type overLedger struct{ receipt.Ledger }

// The claim written the one way it may not be. The read placed first sees
// nothing while the winner is uncommitted, and what an implementation that puts
// it there must then do is decide from it — the insert's zero arrives after the
// decision, so both callers proceed under one key.
type selectsFirst struct{ overLedger }

func (this selectsFirst) Claim(ctx context.Context, row receipt.Receipt) (receipt.Receipt, bool, error) {
	found, taken, err := this.Ledger.Find(ctx, row.Key)
	if err != nil {
		return receipt.Receipt{}, false, err
	}
	if taken {
		return found, false, nil
	}
	held, _, err := this.Ledger.Claim(ctx, row)
	if err != nil {
		return receipt.Receipt{}, false, err
	}
	return held, true, nil
}

type echoes struct{ overLedger }

func (this echoes) Claim(ctx context.Context, row receipt.Receipt) (receipt.Receipt, bool, error) {
	held, won, err := this.Ledger.Claim(ctx, row)
	if err != nil || won {
		return held, won, err
	}
	row.RecordedAt = held.RecordedAt
	return row, false, nil
}

type alwaysWon struct{ overLedger }

func (this alwaysWon) Claim(ctx context.Context, row receipt.Receipt) (receipt.Receipt, bool, error) {
	held, _, err := this.Ledger.Claim(ctx, row)
	return held, true, err
}

type undated struct{ overLedger }

func (this undated) Claim(ctx context.Context, row receipt.Receipt) (receipt.Receipt, bool, error) {
	held, won, err := this.Ledger.Claim(ctx, row)
	held.RecordedAt = time.Time{}
	return held, won, err
}

type completesNothing struct{ overLedger }

func (this completesNothing) Complete(context.Context, receipt.Receipt) error { return nil }

// One authority for the life of the value, which is what memoising the first
// transaction a ledger resolved looks like from outside: a completion issued on
// another unit compares Same with the claim's, and a claim on the ledger's own
// autocommit passes the check that it and its append are one commit.
type oneAuthority struct {
	overLedger
	held event.Authority
}

func (this *oneAuthority) Transaction(ctx context.Context) (event.Authority, error) {
	if this.held.Valid() {
		return this.held, nil
	}
	found, err := this.Ledger.Transaction(ctx)
	if err == nil && found.Valid() {
		this.held = found
	}
	return found, err
}
