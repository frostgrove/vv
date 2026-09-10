package lock_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/crud/sqlfault"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/vvdb/lock"
)

type engineError struct{ state string }

func (this engineError) Error() string    { return "the engine refused: " + this.state }
func (this engineError) SQLState() string { return this.state }

func locksOn(t *testing.T, recorder *crudtest.Recorder, policy lock.Policy) *lock.Locks {
	t.Helper()
	locks, err := lock.For(recorder, policy)
	if err != nil {
		t.Fatalf("a PostgreSQL source was refused: %v", err)
	}
	return locks
}

func transaction(t *testing.T, recorder *crudtest.Recorder) crud.Executor {
	t.Helper()
	tx, err := recorder.Begin(t.Context())
	if err != nil {
		t.Fatalf("the recorder refused to begin: %v", err)
	}
	return tx
}

func TestALockOutsideATransactionIsRefused(t *testing.T) {
	recorder := crudtest.Postgres()
	locks := locksOn(t, recorder, lock.Policy{Timeout: time.Second, Retries: 1})

	err := locks.Take(t.Context(), recorder, lock.Exclusively(lock.KeyOf("scope", "x")))

	if !errors.Is(err, lock.ErrNoTransaction) {
		t.Fatalf("taking a lock outside a transaction answered %v, want ErrNoTransaction", err)
	}
	if got := recorder.SQL(); len(got) != 0 {
		t.Fatalf("a refused lock still ran %d statements: %v", len(got), got)
	}
}

func TestTakingALockSetsTheTimeoutBeforeItWaits(t *testing.T) {
	recorder := crudtest.Postgres()
	locks := locksOn(t, recorder, lock.Policy{Timeout: 250 * time.Millisecond, Retries: 1})

	if err := locks.Take(t.Context(), transaction(t, recorder), lock.Exclusively(lock.KeyFrom(42))); err != nil {
		t.Fatal(err)
	}

	statements := recorder.Statements()
	if len(statements) != 2 {
		t.Fatalf("expected the timeout and the lock, got %v", recorder.SQL())
	}
	if got := statements[0].Args[0]; got != "250" {
		t.Fatalf("lock_timeout was set to %v, want the timeout in milliseconds", got)
	}
	if got := statements[1].Args[0]; got != int64(42) {
		t.Fatalf("the lock was taken on %v, want the key it was given", got)
	}
}

func TestAZeroTimeoutDoesNotSetOne(t *testing.T) {
	recorder := crudtest.Postgres()
	locks := locksOn(t, recorder, lock.Policy{Retries: 1})

	if err := locks.Take(t.Context(), transaction(t, recorder), lock.Exclusively(lock.KeyFrom(1))); err != nil {
		t.Fatal(err)
	}

	if got := recorder.SQL(); len(got) != 1 {
		t.Fatalf("expected only the lock, got %v", got)
	}
}

func TestTheTimeoutIsSetOnceHoweverManyGuardsAreTaken(t *testing.T) {
	recorder := crudtest.Postgres()
	locks := locksOn(t, recorder, lock.Policy{Timeout: time.Second, Retries: 1})

	err := locks.Take(t.Context(), transaction(t, recorder),
		lock.Exclusively(lock.KeyFrom(3)), lock.Sharing(lock.KeyFrom(1)), lock.Exclusively(lock.KeyFrom(2)))
	if err != nil {
		t.Fatal(err)
	}

	var timeouts int
	for _, statement := range recorder.SQL() {
		if strings.Contains(statement, "lock_timeout") {
			timeouts++
		}
	}
	if timeouts != 1 {
		t.Fatalf("three guards set lock_timeout %d times; it is transaction-wide and once is enough: %v",
			timeouts, recorder.SQL())
	}
}

func TestASharedGuardWaitsOnlyForAnExclusiveHolder(t *testing.T) {
	shared := crudtest.Postgres()
	if err := locksOn(t, shared, lock.Policy{}).Take(t.Context(), transaction(t, shared), lock.Sharing(lock.KeyFrom(7))); err != nil {
		t.Fatal(err)
	}
	if got := shared.SQL(); len(got) != 1 || !strings.Contains(got[0], "pg_advisory_xact_lock_shared") {
		t.Fatalf("a shared guard ran %v; two readers of the same decision would queue behind each other", got)
	}

	exclusive := crudtest.Postgres()
	if err := locksOn(t, exclusive, lock.Policy{}).Take(t.Context(), transaction(t, exclusive), lock.Exclusively(lock.KeyFrom(7))); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(exclusive.SQL()[0], "shared") {
		t.Fatalf("an exclusive guard ran %v", exclusive.SQL())
	}
}

