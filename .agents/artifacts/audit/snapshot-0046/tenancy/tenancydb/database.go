package tenancydb

import (
	"context"
	"errors"
	"io"
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
	OpenTimeout time.Duration
	Now         func() time.Time
}

type Directory struct {
	authority   *tenancy.Authority
	sources     Sources
	max         int
	ttl         time.Duration
	fence       func(ctx context.Context, source crud.Source, scope tenancy.Scope) error
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

	held, mine, err := this.reserve(key)
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
func (this *Directory) reserve(key binding) (*entry, bool, error) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.closed {
		return nil, false, tenancy.ErrUnavailable
	}
	this.sweep()
	if held, ok := this.entries[key]; ok {
		held.borrowers++
		held.expires = this.now().Add(this.ttl)
		return held, false, nil
	}
	if len(this.entries) >= this.max {
		return nil, false, tenancy.ErrCapacity
	}
	held := &entry{expires: this.now().Add(this.ttl), borrowers: 1, ready: make(chan struct{})}
	this.entries[key] = held
	return held, true, nil
}

// The opener's context is one borrower's, and a burst of borrowers for one tenant
// share its work. Its *cancellation* is not shared: a request that gave up must
// not fail the three beside it, so the open runs detached from the caller's
// cancellation and keeps only its values and a bound of its own.
func (this *Directory) open(ctx context.Context, scope tenancy.Scope) (crud.Source, error) {
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
		closeSource(source)
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
		delete(this.entries, key)
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

func (this *Directory) release(held *entry) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	held.borrowers--
	if held.evicted && held.borrowers == 0 {
		closeSource(held.source)
	}
}

func (this *Directory) Evict(reference tenancy.Reference) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	for key, held := range this.entries {
		if key.reference == reference {
			this.unlink(key, held)
		}
	}
}

func (this *Directory) sweep() {
	now := this.now()
	for key, held := range this.entries {
		if now.After(held.expires) && held.borrowers == 0 {
			this.unlink(key, held)
		}
	}
}

func (this *Directory) unlink(key binding, held *entry) {
	delete(this.entries, key)
	held.evicted = true
	if held.borrowers == 0 {
		closeSource(held.source)
	}
}

func (this *Directory) Close() error {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.closed = true
	for key, held := range this.entries {
		this.unlink(key, held)
	}
	return nil
}

func (this *Directory) Cached() int {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return len(this.entries)
}

func closeSource(source crud.Source) {
	if closer, ok := source.(io.Closer); ok {
		_ = closer.Close()
	}
}
