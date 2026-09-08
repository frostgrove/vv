package tenancydb_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/tenancy"
	"github.com/frostgrove/vv/tenancy/tenancydb"
)

type closeable struct {
	*crudtest.Recorder
	mutex  sync.Mutex
	closed bool
}

func (this *closeable) Close() error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.closed = true
	return nil
}

func (this *closeable) isClosed() bool {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.closed
}

type openings struct {
	mutex   sync.Mutex
	opened  map[string]*closeable
	live    int
	peak    int
	asked   []tenancy.Scope
	fail    error
	holdFor time.Duration
}

func (this *openings) Source(ctx context.Context, scope tenancy.Scope) (crud.Source, error) {
	this.mutex.Lock()
	if this.fail != nil {
		this.mutex.Unlock()
		return nil, this.fail
	}
	this.live++
	if this.live > this.peak {
		this.peak = this.live
	}
	hold := this.holdFor
	this.mutex.Unlock()

	if hold > 0 {
		select {
		case <-time.After(hold):
		case <-ctx.Done():
			this.mutex.Lock()
			this.live--
			this.mutex.Unlock()
			return nil, ctx.Err()
		}
	}

	source := &closeable{Recorder: crudtest.Postgres()}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.live--
	if this.opened == nil {
		this.opened = map[string]*closeable{}
	}
	this.asked = append(this.asked, scope)
	this.opened[scope.Reference().Value()] = source
	return source, nil
}

func (this *openings) askrecord() []tenancy.Scope {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return append([]tenancy.Scope(nil), this.asked...)
}

func (this *openings) peakOpen() int {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.peak
}

func (this *openings) count() int {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return len(this.opened)
}

func directoryOf(t *testing.T, authority *tenancy.Authority, spec tenancydb.DirectorySpec) *tenancydb.Directory {
	t.Helper()
	spec.Authority = authority
	if spec.Close == nil {
		spec.Close = func(source crud.Source) error {
			if closer, ok := source.(*closeable); ok {
				return closer.Close()
			}
			return nil
		}
	}
	directory, err := tenancydb.NewDirectory(spec)
	if err != nil {
		t.Fatalf("cannot build the directory: %v", err)
	}
	return directory
}

func TestADirectoryRefusesToExistWithoutItsBounds(t *testing.T) {
	sources := &openings{}
	authority := activeAuthority(t, secretReference)
	for name, spec := range map[string]tenancydb.DirectorySpec{
		"no authority":                 {Sources: sources, MaxCached: 4, TTL: time.Minute},
		"no source factory":            {Authority: authority, MaxCached: 4, TTL: time.Minute},
		"no cache bound":               {Authority: authority, Sources: sources, TTL: time.Minute},
		"no borrower bound":            {Authority: authority, Sources: sources, MaxCached: 4},
		"no way to give a source back": {Authority: authority, Sources: sources, MaxCached: 4, TTL: time.Minute},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tenancydb.NewDirectory(spec); err == nil {
				t.Fatal("a directory with no bound was built — one tenant can then take the deployment down")
			}
		})
	}
}

