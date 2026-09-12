# UC-036 — Read a change back without sleeping for it

**Actor:** the application author whose command writes to a log and whose next
read comes from a read model something else builds
**Covered by:** [[FL-038]]

## Scenario

The command succeeded. The handler has a confirmation in its hand and the client
is about to be redirected to the page that shows the result — and the page shows
the old state, because the read model is built by a consumer that has not got
there yet. The author's first fix is a `sleep`, which is wrong on a loaded day
and slow on every other. The second is a loop over the business table, which
works until the change is one that does not alter any column the loop looks at.
The third is "wait until the consumer has no lag", which is a question nobody can
answer: there is no end of the log to be at the end of.

The author who reaches for the real answer then meets the part that is silent.
A consumer that parks a failing sequence and carries on **advances its cursor
past the change it did not apply**, so a cursor that has passed the change proves
delivery and not application: the wait finishes, the page still shows the old
state, and nothing anywhere reports a problem. If the deployment is rebuilding
that read model beside the live one, there is a second trap on top: the wait can
be perfectly true about a generation nobody is reading.

What the author wants is to name **the change they just made**, wait for it in
the read model they are **actually** reading, get an answer inside a request's
own budget, and be told which kind of *not yet* it was when the budget runs out —
so that the branch that serves a stale page is code somebody wrote rather than a
flag somebody set.

## What must hold

1. **A wait waits for a specific change, and the target is a value the framework
   minted.** The two things that can be waited for are a **confirmed append** —
   the commit a successful write handed back — and a **barrier a generation's own
   rows were folded into**. A caller cannot write the target itself: a target a
   caller invented is evidence it invented.
2. **"Until the lag reaches zero" is not offered, in any spelling.** There is no
   head, no lag number and no "caught up". What the answer says is that everything
   at or below the named target has been delivered in the generation named, and it
   says nothing about anything above it.
3. **A target is resolved after the writing transaction has committed, never
   inside it.** A position read inside the transaction that wrote it belongs to an
   append that can still roll back, and a rolled-back append's position is never
   delivered to anybody — so a wait on such a target would never finish. Minting
   inside the writing transaction is refused; so is a commit the store does not
   show, which is *"not committed yet"* and *"rolled back"* told apart no better
   than a second connection can tell them apart.
4. **A parked change is named, not waited out.** For a consumer that has a queue
   for permanently failing work, the queue is asked **before** the progress is —
   every time, not only once the progress has arrived — and a target whose change
   is held in that queue is a refusal of its own kind, immediately. Waiting longer
   cannot help; a redrive can. This is the whole point of the guarantee: a cursor
   that passed a parked change does not prove the change was applied.
5. **Success means *applied* when a queue was supplied, and *delivered* when one
   was not**, and the difference is visible in the wiring rather than in a
   footnote. A consumer that parks and is waited on without its queue answers the
   weaker question; a target folded from a generation's rows carries nothing the
   queue can be asked about, so it is refused beside a queue rather than quietly
   answered at the weaker level.
6. **The five facts a wait needs are derived from the consumer's own
   configuration**, not restated by the request path. Three of them are silently
   wrong-able: a sequence function that is not the consumer's reports success for
   a parked change, an absent queue reports delivered where the caller asked
   applied, and a partition set that is not the one the rows were recorded at
   folds a minimum over the wrong set. Assembling the five by hand stays possible
   for a host that assembles its consumers by hand, and that is where the
   obligation lives.
7. **A target minted for one consumer is refused by another**, at the door and
   before anything is read. Two consumers over one log is an ordinary deployment;
   the position is global, so the progress comparison would succeed, and the queue
   would be asked under a key no entry of the second consumer was ever written
   under. A caller that genuinely wants the second consumer derives that
   consumer's own wait and mints its own target.
8. **The deadline is the caller's own context, and the answer says which kind of
   not-yet it was.** A deadline that elapses is a refusal carrying what the last
   readable attempt saw — where the progress had got to, how far short of the
   target it was, whether it **moved** across the attempts, and how many were
   made. A cancellation is the caller's own cancellation and nothing else.
9. **There is no field that turns a refusal into a success.** Serving stale data
   is a branch a reviewer can see — *if the wait refused because the change is not
   visible yet, serve the old page* — and never a flag every caller ends up
   passing.
10. **A wait scoped to a generation is refused when reads resolve elsewhere.**
    Where the deployment can say which generation reads resolve to, the wait reads
    that before it starts and again on the attempt that would otherwise succeed,
    and a cutover that committed in between is a refusal rather than a true
    statement about a read model this caller is not reading. Where the deployment
    supplies nothing, the caller is the party that knows, and the wait says so by
    not asking.
11. **A wait that cannot be made at all is refused at once rather than after the
    deadline.** A misconfigured partition set, a checkpoint store pointing
    somewhere else and a queue that refuses to answer are all wrong from the first
    attempt and wrong for ever; a failure that appears only later is the
    deployment moving, and the deadline decides, carrying that last failure with
    it. A budget that ran out during the attempt is the budget and never the
    caller asking wrong.
12. **A wait starts nothing and writes nothing.** It runs on the caller's own
    goroutine, is bounded by the caller's deadline, costs nothing when nobody is
    waiting, saves no cursor, forgets nothing, opens no transaction and emits no
    log line, span or metric. Its report is its return value.
13. **A healthy consumer costs one extra count per attempt.** The queue's count
    is asked first and the per-change question is asked only behind a non-zero
    count, so a consumer that has parked nothing never pays for the queue at all.
    The number of attempts per second is a field the caller sets, and what that
    costs a deployment is written down rather than left to be discovered.

## Out of scope

- **Global linearizability.** Success is a statement about one named consumer
  generation, over one named partition set, resolved against one checkpoint
  store. A second consumer of the same log, a second database, a second
  generation and anything outside the partition set are all unaddressed.
- **Another replica's freshness.** The wait reports what the connection it was
  given can see. Visibility up to the target holds for a reader that resolves the
  read model on the same authority the consumer wrote it through, and for no other
  reader.
- **"The consumer is caught up."** There is no head to be at the end of, and no
  number is published that could be mistaken for one.
- **A shared background poller, a cached progress or any pre-warming.** Anything
  continuous would be a supervised runner the host has to own, and this costs
  nothing when nobody is waiting.
- **Waiting inside the read itself**, for one aggregate, by catching that
  aggregate up on demand. That cannot serve a multi-stream read model, which is
  the only case worth waiting for, and for the single-stream case a full replay
  already answers.
- **Deduplication, ordering or exactly-once anything.** Delivery is at least
  once and a wait changes nothing about it.
- **A retry, a backoff or a second attempt at the command.** A wait observes; it
  never re-issues.

## See also

[[UC-032]] is the contract this reads from — the log, the consumer and the
checkpoint rows a wait observes are all its. Nothing of UC-032's guarantees
changes here; this is a new read over the same rows. [[UC-037]] is the other half
of the same request path: what to do when the command's own outcome is the thing
that is unknown.
