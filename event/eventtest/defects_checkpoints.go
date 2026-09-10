package eventtest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/frostgrove/vv/event"
)

// One inventory of the defects the checkpoint suite is built to detect, in code,
// because a count kept in prose is what drifts: a test runs the section each row
// names against the store that row describes and fails when the section passes.
//
// All eight are decorators over a checkpoint store that is otherwise correct, and
// each is a shape a real implementation reaches by writing one statement wrong —
// a read-then-write instead of a conditional update, a Load whose parameter is
// the wrong one, a save issued on a pool while a caller holds a transaction, an
// ON CONFLICT DO UPDATE where the row that is there is the answer.
type checkpointDefect struct {
	name    string
	section string
	over    func(event.Checkpoints) event.Checkpoints
}

func checkpointDefects() []checkpointDefect {
	return []checkpointDefect{
		{"reads the row and then writes it, so every save lands", "concurrency",
			func(held event.Checkpoints) event.Checkpoints { return unfenced{held} }},
		{"admits a save that skips an advance", "fence",
			func(held event.Checkpoints) event.Checkpoints { return ahead{held} }},
		{"answers a transaction of its own outside every unit of work", "binding",
			func(held event.Checkpoints) event.Checkpoints { return ambient{held} }},
		{"refuses to forget a name that has no row", "forget",
			func(held event.Checkpoints) event.Checkpoints { return pedantic{held} }},
		{"keeps a cursor in a column narrower than the published ceiling", "bounds",
			func(held event.Checkpoints) event.Checkpoints { return narrow{held} }},
		{"wraps a cancellation in its own text", "refusal classes",
			func(held event.Checkpoints) event.Checkpoints { return verbose{held} }},
		{"answers an error the second time it is closed", "lifecycle",
			func(held event.Checkpoints) event.Checkpoints { return &closing{Checkpoints: held} }},
		{"answers the cursor it held before the last save", "round trip",
			func(held event.Checkpoints) event.Checkpoints {
				return &stale{Checkpoints: held, seen: map[string][]event.Cursor{}}
			}},
		{"reports absence for a row that exists", "absence",
			func(held event.Checkpoints) event.Checkpoints { return absent{held} }},
		{"ignores the projection argument of Load", "names",
			func(held event.Checkpoints) event.Checkpoints { return &oneName{Checkpoints: held} }},
		{"saves outside the caller's transaction", "transactions",
			func(held event.Checkpoints) event.Checkpoints { return detaching{held} }},
		{"binds the cursor to the name that saved it", "topology",
			func(held event.Checkpoints) event.Checkpoints {
				return &namespaced{Checkpoints: held, minted: map[event.Cursor]string{}}
			}},
		{"creates a row at advance 1 over a live one", "topology",
			func(held event.Checkpoints) event.Checkpoints { return adopting{held} }},
		{"forgets outside the caller's transaction", "topology handoff",
			func(held event.Checkpoints) event.Checkpoints { return retiring{held} }},
	}
}

// The fence read into the process and applied there, which is correct only
// against a row nobody else can move.
type unfenced struct{ event.Checkpoints }

func (this unfenced) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	held, err := this.Checkpoints.Load(ctx, checkpoint.Projection)
	if err != nil {
		return err
	}
	checkpoint.Advance = held.Advance + 1
	return this.Checkpoints.Save(ctx, checkpoint)
}

// The fence written as "not below what we hold" rather than "exactly one above
// it": a save that skips an advance is taken for the next one, so two writers
// that reach it at different speeds each go on believing they own the row. It is
// correct under contention at one advance, which is what keeps it out of the
// section that drives that.
type ahead struct{ event.Checkpoints }

func (this ahead) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	held, err := this.Checkpoints.Load(ctx, checkpoint.Projection)
	if err != nil {
		return err
	}
	if checkpoint.Advance > held.Advance+1 {
		checkpoint.Advance = held.Advance + 1
	}
	return this.Checkpoints.Save(ctx, checkpoint)
}