func TestOneTenantSelectsOneDatabaseAndKeepsIt(t *testing.T) {
	sources := &openings{}
	authority := activeAuthority(t, secretReference)
	directory := directoryOf(t, authority, tenancydb.DirectorySpec{Sources: sources, MaxCached: 4, TTL: time.Minute})
	ctx := bound(t, authority)

	first, err := directory.Borrow(ctx, tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	second, err := directory.Borrow(ctx, tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	if first.Source() != second.Source() {
		t.Fatal("one tenant reached two databases inside one request")
	}
	if sources.count() != 1 {
		t.Fatalf("the factory was asked %d times for one tenant", sources.count())
	}
	first.Release()
	second.Release()
}

func TestWorkWithNoScopeReachesNoDatabase(t *testing.T) {
	sources := &openings{}
	directory := directoryOf(t, activeAuthority(t, secretReference), tenancydb.DirectorySpec{Sources: sources, MaxCached: 4, TTL: time.Minute})

	if _, err := directory.Borrow(context.Background(), tenancy.ClassWrite); !errors.Is(err, tenancy.ErrNoScope) {
		t.Fatalf("err = %v, want ErrNoScope", err)
	}
	if sources.count() != 0 {
		t.Fatal("a database was opened for a request that named no tenant")
	}
}

func TestAMissingMappingHasNoFallback(t *testing.T) {
	sources := &openings{fail: tenancy.ErrUnmapped}
	authority := activeAuthority(t, secretReference)
	directory := directoryOf(t, authority, tenancydb.DirectorySpec{Sources: sources, MaxCached: 4, TTL: time.Minute})

	if _, err := directory.Borrow(bound(t, authority), tenancy.ClassWrite); !errors.Is(err, tenancy.ErrUnmapped) {
		t.Fatalf("err = %v, want ErrUnmapped — a tenant with no database must not reach another tenant's", err)
	}
}

func TestADatabaseTheFenceDoesNotRecogniseIsRefused(t *testing.T) {
	sources := &openings{}
	authority := activeAuthority(t, secretReference)
	directory := directoryOf(t, authority, tenancydb.DirectorySpec{
		Sources: sources, MaxCached: 4, TTL: time.Minute,
		Fence: func(_ context.Context, _ crud.Source, scope tenancy.Scope) error {
			return fmt.Errorf("this database belongs to another tenant: %w", tenancy.ErrIncompatible)
		},
	})

	if _, err := directory.Borrow(bound(t, authority), tenancy.ClassWrite); !errors.Is(err, tenancy.ErrIncompatible) {
		t.Fatalf("err = %v, want ErrIncompatible", err)
	}
	if directory.Cached() != 0 {
		t.Fatal("a fenced database was cached anyway")
	}

	t.Run("and a matching generation is admitted", func(t *testing.T) {
		agreed := directoryOf(t, authority, tenancydb.DirectorySpec{
			Sources: &openings{}, MaxCached: 4, TTL: time.Minute,
			Fence: func(context.Context, crud.Source, tenancy.Scope) error { return nil },
		})
		lease, err := agreed.Borrow(bound(t, authority), tenancy.ClassWrite)
		if err != nil {
			t.Fatalf("the control fences too, so the assertion above proves nothing: %v", err)
		}
		lease.Release()
	})
}

func TestTheCacheIsBoundedRatherThanGrowingWithTenants(t *testing.T) {
	sources := &openings{}
	authority, cohort := manyTenantAuthority(t, 3)
	directory := directoryOf(t, authority, tenancydb.DirectorySpec{Sources: sources, MaxCached: 2, TTL: time.Minute})

	held := make([]*tenancydb.Lease, 0, 2)
	for _, raw := range cohort[:2] {
		lease, err := directory.Borrow(boundTo(t, authority, raw), tenancy.ClassWrite)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		held = append(held, lease)
	}

	// Both slots are in use, which is the only thing ErrCapacity now means: the
	// idlest binding is closed to make room when there is one, so a directory that
	// still refuses here is refusing for the honest reason.
	if _, err := directory.Borrow(boundTo(t, authority, cohort[2]), tenancy.ClassWrite); !errors.Is(err, tenancy.ErrCapacity) {
		t.Fatalf("err = %v, want ErrCapacity — the pool grew with the tenant list", err)
	}
	for _, lease := range held {
		lease.Release()
	}
}

func TestEvictionWaitsForTheLastBorrower(t *testing.T) {
	sources := &openings{}
	authority := activeAuthority(t, secretReference)
	directory := directoryOf(t, authority, tenancydb.DirectorySpec{Sources: sources, MaxCached: 4, TTL: time.Minute})

	lease, err := directory.Borrow(bound(t, authority), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	opened := lease.Source().(*closeable)

	directory.Evict(reference(t, secretReference))
	if opened.isClosed() {
		t.Fatal("the pool was closed under a caller still holding it")
	}
	if directory.Cached() != 0 {
		t.Fatal("an evicted binding is still handed to the next request")
	}

	lease.Release()
	if !opened.isClosed() {
		t.Fatal("the last borrower gave the pool back and nothing closed it")
	}
}

func TestARotatedGenerationDoesNotReachTheBindingItReplaced(t *testing.T) {
	sources := &openings{}
	rotating := &tenancy.Resolution{Reference: reference(t, secretReference), Lifecycle: tenancy.Active, Epoch: epochOf(t, 1)}
	authority, err := tenancy.New(tenancy.Spec{Resolver: switchable{current: rotating}})
	if err != nil {
		t.Fatal(err)
	}
	directory := directoryOf(t, authority, tenancydb.DirectorySpec{Sources: sources, MaxCached: 4, TTL: time.Minute})

	before, err := directory.Borrow(bound(t, authority), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	rotating.Epoch = epochOf(t, 2)
	after, err := directory.Borrow(bound(t, authority), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	if before.Source() == after.Source() {
		t.Fatal("the generation that replaced a rotated credential was handed the binding it replaced")
	}
	before.Release()
	after.Release()
}

func TestABindingIsGivenBackAfterItsBorrowerLifetime(t *testing.T) {
	sources := &openings{}
	clock := time.Now()
	authority, cohort := manyTenantAuthority(t, 2)
	directory := directoryOf(t, authority, tenancydb.DirectorySpec{
		Sources: sources, MaxCached: 1, TTL: time.Minute,
		Now: func() time.Time { return clock },
	})

	first, err := directory.Borrow(boundTo(t, authority, cohort[0]), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	first.Release()
	if directory.Cached() != 1 {
		t.Fatal("the binding was not kept at all, so the lifetime below proves nothing")
	}

	// Nobody is asking for the slot, so the lifetime is what gives the binding
	// back. Borrowing the same tenant again inside the lifetime reuses it rather
	// than opening a second pool.
	again, err := directory.Borrow(boundTo(t, authority, cohort[0]), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	if again.Source() != first.Source() {
		t.Fatal("a binding still inside its lifetime was reopened rather than reused")
	}
	again.Release()

	clock = clock.Add(2 * time.Minute)
	second, err := directory.Borrow(boundTo(t, authority, cohort[1]), tenancy.ClassWrite)
	if err != nil {
		t.Fatalf("a binding nobody holds was never given back: %v", err)
	}
	second.Release()
}

// MaxCached is a connection budget, not a ceiling on the tenants a process can
// serve. Refusing a newcomer while an idle binding sits in the map hands the
// budget to whoever arrived first: every borrow renews its own expiry, so under
// steady traffic nothing expires and the servable set is frozen for the life of
// the process. The idlest binding is closed instead.
func TestANewcomerClosesTheIdlestBindingRatherThanBeingRefused(t *testing.T) {
	sources := &openings{}
	clock := time.Now()
	authority, cohort := manyTenantAuthority(t, 3)
	directory := directoryOf(t, authority, tenancydb.DirectorySpec{
		Sources: sources, MaxCached: 2, TTL: time.Hour,
		Now: func() time.Time { return clock },
	})

	first, err := directory.Borrow(boundTo(t, authority, cohort[0]), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	oldest := first.Source().(*closeable)
	first.Release()

	clock = clock.Add(time.Minute)
	second, err := directory.Borrow(boundTo(t, authority, cohort[1]), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	newer := second.Source().(*closeable)
	second.Release()

	// Well inside the hour, so nothing here is the sweep giving a binding back.
	clock = clock.Add(time.Minute)
	third, err := directory.Borrow(boundTo(t, authority, cohort[2]), tenancy.ClassWrite)
	if err != nil {
		t.Fatalf("a third tenant was refused while two idle bindings sat in the map: %v", err)
	}
	defer third.Release()

	if !oldest.isClosed() {
		t.Fatal("room was made without closing anything, so the connection budget is not a budget")
	}
	if newer.isClosed() {
		t.Fatal("the more recently used binding was closed, so the replacement is not least-recently-used")
	}
	if directory.Cached() != 2 {
		t.Fatalf("the directory holds %d bindings, want 2", directory.Cached())
	}
}

func boundTo(t *testing.T, authority *tenancy.Authority, raw string) context.Context {
	t.Helper()
	scope, err := authority.Lookup(context.Background(), reference(t, raw), tenancy.ClassWrite)
	if err != nil {
		t.Fatalf("cannot look up %q: %v", raw, err)
	}
	ctx, err := authority.With(context.Background(), scope)
	if err != nil {
		t.Fatalf("cannot carry the scope for %q: %v", raw, err)
	}
	return ctx
}

func epochOf(t *testing.T, value uint64) tenancy.Epoch {
	t.Helper()
	epoch, err := tenancy.NewEpoch(value)
	if err != nil {
		t.Fatal(err)
	}
	return epoch
}

func TestADatabaseIsNotHandedToAScopeTheClassNoLongerAdmits(t *testing.T) {
	sources := &openings{}
	epoch := epochOf(t, 1)
	readable := tenancy.Admit(tenancy.ClassRead, tenancy.Active, tenancy.Suspended).
		Merge(tenancy.Admit(tenancy.ClassWrite, tenancy.Active)).
		Merge(tenancy.Admit(tenancy.ClassDurable, tenancy.Active))

	suspended := reference(t, secretReference)
	authority, err := tenancy.New(tenancy.Spec{
		Admission: readable,
		Resolver: directory{byReference: map[tenancy.Reference]tenancy.Resolution{
			suspended: {Reference: suspended, Lifecycle: tenancy.Suspended, Epoch: epoch},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pool := directoryOf(t, authority, tenancydb.DirectorySpec{Sources: sources, MaxCached: 4, TTL: time.Minute})

	scope, err := authority.Lookup(context.Background(), suspended, tenancy.ClassRead)
	if err != nil {
		t.Fatalf("a suspended tenant this deployment keeps readable was refused a read scope: %v", err)
	}
	ctx, err := authority.With(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}

	lease, err := pool.Borrow(ctx, tenancy.ClassRead)
	if err != nil {
		t.Fatalf("the read the deployment allows was refused: %v", err)
	}
	lease.Release()

	if _, err := pool.Borrow(ctx, tenancy.ClassWrite); !errors.Is(err, tenancy.ErrInactive) {
		t.Fatalf("err = %v, want ErrInactive — a scope carried for a read opened a database to write in", err)
	}
}

func TestOneLeaseReleasedFromManyGoroutinesCountsOnce(t *testing.T) {
	sources := &openings{}
	authority := activeAuthority(t, secretReference)
	pool := directoryOf(t, authority, tenancydb.DirectorySpec{Sources: sources, MaxCached: 4, TTL: time.Minute})

	shared, err := pool.Borrow(bound(t, authority), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	live, err := pool.Borrow(bound(t, authority), tenancy.ClassWrite)
	if err != nil {
		t.Fatal(err)
	}
	if shared.Source() != live.Source() {
		t.Fatal("the two borrowers did not share one binding, so the accounting below is not under test")
	}
	opened := shared.Source().(*closeable)

	// One *Lease, released from sixteen goroutines at once. If the guard were an
	// unsynchronised bool the decrement it protects would run more than once, and
	// the count would reach zero while `live` is still holding the source.
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			shared.Release()
		}()
	}
	wg.Wait()

	pool.Evict(reference(t, secretReference))
	if opened.isClosed() {
		t.Fatal("the pool was closed under a borrower that never gave it back — the release counted more than once")
	}

	live.Release()
	if !opened.isClosed() {
		t.Fatal("the last borrower gave the pool back and nothing closed it")
	}
}

func TestNoMoreSourcesAreOpenAtOnceThanTheBudgetAllows(t *testing.T) {
	const budget = 4
	sources := &openings{holdFor: 5 * time.Millisecond}
	authority, cohort := manyTenantAuthority(t, 64)
	pool := directoryOf(t, authority, tenancydb.DirectorySpec{Sources: sources, MaxCached: budget, TTL: time.Minute})

	var wg sync.WaitGroup
	for _, raw := range cohort {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := pool.Borrow(boundTo(t, authority, raw), tenancy.ClassWrite)
			if err != nil {
				return
			}
			lease.Release()
		}()
	}
	wg.Wait()

	if peak := sources.peakOpen(); peak > budget {
		t.Fatalf("%d sources were open at once against a declared budget of %d — the bound counts map entries, not connections", peak, budget)
	}
	if pool.Cached() > budget {
		t.Fatalf("the directory holds %d bindings past its bound", pool.Cached())
	}
}

func TestConcurrentBorrowersForOneTenantShareOneOpen(t *testing.T) {
	sources := &openings{holdFor: 5 * time.Millisecond}
	authority := activeAuthority(t, secretReference)
	pool := directoryOf(t, authority, tenancydb.DirectorySpec{Sources: sources, MaxCached: 4, TTL: time.Minute})

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := pool.Borrow(bound(t, authority), tenancy.ClassWrite)
			if err != nil {
				t.Errorf("borrow: %v", err)
				return
			}
			lease.Release()
		}()
	}
	wg.Wait()

	if peak := sources.peakOpen(); peak != 1 {
		t.Fatalf("%d sources were opened concurrently for one tenant — sixteen borrowers raced and fifteen pools were discarded", peak)
	}
}

func TestABorrowerThatGivesUpDoesNotFailTheOnesBesideIt(t *testing.T) {
	sources := &openings{holdFor: 60 * time.Millisecond}
	authority := activeAuthority(t, secretReference)
	pool := directoryOf(t, authority, tenancydb.DirectorySpec{Sources: sources, MaxCached: 4, TTL: time.Minute})

	abandoning, giveUp := context.WithCancel(bound(t, authority))
	started := make(chan struct{})
	go func() {
		close(started)
		if lease, err := pool.Borrow(abandoning, tenancy.ClassWrite); err == nil {
			lease.Release()
		}
	}()
	<-started
	time.Sleep(10 * time.Millisecond)

	var wg sync.WaitGroup
	failures := make([]error, 3)
	for i := range failures {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := pool.Borrow(bound(t, authority), tenancy.ClassWrite)
			failures[i] = err
			if err == nil {
				lease.Release()
			}
		}()
	}
	time.Sleep(10 * time.Millisecond)
	giveUp()
	wg.Wait()

	for i, err := range failures {
		if err != nil {
			t.Fatalf("borrower %d was failed by a request that gave up beside it: %v", i, err)
		}
	}
}

func TestAWaiterHonoursItsOwnDeadlineRatherThanTheOpeners(t *testing.T) {
	sources := &openings{holdFor: 2 * time.Second}
	authority := activeAuthority(t, secretReference)
	pool := directoryOf(t, authority, tenancydb.DirectorySpec{Sources: sources, MaxCached: 4, TTL: time.Minute})

	started := make(chan struct{})
	go func() {
		close(started)
		if lease, err := pool.Borrow(bound(t, authority), tenancy.ClassWrite); err == nil {
			lease.Release()
		}
	}()
	<-started
	time.Sleep(20 * time.Millisecond)

	impatient, stop := context.WithTimeout(bound(t, authority), 50*time.Millisecond)
	defer stop()

	began := time.Now()
	if _, err := pool.Borrow(impatient, tenancy.ClassWrite); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if waited := time.Since(began); waited > time.Second {
		t.Fatalf("the waiter blocked %v on somebody else's open, long past its own 50ms deadline", waited)
	}
}

func TestTheFactoryAndTheFenceAreToldWhichTenantIsAsking(t *testing.T) {
	sources := &openings{}
	authority, cohort := manyTenantAuthority(t, 3)

	var fenced []tenancy.Scope
	var fencedSources []crud.Source
	pool := directoryOf(t, authority, tenancydb.DirectorySpec{
		Sources: sources, MaxCached: 8, TTL: time.Minute,
		Fence: func(_ context.Context, source crud.Source, scope tenancy.Scope) error {
			fenced = append(fenced, scope)
			fencedSources = append(fencedSources, source)
			return nil
		},
	})

	for _, raw := range cohort {
		lease, err := pool.Borrow(boundTo(t, authority, raw), tenancy.ClassWrite)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		lease.Release()
	}

	asked := sources.askrecord()
	if len(asked) != len(cohort) || len(fenced) != len(cohort) {
		t.Fatalf("the factory was asked %d times and the fence %d, for %d tenants", len(asked), len(fenced), len(cohort))
	}
	for i, raw := range cohort {
		if asked[i].Reference().Value() != raw {
			t.Fatalf("the factory was asked for %q while %q was borrowing — every tenant would get the first tenant's database", asked[i].Reference().Value(), raw)
		}
		if fenced[i].Reference().Value() != raw {
			t.Fatalf("the fence was shown %q while %q was borrowing, so it cannot tell whose database it is", fenced[i].Reference().Value(), raw)
		}
		if asked[i].Epoch() != fenced[i].Epoch() || asked[i].Epoch().IsZero() {
			t.Fatalf("the generation reached the factory as %v and the fence as %v", asked[i].Epoch(), fenced[i].Epoch())
		}
		if fencedSources[i] == nil {
			t.Fatal("the fence was handed no source, so it can prove nothing about one")
		}
	}
}
