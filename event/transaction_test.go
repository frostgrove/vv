package event

import (
	"context"
	"errors"
	"testing"
)

// Within opens nothing and asks one question, so its two refusals are the two
// ways the answer can be no — this context has no transaction of the store's,
// and this store has no transactions at all. They are fixed in different places,
// which is why they are two sentinels.
func TestWithinAnswersTheStoresTransactionQuestion(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	background := context.Background()

	t.Run("a context carrying nothing of this store's", func(t *testing.T) {
		store := newRecordingStore(t)
		repo, _ := bindAccounts(t, store)

		store.forget()
		inside, err := repo.Within(background)
		if !errors.Is(err, ErrNoTransaction) {
			t.Fatalf("Within on a context carrying no transaction answered %v, and an operation that asked to be atomic and was not is what this refusal exists for", err)
		}
		if inside != background {
			t.Fatal("a refused Within answered a context other than the one it was given, so a caller that assigns it back operates on a context the framework invented")
		}
		if store.count("Transaction") != 1 {
			t.Fatalf("Within decided without asking the store what the context carries, and only the store knows what its own executors are")
		}
	})

	t.Run("a store that has no transactions to be inside", func(t *testing.T) {
		store := newRecordingStore(t)
		store.capabilities.Transactions = Unsupported
		repo, _ := bindAccounts(t, store)
		given := withRecordingTransaction(background, &recordingTx{store: store})

		store.forget()
		inside, err := repo.Within(given)
		if !errors.Is(err, ErrNoTransactionBinding) || errors.Is(err, ErrNoTransaction) {
			t.Fatalf("Within on a store that has no transactions answered %v; a deployment fact and a wiring mistake are fixed in different places", err)
		}
		if inside != given {
			t.Fatal("a refused Within answered a context other than the one it was given")
		}
		if store.count("Transaction") != 0 {
			t.Fatalf("Within asked a store with no transactions what the context carries, and the capability is read from the store value itself")
		}
	})

	t.Run("a context carrying something of this store's that is not a transaction", func(t *testing.T) {
		store := newRecordingStore(t)
		repo, _ := bindAccounts(t, store)
		given := withRecordingExecutor(background, store)

		inside, err := repo.Within(given)
		if !errors.Is(err, ErrAmbientNotTransaction) {
			t.Fatalf("Within on a context carrying an executor that is not a transaction answered %v", err)
		}
		if !errors.Is(CauseOf(err), errNotATransaction) || inside != given {
			t.Fatalf("the store's own answer is not reachable from %v, and it is the only statement of why this executor cannot carry an append", err)
		}
	})

	t.Run("a transaction bound and Within never called", func(t *testing.T) {
		store := newRecordingStore(t)
		repo, declared := bindAccounts(t, store)
		ctx := withRecordingTransaction(background, &recordingTx{store: store})

		_, at, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("a load on a context carrying a transaction nobody marked answered %v, and the operation runs inside it whether or not Within was called", err)
		}
		_, receipt, err := repo.Append(ctx, at, declared.opened.New(acme, opened{Owner: "acme"}))
		if err != nil {
			t.Fatalf("an append on a context carrying a transaction nobody marked answered %v", err)
		}
		held, err := repo.Authority(ctx)
		if err != nil || !receipt.Authority().Same(held) {
			t.Fatalf("the append reported an authority the repository does not report for the same context (%v), so the write did not land in the transaction the caller has", err)
		}
	})

	t.Run("an executor that is not a transaction, and Within never called", func(t *testing.T) {
		store := newRecordingStore(t)
		repo, declared := bindAccounts(t, store)
		clean := withRecordingTransaction(background, &recordingTx{store: store})
		_, at, err := repo.Load(clean, acme)
		if err != nil {
			t.Fatalf("the token this case appends through could not be minted: %v", err)
		}
		ctx := withRecordingExecutor(background, store)

		store.forget()
		if _, _, err := repo.Load(ctx, acme); !errors.Is(err, ErrAmbientNotTransaction) {
			t.Fatalf("a load on a context carrying an executor that is not a transaction answered %v, and falling back to autocommit is the escape this refusal prevents", err)
		}
		if _, _, err := repo.Append(ctx, at, declared.opened.New(acme, opened{Owner: "acme"})); !errors.Is(err, ErrAmbientNotTransaction) {
			t.Fatalf("an append on a context carrying an executor that is not a transaction answered %v", err)
		}
		store.exactly(t, "a load and an append refused before any statement", map[string]int{"Backing": 2, "Transaction": 2})
	})

	t.Run("a closed store answers this question and no other", func(t *testing.T) {
		store := newRecordingStore(t)
		repo, declared := bindAccounts(t, store)
		ctx := withRecordingTransaction(background, &recordingTx{store: store})
		_, at, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("the token this case appends through could not be minted: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("the fixture store refused to close: %v", err)
		}

		inside, err := repo.Within(ctx)
		if err != nil {
			t.Fatalf("Within on a closed store carrying a transaction answered %v, and what the context carries did not change when the store closed", err)
		}
		if _, _, err := repo.Load(inside, acme); !errors.Is(err, ErrClosed) {
			t.Fatalf("a load through a closed store answered %v, and the refusal for the closure belongs to the operation that follows", err)
		}
		if _, _, err := repo.Append(inside, at, declared.opened.New(acme, opened{Owner: "acme"})); !errors.Is(err, ErrClosed) {
			t.Fatalf("an append through a closed store answered %v", err)
		}
		if _, err := repo.Within(background); !errors.Is(err, ErrNoTransaction) {
			t.Fatalf("Within on a closed store with nothing bound answered %v rather than the answer an open one gives", err)
		}
	})

	t.Run("the context a transaction is bound in is the control", func(t *testing.T) {
		store := newRecordingStore(t)
		repo, declared := bindAccounts(t, store)
		ctx, err := repo.Within(withRecordingTransaction(background, &recordingTx{store: store}))
		if err != nil {
			t.Fatalf("Within on a context carrying this store's transaction answered %v, so every refusal above passes against a Within that refuses everything", err)
		}
		_, at, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("a load inside a marked context answered %v", err)
		}
		if _, _, err := repo.Append(ctx, at, declared.opened.New(acme, opened{Owner: "acme"})); err != nil {
			t.Fatalf("an append inside a marked context answered %v", err)
		}
	})
}

