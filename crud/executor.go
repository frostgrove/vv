package crud

import (
	"context"
	"reflect"
	"sync/atomic"
	"time"
)

type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

type Result struct {
	RowsAffected    int64
	LastInsertID    int64
	HasLastInsertID bool
}

type Executor interface {
	Exec(ctx context.Context, query string, args ...any) (Result, error)
	Query(ctx context.Context, query string, args ...any) (Rows, error)
}

type Tx interface {
	Executor
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type Transactional interface {
	InTransaction() bool
}

func IsTransaction(e Executor) bool {
	if isNilValue(e) {
		return false
	}
	_, ok := unwrapSource(e, func(v any) bool {
		if _, tx := v.(Tx); tx {
			return true
		}
		t, transactional := v.(Transactional)
		return transactional && t.InTransaction()
	})
	return ok
}

type Beginner interface {
	Begin(ctx context.Context) (Tx, error)
}

type Source interface {
	Executor
	Dialect() Dialect
}

type ReadSourcer interface {
	ReadSource() Source
}

func ReadWrite(primary, replica Source) Source {
	if replica == nil {
		return primary
	}
	rw := readWrite{Source: primary, replica: replica}

	if b, ok := BeginnerOf(primary); ok {
		return readWriteTx{readWrite: rw, Beginner: b}
	}
	return rw
}

type readWrite struct {
	Source
	replica Source
}

func (this readWrite) ReadSource() Source { return this.replica }

func (this readWrite) UnsafeBulkInsert(ctx context.Context, target Executor, table TableRef, columns []string, rows [][]any) (int64, error) {
	bulk, ok := UnsafeBulkInserterOf(this.Source)
	if !ok {
		return 0, ErrNoBulkInsertSupport
	}
	return bulk.UnsafeBulkInsert(ctx, target, table, columns, rows)
}

func (this readWrite) UnwrapSource() Source { return this.Source }

func (this readWrite) DataSource() any {
	return identityOf(this.Source)
}

type readWriteTx struct {
	readWrite
	Beginner
}

type UnsafeBulkInserter interface {
	UnsafeBulkInsert(ctx context.Context, target Executor, table TableRef, columns []string, rows [][]any) (int64, error)
}

type Identified interface {
	DataSource() any
}

type Sourced interface {
	Source() Source
}

type Nexter[M any, ID comparable] interface {
	Next() Core[M, ID]
}

const maxChainDepth = 64

func SourceOf[M any, ID comparable](c Core[M, ID]) (Source, bool) {
	for i := 0; c != nil && i < maxChainDepth; i++ {
		if sd, ok := c.(Sourced); ok {
			if source := sd.Source(); source != nil {
				return source, true
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

type UnscopedExister[M any, ID comparable] interface {
	ExistsUnscoped(ctx context.Context, options ...Option) (bool, error)
}

func ExistsUnscopedOf[M any, ID comparable](c Core[M, ID], ctx context.Context, options ...Option) (bool, error, bool) {
	for i := 0; c != nil && i < maxChainDepth; i++ {
		if x, ok := c.(UnscopedExister[M, ID]); ok {
			found, err := x.ExistsUnscoped(ctx, options...)
			return found, err, true
		}
		n, ok := c.(Nexter[M, ID])
		if !ok {
			return false, nil, false
		}
		c = n.Next()
	}
	return false, nil, false
}

type ScopedSave[M any] struct {
	Previous       *M
	Scope          Predicate
	RelationScopes *RelationScopes
}

type ScopedSaver[M any, ID comparable] interface {
	SaveScoped(ctx context.Context, m *M, save *ScopedSave[M]) error
}

type ScopedSaveOnlyer[M any, ID comparable] interface {
	SaveScopedOnly(ctx context.Context, m *M, save *ScopedSave[M]) error
}

type ScopedDelete[ID comparable] struct {
	IDs            []ID
	Scope          Predicate
	RelationScopes *RelationScopes
	Snapshots      map[ID]Predicate
}

type ScopedDeleter[M any, ID comparable] interface {
	DeleteScoped(ctx context.Context, deletion *ScopedDelete[ID]) (int64, error)
}

func SaveScopedOf[M any, ID comparable](c Core[M, ID], ctx context.Context, m *M, save *ScopedSave[M]) (error, bool) {
	if save == nil {
		return ErrBadRequest, true
	}
	if s, ok := c.(ScopedSaver[M, ID]); ok {
		return s.SaveScoped(ctx, m, save), true
	}
	return nil, false
}

func SaveScopedOnlyOf[M any, ID comparable](c Core[M, ID], ctx context.Context, m *M, save *ScopedSave[M]) (error, bool) {
	if save == nil {
		return ErrBadRequest, true
	}
	if s, ok := c.(ScopedSaveOnlyer[M, ID]); ok {
		return s.SaveScopedOnly(ctx, m, save), true
	}
	return nil, false
}

func DeleteScopedOf[M any, ID comparable](c Core[M, ID], ctx context.Context, deletion *ScopedDelete[ID]) (int64, error, bool) {
	if deletion == nil {
		return 0, ErrBadRequest, true
	}
	if d, ok := c.(ScopedDeleter[M, ID]); ok {
		n, err := d.DeleteScoped(ctx, deletion)
		return n, err, true
	}
	return 0, nil, false
}

type SourceUnwrapper interface {
	UnwrapSource() Source
}

func unwrapSource(v any, want func(any) bool) (any, bool) {
	for i := 0; !isNilValue(v) && i < maxChainDepth; i++ {
		if want(v) {
			return v, true
		}
		u, ok := v.(SourceUnwrapper)
		if !ok {
			return nil, false
		}
		inner := u.UnwrapSource()
		if isNilValue(inner) {
			return nil, false
		}
		v = inner
	}
	return nil, false
}

func BeginnerOf(v any) (Beginner, bool) {
	found, ok := unwrapSource(v, func(x any) bool { _, is := x.(Beginner); return is })
	if !ok {
		return nil, false
	}
	return found.(Beginner), true
}

func ReadSourceOf(v any) (Source, bool) {
	found, ok := unwrapSource(v, func(x any) bool { _, is := x.(ReadSourcer); return is })
	if !ok {
		return nil, false
	}
	return found.(ReadSourcer).ReadSource(), true
}

func UnsafeBulkInserterOf(v any) (UnsafeBulkInserter, bool) {
	if isNilValue(v) {
		return nil, false
	}
	inserter, ok := v.(UnsafeBulkInserter)
	return inserter, ok && !isNilValue(inserter)
}

type ctxKey struct{}

type binding struct {
	ds     any
	e      Executor
	prev   *binding
	owned  bool
	strict bool
	err    error
	saves  atomic.Int64
}

func push(ctx context.Context, ds any, e Executor, owned, strict bool) context.Context {
	prev, _ := ctx.Value(ctxKey{}).(*binding)
	return context.WithValue(ctx, ctxKey{}, &binding{ds: ds, e: e, prev: prev, owned: owned, strict: strict})
}

func pushFailure(ctx context.Context, err error) context.Context {
	prev, _ := ctx.Value(ctxKey{}).(*binding)
	return context.WithValue(ctx, ctxKey{}, &binding{prev: prev, strict: true, err: err})
}

type Session struct {
	ds    any
	e     Executor
	ready bool
}

func NewSession(source Source, e Executor) (Session, error) {
	if isNilValue(source) {
		return Session{}, scopeError(ExecutorScopeMissingSource)
	}
	if err := validateExecutor(e); err != nil {
		return Session{}, err
	}
	if IsTransaction(source) {
		return Session{}, scopeError(ExecutorScopeTransactionSource)
	}
	ds := identityOf(source)
	if isNilValue(ds) {
		return Session{}, scopeError(ExecutorScopeMissingSource)
	}
	if !comparableIdentity(ds) {
		return Session{}, scopeError(ExecutorScopeInvalidSource)
	}
	return Session{ds: ds, e: e, ready: true}, nil
}

func MustSession(source Source, e Executor) Session {
	session, err := NewSession(source, e)
	if err != nil {
		panic(err)
	}
	return session
}

func (this Session) Bind(ctx context.Context) context.Context {
	if !this.ready || !comparableIdentity(this.ds) || isNilValue(this.e) {
		return pushFailure(ctx, scopeError(ExecutorScopeInvalidSession))
	}
	return push(ctx, this.ds, this.e, false, false)
}

func BindExecutor(ctx context.Context, source Source, e Executor) context.Context {
	session, err := NewSession(source, e)
	if err != nil {
		return pushFailure(ctx, err)
	}
	return session.Bind(ctx)
}

func WithExecutor(ctx context.Context, e Executor) context.Context {
	if err := validateExecutor(e); err != nil {
		return pushFailure(ctx, err)
	}
	ds := KeyOf(e)
	if !comparableIdentity(ds) {
		return pushFailure(ctx, scopeError(ExecutorScopeInvalidSource))
	}
	return push(ctx, ds, e, false, true)
}

func WithUnsafeExecutor(ctx context.Context, e Executor) context.Context {
	if err := validateExecutor(e); err != nil {
		return pushFailure(ctx, err)
	}
	return push(ctx, nil, e, false, false)
}

func WithExecutorFor(ctx context.Context, ds any, e Executor) context.Context {
	if isNilValue(ds) {
		return pushFailure(ctx, scopeError(ExecutorScopeMissingSource))
	}
	if err := validateExecutor(e); err != nil {
		return pushFailure(ctx, err)
	}
	key := KeyOf(ds)
	if !comparableIdentity(key) {
		return pushFailure(ctx, scopeError(ExecutorScopeInvalidSource))
	}
	strict := IsTransaction(e) && SameDataSource(key, KeyOf(e))
	return push(ctx, key, e, false, strict)
}

func ExecutorFrom(ctx context.Context) (Executor, bool) {
	b, ok := ctx.Value(ctxKey{}).(*binding)
	if !ok {
		return nil, false
	}

	for current := b; current != nil; current = current.prev {
		if current.err != nil {
			return failedExecutor{err: current.err}, true
		}
	}
	return b.e, true
}

func ExecutorFor(ctx context.Context, source any) (Executor, bool) {
	e, found, _ := OwnedExecutorFor(ctx, source)
	return e, found
}

func UnsafeExecFor(ctx context.Context, source Source, query string, args ...any) (Result, error) {
	e := executorForSource(ctx, source)
	return e.Exec(ctx, query, args...)
}

func UnsafeQueryFor(ctx context.Context, source Source, query string, args ...any) (Rows, error) {
	e := executorForSource(ctx, source)
	return e.Query(ctx, query, args...)
}

func UnsafeBulkInsertFor(ctx context.Context, source Source, table TableRef, columns []string, rows [][]any) (int64, error) {
	if err := table.Validate(); err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if isNilValue(source) {
		return 0, scopeError(ExecutorScopeMissingSource)
	}
	var target Executor
	if resolved, ok := ExecutorFor(ctx, source); ok {
		if err := executorFailure(resolved); err != nil {
			return 0, err
		}
		target = resolved
	}
	bulk, ok := UnsafeBulkInserterOf(source)
	if !ok {
		return 0, ErrNoBulkInsertSupport
	}
	return bulk.UnsafeBulkInsert(ctx, target, table, columns, rows)
}

func executorForSource(ctx context.Context, source Source) Executor {
	if isNilValue(source) {
		return failedExecutor{err: scopeError(ExecutorScopeMissingSource)}
	}
	if e, ok := ExecutorFor(ctx, source); ok {
		return e
	}
	return source
}

func OwnedExecutorFor(ctx context.Context, source any) (e Executor, found, owned bool) {
	b, err := bindingFor(ctx, source)
	if err != nil {
		return failedExecutor{err: err}, true, false
	}
	if b == nil {
		return nil, false, false
	}
	return b.e, true, b.owned
}

func ClaimSavepoint(ctx context.Context, source any) (int64, bool) {
	b, err := bindingFor(ctx, source)
	if err != nil {
		return 0, false
	}
	if b == nil || !b.owned {
		return 0, false
	}
	return b.saves.Add(1), true
}

func bindingFor(ctx context.Context, source any) (*binding, error) {
	b, ok := ctx.Value(ctxKey{}).(*binding)
	if !ok {
		return nil, nil
	}
	want := KeyOf(source)
	var matched *binding
	var fallback *binding
	for ; b != nil; b = b.prev {
		if b.err != nil {
			return nil, b.err
		}
		if b.ds == nil {
			if fallback == nil {
				fallback = b
			}
			continue
		}
		if SameDataSource(b.ds, want) {
			if matched == nil {
				matched = b
			}

			continue
		}
		if b.strict {
			return nil, scopeError(ExecutorScopeMismatch)
		}
	}
	if matched != nil {
		return matched, nil
	}
	return fallback, nil
}

type failedExecutor struct{ err error }

func (this failedExecutor) Exec(context.Context, string, ...any) (Result, error) {
	return Result{}, this.err
}

func (this failedExecutor) Query(context.Context, string, ...any) (Rows, error) {
	return nil, this.err
}

func executorFailure(e Executor) error {
	if failed, ok := e.(failedExecutor); ok {
		return failed.err
	}
	return nil
}

func scopeError(reason ExecutorScopeReason) error {
	return &ExecutorScopeError{Reason: reason}
}

func validateExecutor(e Executor) error {
	if isNilValue(e) {
		return scopeError(ExecutorScopeMissingExecutor)
	}
	if declaresIdentity(e) && isNilValue(identityOf(e)) {
		return scopeError(ExecutorScopeMissingExecutor)
	}
	return nil
}

func isNilValue(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

func comparableIdentity(v any) bool {
	return !isNilValue(v) && reflect.ValueOf(v).Comparable()
}

func KeyOf(v any) any {
	if ds := identityOf(v); ds != nil {
		return ds
	}
	return v
}

func ownScope(v any) any {
	return identityOf(v)
}

func identityOf(v any) any {
	found, ok := unwrapSource(v, func(x any) bool {
		id, is := x.(Identified)

		return is && id.DataSource() != nil
	})
	if !ok {
		return nil
	}
	return found.(Identified).DataSource()
}

func declaresIdentity(v any) bool {
	_, ok := unwrapSource(v, func(x any) bool {
		_, identified := x.(Identified)
		return identified
	})
	return ok
}

var (
	_ Source      = readWrite{}
	_ ReadSourcer = readWrite{}
	_ Identified  = readWrite{}
	_ Beginner    = readWriteTx{}
)

func SameDataSource(a, b any) bool {
	if a == nil || b == nil {
		return false
	}
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	if va.Type() != vb.Type() || !va.Comparable() || !vb.Comparable() {
		return false
	}
	return a == b
}

func InTx(ctx context.Context, source Executor, fn func(context.Context) error) (err error) {
	if executor, ok := ExecutorFor(ctx, source); ok {
		if err := executorFailure(executor); err != nil {
			return err
		}
		return fn(ctx)
	}
	return inNewTx(ctx, source, fn)
}

func InNewTx(ctx context.Context, source Executor, fn func(context.Context) error) (err error) {
	return inNewTx(ctx, source, fn)
}

func InAtomic(ctx context.Context, source Executor, fn func(context.Context) error) error {
	if executor, ok := ExecutorFor(ctx, source); ok {
		if err := executorFailure(executor); err != nil {
			return err
		}
		if IsTransaction(executor) {
			return fn(ctx)
		}
	}
	return inNewTx(ctx, source, fn)
}

func inNewTx(ctx context.Context, source Executor, fn func(context.Context) error) (err error) {
	if executor, ok := ExecutorFor(ctx, source); ok {
		if err := executorFailure(executor); err != nil {
			return err
		}
	}
	if IsTransaction(source) {
		return scopeError(ExecutorScopeTransactionSource)
	}
	b, ok := BeginnerOf(source)
	if !ok {
		return ErrNoTxSupport
	}
	ds := ownScope(source)
	if isNilValue(ds) {
		return scopeError(ExecutorScopeMissingSource)
	}
	if !comparableIdentity(ds) {
		return scopeError(ExecutorScopeInvalidSource)
	}
	tx, err := b.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = rollback(tx, ctx)
			panic(p)
		}
	}()
	if err := fn(push(ctx, ds, tx, true, false)); err != nil {
		if rbErr := rollback(tx, ctx); rbErr != nil {
			return errJoin(err, rbErr)
		}
		return err
	}
	return tx.Commit(ctx)
}

const rollbackTimeout = 5 * time.Second

func rollback(tx Tx, ctx context.Context) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()
	return tx.Rollback(cleanup)
}
