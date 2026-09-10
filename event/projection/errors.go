package projection

import "errors"

// Seven, and none of them crosses a store seam: construction, lifecycle,
// contention, routing, topology, the park's room and a redrive's grant. What a
// store refused travels as the sentinel event already publishes, so a consumer
// reads one vocabulary rather than two.
//
// ErrHalted is never what Run returns — Run returns ctx.Err() — it is what Ready
// reports and what State.Err carries.
//
// ErrOvertaken is the one a deployment acts on. A second live writer at one
// projection name is a deployment that is running two of a singleton, which is
// what a rolling restart does on purpose for a few seconds and what a
// misconfiguration does for ever; the projection carries on either way, so this
// is how the difference is told — it reaches State.Err on every lost fence and
// Ready once the losing streak outlasts Tolerate.
//
// ErrTopology is about a set of partitions or a change to one, never about a
// spec: a mask that is not a mask, a cover with a gap or an overlap, a split at
// the published ceiling. A name a spec chose is still ErrSpec, because a name is
// something a spec names.
//
// ErrParkFull is the third verdict beside retryable and permanent, and it is the
// one an operator clears: the pass ends, the unit rolls back, nothing is parked
// and no envelope is skipped, and the partition retries without an attempt
// budget until the queue has room again.
//
// ErrClaimLost is a Park implementation's, answered to a redrive whose claim no
// longer owns the sequence it was granted. It is the operation failing rather
// than the letter failing, so it never reaches Retried.Cause.
var (
	ErrSpec      = errors.New("projection: this projection cannot be assembled from this spec")
	ErrHalted    = errors.New("projection: this projection stopped advancing and is not applying events")
	ErrOvertaken = errors.New("projection: a second live writer at this projection name holds the checkpoint")
	ErrUnrouted  = errors.New("projection: this envelope's type is of a family this router routes and no route claims it")
	ErrTopology  = errors.New("projection: this topology change is not one this projection can make")
	ErrParkFull  = errors.New("projection: this park has no room for the letter this pass has to write")
	ErrClaimLost = errors.New("projection: this claim no longer owns the sequence it was granted")
)
