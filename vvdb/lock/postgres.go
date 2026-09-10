package lock

import (
	"fmt"
	"strconv"
	"time"

	"github.com/frostgrove/vv/crud"
)

type backend struct {
	dialect string
	timeout func(timeout time.Duration) (string, []any)
	take    func(guard Guard) (string, []any)
	try     func(guard Guard) (string, []any)
}

func backendFor(dialect crud.Dialect) (backend, error) {
	if dialect == nil {
		return backend{}, fmt.Errorf("%w: the source has no dialect", ErrDialectUnsupported)
	}
	switch dialect.Name() {
	case "postgres":
		return backend{dialect: "postgres", timeout: postgresTimeout, take: postgresTake, try: postgresTry}, nil
	}
	return backend{}, fmt.Errorf("%w: %q", ErrDialectUnsupported, dialect.Name())
}

func postgresTimeout(timeout time.Duration) (string, []any) {
	milliseconds := strconv.FormatInt(timeout.Milliseconds(), 10)
	return `SELECT set_config('lock_timeout', $1, true)`, []any{milliseconds}
}

func postgresTake(guard Guard) (string, []any) {
	if guard.Shared {
		return `SELECT pg_advisory_xact_lock_shared($1)`, []any{int64(guard.Key)}
	}
	return `SELECT pg_advisory_xact_lock($1)`, []any{int64(guard.Key)}
}

func postgresTry(guard Guard) (string, []any) {
	if guard.Shared {
		return `SELECT pg_try_advisory_xact_lock_shared($1)`, []any{int64(guard.Key)}
	}
	return `SELECT pg_try_advisory_xact_lock($1)`, []any{int64(guard.Key)}
}
