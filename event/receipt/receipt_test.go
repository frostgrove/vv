package receipt_test

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/event"
	"github.com/frostgrove/vv/event/eventmemory"
	"github.com/frostgrove/vv/event/receipt"
)

type account struct{ Balance int64 }

type credited struct {
	Minor  int64
	Reason string
}

// One store, one log, one aggregate and one ledger over the store's own
// transaction. A second stand is a second handle on what a deployment would call
// one database, which is what the two-pool refusal is measured against.
type stand struct {
	log    *eventmemory.Log
	store  *eventmemory.Store
	repo   *event.Repo[account, string]
	credit *event.Fact[account, string, credited]
	ledger *ledger
}

func newStand(t *testing.T) *stand {
	t.Helper()
	log, err := eventmemory.NewLog(eventmemory.LogSpec{})
	if err != nil {
		t.Fatalf("the log the receipts are written beside was refused: %v", err)
	}
	store, err := eventmemory.New(eventmemory.Spec{Log: log})
	if err != nil {
		t.Fatalf("the store the receipts are written beside was refused: %v", err)
	}
	aggregate := event.Define[account]("receipts.account", func(id string) event.Key { return event.Key(id) })
	credit := event.Declare(aggregate, "receipts.credited", event.From(event.JSON[credited]()),
		func(this account, fact credited) account {
			this.Balance += fact.Minor
			return this
		})
	repo, err := event.Bind(event.Open(store), aggregate)
	if err != nil {
		t.Fatalf("the declaration the operations are decided on was not bound: %v", err)
	}
	return &stand{log: log, store: store, repo: repo, credit: credit, ledger: newLedger(store)}
}

// What crud.InNewTx is in a wiring with a database: one transaction the store,
// the ledger and the caller all run inside, committed or rolled back as a whole.
// The authority is resolved before the commit because a finished eventmemory
// transaction answers an error rather than a name.
func (this *stand) unit(ctx context.Context, work func(context.Context) error) error {
	tx, err := this.store.Begin(ctx)
	if err != nil {
		return err
	}
	inner := eventmemory.WithTransaction(ctx, tx)
	authority, err := this.store.Transaction(inner)
	if err != nil {
		return err
	}
	if err := work(inner); err != nil {
		_ = tx.Rollback(ctx)
		this.ledger.discard(authority)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		this.ledger.discard(authority)
		return err
	}
	this.ledger.commit(authority)
	return nil
}

func (this *stand) inUnit(t *testing.T, ctx context.Context, work func(context.Context) error) {
	t.Helper()
	if err := this.unit(ctx, work); err != nil {
		t.Fatalf("the unit of work answered %v where it was to commit", err)
	}
}

// A decision under one key: the load, the change, the digest and the
// fingerprint, which is every step a caller takes before it claims.
func (this *stand) decide(t *testing.T, ctx context.Context, id string, facts ...credited) (event.At[account], []event.Change[account], receipt.Fingerprint) {
	t.Helper()
	_, at, err := this.repo.Load(ctx, id)
	if err != nil {
		t.Fatalf("loading %s answered %v", id, err)
	}
	changes := make([]event.Change[account], 0, len(facts))
	for _, fact := range facts {
		changes = append(changes, this.credit.New(id, fact))
	}
	digest, err := this.repo.Digest(at, changes...)
	if err != nil {
		t.Fatalf("digesting the decision answered %v", err)
	}
	print, err := receipt.NewFingerprint(digest)
	if err != nil {
		t.Fatalf("the fingerprint of the decision was refused: %v", err)
	}
	return at, changes, print
}

func (this *stand) claimSpec(t *testing.T, key receipt.Key, at event.At[account], print receipt.Fingerprint) receipt.ClaimSpec {
	t.Helper()
	return receipt.ClaimSpec{Ledger: this.ledger, Store: this.store, Key: key, Fingerprint: print, Stream: at.Stream()}
}

