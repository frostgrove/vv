package projection

import (
	"time"

	"github.com/frostgrove/vv/event"
)

type Phase string

const (
	PhaseStarting  Phase = "starting"
	PhaseDraining  Phase = "draining"
	PhaseFollowing Phase = "following"
	PhaseRetrying  Phase = "retrying"
	PhaseHalted    Phase = "halted"
)

type State struct {
	Projection string
	Phase      Phase
	Progress   event.Progress
	Attempt    int
	Err        error
	At         time.Time
}

type Observer interface {
	Observed(state State)
}

type ObserverFunc func(state State)

func (this ObserverFunc) Observed(state State) { this(state) }

// A blocking observer blocks the loop and a panicking one does not take it down:
// an observation is where a composition root logs, counts or exports, and none
// of those is a reason for a projection to stop applying events.
func observing(observer Observer, state State) {
	defer func() { _ = recover() }()
	observer.Observed(state)
}

func (this *Projection) State() State {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	return this.state
}

// Published on a change and not on every pass: an idle projection polling on its
// ticker is in the same phase at the same attempt with the same failure, so it
// publishes nothing, and a halted one publishes its halt once and no more.
func (this *Projection) transition(phase Phase, attempt int, err error) {
	this.mutex.Lock()
	moved := this.state.Phase != phase || this.state.Attempt != attempt || (this.state.Err == nil) != (err == nil)
	this.state.Phase, this.state.Attempt, this.state.Err = phase, attempt, err
	if moved {
		this.state.At = time.Now()
	}
	state := this.state
	this.mutex.Unlock()

	if moved {
		this.publish(state)
	}
}

func (this *Projection) progressed(progress event.Progress) {
	this.mutex.Lock()
	this.state.Progress, this.state.At = progress, time.Now()
	state := this.state
	this.mutex.Unlock()

	this.publish(state)
}

// The progress a resume answered is the projection's own from that moment, and
// it is not a transition: nothing has happened yet that an operator wants told
// about, and the first phase this loop enters is what says the projection is
// running.
func (this *Projection) seed(progress event.Progress) {
	this.mutex.Lock()
	defer this.mutex.Unlock()
	this.state.Progress = progress
}

func (this *Projection) publish(state State) {
	if this.spec.Observer == nil {
		return
	}
	observing(this.spec.Observer, state)
}
