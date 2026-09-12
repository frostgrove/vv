package projection

import "errors"

// Twelve, and none of them crosses a store seam: construction, lifecycle,
// contention, routing, topology, the park's room, a redrive's grant, a
// generation's rows, and the four a wait answers. What a store refused travels as
// the sentinel event already publishes, so a consumer reads one vocabulary rather
// than two.
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
//
// ErrRetired is a cutover to a generation whose checkpoint rows are gone. A
// generation that was dropped and one that never ran read alike from the rows,
// and the read target is pointed at neither: what stands behind both is nothing.
//
// ErrNotVisible is a deadline and never a defect: the generation had not
// delivered the mark when the caller stopped being able to wait. What kind of
// not-yet it was travels on Visibility rather than in the sentinel.
//
// ErrParked is the answer a scan checkpoint cannot give. The sequence this change
// belongs to is blocked, so the watermark passed the event and the read model
// never received it, and no amount of waiting changes that.
//
// ErrUncommitted is about the caller's own transaction and not about a store: a
// store that answers no row at the version a Commit reports is a store whose
// writer has not committed or has rolled back, and the two are indistinguishable
// from a second connection.
//
// ErrGeneration is the wait's own scope, refused rather than answered: the
// generation this wait names is not the one the ownership row resolves reads to,
// so reaching the mark in it would be a true statement about a read model this
// caller is not reading.
var (
	ErrSpec        = errors.New("projection: this projection cannot be assembled from this spec")
	ErrHalted      = errors.New("projection: this projection stopped advancing and is not applying events")
	ErrOvertaken   = errors.New("projection: a second live writer at this projection name holds the checkpoint")
	ErrUnrouted    = errors.New("projection: this envelope's type is of a family this router routes and no route claims it")
	ErrTopology    = errors.New("projection: this topology change is not one this projection can make")
	ErrParkFull    = errors.New("projection: this park has no room for the letter this pass has to write")
	ErrClaimLost   = errors.New("projection: this claim no longer owns the sequence it was granted")
	ErrRetired     = errors.New("projection: this generation's checkpoints are gone, so nothing may be pointed back at it")
	ErrNotVisible  = errors.New("projection: this change was not visible in this generation's read model within the deadline this wait was given")
	ErrParked      = errors.New("projection: the sequence this change belongs to is parked, so the scan passed it and the read model never received it")
	ErrUncommitted = errors.New("projection: this store shows no event at the version this commit reports, so the transaction that wrote it has not committed or it rolled back")
	ErrGeneration  = errors.New("projection: this wait names a generation that is not the one reads of this projection resolve to")
)