func (this *stand) versionOf(t *testing.T, ctx context.Context, id string) event.Version {
	t.Helper()
	_, at, err := this.repo.Load(ctx, id)
	if err != nil {
		t.Fatalf("loading %s answered %v", id, err)
	}
	return at.Version()
}

func keyed(t *testing.T, raw string) receipt.Key {
	t.Helper()
	key, err := receipt.NewKey(raw)
	if err != nil {
		t.Fatalf("the operation key was refused: %v", err)
	}
	return key
}

// The instants the ledger stamps rows with. A counter over a fixed base rather
// than a clock: the horizon arithmetic is exact, and a case that offsets a
// caller's own instant from the ledger's is measuring the offset it chose.
type ledgerClock struct {
	mutex sync.Mutex
	base  time.Time
	nth   int
}

func (this *ledgerClock) now() time.Time {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.nth++
	return this.base.Add(time.Duration(this.nth) * time.Second)
}

// The row as a table holds it: text, not the values this package publishes, so
// the ledger writes what Key.Value and Fingerprint.String render and reads it
// back through NewKey and ParseFingerprint — which is what a SQL implementation
// binds and scans.
type row struct {
	key         string
	fingerprint string
	family      string
	streamKey   string
	first, last event.Version
	complete    bool
	recordedAt  time.Time
}

func (this row) receipt() (receipt.Receipt, error) {
	key, err := receipt.NewKey(this.key)
	if err != nil {
		return receipt.Receipt{}, err
	}
	print, err := receipt.ParseFingerprint(this.fingerprint)
	if err != nil {
		return receipt.Receipt{}, err
	}
	return receipt.Receipt{
		Key:         key,
		Fingerprint: print,
		Stream:      event.Stream{Family: this.family, Key: event.Key(this.streamKey)},
		First:       this.first,
		Last:        this.last,
		Complete:    this.complete,
		RecordedAt:  this.recordedAt,
	}, nil
}

// The reference implementation's two statements, in memory: the insert that
// takes the key or does nothing, and then the read of the same key — in that
// order, inside the caller's transaction. Staged rows are the transaction's own
// and Find never sees them, because Find runs on a second connection.
type ledger struct {
	store event.Store
	clock ledgerClock

	claims    atomic.Int64
	completes atomic.Int64

	mutex   sync.Mutex
	rows    map[string]row
	staged  map[event.Authority]map[string]row
	horizon time.Time

	transaction func(ctx context.Context) (event.Authority, error)
	onClaim     func(ctx context.Context, held receipt.Receipt) (receipt.Receipt, bool, error)
	onFind      func(ctx context.Context, key receipt.Key) (receipt.Receipt, bool, error)
	onHorizon   func(ctx context.Context) (time.Time, error)
}

func newLedger(store event.Store) *ledger {
	return &ledger{
		store:  store,
		clock:  ledgerClock{base: time.Date(2026, time.September, 12, 9, 0, 0, 0, time.UTC)},
		rows:   map[string]row{},
		staged: map[event.Authority]map[string]row{},
	}
}

func (this *ledger) Transaction(ctx context.Context) (event.Authority, error) {
	if this.transaction != nil {
		return this.transaction(ctx)
	}
	return this.store.Transaction(ctx)
}

