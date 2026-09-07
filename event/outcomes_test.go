package event

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/frostgrove/vv/crud"
)

var errQuota = errors.New("a decorator's own refusal")

// The store selects a row and the kernel maps the selection; it inspects no
// message, re-reads no stream to work out whether an append conflicted, and
// infers nothing from a driver code it does not import. The door decides exactly
// one thing — what an error the store did not classify means — because guessing
// "not written" at a write is a silent double write, and a read wrote nothing
// there is anything to be uncertain about.
func TestAnOutcomeOutsideTheVocabularyTakesTheFailSafeDefault(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	ctx := context.Background()

	for _, one := range []struct {
		what     string
		answered error
		want     error
	}{
		{"a conflict", Failure(Conflict, nil), ErrConflict},
		{"a closed store", Failure(Closed, nil), ErrClosed},
		{"a policy refusal", Failure(Refused, errQuota), ErrRefused},
		{"a write that certainly did not land", Failure(NotWritten, nil), ErrBackend},
		{"a write that was never confirmed", Failure(Unconfirmed, nil), ErrUncertain},
		{"an error the store did not classify", errors.New("the connection went away"), ErrUncertain},
		{"an outcome outside the vocabulary", Failure(Outcome(200), nil), ErrUncertain},
	} {
		store := newRecordingStore(t)
		repo, declared := bindAccounts(t, store)
		_, at, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("%s: the token this row appends through could not be minted: %v", one.what, err)
		}
		store.fail = one.answered

		answered, receipt, err := repo.Append(ctx, at, declared.opened.New(acme, opened{Owner: "acme"}))
		if !errors.Is(err, one.want) {
			t.Fatalf("%s reached the caller as %v where %v is what the store selected", one.what, err, one.want)
		}
		if answered != at || !receipt.Empty() {
			t.Fatalf("%s advanced the caller's token or produced a receipt for an append that did not land", one.what)
		}
	}

	store := newRecordingStore(t)
	repo, declared := bindAccounts(t, store)
	_, at, err := repo.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the control token could not be minted: %v", err)
	}
	if _, _, err := repo.Append(ctx, at, declared.opened.New(acme, opened{Owner: "acme"})); err != nil {
		t.Fatalf("the control append was refused with %v, so every row above passes against a repository that refuses every append", err)
	}

	store.failRead = errors.New("the connection went away")
	if _, _, err := repo.Load(ctx, acme); !errors.Is(err, ErrBackend) || errors.Is(err, ErrUncertain) {
		t.Fatalf("an unclassified failure from a read reached the caller as %v; a read wrote nothing there is anything to be uncertain about, and the two doors answer it differently", err)
	}

	store.failRead = Failure(Refused, errQuota)
	err = func() error { _, _, err := repo.Load(ctx, acme); return err }()
	if !errors.Is(err, ErrRefused) || !errors.Is(CauseOf(err), errQuota) {
		t.Fatalf("a decorator's own refusal at a read reached the caller as %v with the cause %v, so a wrapper that polices a store cannot say why", err, CauseOf(err))
	}
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrBackend) {
		t.Fatalf("a policy refusal also answers for another class in %v, and a caller's retry loop would sit on a refusal that will never clear", err)
	}

	reader, err := Read(ReadOnly(store), "")
	if err != nil {
		t.Fatalf("a reader over an honest store was refused with %v", err)
	}
	store.failWhole = errors.New("the connection went away")
	if _, err := reader.Next(ctx); !errors.Is(err, ErrBackend) || errors.Is(err, ErrUncertain) {
		t.Fatalf("an unclassified failure from a projector's read reached the caller as %v; a projector that receives an uncertainty from a call that wrote nothing takes a write-side recovery, and the two read doors answer one rule", err)
	}

	store.failWhole = fmt.Errorf("%w: the connection went away", crud.ErrUnavailable)
	if _, err := reader.Next(ctx); !errors.Is(err, crud.ErrUnavailable) {
		t.Fatalf("a retryable cause the store did not classify reached a projector as %v, and a retry loop keyed on retryability stops retrying a failure that would have cleared", err)
	}

	store.failWhole = nil
	if more, err := reader.Next(ctx); err != nil || !more {
		t.Fatalf("the control read answered %v and more=%v, so both rows above pass against a reader that refuses every page", err, more)
	}
}
