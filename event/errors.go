package event

import (
	"context"
	"errors"
	"fmt"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/errs"
)

// Every sentinel belongs to exactly one of six classes and errors.Is never
// crosses a class. Two sentinels wrap another sentinel — both into
// ErrDeclaration — and no others do. What a sentinel or a refusal wraps out of
// this vocabulary is a class a transport already maps, and that is the whole
// list: crud.ErrBadRequest for the request class, crud.ErrConflict,
// crud.ErrUnavailable when the cause is retryable, an *errs.Fault, and — for
// the two rows that have one — the policy's or the application's own error.
// Adding a sentinel to a class is additive; adding a class, or moving a
// sentinel between two, is breaking.
var (
	// declaration — a programmer wrote the declaration wrong. Panicked, never returned.
	ErrDeclaration = errors.New("event: the declaration is malformed")
	ErrSealed      = fmt.Errorf("event: a fact was declared after the declaration had been read: %w", ErrDeclaration)
	ErrCodecType   = fmt.Errorf("event: the codec cannot encode its own reader type: %w", ErrDeclaration)

	// wiring — this program was assembled from values that do not belong together.
	ErrFamily                = errors.New("event: this family is already bound to another aggregate")
	ErrWrongStore            = errors.New("event: this store is not the one this value was minted over, or it did not state its own bounds")
	ErrWrongStream           = errors.New("event: this change was decided for another stream")
	ErrNoTransaction         = errors.New("event: this context carries no transaction of this store's")
	ErrNoTransactionBinding  = errors.New("event: this store has no transactions to be inside")
	ErrAmbientNotTransaction = errors.New("event: what this context carries for this store is not a transaction")
	ErrTransactionMismatch   = errors.New("event: the bound transaction is not the one this context was marked for")
	ErrCursor                = errors.New("event: this cursor was not minted over this backing, or it is no longer readable")

	// request — the data this operation was given cannot be used, and a transport
	// answers a client error. Every member declares that on the sentinel rather
	// than at the door that raises it: an undeclared wrap falls through to
	// errs.KindInternal, so a request that omitted an id would render 500 and the
	// caller would be told the server broke over data only the caller can correct.
	ErrKey      = fmt.Errorf("event: the identity does not render a legal stream key: %w", crud.ErrBadRequest)
	ErrEncode   = fmt.Errorf("event: the payload cannot be encoded by its declared codec: %w", crud.ErrBadRequest)
	ErrSample   = fmt.Errorf("event: this sample cannot prove what a round trip claims for it: %w", crud.ErrBadRequest)
	ErrTooLarge = fmt.Errorf("event: the value is over a declared bound: %w", crud.ErrBadRequest)

	// history — a fact's recorded bytes cannot be read by this declaration.
	ErrUnknownType = errors.New("event: the recorded type names no declared fact")
	ErrRevision    = errors.New("event: the recorded revision is not one this fact declares")
	ErrPayload     = errors.New("event: the recorded payload cannot be read by this declaration")
	ErrUpcast      = errors.New("event: an upcaster refused the recorded payload")

	// write — the append did not do what you asked, and here is your obligation.
	ErrConflict  = fmt.Errorf("event: the stream is not at the version this append was decided at: %w", crud.ErrConflict)
	ErrUncertain = errors.New("event: the append was issued and its outcome was never confirmed")

	// store — the store itself refused or failed.
	ErrBackend = errors.New("event: the store failed")
	ErrClosed  = errors.New("event: the store is closed")
	ErrRefused = errors.New("event: the store refused this operation as a matter of its own policy")
)

func vocabulary() []error {
	return []error{
		ErrDeclaration, ErrSealed, ErrCodecType,
		ErrFamily, ErrWrongStore, ErrWrongStream, ErrNoTransaction, ErrNoTransactionBinding,
		ErrAmbientNotTransaction, ErrTransactionMismatch, ErrCursor,
		ErrKey, ErrEncode, ErrSample, ErrTooLarge,
		ErrUnknownType, ErrRevision, ErrPayload, ErrUpcast,
		ErrConflict, ErrUncertain,
		ErrBackend, ErrClosed, ErrRefused,
	}
}