func (this *ledger) Claim(ctx context.Context, held receipt.Receipt) (receipt.Receipt, bool, error) {
	this.claims.Add(1)
	if this.onClaim != nil {
		return this.onClaim(ctx, held)
	}
	authority, err := this.Transaction(ctx)
	if err != nil {
		return receipt.Receipt{}, false, err
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	won := false
	if _, taken := this.visible(authority, held.Key.Value()); !taken {
		this.stage(authority, row{
			key:         held.Key.Value(),
			fingerprint: held.Fingerprint.String(),
			family:      held.Stream.Family,
			streamKey:   string(held.Stream.Key),
			recordedAt:  this.clock.now(),
		})
		won = true
	}
	found, taken := this.visible(authority, held.Key.Value())
	if !taken {
		return receipt.Receipt{}, won, nil
	}
	answered, err := found.receipt()
	return answered, won, err
}

func (this *ledger) Complete(ctx context.Context, held receipt.Receipt) error {
	this.completes.Add(1)
	authority, err := this.Transaction(ctx)
	if err != nil {
		return err
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	found, taken := this.visible(authority, held.Key.Value())
	if !taken {
		return fmt.Errorf("the ledger holds no row for the key this completion names")
	}
	found.first, found.last, found.complete = held.First, held.Last, true
	this.stage(authority, found)
	return nil
}

func (this *ledger) Find(_ context.Context, key receipt.Key) (receipt.Receipt, bool, error) {
	if this.onFind != nil {
		return this.onFind(context.Background(), key)
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	found, taken := this.rows[key.Value()]
	if !taken {
		return receipt.Receipt{}, false, nil
	}
	answered, err := found.receipt()
	return answered, err == nil, err
}

func (this *ledger) Horizon(ctx context.Context) (time.Time, error) {
	if this.onHorizon != nil {
		return this.onHorizon(ctx)
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.horizon, nil
}

func (this *ledger) visible(authority event.Authority, key string) (row, bool) {
	if staged, opened := this.staged[authority]; opened {
		if found, taken := staged[key]; taken {
			return found, true
		}
	}
	found, taken := this.rows[key]
	return found, taken
}

func (this *ledger) stage(authority event.Authority, held row) {
	if this.staged[authority] == nil {
		this.staged[authority] = map[string]row{}
	}
	this.staged[authority][held.key] = held
}

func (this *ledger) commit(authority event.Authority) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	for key, held := range this.staged[authority] {
		this.rows[key] = held
	}
	delete(this.staged, authority)
}

func (this *ledger) discard(authority event.Authority) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	delete(this.staged, authority)
}

func (this *ledger) committed(key receipt.Key) (row, bool) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	found, taken := this.rows[key.Value()]
	return found, taken
}

func (this *ledger) count() int {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return len(this.rows)
}

func (this *ledger) forget(key receipt.Key) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	delete(this.rows, key.Value())
}

func (this *ledger) setHorizon(at time.Time) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.horizon = at
}

// A store that answers everything the one it wraps answers and counts what it
// was asked, so a case can say a call reached the event store and how often.
type countedStore struct {
	event.Store

	mutex sync.Mutex
	calls []string

	transactions event.Support
}

func counting(store event.Store) *countedStore { return &countedStore{Store: store} }

func (this *countedStore) asked(what string) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.calls = append(this.calls, what)
}

func (this *countedStore) asks() []string {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return slices.Clone(this.calls)
}

func (this *countedStore) Capabilities() event.Capabilities {
	this.asked("Capabilities")
	held := this.Store.Capabilities()
	if this.transactions != event.Unstated {
		held.Transactions = this.transactions
	}
	return held
}

func (this *countedStore) Limits() event.Limits {
	this.asked("Limits")
	return this.Store.Limits()
}

func (this *countedStore) Backing() event.Backing {
	this.asked("Backing")
	return this.Store.Backing()
}

func (this *countedStore) Transaction(ctx context.Context) (event.Authority, error) {
	this.asked("Transaction")
	return this.Store.Transaction(ctx)
}

func (this *countedStore) ReadStream(ctx context.Context, stream event.Stream, after event.Version) ([]event.Envelope, error) {
	this.asked("ReadStream")
	return this.Store.ReadStream(ctx, stream, after)
}

func (this *countedStore) ReadAll(ctx context.Context, after event.Cursor) ([]event.Envelope, event.Cursor, error) {
	this.asked("ReadAll")
	return this.Store.ReadAll(ctx, after)
}

