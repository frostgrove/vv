package auditpg

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"sync"

	"github.com/frostgrove/vv/audit"
)

type attemptTypeLease struct {
	mu    sync.Mutex
	state audit.AttemptTypeStateResult
	tx    *sql.Tx
	done  bool
}

func (s *Store) LockAttemptType(ctx context.Context, query audit.AttemptTypeStateQuery) (audit.AttemptTypeLease, error) {
	ready, err := s.ready()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	view := query.View()
	if !attemptTypeStateQueryMatchesReady(view, ready) {
		return nil, audit.Failure(audit.Refused, errors.New("auditpg: attempt type query is stale"))
	}
	tx, err := s.value.configured.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, classifySQL(err, false)
	}
	failed := true
	defer func() {
		if failed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, attemptTypeLockKey(view)); err != nil {
		return nil, classifySQL(err, false)
	}
	state, err := attemptTypeStateFrom(ctx, query, ready, s.value.configured.schema.Name, tx)
	if err != nil {
		return nil, err
	}
	failed = false
	return &attemptTypeLease{state: state, tx: tx}, nil
}

func (l *attemptTypeLease) State() audit.AttemptTypeStateResult {
	if l == nil {
		return audit.AttemptTypeStateResult{}
	}
	return l.state
}

func (l *attemptTypeLease) Append(ctx context.Context, writer audit.Writer, request audit.AppendRequest) (audit.AppendResult, error) {
	if l == nil {
		return audit.AppendResult{}, audit.Failure(audit.Closed, errors.New("auditpg: attempt type lease is closed"))
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done || l.tx == nil || writer == nil {
		return audit.AppendResult{}, audit.Failure(audit.Closed, errors.New("auditpg: attempt type lease is closed"))
	}
	result, err := writer.Append(ctx, request)
	_ = l.tx.Rollback()
	l.done = true
	return result, err
}

func (l *attemptTypeLease) Release() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.done {
		return
	}
	l.done = true
	if l.tx != nil {
		_ = l.tx.Rollback()
	}
}

func attemptTypeLockKey(view audit.AttemptTypeStateQueryView) int64 {
	digest := sha256.New()
	_, _ = digest.Write([]byte("frostgrove.audit/postgres-attempt-type-lock/v1\x00"))
	_, _ = digest.Write(view.Log[:])
	_, _ = digest.Write([]byte(view.Type.Catalog))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(view.Type.Operation))
	_, _ = digest.Write(view.Type.Policy[:])
	_, _ = digest.Write(view.Type.Replay[:])
	return int64(binary.BigEndian.Uint64(digest.Sum(nil)))
}