// An application that must prove two subsystems wrote in one transaction needs a
// value to compare, and it must be the transaction's identity rather than
// anything derivable from the store or the data source: two stores over one
// database in one transaction and in two are the case that tells them apart.
func TestRepoAuthorityIsTheComparisonTwoSubsystemsUse(t *testing.T) {
	acme := accountID{tenant: "acme", number: "A-17"}
	background := context.Background()

	t.Run("the three answers a store gives about one context", func(t *testing.T) {
		store := newRecordingStore(t)
		repo, _ := bindAccounts(t, store)
		for _, one := range []struct {
			what  string
			ctx   context.Context
			valid bool
			want  error
		}{
			{"a context carrying nothing of this store's", background, false, nil},
			{"a context carrying a transaction of this store's", withRecordingTransaction(background, &recordingTx{store: store}), true, nil},
			{"a context carrying an executor that is not a transaction", withRecordingExecutor(background, store), false, ErrAmbientNotTransaction},
		} {
			held, err := repo.Authority(one.ctx)
			if !errors.Is(err, one.want) {
				t.Fatalf("%s was reported as %v where the store's answer is %v", one.what, err, one.want)
			}
			if held.Valid() != one.valid {
				t.Fatalf("%s reported an authority whose validity is %v, and an application reads it to decide whether its atomicity claim holds", one.what, held.Valid())
			}
		}
	})

	t.Run("the value compared is the transaction's and not the store's", func(t *testing.T) {
		store := newRecordingStore(t)
		repo, declared := bindAccounts(t, store)
		ctx, err := repo.Within(withRecordingTransaction(background, &recordingTx{store: store}))
		if err != nil {
			t.Fatalf("the transaction this case writes in could not be entered: %v", err)
		}

		_, at, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("the load inside the transaction answered %v", err)
		}
		at, first, err := repo.Append(ctx, at, declared.opened.New(acme, opened{Owner: "acme"}))
		if err != nil {
			t.Fatalf("the first append inside the transaction answered %v", err)
		}
		_, second, err := repo.Append(ctx, at, declared.credited.New(acme, creditedV2{Minor: 5, Reason: "one"}))
		if err != nil {
			t.Fatalf("the second append inside the transaction answered %v", err)
		}
		held, err := repo.Authority(ctx)
		if err != nil {
			t.Fatalf("the repository was asked what this context carries and answered %v", err)
		}
		if !first.Authority().Same(second.Authority()) || !first.Authority().Same(held) {
			t.Fatal("two appends in one transaction answered authorities that are not the same value, so an application proving two subsystems wrote together is told no by two that did")
		}

		elsewhere, err := repo.Within(withRecordingTransaction(background, &recordingTx{store: store}))
		if err != nil {
			t.Fatalf("the second transaction could not be entered: %v", err)
		}
		apart, err := repo.Authority(elsewhere)
		if err != nil {
			t.Fatalf("the repository was asked what the second context carries and answered %v", err)
		}
		if apart.Same(held) {
			t.Fatal("two live transactions of one store answered authorities that compare the same, so a store answering a constant passes the case above")
		}
	})

	t.Run("a closed store answers exactly what an open one would", func(t *testing.T) {
		store := newRecordingStore(t)
		repo, _ := bindAccounts(t, store)
		ctx := withRecordingTransaction(background, &recordingTx{store: store})
		open, err := repo.Authority(ctx)
		if err != nil {
			t.Fatalf("the authority an open store reports answered %v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("the fixture store refused to close: %v", err)
		}

		held, err := repo.Authority(ctx)
		if err != nil || !held.Same(open) {
			t.Fatalf("a closed store reported %v for a context whose transaction it had already named, and this method reads and writes nothing that closing changed", err)
		}
		if _, _, err := repo.Load(ctx, acme); !errors.Is(err, ErrClosed) {
			t.Fatalf("the load that follows answered %v, so the case above passes against a store that is not closed at all", err)
		}
	})

	t.Run("a marked context that has since acquired another transaction", func(t *testing.T) {
		store := newRecordingStore(t)
		repo, _ := bindAccounts(t, store)
		marked, err := repo.Within(withRecordingTransaction(background, &recordingTx{store: store}))
		if err != nil {
			t.Fatalf("the transaction this case marks its context for could not be entered: %v", err)
		}
		second := &recordingTx{store: store}
		crossed := withRecordingTransaction(marked, second)

		held, err := repo.Authority(crossed)
		if err != nil {
			t.Fatalf("the repository was asked what a crossed context carries and answered %v", err)
		}
		ambient, err := NewAuthority(store.backing, second)
		if err != nil {
			t.Fatalf("the authority this case compares against could not be built: %v", err)
		}
		if !held.Same(ambient) {
			t.Fatal("Authority reported the transaction the context was marked for rather than the one it now carries, and what it answers is the store's answer for this context")
		}
		if _, _, err := repo.Load(crossed, acme); !errors.Is(err, ErrTransactionMismatch) {
			t.Fatalf("the operation on the same context answered %v, so an application reading Authority alone cannot tell that its atomicity claim was about another transaction", err)
		}
	})
}