// A refusal renders its own sentinel and nothing of what produced it, so a
// driver's text, a codec's text and every value in either stay out of a log
// line. The two traversals differ deliberately:
//
//	errors.Is reaches the sentinel and the refusal's declared class wrap, and
//	never the cause. A store's incidental error must not decide a caller's
//	transport status: an uncertain commit whose cause happens to carry the
//	retryable class would render as "retry me", and retrying an unconfirmed
//	append writes the same decision twice.
//
//	errors.As reaches the declared class wrap only, which is what makes a wrap
//	readable — an oversized payload is a client's 413 — without letting a cause
//	be read the same way.
//
// Both answer false for context.Canceled and context.DeadlineExceeded, always:
// a cancellation travels as itself or not at all, because uncertainty outranks
// it and an uncertain commit reported as a cancellation cannot be recovered.
// The cause is reachable, deliberately and by name, through CauseOf.
type refusal struct {
	sentinel error
	wrapped  error
	cause    error
}

func (this *refusal) Error() string { return this.sentinel.Error() }

func (this *refusal) Is(target error) bool {
	if target == context.Canceled || target == context.DeadlineExceeded {
		return false
	}
	if matches(this.sentinel, target) {
		return true
	}
	return !inVocabulary(target) && matches(this.wrapped, target)
}

// A wrap never answers for a sentinel of this vocabulary: the sentinel field
// alone decides, so no refusal of one class can reach a sentinel of another
// through a wrap. Two rows take their cause as their wrap — ErrRefused, because
// a policy refusal's transport status is the policy's to state, and ErrUpcast,
// because the application's own error is what the caller reads — and those two
// are the doors the partition would otherwise be lost through: a quota
// decorator that wraps ErrConflict to mean 409 would put a caller's retry loop
// on a policy refusal that will never clear. Gating the target rather than the
// promotion is what keeps the decorator's and the upcaster's own error
// matchable when it happens to co-carry one of the twenty-four.
func inVocabulary(target error) bool {
	for _, sentinel := range vocabulary() {
		if sameError(target, sentinel) {
			return true
		}
	}
	return false
}

// The wrap is either the kernel's own value or a cause causeAsWrap read to the
// end, so the one stdlib traversal in this file runs over a chain that is known
// to terminate inside the budget. The recover is for a foreign As method.
func (this *refusal) As(target any) (found bool) {
	defer func() {
		if recover() != nil {
			found = false
		}
	}()
	return this.wrapped != nil && errors.As(this.wrapped, target)
}

func CauseOf(err error) error {
	if refused, ok := findAs[*refusal](err); ok {
		return refused.cause
	}
	return nil
}

func newRefusal(sentinel, wrapped, cause error) error {
	return &refusal{sentinel: sentinel, wrapped: wrapped, cause: cause}
}

// A cause the kernel cannot read to the end is not promoted to a wrap. Past the
// budget the two traversals would disagree — errors.Is stops at causeHops and
// the stdlib errors.As inside refusal.As does not — so a crud.ErrForbidden a
// decorator meant as a 403 would be invisible while an *errs.Fault at the same
// depth still set the status. The refusal keeps its own class and the cause
// stays readable through CauseOf, which is where a cause belongs when the
// kernel will not vouch for it.
func causeAsWrap(cause error) error {
	if withinBudget(cause) {
		return cause
	}
	return nil
}

func tooLarge(rule string, actual, bound int) error {
	return overBound(fmt.Sprintf("%s is %d bytes against a bound of %d", rule, actual, bound))
}

func tooMany(rule string, actual, bound int) error {
	return overBound(fmt.Sprintf("%s holds %d against a bound of %d", rule, actual, bound))
}

func overBound(broken string) error {
	return newRefusal(ErrTooLarge, errs.TooLarge().Code(errs.CodeTooLarge).Message(broken).Fault(), nil)
}

func upcastRefusal(cause error) error { return newRefusal(ErrUpcast, causeAsWrap(cause), cause) }

func refuseTransaction(cause error) error {
	return newRefusal(ErrAmbientNotTransaction, nil, cause)
}

func refuseAppend(err error) error { return refuse(err, appendDoor) }

func refuseRead(err error) error { return refuse(err, readDoor) }

type door uint8

const (
	appendDoor door = iota
	readDoor
)