// A store that derives its authority from a connection it holds rather than
// from the unit of work the caller bound: asked outside every one of them it
// answers a transaction anyway, and a consumer checking that its advance rides
// in the caller's is told it does when it does not.
type ambient struct{ event.Checkpoints }

func (this ambient) Transaction(ctx context.Context) (event.Authority, error) {
	held, err := this.Checkpoints.Transaction(ctx)
	if err != nil || held.Valid() {
		return held, err
	}
	return event.NewAuthority(this.Checkpoints.Backing(), this)
}

// The removal written as an update of a row it read first: a name that has no
// row is refused rather than answered, so retiring a projection is not something
// an operator can run twice, and neither is the second half of a handoff that
// was interrupted.
type pedantic struct{ event.Checkpoints }

func (this pedantic) Forget(ctx context.Context, projection string) error {
	held, err := this.Checkpoints.Load(ctx, projection)
	if err != nil {
		return err
	}
	if held.Fresh() {
		return event.Failure(event.Refused, errNothingToForget)
	}
	return this.Checkpoints.Forget(ctx, projection)
}

var errNothingToForget = errors.New("eventtest: this store refuses to remove a row it does not have")

// The cursor column that is a varchar somebody sized by eye: everything this
// suite and every shipped store mints fits, and the ceiling the kernel publishes
// does not, so the one cursor that is refused is the widest one a log is allowed
// to answer.
type narrow struct{ event.Checkpoints }

const narrowestColumn = 255

func (this narrow) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	if len(checkpoint.Cursor) > narrowestColumn {
		checkpoint.Cursor = checkpoint.Cursor[:narrowestColumn]
	}
	return this.Checkpoints.Save(ctx, checkpoint)
}

// A cancellation carried inside this store's own sentence, which is what an
// error wrapped for a log line looks like from the outside: a caller matching a
// cancellation no longer recognises one, and a shutdown reads as a store that
// failed.
type verbose struct{ event.Checkpoints }

func (this verbose) Load(ctx context.Context, projection string) (event.Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return event.Checkpoint{}, fmt.Errorf("eventtest: this store was reading %q: %w", projection, err)
	}
	return this.Checkpoints.Load(ctx, projection)
}

func (this verbose) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("eventtest: this store was writing %q: %w", checkpoint.Projection, err)
	}
	return this.Checkpoints.Save(ctx, checkpoint)
}

func (this verbose) Forget(ctx context.Context, projection string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("eventtest: this store was removing %q: %w", projection, err)
	}
	return this.Checkpoints.Forget(ctx, projection)
}

// The close written as a state transition rather than as the idempotent one it
// is: a composition root that closes twice — a shutdown running its own defer
// beside the container's — is told the second one failed and reports a clean
// stop as a fault.
type closing struct {
	event.Checkpoints
	closed atomic.Bool
}

func (this *closing) Close() error {
	if !this.closed.CompareAndSwap(false, true) {
		return errClosedTwice
	}
	return this.Checkpoints.Close()
}

var errClosedTwice = errors.New("eventtest: this store has already been closed")

// A checkpoint one save behind: the row is this store's own and the cursor is
// the one before it, which is what a statement that reads a column of a
// yesterday's copy answers.
type stale struct {
	event.Checkpoints

	mutex sync.Mutex
	seen  map[string][]event.Cursor
}

func (this *stale) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	if err := this.Checkpoints.Save(ctx, checkpoint); err != nil {
		return err
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.seen[checkpoint.Projection] = append(this.seen[checkpoint.Projection], checkpoint.Cursor)
	return nil
}