func TestGuardsAreTakenInOneOrderWhicheverOrderTheCallerAsked(t *testing.T) {
	ascending := crudtest.Postgres()
	if err := locksOn(t, ascending, lock.Policy{}).Take(t.Context(), transaction(t, ascending),
		lock.Exclusively(lock.KeyFrom(1)), lock.Exclusively(lock.KeyFrom(2))); err != nil {
		t.Fatal(err)
	}

	descending := crudtest.Postgres()
	if err := locksOn(t, descending, lock.Policy{}).Take(t.Context(), transaction(t, descending),
		lock.Exclusively(lock.KeyFrom(2)), lock.Exclusively(lock.KeyFrom(1))); err != nil {
		t.Fatal(err)
	}

	first := ascending.Statements()
	second := descending.Statements()
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("expected two locks each, got %v and %v", ascending.SQL(), descending.SQL())
	}
	for index := range first {
		if first[index].Args[0] != second[index].Args[0] {
			t.Fatalf("two callers asking for the same pair took it in different orders (%v then %v); "+
				"that is the deadlock this ordering exists to prevent",
				[]any{first[0].Args[0], first[1].Args[0]}, []any{second[0].Args[0], second[1].Args[0]})
		}
	}
}

func TestTheSameKeyAskedForBothWaysIsTakenOnceAndExclusively(t *testing.T) {
	recorder := crudtest.Postgres()

	err := locksOn(t, recorder, lock.Policy{}).Take(t.Context(), transaction(t, recorder),
		lock.Sharing(lock.KeyFrom(9)), lock.Exclusively(lock.KeyFrom(9)))
	if err != nil {
		t.Fatal(err)
	}

	statements := recorder.SQL()
	if len(statements) != 1 {
		t.Fatalf("one key was locked %d times: %v", len(statements), statements)
	}
	if strings.Contains(statements[0], "shared") {
		t.Fatalf("a key asked for both ways was taken as %v; the shared hold does not cover the "+
			"exclusive one the caller also asked for", statements)
	}
}

func TestAnEngineWithoutTheseSemanticsIsRefusedRatherThanAnsweredWeakly(t *testing.T) {
	mysql := crudtest.MySQL()

	locks, err := lock.For(mysql, lock.Policy{Timeout: time.Second, Retries: 1})

	if !errors.Is(err, lock.ErrDialectUnsupported) {
		t.Fatalf("a MySQL source answered %v, want ErrDialectUnsupported: GET_LOCK is session-scoped "+
			"and exclusive-only, so a caller that got it would not have what it asked for", err)
	}
	if locks != nil {
		t.Fatal("a refused engine still handed back something to lock with")
	}
	if got := mysql.SQL(); len(got) != 0 {
		t.Fatalf("a refused engine was still sent %d statements: %v", len(got), got)
	}

	// The control: without it this test also passes for a For that refuses everything.
	if _, err := lock.For(crudtest.Postgres(), lock.Policy{}); err != nil {
		t.Fatalf("PostgreSQL was refused too: %v", err)
	}
}

func TestTwoDatabasesDoNotShareACriticalSection(t *testing.T) {
	first := crudtest.Postgres()
	second := crudtest.Postgres()
	key := lock.KeyOf("the same name in both")

	if err := locksOn(t, first, lock.Policy{}).Take(t.Context(), transaction(t, first), lock.Exclusively(key)); err != nil {
		t.Fatal(err)
	}
	if err := locksOn(t, second, lock.Policy{}).Take(t.Context(), transaction(t, second), lock.Exclusively(key)); err != nil {
		t.Fatal(err)
	}

	if len(first.SQL()) != 1 || len(second.SQL()) != 1 {
		t.Fatalf("an advisory lock is scoped to one database, so each source should have seen its own "+
			"single statement; got %v and %v", first.SQL(), second.SQL())
	}

	// The control: one source behind two Locks is one critical section, and both statements land on it.
	shared := crudtest.Postgres()
	if err := locksOn(t, shared, lock.Policy{}).Take(t.Context(), transaction(t, shared), lock.Exclusively(key)); err != nil {
		t.Fatal(err)
	}
	if err := locksOn(t, shared, lock.Policy{}).Take(t.Context(), transaction(t, shared), lock.Exclusively(key)); err != nil {
		t.Fatal(err)
	}
	if len(shared.SQL()) != 2 {
		t.Fatalf("two Locks over one source sent %v; if these do not meet, nothing here excludes anything",
			shared.SQL())
	}
}

