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

type List struct {
	client redis.UniversalClient
	prefix string
}

const DefaultPrefix = "access:revoked:"

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

type Option func(*List)

func Prefix(prefix string) Option {
	return func(list *List) { list.prefix = prefix }
}

var _ accessjwt.RevocationList = (*List)(nil)

func (this *List) Revoked(ctx context.Context, session uuid.UUID) (bool, error) {
	count, err := this.client.Exists(ctx, this.key(session)).Result()
	if err != nil {
		return false, fmt.Errorf("revokeredis: reading the revocation list: %w", err)
	}
	return count > 0, nil
}

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

const MinimumTTL = time.Second

func (this *List) key(session uuid.UUID) string { return this.prefix + session.String() }

func (this *List) Ping(ctx context.Context) error {
	if err := this.client.Ping(ctx).Err(); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("revokeredis: the revocation list is not reachable: %w", err)
	}
	return nil
}
