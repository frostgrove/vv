# UC-026 — Run background work without losing a worker silently

**Actor:** the application author with sweeps, collectors and consumers
**Covered by:** [[FL-028]]

## Scenario

The author has half a dozen background activities: a sweep that repairs
unattended work every few minutes, a collector that removes orphaned files, a
queue consumer that blocks on its own driver. Today each one starts itself from
its constructor, which means the composition root has to mention every type in
an otherwise empty call so the container builds them at all — and a worker whose
goroutine panics, or returns early on a branch nobody thought about, disappears
without a line anywhere.

The author wants the activity to be a value the program collects, started by
something whose job that is, and wants a worker that stops to be as loud as a
worker that never started.

## What must hold

1. A background activity is a value with a name and a blocking run. Contributing
   it is what makes it run; no call that merely mentions its type is needed, and
   deleting such a call cannot silently stop it.
2. A run that ends before it was told to — with an error, without one, or by
   panicking — is recorded as a failure against that activity's name and
   reported. One activity's panic does not end another's.
3. By default a failed activity brings the process down, and choosing otherwise
   is an explicit named decision rather than an omitted flag.
4. An activity is not handed the context that ends when start-up ends.
5. Shutdown lets an activity finish what it is holding before it is cancelled,
   within a bounded grace, and a shutdown that overran names the activities still
   running.
6. Two activities cannot share a name, and that is refused before anything
   starts.
7. An activity may optionally say whether it is working, and the program can ask
   for that answer as one value — including the fact that an activity has died —
   without the runtime knowing what a health page is.
8. A periodic activity is one declaration: an interval, a budget per pass and the
   work. A pass that fails or panics is reported and the schedule continues; a
   pass that overruns its budget is cancelled rather than delaying every later
   one.
9. A periodic activity says out loud that it runs on every replica and survives
   nothing, so it is not mistaken for a durable schedule.
10. The short forms and the explicit specification build the same thing and
    refuse the same mistakes; neither is the only way in.
11. Observing what the activities are doing is a seam an application fills. The
    runtime does not know about metrics, traces or health, and an observer that
    panics cannot break it.
12. Stopping and starting the program's background work again does not weaken
    any of the above: a run that ends on its own after the restart is still a
    failure, the answer to "is it working" carries nothing from before the
    restart, and a start over activities the last shutdown never got back is
    refused instead of running each of them twice.

## Out of scope

- Running an activity exactly once across a cluster, or resuming one that was
  interrupted. That is durable scheduling and belongs to `jobs`.
- Restarting a failed activity in place. The supervisor reports and, by default,
  ends the process; a supervision strategy with backoff is not offered.
- Cron expressions, calendars and timezones.
- Detecting an activity that is running but making no progress. Nothing here can
  tell that from slow work; an activity that can tell says so through its own
  readiness.
