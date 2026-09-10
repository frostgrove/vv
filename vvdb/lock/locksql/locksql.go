package locksql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/frostgrove/vv/vvdb/lock"
)

// ErrNotHeld is the unlock answering false: this session did not hold the lock, so it never
// serialised anything and whatever ran under it ran unprotected.
var ErrNotHeld = errors.New("locksql: the session did not hold the lock it released")

const (
	retryFloor  = 250 * time.Millisecond
	retryJitter = 250 * time.Millisecond
	unlockGrace = 5 * time.Second
)

// Hold checks one connection out of the pool, takes key on it as a session lock, and runs work
// while it is held. It is the shape a schema migration needs and the transactional lock cannot
// give: DDL that spans several transactions still has to exclude a second replica running the
// same migration, so the lock has to outlive any one transaction.
//
// Three things here are not incidental. The lock is taken on a pinned *sql.Conn rather than the
// pool, because a session lock taken through a *sql.DB lands on whichever connection the pool
// happened to hand out and serialises nothing. The wait is a jittered try-loop rather than a
// blocking pg_advisory_lock, so a caller whose context is cancelled stops waiting. And an unlock
// that answers false, or fails, poisons the connection instead of returning it to the pool still
// holding a lock nobody will release.
func Hold(ctx context.Context, db *sql.DB, key lock.Key, work func(*sql.Conn) error) (resultErr error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("locksql: checking out a connection for %d: %w", int64(key), err)
	}
	locked := false
	discard := false
	defer func() {
		if locked {
			unlocking, cancel := context.WithTimeout(context.WithoutCancel(ctx), unlockGrace)
			var unlocked bool
			err := conn.QueryRowContext(unlocking, `SELECT pg_advisory_unlock($1)`, int64(key)).Scan(&unlocked)
			cancel()
			if err == nil && !unlocked {
				err = ErrNotHeld
			}
			if err != nil {
				discard = true
				resultErr = errors.Join(resultErr, fmt.Errorf("locksql: releasing %d: %w", int64(key), err))
			}
		}
		if discard {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		resultErr = errors.Join(resultErr, conn.Close())
	}()
	for !locked {
		if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, int64(key)).Scan(&locked); err != nil {
			discard = true
			return fmt.Errorf("locksql: taking %d: %w", int64(key), err)
		}
		if locked {
			break
		}
		waiting := time.NewTimer(retryFloor + time.Duration(rand.Int64N(int64(retryJitter))))
		select {
		case <-ctx.Done():
			waiting.Stop()
			return ctx.Err()
		case <-waiting.C:
		}
	}
	return work(conn)
}