func (this *countedStore) Append(ctx context.Context, req event.AppendRequest) error {
	this.asked("Append")
	return this.Store.Append(ctx, req)
}

// A receipt records what happened and offers nothing to append with: no field
// and no return of any exported type of this package is an At[S], which is the
// value the kernel mints at a load and admits at an append. Offering one would
// let a caller hand a repeat's record back to Append and re-propose a decision
// made at another version.
func TestNoReceiptTypeExposesAnAppendToken(t *testing.T) {
	walked := []reflect.Type{
		reflect.TypeFor[receipt.Key](),
		reflect.TypeFor[receipt.Fingerprint](),
		reflect.TypeFor[receipt.Receipt](),
		reflect.TypeFor[receipt.Ledger](),
		reflect.TypeFor[receipt.Verdict](),
		reflect.TypeFor[receipt.ClaimSpec](),
		reflect.TypeFor[receipt.Held](),
		reflect.TypeFor[receipt.Standing](),
		reflect.TypeFor[receipt.Resolution](),
		reflect.TypeFor[receipt.ResolveSpec](),
	}
	for _, held := range walked {
		for _, complaint := range tokensReachedFrom(held) {
			t.Error(complaint)
		}
	}

	t.Run("the control: every exported type of the package is one of the ten walked", func(t *testing.T) {
		declared := exportedTypesOf(t, ".")
		if len(declared) == 0 {
			t.Fatal("no exported type was parsed out of the package, so the walk above asked about a list nobody checked")
		}
		var named []string
		for _, held := range walked {
			named = append(named, held.Name())
		}
		for _, one := range declared {
			if !slices.Contains(named, one) {
				t.Errorf("the package declares %s and the walk does not ask about it, so a token on it would ship unread", one)
			}
		}
		for _, one := range named {
			if !slices.Contains(declared, one) {
				t.Errorf("the walk asks about %s and the package declares no such type, so the list is stale", one)
			}
		}
	})

	t.Run("the control: a form that does carry one is reported", func(t *testing.T) {
		if reported := tokensReachedFrom(reflect.TypeFor[carriesAToken]()); len(reported) != 2 {
			t.Fatalf("the fixture carries a token in a field and answers one from a method and %v came back, so the arm above proves nothing", reported)
		}
	})
}

type carriesAToken struct {
	At event.At[account]
}

func (carriesAToken) Token() event.At[account] { return event.At[account]{} }

func tokensReachedFrom(held reflect.Type) []string {
	var complaints []string
	if held.Kind() == reflect.Struct {
		for index := range held.NumField() {
			field := held.Field(index)
			if isAppendToken(field.Type) {
				complaints = append(complaints, held.Name()+"."+field.Name+" is an append token, and a receipt records what happened rather than authorising what happens next")
			}
		}
	}
	for index := range held.NumMethod() {
		method := held.Method(index)
		for result := range method.Type.NumOut() {
			if isAppendToken(method.Type.Out(result)) {
				complaints = append(complaints, held.Name()+"."+method.Name+" answers an append token, and a receipt records what happened rather than authorising what happens next")
			}
		}
	}
	return complaints
}

func isAppendToken(held reflect.Type) bool {
	return held.PkgPath() == "github.com/frostgrove/vv/event" && strings.HasPrefix(held.Name(), "At[")
}

func exportedTypesOf(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("the package directory could not be listed: %v", err)
	}
	var found []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), directory+"/"+name, nil, 0)
		if err != nil {
			t.Fatalf("%s could not be parsed, so nothing about it was read: %v", name, err)
		}
		for _, declaration := range parsed.Decls {
			general, is := declaration.(*ast.GenDecl)
			if !is || general.Tok != token.TYPE {
				continue
			}
			for _, spec := range general.Specs {
				if typed, is := spec.(*ast.TypeSpec); is && typed.Name.IsExported() {
					found = append(found, typed.Name.Name)
				}
			}
		}
	}
	return found
}
