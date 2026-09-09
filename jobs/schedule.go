package jobs

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"
)

type ScheduleRevision uint16

func (revision ScheduleRevision) Valid() bool { return revision > 0 }

type ScheduleCadence struct {
	at     time.Time
	anchor time.Time
	every  time.Duration
	err    error
}

type ScheduleCadenceOption interface{ applyScheduleCadence(*ScheduleCadence) error }

type scheduleCadenceOption func(*ScheduleCadence) error

func (option scheduleCadenceOption) applyScheduleCadence(cadence *ScheduleCadence) error {
	return option(cadence)
}

func Anchor(value time.Time) ScheduleCadenceOption {
	return scheduleCadenceOption(func(cadence *ScheduleCadence) error {
		if !cadence.anchor.IsZero() {
			return invalid("schedule anchor")
		}
		canonical, err := requiredTime(value, "schedule anchor")
		if err != nil {
			return err
		}
		cadence.anchor = canonical
		return nil
	})
}

func At(value time.Time) ScheduleCadence {
	canonical, err := requiredTime(value, "schedule time")
	return ScheduleCadence{at: canonical, err: err}
}

func FixedEvery(every time.Duration, options ...ScheduleCadenceOption) ScheduleCadence {
	cadence := ScheduleCadence{every: every}
	if every <= 0 || every > MaxRetention {
		cadence.err = invalid("schedule interval")
		return cadence
	}
	for index, option := range options {
		if nilInterface(option) {
			cadence.err = fmt.Errorf("%w: schedule cadence option %d", ErrInvalid, index)
			return cadence
		}
		if err := option.applyScheduleCadence(&cadence); err != nil {
			cadence.err = fmt.Errorf("schedule cadence option %d: %w", index, err)
			return cadence
		}
	}
	if cadence.anchor.IsZero() {
		cadence.anchor = time.Unix(0, 0).UTC()
	}
	return cadence
}

func (cadence ScheduleCadence) valid() bool {
	if cadence.err != nil {
		return false
	}
	if cadence.every == 0 {
		return !cadence.at.IsZero() && cadence.anchor.IsZero()
	}
	return cadence.every > 0 && cadence.every <= MaxRetention && cadence.at.IsZero() && !cadence.anchor.IsZero()
}

func (cadence ScheduleCadence) occurrence(now time.Time) (time.Time, time.Time, bool) {
	if !cadence.valid() || now.IsZero() {
		return time.Time{}, time.Time{}, false
	}
	if cadence.every == 0 {
		if now.Before(cadence.at) {
			return time.Time{}, cadence.at, false
		}
		return cadence.at, time.Time{}, true
	}
	if now.Before(cadence.anchor) {
		return time.Time{}, cadence.anchor, false
	}
	due := cadence.anchor.Add(now.Sub(cadence.anchor) / cadence.every * cadence.every)
	return due, due.Add(cadence.every), true
}

// A scheduled run may overlap the previous one. There is no way to ask for
// otherwise, and there was not before either: SkipOverlap was a public constant
// DefineSchedule refused every time, and ScheduleDescription.NoOverlap could
// therefore never be true. Naming a capability that does not exist is worse than
// not naming it, so both are gone. A consumer that needs one run at a time takes
// its own lock inside the handler, where it knows what "at a time" means.
type ScheduleSpec[P any] struct {
	Name     Name
	Revision ScheduleRevision
	Cadence  ScheduleCadence
	Job      DefinitionOf[P]
	Payload  func(time.Time) (P, error)
}

type ScheduleDescription struct {
	Name     Name
	Revision ScheduleRevision
	Job      Name
	At       time.Time
	Anchor   time.Time
	Every    time.Duration
}

type Schedule interface {
	Describe() ScheduleDescription
	scheduleEntry() scheduleEntry
}

type typedSchedule[P any] struct {
	description ScheduleDescription
	definition  DefinitionOf[P]
	payload     func(time.Time) (P, error)
}

type scheduleEntry struct {
	description ScheduleDescription
	declaration Declaration
	enqueue     func(context.Context, *Queue, time.Time) (EnqueueOnceOutcome, error)
}