// The map from a store's own classification to a refusal, and it is the only
// thing the kernel reads about a store's failure: it inspects no message,
// re-reads no stream to work out whether an append conflicted, and infers
// nothing from a driver code it does not import. The door decides exactly one
// thing — what an unclassified error means — because the safe guess differs
// between a write, where guessing "not written" is a silent double write, and a
// read, which wrote nothing there is anything to be uncertain about. A
// classified outcome is mapped the same way at every door. A cancellation
// overrides both defaults and travels as the bare sentinel: a store's own text
// around it names the key and the version it was reading, and those are the
// first two values a refusal may not carry. The classification is found through
// the wrapping and never by a type assertion, so a decorator that forwards a
// store's failure and adds its own context keeps it.
func refuse(err error, entered door) error {
	if err == nil {
		return nil
	}
	if classified, ok := findAs[*failure](err); ok {
		return classified.refuse(entered)
	}
	if matches(err, context.Canceled) {
		return context.Canceled
	}
	if matches(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return entered.unclassified(err)
}

func (this *failure) refuse(entered door) error {
	switch this.outcome {
	case Conflict:
		return newRefusal(ErrConflict, nil, this.cause)
	case NotWritten:
		return backendRefusal(this.cause)
	case Unconfirmed:
		return newRefusal(ErrUncertain, nil, this.cause)
	case Closed:
		return newRefusal(ErrClosed, nil, this.cause)
	case BadCursor:
		return newRefusal(ErrCursor, nil, this.cause)
	case Refused:
		return newRefusal(ErrRefused, causeAsWrap(this.cause), this.cause)
	default:
		return entered.unclassified(this.cause)
	}
}

func (this door) unclassified(cause error) error {
	if this == readDoor {
		return backendRefusal(cause)
	}
	return newRefusal(ErrUncertain, nil, cause)
}

func backendRefusal(cause error) error {
	if retryable(cause) {
		return newRefusal(ErrBackend, crud.ErrUnavailable, cause)
	}
	return newRefusal(ErrBackend, nil, cause)
}

// The framework spells its retryable class two ways — the sentinel a package
// wraps, and the kind a fault carries — and a store that classified its failure
// either way carries it.
func retryable(cause error) bool {
	if matches(cause, crud.ErrUnavailable) {
		return true
	}
	fault, found := findAs[*errs.Fault](cause)
	return found && fault.Kind == errs.KindRetryable
}

const causeHops = 64

// The kernel reads errors it did not build — a store's cause, a decorator's
// refusal, an application's upcast error — so every question it asks of one is
// asked here, under a hop budget and a recover: a chain that loops, a matcher
// that panics and a matcher that answers for a value it never set are that
// party's defect and must not become the kernel's — the last of the three is
// why findAs rejects a T that is nil, since every caller dereferences what it
// is handed and the producing input is an unpopulated *errs.Fault a store
// wrapped, which is a nil-interface mistake rather than a contract violation.
// matches, findAs and withinBudget are the three, and through them refuse,
// retryable, CauseOf and a refusal's own Is; the stdlib errors.As in refusal.As
// is bounded instead by withinBudget at promotion time. What none of it bounds
// is the work a foreign Is or As method does inside one visit.
//
// The budget bounds the total number of errors visited and not the depth
// reached, because errors.Join branches: a budget spent per path lets a store
// that joins per-shard failures of per-row failures cost two to the depth, and
// the request goroutine stops inside the kernel.
func walk(err error, visit func(error) bool) (found, whole bool) {
	defer func() {
		if recover() != nil {
			found, whole = false, false
		}
	}()
	budget := causeHops
	found = walkWithin(err, visit, &budget)
	return found, budget > 0
}

func finds(err error, visit func(error) bool) bool {
	found, _ := walk(err, visit)
	return found
}

func withinBudget(err error) bool {
	_, whole := walk(err, func(error) bool { return false })
	return whole
}

func walkWithin(err error, visit func(error) bool, budget *int) bool {
	for *budget > 0 && err != nil {
		*budget--
		if visit(err) {
			return true
		}
		switch unwrapper := err.(type) {
		case interface{ Unwrap() error }:
			err = unwrapper.Unwrap()
		case interface{ Unwrap() []error }:
			for _, child := range unwrapper.Unwrap() {
				if walkWithin(child, visit, budget) {
					return true
				}
			}
			return false
		default:
			return false
		}
	}
	return false
}

func matches(err, target error) bool {
	if err == nil || target == nil {
		return false
	}
	return finds(err, func(node error) bool { return sameError(node, target) || asksIs(node, target) })
}

// The bounded errors.As, and the framework has one answer to "does this chain
// carry a T": a node is either the type asked for or answers for it, which is
// what the stdlib asks at each step and what a node whose T is reachable only
// through an As method needs.
func findAs[T error](err error) (T, bool) {
	var found T
	ok := finds(err, func(node error) bool {
		var candidate T
		if typed, is := node.(T); is {
			candidate = typed
		} else if asker, is := node.(interface{ As(any) bool }); !is || !asker.As(&candidate) {
			return false
		}
		if nilByAnyRoute(candidate) {
			return false
		}
		found = candidate
		return true
	})
	return found, ok
}

func sameError(node, target error) (same bool) {
	defer func() { _ = recover() }()
	return node == target
}

func asksIs(node, target error) bool {
	matcher, ok := node.(interface{ Is(error) bool })
	return ok && matcher.Is(target)
}
