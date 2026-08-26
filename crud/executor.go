// Package crud holds the contracts and value types shared by every layer of
// vv: the datasource seam (Executor/Source/Dialect), the model metadata
// reader, the predicate AST, pagination types and the three-state Opt.
//
// It has zero dependencies outside the standard library — deliberately. Only
// two things ever cross the abstraction boundary: "run this statement" and
// "give me rows". Scanning stays with the mapper, dialect stays with the
// repository. That is why any foreign transaction can be pushed into a context:
// all vv asks of it is Exec and Query.
package crud

import (
	"context"
	"reflect"
	"sync/atomic"
)

// Rows is the minimal cursor vv needs. pgx.Rows satisfies it as-is;
// *sql.Rows needs a two-line wrapper because its Close returns an error.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

// Result is what a statement reports back. LastInsertID is only meaningful on
// drivers that expose it (MySQL); PostgreSQL uses RETURNING instead.
type Result struct {
	RowsAffected    int64
	LastInsertID    int64
	HasLastInsertID bool
}

// Executor is everything vv requires from a connection, a pool or a
// foreign transaction.
type Executor interface {
	Exec(ctx context.Context, query string, args ...any) (Result, error)
	Query(ctx context.Context, query string, args ...any) (Rows, error)
}

// Tx is an Executor with a lifetime.
type Tx interface {
	Executor
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Beginner is implemented by executors that can start their own transaction.
// It is optional: an Executor handed over by a foreign framework usually is not
// one, and vv simply joins whatever transaction it was given.
type Beginner interface {
	Begin(ctx context.Context) (Tx, error)
}

// Source is what an adapter returns: an executor that also knows its dialect.
type Source interface {
	Executor
	Dialect() Dialect
}

// ReadSourcer is the optional interface a Source implements to offer a second,
// read-only datasource. A repository asks for it once, at Bind time.
type ReadSourcer interface {
	ReadSource() Source
}

// ReadWrite pairs a writable datasource with a replica: reads that only read go
// to the replica, everything else to the primary.
//
// Two things are deliberately not negotiable, and both exist because a replica
// is *behind*. A read made with a context that carries an executor goes to that
// executor — joining a transaction and then reading around it would defeat the
// transaction, and it is also what keeps read-your-own-writes working inside
// one. And a read marked PrimaryOnly stays on the primary, which is what the
// repository does for the load half of an Update and the security gate does for
// its checks: a decision taken against a lagging row is taken against a row as
// it was, not as it is.
//
// What is left is the caller's, and cannot be solved here: write, then read in a
// separate call before the replica has caught up, and the row is missing. Wrap
// the pair in a transaction, or read with PrimaryOnly.
func ReadWrite(primary, replica Source) Source {
	if replica == nil {
		return primary
	}
	rw := readWrite{Source: primary, replica: replica}
	// Two types rather than one that always has Begin and sometimes refuses:
	// a caller testing src.(crud.Beginner) is asking whether transactions work,
	// and a wrapper that answers yes and then says no has lied about the pool it
	// is standing in front of.
	if b, ok := BeginnerOf(primary); ok {
		return readWriteTx{readWrite: rw, Beginner: b}
	}
	return rw
}

type readWrite struct {
	Source
	replica Source
}

func (rw readWrite) ReadSource() Source { return rw.replica }

// UnwrapSource hands back the primary, so a walk that reaches a ReadWrite can
// carry on through it.
//
// Without this the pair is the end of every walk: unwrapSource stops at any
// layer that cannot say what it wraps, so a ReadWrite over an instrumented
// primary answered for nothing underneath it. This type is itself a wrapper and
// has to obey the rule it was written before ([[D-061]]).
func (rw readWrite) UnwrapSource() Source { return rw.Source }

// DataSource forwards the primary's identity, so a scoped executor binding still
// matches a repository bound through the pair.
//
// The walk and not a bare assertion, for the reason [[D-061]] gives: the primary
// may itself be wrapped — ReadWrite(tracing{db}, replica) is the ordinary shape
// once an application instruments its statements — and an assertion answers nil
// for it, which reads as "I cannot say" and silently unscopes every binding.
func (rw readWrite) DataSource() any {
	return identityOf(rw.Source)
}

type readWriteTx struct {
	readWrite
	Beginner
}

// BulkInserter is an optional fast path (pgx COPY). Nothing in the library
// reaches for it: the repository writes one statement at a time, so this is a
// door an application opens itself, by type-asserting its own source.
//
//	if bulk, ok := src.(crud.BulkInserter); ok {
//	    n, err := bulk.CopyFrom(ctx, "users", cols, rows)
//	}
type BulkInserter interface {
	CopyFrom(ctx context.Context, table string, columns []string, rows [][]any) (int64, error)
}

// Identified is the optional interface a Source implements to name the physical
// database it speaks to — the *sql.DB, the *pgxpool.Pool, whatever the adapter
// holds. Two sources that answer with the same handle are the same database, and
// that is the only question WithExecutorFor needs answered.
//
// A Source that does not implement it is simply never matched by a scoped
// binding, so it keeps the plain WithExecutor behaviour.
type Identified interface {
	DataSource() any
}

// Sourced is the optional interface a repository implements to hand back the
// datasource it was bound to. A decorator that has to run its own statement —
// the probe is the only one — needs the source to resolve an executor through
// ExecutorFor, and a decorator holds a Core, which does not carry one.
//
// It is on the concrete repository rather than on Core because a middleware
// embeds Core as an interface, and an interface embedded in a struct promotes
// only its own method set. A decorator that is not innermost therefore does not
// forward it — which is why [SourceOf] exists rather than a type assertion.
type Sourced interface {
	Source() Source
}

// Nexter is the optional interface a decorator implements to hand back the Core
// it wraps. [Base] provides it, so a decorator written the way this package
// recommends has it for free.
//
// It is what makes a chain walkable. Without it, every optional interface the
// innermost repository offers is invisible to anything above the first
// decorator — an interface embedded in a struct promotes only its own method
// set, so a wrapper erases every method the wrapped value had that Core does
// not name. That erasure is silent, which is the whole problem with it.
type Nexter[M any, ID comparable] interface {
	Next() Core[M, ID]
}

// maxChainDepth bounds the walks below. A decorator chain is built once at
// start-up and is a handful of layers deep; a walk that has taken this many
// steps is following a cycle somebody built by accident, and returning "not
// found" is a better answer than not returning.
const maxChainDepth = 64

// SourceOf answers the datasource a decorator chain is ultimately bound to.
//
// It asks each layer in turn and follows [Nexter] down when the answer is no,
// so a probe wired above a security gate finds the repository underneath it
// instead of refusing. The alternative — a plain assertion on the layer directly
// below — makes the order decorators are listed in decide whether a feature
// works, which is a requirement nothing states and nothing checks.
//
// A layer that implements neither Sourced nor Nexter ends the walk. That is
// still the honest answer for a decorator somebody wrote without either: it does
// not say what it wraps, so nothing here can say it for it.
func SourceOf[M any, ID comparable](c Core[M, ID]) (Source, bool) {
	for i := 0; c != nil && i < maxChainDepth; i++ {
		if sd, ok := c.(Sourced); ok {
			if src := sd.Source(); src != nil {
				return src, true
			}
		}
		n, ok := c.(Nexter[M, ID])
		if !ok {
			return nil, false
		}
		c = n.Next()
	}
	return nil, false
}

// SourceUnwrapper is the optional interface a Source *wrapper* implements to
// hand back the Source it decorates — the mirror of [Nexter], one level down.
//
// Wrapping a Source is how an application instruments statements, and doing it
// costs three things that are not obvious and do not announce themselves: a
// wrapper that does not forward [ReadSourcer] silently stops reads going to the
// replica, one that does not forward [Beginner] turns every Tx into
// ErrNoTxSupport, and one that does not forward [Identified] misses the catalog
// keyed on the handle. Only the third fails loudly.
//
// Implementing this one method is the whole obligation: [BeginnerOf],
// [ReadSourceOf] and [KeyOf] all follow it, so a wrapper that says what it wraps
// keeps every behaviour the wrapped Source had.
type SourceUnwrapper interface {
	UnwrapSource() Source
}

// unwrapSource walks a Source wrapper chain, yielding each layer including the
// first, so a caller can ask each one whether it implements what it needs.
func unwrapSource(v any, want func(any) bool) (any, bool) {
	for i := 0; v != nil && i < maxChainDepth; i++ {
		if want(v) {
			return v, true
		}
		u, ok := v.(SourceUnwrapper)
		if !ok {
			return nil, false
		}
		inner := u.UnwrapSource()
		if inner == nil {
			return nil, false
		}
		v = inner
	}
	return nil, false
}

// BeginnerOf answers the Beginner a Source carries, following [SourceUnwrapper]
// through any wrapper that says what it wraps.
//
// A wrapper that forwards Exec and Query but not Begin is the ordinary result of
// instrumenting a datasource, and without this every transaction through it
// would fail with ErrNoTxSupport for a reason nothing in the message names.
func BeginnerOf(v any) (Beginner, bool) {
	found, ok := unwrapSource(v, func(x any) bool { _, is := x.(Beginner); return is })
	if !ok {
		return nil, false
	}
	return found.(Beginner), true
}

// ReadSourceOf answers the read-only datasource a Source offers, following
// [SourceUnwrapper] through any wrapper.
//
// This is the one of the three whose loss is silent: a wrapped ReadWrite that
// does not forward ReadSourcer simply sends every read to the primary, and the
// replica sits idle with nothing anywhere saying why.
func ReadSourceOf(v any) (Source, bool) {
	found, ok := unwrapSource(v, func(x any) bool { _, is := x.(ReadSourcer); return is })
	if !ok {
		return nil, false
	}
	return found.(ReadSourcer).ReadSource(), true
}

type ctxKey struct{}

// binding is one executor pushed into a context. ds is the datasource it belongs
// to, or nil for "whoever asks". They chain rather than replace so an inner
// scoped binding cannot hide an outer unscoped one from a different repository.
//
// owned says vv opened this transaction. Nothing in the seam needed the
// answer until something wanted to take a savepoint inside it: issuing
// ROLLBACK TO SAVEPOINT in the middle of somebody else's unit of work can
// discard work its owner has not finished with, and WithExecutor and InTx are
// otherwise indistinguishable from the inside.
//
// saves counts the savepoints claimed against this transaction. It counts up
// and never down, which is the shape of the limit rather than an oversight:
// PostgreSQL's 64-entry subxid cache overflows on the number of subtransactions
// a top-level transaction has assigned XIDs to, and releasing a savepoint does
// not give the entry back. The overflow is not a round trip — it forces
// pg_subtrans lookups on every reader in the cluster.
type binding struct {
	ds    any
	e     Executor
	prev  *binding
	owned bool
	saves atomic.Int64
}

func push(ctx context.Context, ds any, e Executor, owned bool) context.Context {
	prev, _ := ctx.Value(ctxKey{}).(*binding)
	return context.WithValue(ctx, ctxKey{}, &binding{ds: ds, e: e, prev: prev, owned: owned})
}

// WithExecutor pushes a foreign executor (usually somebody else's transaction)
// into the context. Every repository call made with that context runs on it,
// whatever datasource that repository was bound to. This is the single interop
// point of the whole library, and it is deliberately unconditional: the executor
// an ent or gorm transaction hands over has no relationship to the source a
// repository holds, so no check could pass.
//
// When a process talks to more than one database, name the one you mean with
// WithExecutorFor instead.
func WithExecutor(ctx context.Context, e Executor) context.Context {
	return push(ctx, nil, e, false)
}

// WithExecutorFor is WithExecutor scoped to one database. Only repositories
// bound to ds run on e; a repository bound to anything else ignores it and keeps
// using its own datasource.
//
// ds is either the raw handle or any Source over it — both name the same
// database:
//
//	tx, _ := mainDB.BeginTx(ctx, nil)
//	ctx = crud.WithExecutorFor(ctx, mainDB, crudsql.From(tx))
//
//	users.Save(ctx, &u)    // bound to mainDB      — runs in tx
//	events.Save(ctx, &e)   // bound to analyticsDB — runs on analyticsDB
//
// With a plain WithExecutor that second call would have gone to mainDB, inside
// the transaction, and reported success.
func WithExecutorFor(ctx context.Context, ds any, e Executor) context.Context {
	return push(ctx, KeyOf(ds), e, false)
}

// ExecutorFrom returns the innermost executor bound to ctx, scoped or not. It
// answers "is there a transaction here at all"; to ask the question a repository
// actually asks — "is there one for MY database" — use ExecutorFor.
func ExecutorFrom(ctx context.Context) (Executor, bool) {
	b, ok := ctx.Value(ctxKey{}).(*binding)
	if !ok {
		return nil, false
	}
	return b.e, true
}

// ExecutorFor returns the executor a repository bound to src should run on: the
// innermost binding scoped to src's datasource, or failing that the innermost
// unscoped one. src may be the Source itself or the raw handle.
func ExecutorFor(ctx context.Context, src any) (Executor, bool) {
	e, found, _ := OwnedExecutorFor(ctx, src)
	return e, found
}

// OwnedExecutorFor is ExecutorFor with the answer to the second question:
// whether vv opened the transaction it found. One walk rather than two, so
// found and owned cannot disagree about which binding they describe.
//
// Ask this rather than ExecutorFrom. With a foreign transaction scoped to
// another handle, ExecutorFrom says "in a transaction" while this repository's
// write runs outside one.
func OwnedExecutorFor(ctx context.Context, src any) (e Executor, found, owned bool) {
	b := bindingFor(ctx, src)
	if b == nil {
		return nil, false, false
	}
	return b.e, true, b.owned
}

// ClaimSavepoint reserves one savepoint against the transaction a repository
// bound to src is running in, and reports how many have been claimed against it
// including this one. It answers false when there is no transaction here, or
// when the one there is belongs to somebody else.
//
// The budget lives with the transaction and the limit lives with the caller:
// two repositories sharing one transaction share one count, and two
// transactions do not.
func ClaimSavepoint(ctx context.Context, src any) (int64, bool) {
	b := bindingFor(ctx, src)
	if b == nil || !b.owned {
		return 0, false
	}
	return b.saves.Add(1), true
}

func bindingFor(ctx context.Context, src any) *binding {
	b, ok := ctx.Value(ctxKey{}).(*binding)
	if !ok {
		return nil
	}
	want := KeyOf(src)
	var fallback *binding
	for ; b != nil; b = b.prev {
		if b.ds == nil {
			if fallback == nil {
				fallback = b
			}
			continue
		}
		if SameDataSource(b.ds, want) {
			return b
		}
	}
	return fallback
}

// KeyOf reduces a Source or a raw handle to the value that identifies the
// database, so both spellings land on the same key. Naming something that
// cannot identify itself is taken at face value — the caller said it, so it is
// the key.
func KeyOf(v any) any {
	if ds := identityOf(v); ds != nil {
		return ds
	}
	return v
}

// ownScope is KeyOf for a transaction vv opens itself, where nobody named
// anything. A source that cannot say which database it is gets an unscoped
// binding — the old, unconditional join — because the alternative is worse:
// scoping it to itself would quietly stop a sibling repository from joining a
// transaction it used to join, and a write landing outside the transaction is
// no better than one landing in the wrong database.
//
// It walks wrappers, and it has to. InTx resolves the Beginner with [BeginnerOf],
// which walks — so a wrapped source opens its transaction — and this decides
// what that transaction is scoped to. With a bare assertion the two disagreed:
// the transaction opened and then bound unscoped, and every repository in the
// process adopted it, including ones bound to another database. That is
// [[D-027]]'s territory reached by accident, through the wrapper [[D-062]]
// recommends, with nothing said. Before the walk existed the same wrapper was
// refused at InTx with ErrNoTxSupport, which was wrong but loud.
//
// A wrapped source is not an unidentified one, so this does not bend [[D-009]]:
// nil still means the chain really cannot name a database.
func ownScope(v any) any {
	return identityOf(v)
}

// identityOf answers the innermost datasource identity a value can name, or nil.
//
// The one walk [KeyOf], [ownScope] and readWrite.DataSource all share, so the
// three cannot disagree about what a wrapped source is — which is exactly how
// they came to disagree.
func identityOf(v any) any {
	found, ok := unwrapSource(v, func(x any) bool {
		id, is := x.(Identified)
		// A wrapper that forwards an identity it does not have answers nil; that
		// is "I cannot say", not "my identity is nil", so the walk keeps going.
		return is && id.DataSource() != nil
	})
	if !ok {
		return nil
	}
	return found.(Identified).DataSource()
}

var (
	_ Source      = readWrite{}
	_ ReadSourcer = readWrite{}
	_ Identified  = readWrite{}
	_ Beginner    = readWriteTx{}
)

// SameDataSource compares two identities without panicking on an uncomparable
// one — a datasource handle is a pointer in practice, but nothing in the
// contract says it must be.
//
// It asks reflect, so it answers about the *static* type, which is as far as it
// can see: a struct holding an interface is comparable and == on it still panics
// once that interface turns out to hold a slice. A caller that must not panic on
// anything a user hands it guards the comparison itself — catalog does, and
// [[D-041]] says why.
func SameDataSource(a, b any) bool {
	if a == nil || b == nil {
		return false
	}
	ta, tb := reflect.TypeOf(a), reflect.TypeOf(b)
	if ta != tb || !ta.Comparable() {
		return false
	}
	return a == b
}

// InTx runs fn inside a transaction of src. If ctx already carries an executor
// src would run on, fn simply joins it — no nested transaction is started and
// the outer owner keeps control of commit and rollback.
//
// The transaction it opens is bound to src's own datasource when src is
// Identified, so it reaches every repository over that database and no others.
// A source that cannot name its database binds unscoped, which is what an
// adapter written outside this repository gets: the old, unconditional join.
//
// Use this to span several repositories with one transaction:
//
//	crud.InTx(ctx, db, func(ctx context.Context) error {
//	    if err := users.Save(ctx, &u); err != nil { return err }
//	    return orders.Save(ctx, &o)
//	})
func InTx(ctx context.Context, src Executor, fn func(context.Context) error) (err error) {
	if _, ok := ExecutorFor(ctx, src); ok {
		return fn(ctx)
	}
	b, ok := BeginnerOf(src)
	if !ok {
		return ErrNoTxSupport
	}
	tx, err := b.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()
	if err := fn(push(ctx, ownScope(src), tx, true)); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			return errJoin(err, rbErr)
		}
		return err
	}
	return tx.Commit(ctx)
}