func DefineSchedule[P any](spec ScheduleSpec[P]) (Schedule, error) {
	if !spec.Name.valid() || !spec.Revision.Valid() || !spec.Cadence.valid() || nilInterface(spec.Job) || spec.Payload == nil {
		return nil, invalid("schedule")
	}
	declaration := declarationOf(spec.Job)
	if nilInterface(declaration) || !declaration.declarationName().valid() || spec.Job.Partition() != PartitionGlobal {
		return nil, fmt.Errorf("%w: schedule job must be a resolved global definition", ErrUnsupported)
	}
	// The scheduler has exactly one placement path — EnqueueOnce, keyed on the
	// occurrence — and EnqueueOnce needs a payload identity. Accepting a job
	// without one built a schedule whose every run fails at enqueue, and it fails
	// at run time on the scheduler's goroutine rather than here, where the
	// deployment is still looking at the wiring.
	// The only thing stopping one occurrence firing twice is the EnqueueOnce
	// intent it is keyed on, and that intent is swept on the job's own
	// IntentRetention — a value chosen for deduplicating producers, with no
	// relation to this cadence. When the retention is shorter than the period,
	// the occurrence's intent is gone while the occurrence is still the current
	// one, and the next cycle places it again.
	if spec.Cadence.every > 0 && spec.Job.Policy().IntentRetention < spec.Cadence.every {
		return nil, fmt.Errorf("%w: the job's intent retention is shorter than this cadence, so an occurrence would be placed more than once", ErrUnsupported)
	}
	if !spec.Job.PayloadIdentity().Available {
		return nil, fmt.Errorf("%w: schedule job has no payload identity, and the scheduler places only with EnqueueOnce", ErrUnsupported)
	}
	description := ScheduleDescription{
		Name:     spec.Name,
		Revision: spec.Revision,
		Job:      spec.Job.Name(),
		At:       spec.Cadence.at,
		Anchor:   spec.Cadence.anchor,
		Every:    spec.Cadence.every,
	}
	return &typedSchedule[P]{description: description, definition: spec.Job, payload: spec.Payload}, nil
}

func (schedule *typedSchedule[P]) Describe() ScheduleDescription {
	if schedule == nil {
		return ScheduleDescription{}
	}
	return schedule.description
}

func (schedule *typedSchedule[P]) scheduleEntry() scheduleEntry {
	if schedule == nil {
		return scheduleEntry{}
	}
	return scheduleEntry{
		description: schedule.description,
		declaration: declarationOf(schedule.definition),
		enqueue: func(ctx context.Context, queue *Queue, due time.Time) (EnqueueOnceOutcome, error) {
			payload, err := invokeSchedulePayload(schedule.payload, due)
			if err != nil {
				return 0, err
			}
			key := scheduleIntent(schedule.description, due)
			_, outcome, err := EnqueueOnce(ctx, queue, schedule.definition, Intent(key), payload)
			return outcome, err
		},
	}
}

func invokeSchedulePayload[P any](payload func(time.Time) (P, error), due time.Time) (value P, err error) {
	defer func() {
		if recover() != nil {
			var zero P
			value = zero
			err = fmt.Errorf("%w: schedule payload panicked", ErrInvalid)
		}
	}()
	return payload(due)
}

func scheduleIntent(description ScheduleDescription, due time.Time) string {
	base := "schedule:" + description.Name.Value() + ":" + strconv.FormatUint(uint64(description.Revision), 10)
	return base + ":" + strconv.FormatInt(due.UnixNano(), 10)
}

type SchedulerSpec struct {
	Queue    *Queue
	Clock    Clock
	Observer ScheduleObserver
}

type ScheduleRunResult struct {
	Due       int
	Placed    int
	Existing  int
	Conflicts int
}

type Scheduler struct {
	queue     *Queue
	clock     *workerClock
	observer  ScheduleObserver
	schedules []scheduleEntry
	placed    map[int]time.Time
	cycle     atomic.Bool
	state     atomic.Uint32
}

const (
	schedulerFresh uint32 = iota
	schedulerRunning
	schedulerStopped
)

