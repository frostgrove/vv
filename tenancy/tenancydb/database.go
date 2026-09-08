package tenancydb

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/tenancy"
)

const DefaultOpenTimeout = 30 * time.Second

type Sources interface {
	Source(ctx context.Context, scope tenancy.Scope) (crud.Source, error)
}

type SourcesFunc func(context.Context, tenancy.Scope) (crud.Source, error)

func (this SourcesFunc) Source(ctx context.Context, scope tenancy.Scope) (crud.Source, error) {
	return this(ctx, scope)
}

type DirectorySpec struct {
	Authority   *tenancy.Authority
	Sources     Sources
	MaxCached   int
	TTL         time.Duration
	Fence       func(ctx context.Context, source crud.Source, scope tenancy.Scope) error
	Close       func(crud.Source) error
	OpenTimeout time.Duration
	Now         func() time.Time
}

type Directory struct {
	authority   *tenancy.Authority
	sources     Sources
	max         int
	ttl         time.Duration
	fence       func(ctx context.Context, source crud.Source, scope tenancy.Scope) error
	close       func(crud.Source) error
	openTimeout time.Duration
	now         func() time.Time

	mutex   sync.Mutex
	entries map[binding]*entry
	closed  bool
}

type binding struct {
	reference tenancy.Reference
	epoch     tenancy.Epoch
}

type entry struct {
	source    crud.Source
	expires   time.Time
	borrowers int
	evicted   bool
	ready     chan struct{}
	err       error
}

type Lease struct {
	directory *Directory
	held      *entry
	source    crud.Source
	release   sync.Once
}

func (this *Lease) Source() crud.Source { return this.source }

func (this *Lease) Release() {
	if this == nil {
		return
	}
	this.release.Do(func() { this.directory.release(this.held) })
}

func NewDirectory(spec DirectorySpec) (*Directory, error) {
	if spec.Authority == nil {
		return nil, errors.New("tenancy: a directory needs the authority that mints the scopes it routes on; reading a carried scope without re-checking it is how a suspended tenant keeps a database")
	}
	if spec.Sources == nil {
		return nil, errors.New("tenancy: a directory needs a source factory; there is no last-used database to fall back to")
	}
	if spec.MaxCached <= 0 {
		return nil, errors.New("tenancy: a directory needs a positive MaxCached; an unbounded pool per tenant is how one tenant takes the deployment down")
	}
	if spec.TTL <= 0 {
		return nil, errors.New("tenancy: a directory needs a positive TTL; a binding nobody ever revisits survives its own rotation")
	}
	if spec.Close == nil {
		return nil, errors.New("tenancy: a directory needs a Close for the sources its factory opens; " +
			"a crud.Source is an interface and the pools behind it do not implement io.Closer, so a directory " +
			"without one evicts and rotates for the lifetime of the process and never gives a connection back")
	}
	now := spec.Now
	if now == nil {
		now = time.Now
	}
	openTimeout := spec.OpenTimeout
	if openTimeout <= 0 {
		openTimeout = DefaultOpenTimeout
	}
	return &Directory{
		authority:   spec.Authority,
		sources:     spec.Sources,
		max:         spec.MaxCached,
		ttl:         spec.TTL,
		fence:       spec.Fence,
		close:       spec.Close,
		openTimeout: openTimeout,
		now:         now,
		entries:     map[binding]*entry{},
	}, nil
}

// Selection happens once, before any statement, and the result is keyed on the
// generation as well as the tenant: a binding cached before a rotation belongs to
// the generation it was opened for and is never handed to the one that replaced
// it. Eviction unlinks an entry immediately but closes its source only when the
// last borrower gives it back, because a pool closed under a running transaction
// fails in the caller rather than in the eviction that caused it.
func (this *Directory) Borrow(ctx context.Context, class tenancy.Class) (*Lease, error) {
	scope, err := this.scope(ctx, class)
	if err != nil {
		return nil, err
	}
	key := binding{reference: scope.Reference(), epoch: scope.Epoch()}

	held, mine, expired, err := this.reserve(key)
	this.closeAll(expired)
	if err != nil {
		return nil, err
	}
	if !mine {
		select {
		case <-held.ready:
		case <-ctx.Done():
			this.release(held)
			return nil, ctx.Err()
		}
		if held.err != nil {
			this.release(held)
			return nil, held.err
		}
		return &Lease{directory: this, held: held, source: held.source}, nil
	}

	source, err := this.open(ctx, scope)
	this.finish(key, held, source, err)
	if err != nil {
		this.release(held)
		return nil, err
	}
	return &Lease{directory: this, held: held, source: source}, nil
}

// A slot is taken before the source is opened, not after: checking the bound,
// dropping the lock and then opening lets every borrower in a burst pass a check
// that was true for all of them and open a pool each — a declared budget of four
// and sixty-four live connections. The reserved entry is also what makes
// concurrent borrowers for one tenant share a single open instead of racing and
// discarding all but one.
func (this *Directory) reserve(key binding) (*entry, bool, []crud.Source, error) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.closed {
		return nil, false, nil, tenancy.ErrUnavailable
	}
	expired := this.sweep()
	if held, ok := this.entries[key]; ok {
		held.borrowers++
		held.expires = this.now().Add(this.ttl)
		return held, false, expired, nil
	}
	if len(this.entries) >= this.max {
		closing, made := this.evictIdlest()
		if !made {
			return nil, false, expired, tenancy.ErrCapacity
		}
		expired = append(expired, closing...)
	}
	held := &entry{expires: this.now().Add(this.ttl), borrowers: 1, ready: make(chan struct{})}
	this.entries[key] = held
	return held, true, expired, nil
}