func (this *stale) Load(ctx context.Context, projection string) (event.Checkpoint, error) {
	held, err := this.Checkpoints.Load(ctx, projection)
	if err != nil || held.Fresh() {
		return held, err
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if saved := this.seen[projection]; len(saved) > 1 {
		held.Cursor = saved[len(saved)-2]
	}
	return held, nil
}

type absent struct{ event.Checkpoints }

func (this absent) Load(context.Context, string) (event.Checkpoint, error) {
	return event.Checkpoint{}, nil
}

// The Load whose statement is mis-parameterised: it answers the first row this
// value ever wrote, whoever asks.
type oneName struct {
	event.Checkpoints

	mutex sync.Mutex
	first string
}

func (this *oneName) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	if err := this.Checkpoints.Save(ctx, checkpoint); err != nil {
		return err
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if this.first == "" {
		this.first = checkpoint.Projection
	}
	return nil
}

func (this *oneName) Load(ctx context.Context, projection string) (event.Checkpoint, error) {
	this.mutex.Lock()
	named := this.first
	this.mutex.Unlock()
	if named != "" {
		projection = named
	}
	return this.Checkpoints.Load(ctx, projection)
}

// The store that never looks for the caller's unit of work: the deadline travels
// and the values do not, which is exactly what a save issued on the pool does.
type detaching struct{ event.Checkpoints }

func (this detaching) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	beside, stop := beside(ctx)
	defer stop()
	return this.Checkpoints.Save(beside, checkpoint)
}

// The cursor kept in a column the store also composes the projection name into,
// which is one column too many in a table already keyed by that name: a row read
// back under the name that wrote it is exact, so nothing else in this suite ever
// sees it, and a split — the one write that gives two names one cursor — reads
// what the composition left behind.
type namespaced struct {
	event.Checkpoints

	mutex  sync.Mutex
	minted map[event.Cursor]string
}

func (this *namespaced) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	if err := this.Checkpoints.Save(ctx, checkpoint); err != nil {
		return err
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if _, seen := this.minted[checkpoint.Cursor]; !seen {
		this.minted[checkpoint.Cursor] = checkpoint.Projection
	}
	return nil
}

func (this *namespaced) Load(ctx context.Context, projection string) (event.Checkpoint, error) {
	held, err := this.Checkpoints.Load(ctx, projection)
	if err != nil || held.Fresh() {
		return held, err
	}
	this.mutex.Lock()
	defer this.mutex.Unlock()
	if under, seen := this.minted[held.Cursor]; seen && under != projection {
		held.Cursor = recomposed(held.Cursor, projection)
	}
	return held, nil
}

// A cursor of the width it had, differing from the one that went in: the width
// is what keeps this decorator out of the bounds section, and the difference is
// the whole of what a split reads.
func recomposed(cursor event.Cursor, projection string) event.Cursor {
	held := []byte(cursor)
	digest := byte(len(projection))
	for _, letter := range []byte(projection) {
		digest ^= letter
	}
	held[0] ^= digest | 1
	return event.Cursor(held)
}

// INSERT ... ON CONFLICT DO UPDATE where the row that is there is the answer: a
// save at advance 1 lands over whatever stands at that name, so the child row a
// split writes adopts a partition that is already recording, at a cursor and a
// watermark that are not its own.
type adopting struct{ event.Checkpoints }

func (this adopting) Save(ctx context.Context, checkpoint event.Checkpoint) error {
	if checkpoint.Advance != 1 {
		return this.Checkpoints.Save(ctx, checkpoint)
	}
	if err := this.Checkpoints.Forget(ctx, checkpoint.Projection); err != nil {
		return err
	}
	return this.Checkpoints.Save(ctx, checkpoint)
}

// The retirement issued on the pool while the caller holds a transaction, which
// is what a Forget that never looks for the caller's unit of work does: the
// parent stays retired when the unit rolls back, and the handoff is half made
// with no error on any path.
type retiring struct{ event.Checkpoints }

func (this retiring) Forget(ctx context.Context, projection string) error {
	beside, stop := beside(ctx)
	defer stop()
	return this.Checkpoints.Forget(beside, projection)
}

func beside(ctx context.Context) (context.Context, context.CancelFunc) {
	if deadline, held := ctx.Deadline(); held {
		return context.WithDeadline(context.Background(), deadline)
	}
	return context.WithCancel(context.Background())
}
