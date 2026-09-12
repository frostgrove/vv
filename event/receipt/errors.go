package receipt

import "errors"

// Four, and the split between them is which party got something wrong.
//
// ErrSpec is the caller's: a door handed values that do not belong together, or
// a call placed where its own answer would be a lie. Every placement refusal is
// one — a claim outside the append's transaction, a completion on another
// transaction or another aggregate's commit, a resolve issued inside the
// transaction whose row it would read.
//
// ErrCollision is the appendix's «другое содержимое с тем же ключом — конфликт»,
// and it travels as an error rather than only as a verdict because a verdict is
// a value it is legal to discard: the worst code that compiles must refuse
// instead of spending somebody else's key.
//
// ErrIncomplete is a row nobody resolved, and it is a defect report rather than
// an ordinary outcome. It is not Unresolved under another name: Unresolved is an
// ABSENT row, which may still arrive, and this is a PRESENT row whose claim
// committed without its completion, which never will. A retry told "already
// done" out of a row whose range is 0–0 has reported a success that may never
// have happened.
//
// ErrLedger is the ledger's own: an answer no conformant implementation gives,
// refused before it is compared or rendered. A ledger is trusted exactly as far
// as its answers are, which is the rule the kernel already applies to a store's
// strings and numbers.
var (
	ErrSpec       = errors.New("receipt: this operation cannot be recorded from this spec")
	ErrCollision  = errors.New("receipt: this operation key was spent on another operation")
	ErrIncomplete = errors.New("receipt: this operation key holds a claim nobody resolved, so whether its events reached the log is not a question this row answers")
	ErrLedger     = errors.New("receipt: this ledger answered something no ledger answers")
)
