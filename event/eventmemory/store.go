package eventmemory

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/frostgrove/vv/event"
)

// One decision's facts rather than a migration's, and a page the kernel's own
// resident rule always admits: 256 envelopes is 16 MiB at the log's default
// payload, and at any larger payload bound the rule below is what caps it, so a
// store over a log the kernel would admit is never refused for numbers the
// caller did not set.
const (
	defaultMaxBatch = 64
	defaultPage     = 256
)

type Spec struct {
	Log *Log

	// Retained and called on every append, from every goroutine that appends,
	// so it must be safe for that. Its panic is not recovered: a clock has no
	// error channel for a panic to be a second spelling of.
	Clock func() time.Time

	MaxBatch   int
	StreamPage int
	MaxRead    int
}

type Store struct {
	log    *Log
	clock  func() time.Time
	limits event.Limits
	closed atomic.Bool
}

var _ event.Store = (*Store)(nil)

func New(spec Spec) (*Store, error) {
	if spec.Log == nil {
		return nil, fmt.Errorf("%w: a store writes to a log and this spec names none", event.ErrWrongStore)
	}
	maxBatch, err := bound("MaxBatch", spec.MaxBatch, defaultMaxBatch, event.MaxBatchCount)
	if err != nil {
		return nil, err
	}
	page := min(defaultPage, event.ResidentPage(spec.Log.maxPayload))
	streamPage, err := bound("StreamPage", spec.StreamPage, page, event.MaxPageCount)
	if err != nil {
		return nil, err
	}
	maxRead, err := bound("MaxRead", spec.MaxRead, page, event.MaxPageCount)
	if err != nil {
		return nil, err
	}
	limits := event.Limits{
		MaxPayload: spec.Log.maxPayload,
		MaxBatch:   maxBatch,
		MaxKey:     spec.Log.maxKey,
		StreamPage: streamPage,
		MaxRead:    maxRead,
	}
	if err := resident(limits); err != nil {
		return nil, err
	}
	clock := spec.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Store{log: spec.Log, clock: clock, limits: limits}, nil
}

// The kernel's own rule, applied one door earlier so the refusal can name the
// number the caller set and the one it may not pass.
func resident(limits event.Limits) error {
	page := event.ResidentPage(limits.MaxPayload)
	if limits.StreamPage > page {
		return fmt.Errorf("%w: StreamPage %d at MaxPayload %d is more than the %d envelopes one read may hold",
			event.ErrWrongStore, limits.StreamPage, limits.MaxPayload, page)
	}
	if limits.MaxRead > page {
		return fmt.Errorf("%w: MaxRead %d at MaxPayload %d is more than the %d envelopes one read may hold",
			event.ErrWrongStore, limits.MaxRead, limits.MaxPayload, page)
	}
	return nil
}

func (this *Store) Capabilities() event.Capabilities {
	return event.Capabilities{
		Transactions:       event.Supported,
		Persistence:        event.Unsupported,
		MonotoneVisibility: event.Supported,
		SharedBacking:      event.Supported,
	}
}

func (this *Store) Limits() event.Limits { return this.limits }

func (this *Store) Backing() event.Backing { return this.log.backing }

// It closes nothing: the log was constructed by the composition root and is
// shared with every other store over it, and staged work belongs to the
// transaction that staged it.
func (this *Store) Close() error {
	this.closed.Store(true)
	return nil
}

func (this *Store) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if this.closed.Load() {
		return event.ErrClosed
	}
	return nil
}