// Two backings, one context, and both operations proceed: the marker chains and
// is resolved by backing rather than innermost-first. Read innermost-first, the
// second Within would leave the second store's marker on top and the first
// store's next append would be compared against a transaction that was never
// its own — a correct program refused with a message that by design names
// neither transaction.
func TestWithinComposesForTwoBackings(t *testing.T) {
	first, second := newRecordingStore(t), newRecordingStore(t)
	declared := declareAccounts(t)
	acme := accountID{tenant: "acme", number: "A-17"}

	toFirst, err := Bind(Open(first), declared.aggregate)
	if err != nil {
		t.Fatalf("the first store was refused at Bind: %v", err)
	}
	toSecond, err := Bind(Open(second), declared.aggregate)
	if err != nil {
		t.Fatalf("the second store was refused at Bind: %v", err)
	}

	ctx := withRecordingTransaction(context.Background(), &recordingTx{store: first})
	ctx = withRecordingTransaction(ctx, &recordingTx{store: second})
	ctx, err = toFirst.Within(ctx)
	if err != nil {
		t.Fatalf("Within over the first store answered %v while that store's transaction was bound", err)
	}
	ctx, err = toSecond.Within(ctx)
	if err != nil {
		t.Fatalf("Within over the second store answered %v while that store's transaction was bound", err)
	}

	receipts := map[string]Commit{}
	for name, repo := range map[string]*Repo[account, accountID]{"first": toFirst, "second": toSecond} {
		_, at, err := repo.Load(ctx, acme)
		if err != nil {
			t.Fatalf("a load through the %s store's repository, inside a context marked for both, answered %v", name, err)
		}
		_, receipt, err := repo.Append(ctx, at, declared.opened.New(acme, opened{Owner: "acme"}))
		if err != nil {
			t.Fatalf("an append through the %s store's repository, inside a context marked for both, answered %v", name, err)
		}
		receipts[name] = receipt
	}

	if receipts["first"].Authority().Same(receipts["second"].Authority()) {
		t.Fatal("two appends into two backings' transactions carry authorities that compare the same, so an application proving two subsystems wrote together is told yes by two that did not")
	}
	held, err := toFirst.Authority(ctx)
	if err != nil {
		t.Fatalf("the first store's repository was asked what this context carries for it and answered %v", err)
	}
	if !held.Same(receipts["first"].Authority()) {
		t.Fatal("the authority the repository reports for the context is not the one its own append wrote through")
	}

	_, at, err := toFirst.Load(ctx, acme)
	if err != nil {
		t.Fatalf("the token the control below appends through could not be minted: %v", err)
	}
	crossed := withRecordingTransaction(ctx, &recordingTx{store: first})
	if _, _, err := toFirst.Load(crossed, acme); !errors.Is(err, ErrTransactionMismatch) {
		t.Fatalf("a context marked for one transaction and now carrying another answered %v at Load, so the marker is compared with nothing and the case above passes against a kernel that never looks at it", err)
	}
	if _, _, err := toFirst.Append(crossed, at, declared.opened.New(acme, opened{Owner: "acme"})); !errors.Is(err, ErrTransactionMismatch) {
		t.Fatalf("a context marked for one transaction and now carrying another answered %v at Append, and the write would have landed in a transaction the caller's atomicity claim was not about", err)
	}
}
