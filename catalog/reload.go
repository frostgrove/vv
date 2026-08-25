package catalog

import (
	"context"
	"time"
)

// Reloader is how a caller that met a constraint name the catalog has never
// heard of asks for one more look. A rolling migration adds a constraint while
// the process runs, and the alternative to this is a schema that is wrong until
// the next deploy.
//
// It is deliberately not part of Catalog. A lookup does no I/O and takes no
// context — that is what makes Load the thing that fails, and [[D-041]] turns it
// into a forbid — so the one call that does I/O cannot sit on the same
// interface. Optional interfaces are the house answer to this: crud.Beginner,
// crud.ReadSourcer, crud.OffsetLimiter and crud.Identified all exist so a
// component written outside the package keeps compiling.
//
// What Load returns implements it. A Catalog from somewhere else may not, so ask:
//
//	if r, ok := cat.(catalog.Reloader); ok {
//	    _ = r.Reload(ctx, table, name)
//	}
type Reloader interface {
	Reload(ctx context.Context, table, name string) error
}

// How long an unknown name is remembered as unknown, and how long two whole
// introspection passes must be apart.
//
// These numbers are this phase's own choice. §16 of ROADMAP-errors.md still
// lists the cap defaults as undecided and names catalog load time among them; if
// they are decided there, they move.
const (
	minBackoff   = time.Second
	maxBackoff   = 5 * time.Minute
	reloadFloor  = time.Second
	backoffScale = 2
)

// negative is one name remembered as unknown. wait is the interval the next
// arming will use, so a name that keeps missing is asked about less and less.
type negative struct {
	until time.Time
	wait  time.Duration
}

// Reload re-reads the schema, if it has not just done so.
//
// Two guards, because there are two different loops. The per-name entry is the
// one §7 asks for: one renamed constraint must not turn every failed write into
// a full introspection pass. The per-handle floor is the one the per-name entry
// does not close: fifty *different* unknown names would otherwise cause fifty
// passes a millisecond apart, and a bulk write against a stale catalog is
// exactly where fifty different names come from.
//
// A pass that finds the name forgets it and starts over. One that does not arms
// the entry with a doubling interval. A pass that fails keeps the schema it
// already had, returns the error, and still arms the floor — a database that is
// down would otherwise turn every failed write into a failed introspection pass.
//
// A name that is still unknown after a pass is not an error: the caller looks it
// up again and finds nothing, which is the answer. Only a read that failed
// returns one, and it wraps ErrIntrospection.
func (c *loaded) Reload(ctx context.Context, table, name string) error {
	k := consKey{table: table, name: name}
	now := c.clock()

	c.mu.Lock()
	if n, ok := c.misses[k]; ok && now.Before(n.until) {
		c.mu.Unlock()
		return nil
	}
	if now.Before(c.floor) {
		c.mu.Unlock()
		return nil
	}
	c.floor = now.Add(reloadFloor)
	c.mu.Unlock()

	tables, err := c.backend.read(ctx, c.src)
	if err != nil {
		return err
	}
	s := newSnapshot(tables)
	c.snap.Store(s)

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, found := s.byCons[k]; found {
		delete(c.misses, k)
		return nil
	}
	if c.misses == nil {
		c.misses = map[consKey]negative{}
	}
	n := c.misses[k]
	switch {
	case n.wait == 0:
		n.wait = minBackoff
	case n.wait < maxBackoff:
		n.wait *= backoffScale
		n.wait = min(n.wait, maxBackoff)
	}
	n.until = now.Add(n.wait)
	c.misses[k] = n
	return nil
}

// clock is time.Now everywhere but in the tests that have to watch a window
// close. Nothing else in the root module's non-test source reads a clock, so
// this is the first, and an injectable one is what makes the backoff testable at
// all rather than testable by sleeping.
func (c *loaded) clock() time.Time {
	if c.now == nil {
		return time.Now()
	}
	return c.now()
}