// The opener's context is one borrower's, and a burst of borrowers for one tenant
// share its work. Its *cancellation* is not shared: a request that gave up must
// not fail the three beside it, so the open runs detached from the caller's
// cancellation and keeps only its values and a bound of its own.
func (this *Directory) open(ctx context.Context, scope tenancy.Scope) (opened crud.Source, err error) {
	// The factory and the fence are the application's. A panic in either would
	// unwind past finish, leaving the reserved slot with an unclosed ready channel:
	// every later borrower for that tenant blocks to its own deadline and the slot
	// is never reclaimed, so MaxCached panics retire the directory.
	defer func() {
		if recovered := recover(); recovered != nil {
			opened, err = nil, tenancy.ErrUnavailable
		}
	}()
	opening, cancel := context.WithTimeout(context.WithoutCancel(ctx), this.openTimeout)
	defer cancel()
	source, err := this.sources.Source(opening, scope)
	if err != nil {
		return nil, tenancy.Classify(err)
	}
	if source == nil {
		return nil, tenancy.ErrUnmapped
	}
	if this.fence == nil {
		return source, nil
	}
	if err := this.fence(opening, source, scope); err != nil {
		this.closeSource(source)
		return nil, tenancy.Classify(err)
	}
	return source, nil
}

func (this *Directory) finish(key binding, held *entry, source crud.Source, err error) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	held.source, held.err = source, err
	if err != nil || this.closed {
		held.evicted = true
		// By key alone this would unlink whatever entry now sits there. An Evict
		// between the reservation and here has already unlinked this one and a
		// successor may hold the key, and deleting that successor loses the only
		// reference to a live pool.
		if this.entries[key] == held {
			delete(this.entries, key)
		}
	}
	close(held.ready)
}

func (this *Directory) scope(ctx context.Context, class tenancy.Class) (tenancy.Scope, error) {
	if !class.Valid() {
		return tenancy.Scope{}, tenancy.ErrIncompatible
	}
	scope, err := this.authority.Scope(ctx, class)
	if err != nil {
		return tenancy.Scope{}, err
	}
	if scope.Reference().IsZero() || scope.Epoch().IsZero() {
		return tenancy.Scope{}, tenancy.ErrUntrusted
	}
	return scope, nil
}

// Closing drains a pool, and a driver that takes a second to do it would hold
// every other tenant's Borrow behind this mutex for that second — and a Close
// that reached back into the directory would deadlock on it outright.
func (this *Directory) release(held *entry) {
	this.mutex.Lock()
	held.borrowers--
	closing := held.evicted && held.borrowers == 0
	source := held.source
	this.mutex.Unlock()
	if closing {
		this.closeSource(source)
	}
}

func (this *Directory) Evict(reference tenancy.Reference) {
	this.mutex.Lock()
	var closing []crud.Source
	for key, held := range this.entries {
		if key.reference == reference {
			closing = append(closing, this.unlink(key, held)...)
		}
	}
	this.mutex.Unlock()
	this.closeAll(closing)
}

func (this *Directory) sweep() []crud.Source {
	now := this.now()
	var closing []crud.Source
	for key, held := range this.entries {
		if now.After(held.expires) && held.borrowers == 0 {
			closing = append(closing, this.unlink(key, held)...)
		}
	}
	return closing
}

// MaxCached bounds the connections this process holds, not the tenants it can
// serve. Refusing a newcomer while an idle binding sits in the map spends the
// budget on arrival order: every borrow of an incumbent pushes its own expiry
// out, so under steady traffic nothing ever expires and the servable set freezes
// at the first `max` tenants for the life of the process. The idlest binding is
// closed to make room instead, and `ErrCapacity` is kept for what it honestly
// means — every slot is in use right now, and closing one would cut a live
// transaction.
func (this *Directory) evictIdlest() ([]crud.Source, bool) {
	var (
		idlest *entry
		chosen binding
		found  bool
	)
	for key, held := range this.entries {
		if held.borrowers != 0 {
			continue
		}
		if !found || held.expires.Before(idlest.expires) {
			idlest, chosen, found = held, key, true
		}
	}
	if !found {
		return nil, false
	}
	return this.unlink(chosen, idlest), true
}

func (this *Directory) unlink(key binding, held *entry) []crud.Source {
	delete(this.entries, key)
	held.evicted = true
	if held.borrowers == 0 && held.source != nil {
		return []crud.Source{held.source}
	}
	return nil
}

func (this *Directory) Close() error {
	this.mutex.Lock()
	this.closed = true
	var closing []crud.Source
	for key, held := range this.entries {
		closing = append(closing, this.unlink(key, held)...)
	}
	this.mutex.Unlock()
	return errors.Join(this.closeEach(closing)...)
}

func (this *Directory) Cached() int {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return len(this.entries)
}

func (this *Directory) closeSource(source crud.Source) {
	if source == nil {
		return
	}
	_ = this.close(source)
}

func (this *Directory) closeAll(sources []crud.Source) {
	for _, source := range sources {
		this.closeSource(source)
	}
}

func (this *Directory) closeEach(sources []crud.Source) []error {
	var failures []error
	for _, source := range sources {
		if source == nil {
			continue
		}
		if err := this.close(source); err != nil {
			failures = append(failures, err)
		}
	}
	return failures
}