func TestKeysMadeOfDifferentPartsDiffer(t *testing.T) {
	if lock.KeyOf("a", "bc") == lock.KeyOf("ab", "c") {
		t.Fatal("two different names produced the same key; the parts are not separated")
	}
	if lock.KeyOf("scope", "1") != lock.KeyOf("scope", "1") {
		t.Fatal("the same name produced two keys")
	}
}

// A key is a name resolved to a number, and the number is the whole agreement between two processes
// that have never met. Changing how it is derived does not fail anything at build time and does not
// fail at run time either: during a rolling deploy the old pods and the new ones simply stop
// excluding each other, silently. These literals are what makes that a test failure instead.
func TestKeyOfStillAnswersWhatItAlwaysAnswered(t *testing.T) {
	for _, c := range []struct {
		parts []string
		want  int64
	}{
		{[]string{"language", "offered"}, 6671992922938116168},
		{[]string{"contract", "00000000-0000-0000-0000-000000000000"}, 6006408360897102501},
		{[]string{"structure/publish", "00000000-0000-0000-0000-000000000000"}, -4725325005165946246},
		{[]string{"a", "bc"}, -6106610056285866717},
		{[]string{"ab", "c"}, -188658036487747481},
	} {
		if got := lock.KeyOf(c.parts...); int64(got) != c.want {
			t.Errorf("KeyOf%v = %d, want %d — every holder of this lock that has not been redeployed "+
				"is now locking a different number", c.parts, int64(got), c.want)
		}
	}
}

func TestTheStatementsAreTheOnesEveryHolderOfTheseLocksAlreadySpeaks(t *testing.T) {
	for _, c := range []struct {
		name  string
		guard lock.Guard
		want  string
	}{
		{"exclusive", lock.Exclusively(lock.KeyFrom(5)), `SELECT pg_advisory_xact_lock($1)`},
		{"shared", lock.Sharing(lock.KeyFrom(5)), `SELECT pg_advisory_xact_lock_shared($1)`},
	} {
		recorder := crudtest.Postgres()
		if err := locksOn(t, recorder, lock.Policy{}).Take(t.Context(), transaction(t, recorder), c.guard); err != nil {
			t.Fatal(err)
		}
		if got := recorder.SQL()[0]; got != c.want {
			t.Errorf("%s emitted %q, want %q", c.name, got, c.want)
		}
	}

	recorder := crudtest.Postgres()
	locks := locksOn(t, recorder, lock.Policy{Timeout: 10 * time.Second})
	if err := locks.Take(t.Context(), transaction(t, recorder), lock.Exclusively(lock.KeyFrom(5))); err != nil {
		t.Fatal(err)
	}
	if got := recorder.SQL()[0]; got != `SELECT set_config('lock_timeout', $1, true)` {
		t.Errorf("the timeout was set with %q; a statement that is not transaction-local leaks the "+
			"setting into every later statement on this connection", got)
	}
}

func TestTryTakeAnswersWhetherItGotTheLock(t *testing.T) {
	for _, c := range []struct {
		name string
		row  any
		want bool
	}{
		{"taken", true, true},
		{"held by somebody else", false, false},
	} {
		recorder := crudtest.Postgres().Push(crudtest.Rows([]any{c.row}))
		locks := locksOn(t, recorder, lock.Policy{Timeout: time.Second})

		got, err := locks.TryTake(t.Context(), transaction(t, recorder), lock.Exclusively(lock.KeyFrom(4)))

		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: TryTake = %v, want %v", c.name, got, c.want)
		}
		if statement := recorder.SQL()[0]; !strings.Contains(statement, "pg_try_advisory_xact_lock") {
			t.Errorf("%s: TryTake ran %q; a blocking lock here would stall the sweep it exists to skip",
				c.name, statement)
		}
	}
}

