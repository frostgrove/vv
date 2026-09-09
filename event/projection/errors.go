package projection

import "errors"

// Four, and none of them crosses a store seam: construction, lifecycle,
// contention and routing. What a store refused travels as the sentinel event
// already publishes, so a consumer reads one vocabulary rather than two.
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
var (
	ErrSpec      = errors.New("projection: this projection cannot be assembled from this spec")
	ErrHalted    = errors.New("projection: this projection stopped advancing and is not applying events")
	ErrOvertaken = errors.New("projection: a second live writer at this projection name holds the checkpoint")
	ErrUnrouted  = errors.New("projection: this envelope's type is of a family this router routes and no route claims it")
)
