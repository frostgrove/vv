// Package revokeredis is an accessjwt revocation list held in Redis.
//
// A signed token is refused as soon as its session is closed, rather than when
// it expires. That is the only reason to run this: without it the window is
// [accessjwt.Spec.AccessTTL], which at a few minutes is what most products can
// live with.
//
// Redis and not the database on purpose. A deny-list read happens on every
// authenticated request, so it has to be faster than the session read a signed
// token was chosen to avoid — a list in the same database is that read back
// again, with worse semantics. See the package documentation of accessjwt.
package revokeredis

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/frostgrove/vv/auth/access/accessjwt"
)

// A List is the revocation list. Build it with [New].
type List struct {
	client redis.UniversalClient
	prefix string
}

// DefaultPrefix namespaces the keys, so a Redis shared with a cache does not
// have a session id collide with somebody else's.
const DefaultPrefix = "access:revoked:"

// New builds the list over a client the application owns.
//
// The client is handed in rather than dialled here, for the same reason the
// database connection is: who opens a connection, with which pool and which
// timeouts, is the application's decision and not a library's.
func New(client redis.UniversalClient, options ...Option) (*List, error) {
	if client == nil {
		return nil, fmt.Errorf("revokeredis: a revocation list needs a client")
	}
	list := &List{client: client, prefix: DefaultPrefix}
	for _, option := range options {
		option(list)
	}
	return list, nil
}

// An Option configures a [List].
type Option func(*List)

// Prefix namespaces the keys differently.
func Prefix(prefix string) Option {
	return func(list *List) { list.prefix = prefix }
}

var _ accessjwt.RevocationList = (*List)(nil)

// Revoked implements accessjwt.RevocationList.
//
// An unreachable Redis is an error and never a false. This is the whole
// argument against a deny-list stated as one line of code: an allow-list that
// cannot be read refuses everybody, which is loud and safe, while a deny-list
// that cannot be read would admit everybody unless it says so here.
func (this *List) Revoked(ctx context.Context, session uuid.UUID) (bool, error) {
	count, err := this.client.Exists(ctx, this.key(session)).Result()
	if err != nil {
		return false, fmt.Errorf("revokeredis: reading the revocation list: %w", err)
	}
	return count > 0, nil
}

// Revoke implements accessjwt.RevocationList.
//
// The entry expires when the last token naming this session would have expired
// anyway, so the list stays the size of the tokens actually in flight rather
// than of every session ever closed. A moment already past is written with a
// short floor rather than skipped: the caller's clock and Redis's may disagree,
// and the cost of holding a useless key for a second is nothing beside the cost
// of dropping a live one.
func (this *List) Revoke(ctx context.Context, session uuid.UUID, until time.Time) error {
	ttl := time.Until(until)
	if ttl < MinimumTTL {
		ttl = MinimumTTL
	}
	if err := this.client.Set(ctx, this.key(session), "1", ttl).Err(); err != nil {
		return fmt.Errorf("revokeredis: writing to the revocation list: %w", err)
	}
	return nil
}

// MinimumTTL is the floor an entry is written with.
const MinimumTTL = time.Second

func (this *List) key(session uuid.UUID) string { return this.prefix + session.String() }

// Ping reports whether the list is reachable, for a start-up check.
//
// Worth running: a deployment that configured a revocation list and cannot
// reach it refuses every request, and finding that out at boot is better than
// finding it out from the first caller.
func (this *List) Ping(ctx context.Context) error {
	if err := this.client.Ping(ctx).Err(); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("revokeredis: the revocation list is not reachable: %w", err)
	}
	return nil
}