func NewScheduler(spec SchedulerSpec, schedules ...Schedule) (*Scheduler, error) {
	if spec.Queue == nil || len(schedules) == 0 || len(schedules) > MaxDefinitions {
		return nil, invalid("scheduler queue or schedules")
	}
	clock := spec.Clock
	if nilInterface(clock) {
		clock = systemClock{}
	}
	guarded, err := newWorkerClock(clock)
	if err != nil {
		return nil, err
	}
	entries := make([]scheduleEntry, len(schedules))
	names := make(map[Name]struct{}, len(schedules))
	for index, schedule := range schedules {
		if nilInterface(schedule) {
			return nil, invalid("scheduler schedule")
		}
		entry := schedule.scheduleEntry()
		description := entry.description
		registered, ok := spec.Queue.catalog.Lookup(description.Job)
		if !ok || registered != entry.declaration || !description.Name.valid() || !description.Revision.Valid() || entry.enqueue == nil {
			return nil, invalid("scheduler schedule")
		}
		if _, exists := names[description.Name]; exists {
			return nil, fmt.Errorf("%w: duplicate schedule", ErrConflict)
		}
		names[description.Name] = struct{}{}
		entries[index] = entry
	}
	return &Scheduler{queue: spec.Queue, clock: guarded, observer: spec.Observer, schedules: entries, placed: make(map[int]time.Time, len(entries))}, nil
}

func (scheduler *Scheduler) RunDue(ctx context.Context) (result ScheduleRunResult, err error) {
	if scheduler == nil || nilInterface(ctx) || !scheduler.cycle.CompareAndSwap(false, true) {
		return ScheduleRunResult{}, ErrConflict
	}
	started := time.Now()
	defer func() {
		elapsed := time.Since(started)
		if elapsed < 0 {
			elapsed = 0
		}
		observeSchedule(scheduler.observer, ctx, ScheduleEvent{result: result, err: err, elapsed: elapsed})
		scheduler.cycle.Store(false)
	}()
	if err = ctx.Err(); err != nil {
		return ScheduleRunResult{}, err
	}
	now, clockErr := scheduler.clock.Now()
	if clockErr != nil {
		return ScheduleRunResult{}, ErrInvalid
	}
	return scheduler.runDue(ctx, now)
}

func (scheduler *Scheduler) runDue(ctx context.Context, now time.Time) (ScheduleRunResult, error) {
	var result ScheduleRunResult
	for index, schedule := range scheduler.schedules {
		cadence := ScheduleCadence{at: schedule.description.At, anchor: schedule.description.Anchor, every: schedule.description.Every}
		due, _, ready := cadence.occurrence(now)
		if !ready {
			continue
		}
		// An At occurrence never advances, so once it is past it is due on every
		// cycle forever and the intent is the only thing refusing it. This makes
		// the refusal local as well, which is what stops a swept intent turning a
		// run-once schedule into a run-every-retention-period one for as long as
		// the scheduler stays up. A restart still forgets, which is why the
		// interval form refuses a retention shorter than its period at wiring.
		if placed, seen := scheduler.placed[index]; seen && !due.After(placed) {
			continue
		}
		result.Due++
		outcome, err := schedule.enqueue(ctx, scheduler.queue, due)
		if err != nil {
			return result, err
		}
		scheduler.placed[index] = due
		switch outcome {
		case EnqueueCreated:
			result.Placed++
		case EnqueueExistingSamePayload:
			result.Existing++
		case EnqueueConflict:
			result.Conflicts++
		default:
			return result, ErrAmbiguous
		}
	}
	return result, nil
}

func (scheduler *Scheduler) Run(ctx context.Context) error {
	if scheduler == nil || nilInterface(ctx) || !scheduler.state.CompareAndSwap(schedulerFresh, schedulerRunning) {
		return ErrConflict
	}
	defer scheduler.state.Store(schedulerStopped)
	for {
		if _, err := scheduler.RunDue(ctx); err != nil {
			return err
		}
		now, err := scheduler.clock.Now()
		if err != nil {
			return ErrInvalid
		}
		next := scheduler.next(now)
		if next.IsZero() {
			<-ctx.Done()
			return ctx.Err()
		}
		wait := next.Sub(now)
		if wait <= 0 {
			continue
		}
		timer, err := scheduler.clock.NewTimer(wait)
		if err != nil {
			return ErrInvalid
		}
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C():
			timer.Stop()
		}
	}
}

func (scheduler *Scheduler) next(now time.Time) time.Time {
	var next time.Time
	for _, schedule := range scheduler.schedules {
		cadence := ScheduleCadence{at: schedule.description.At, anchor: schedule.description.Anchor, every: schedule.description.Every}
		_, candidate, _ := cadence.occurrence(now)
		if !candidate.IsZero() && (next.IsZero() || candidate.Before(next)) {
			next = candidate
		}
	}
	return next
}