func TestRetryableRecognisesTheFailuresThatGoAwayOnTheirOwn(t *testing.T) {
	locks := locksOn(t, crudtest.Postgres(), lock.Policy{})

	cases := []struct {
		code string
		want bool
	}{
		{"40P01", true},  // deadlock_detected
		{"55P03", true},  // lock_not_available, which is what lock_timeout gives
		{"40001", true},  // serialization_failure
		{"25P02", true},  // every statement after the first failure inside a transaction
		{"23505", false}, // a unique violation is not going to fix itself
		{"42601", false}, // and neither is a syntax error
	}
	// Both shapes one failure arrives in: the plain wrapping Take does, and the fault a repository
	// hands back once the classifier has read it. A predicate that reads only one of them stops
	// retrying the moment the statement that failed came from a repository rather than from here.
	for _, c := range cases {
		driver := engineError{state: c.code}
		shapes := map[string]error{
			"wrapped":    errors.Join(errors.New("wrapped"), driver),
			"classified": sqlfault.Wrap(sqlfault.New("postgres"), driver),
		}
		for name, err := range shapes {
			if got := locks.Retryable(err); got != c.want {
				t.Errorf("Retryable(%s, %s) = %v, want %v", c.code, name, got, c.want)
			}
		}
	}
	if locks.Retryable(errors.New("something else entirely")) {
		t.Error("a plain error was called retryable")
	}
	if locks.Retryable(nil) {
		t.Error("nil was called retryable")
	}
}

// The 503 a caller sees instead of a 500 when two writers meet on one key rests on three things
// that live in different packages: the classifier reading the driver's answer, Take wrapping it
// with %w rather than %v, and the boundary reading the fault back out of the chain. Nothing else
// pins that, and any one of the three is a quiet edit away from turning the answer into a 500.
func TestALockFailureIsAnswerableAsRetryableAtTheBoundary(t *testing.T) {
	timedOut := sqlfault.Wrap(sqlfault.New("postgres"), engineError{state: "55P03"})
	recorder := crudtest.Postgres()
	recorder.Fail(timedOut)
	locks := locksOn(t, recorder, lock.Policy{Retries: 1})

	err := locks.Take(t.Context(), transaction(t, recorder), lock.Exclusively(lock.KeyOf("one")))

	if err == nil {
		t.Fatal("a lock that timed out was reported as taken")
	}
	if kind := port.KindOf(err); kind != errs.KindRetryable {
		t.Fatalf("a lock timeout is answered as %v; the caller is told to change something it cannot", kind)
	}
	if code := locks.RetryCode(err); code != errs.CodeLockTimeout {
		t.Fatalf("RetryCode = %q, want %q", code, errs.CodeLockTimeout)
	}
}

func TestGuardedRunsTheWholeTransactionAgainWhenTheEngineSaysToTryAgain(t *testing.T) {
	recorder := crudtest.Postgres()
	recorder.Fail(sqlfault.Wrap(sqlfault.New("postgres"), engineError{state: "40P01"}))
	locks := locksOn(t, recorder, lock.Policy{Retries: 3})

	var ran int
	err := locks.Guarded(t.Context(), []lock.Guard{lock.Exclusively(lock.KeyFrom(11))},
		func(context.Context) error {
			ran++
			return nil
		})

	if err != nil {
		t.Fatalf("a deadlock on the first attempt was not retried: %v", err)
	}
	if ran != 1 {
		t.Fatalf("the body ran %d times; the attempt that deadlocked never reached it", ran)
	}
	if recorder.TxDepth() != 2 {
		t.Fatalf("the retry opened %d transactions; a retry that reuses the poisoned one is not a retry",
			recorder.TxDepth())
	}
}

func TestGuardedHandsBackWhatASecondAttemptWillNotFix(t *testing.T) {
	recorder := crudtest.Postgres()
	recorder.Fail(sqlfault.Wrap(sqlfault.New("postgres"), engineError{state: "23505"}))
	locks := locksOn(t, recorder, lock.Policy{Retries: 3})

	err := locks.Guarded(t.Context(), []lock.Guard{lock.Exclusively(lock.KeyFrom(11))},
		func(context.Context) error { return nil })

	if err == nil {
		t.Fatal("a unique violation was swallowed by the retry loop")
	}
	if recorder.TxDepth() != 1 {
		t.Fatalf("a failure that cannot fix itself was attempted %d times", recorder.TxDepth())
	}
}

func TestGuardedStopsRetryingWhenTheCallerGivesUp(t *testing.T) {
	recorder := crudtest.Postgres()
	locks := locksOn(t, recorder, lock.Policy{Timeout: time.Second, Retries: 5})
	ctx, cancel := context.WithCancel(t.Context())

	err := locks.Guarded(ctx, []lock.Guard{lock.Exclusively(lock.KeyFrom(12))},
		func(context.Context) error {
			cancel()
			return sqlfault.Wrap(sqlfault.New("postgres"), engineError{state: "40P01"})
		})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled caller was answered %v; the retry slept on regardless", err)
	}
	if recorder.TxDepth() != 1 {
		t.Fatalf("a cancelled caller still got %d attempts", recorder.TxDepth())
	}
}
