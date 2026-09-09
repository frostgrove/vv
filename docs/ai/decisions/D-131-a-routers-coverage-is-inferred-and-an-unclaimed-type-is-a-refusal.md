# D-131 — A router's coverage is inferred from its routes, and an unclaimed type inside it is a refusal

**Status:** accepted
**Invariant:** A `Router`'s covered families are the families of the facts
registered on it, and inside a covered family every recorded type is either
routed by `On` or declared by `Ignore`; anything else is `ErrUnrouted`, which the
default classifier calls permanent and which halts the projection. Outside every
covered family an envelope is skipped, counted by `Router.Skipped()`, and carried
nowhere else. What a projection did not apply is therefore declared, or a family
it has no business with, or a halt — and never a silence.

## The decision

A global log carries every aggregate's events, and a projection wants some of
them. Three things follow, and the third is the whole decision.

**Coverage is inferred, so it cannot drift from the routes.** Every `On` declares
its fact's family; nothing declares a family on its own. A configured coverage
list is a second place to write the same thing, and the interesting failure is
the one where the two disagree: a family listed and no route registered under it
turns every event of that family into a halt, and a route registered under a
family the list omits turns it into a silent skip. Neither is a thing anyone
meant to configure.

**A missing `On` inside a covered family must not be a skip.** This is what the
decision is for. The forgotten registration is the ordinary mistake — a new fact
declared on an aggregate this projection already routes, and nobody added the
route — and its two possible behaviours are not close. As a skip, the read model
is missing a whole event type, for ever, with no error on any path and a
checkpoint that advances past it; the symptom is a number that is wrong in a
report, months later. As a refusal, the projection halts on the first such event,
`Ready` says so, and the read model stops at a point it is consistent up to.
Halting a projection is loud and recoverable; a wrong read model is quiet and
is not.

**The skip that remains is not on `State`.** A foreign family is a real,
permanent, expected condition — a projection over `orders` reads every
`payments` event ever written and wants none of them — so it is neither an error
nor an observation an operator is asked about. It is a counter,
`Router.Skipped()`, and there is exactly one because inside a covered family this
router skips nothing. `State` carries phase, progress, attempt and failure; a
number that rises on every foreign event would be read as a lag signal and is
not one.

**`RefuseForeign` exists for the other deployment**, where the log is this
projection's alone and a family it does not route is a wiring mistake rather than
somebody else's data. It answers the **same** sentinel: `ErrUnrouted`, so a
consumer branches on one refusal rather than two, and `Classify` calls it
permanent by its own rule — a type nothing routes does not become routed by
trying again — rather than by membership of the history class it is not in.

**`Ignore` is by name and never by fact.** Naming a type this build declares no
fact for is the case that matters: a type retired from the model still exists in
the log for ever, and a projection that must not halt on it needs a way to say so
without resurrecting a declaration. It also fails in the safe direction under a
rename: the old name stays ignored and the new one halts, which is a build that
stops rather than a build that quietly drops a renamed event.

**The router seals on its first `Apply`.** It is the aggregate's own idiom one
level out, and it is what makes the maps safe to read without a lock on the hot
path: they are written only under the mutex and only while unsealed, the seal is
taken under that same mutex, and a registration arriving afterwards is
`event.ErrDeclaration` rather than a race against a page. A route a projection
has already run without is not one it can be given now — the events it would have
claimed are behind the checkpoint.

**Two panicking doors and two returning ones**, which is [[D-123]] unchanged:
`On` and `Ignore` panic, `TryOn` and `TryIgnore` return. A route table is
composition-root code, written once at start-up, and a program assembled wrong
there must not start.

## What it forbids

- Do not make an unclaimed type of a covered family a skip, a log line or a
  counter. It is `ErrUnrouted`.
- Do not add a configured coverage list, a `Cover(family)` or a
  `Router.Families()` setter. Coverage is what the routes say it is.
- Do not put the foreign-skip count on `State`, and do not add a second counter
  for a skip inside a covered family — there is no such skip.
- Do not give the two refusals two sentinels.
- Do not make `Ignore` take a `*event.Fact`. A type with no declaration is the
  case it exists for.
- Do not allow a registration after the first `Apply`.

## Where it lives

- `event/projection/router.go` — `Router`, `NewRouter`, `On`, `TryOn`, `Ignore`,
  `TryIgnore`, `Apply`, `Skipped`, `foreignTo`, `unrouted`, `seal`, and
  `refusedName`, the kernel's identifier rule restated for a name a store
  answered rather than a name a declaration minted.
- `event/projection/classify.go` — `Classify`, where `ErrUnrouted` is permanent
  by its own rule.
- `event/fact.go` — `Fact.Read` and `Fact.Family`, which are what a route closes
  over: the decoding a route does is the declaration's own, so a routed event and
  a replayed one cannot be read two ways.

## Proven by

- `TestARouterRoutesDeclaresAndRefusesTheUnclaimed` (`event/projection`, and the
  live twin in `event/eventpg`) — two aggregates, four routes, one `Ignore`, and
  a third aggregate in the same log. **Control:** the same router with one `On`
  removed inside a covered family halts on the first such event, and the same
  removal outside every covered family skips and counts — the pair is what makes
  the rule discriminating rather than universal refusal or universal silence, and
  a third arm builds the router with `RefuseForeign` and halts on the third
  aggregate instead.
- `TestFactReadDecodesThroughItsOwnChainAndRefusesTheFourHistoryClasses` — what a
  route decodes through, and the four history-class refusals it produces and no
  other.
- `TestEveryMalformedDeclarationPanicsAndTryDefineReturnsIt` — [[D-123]]'s pair,
  which `On`/`TryOn` and `Ignore`/`TryIgnore` follow.
- `TestAHistoryClassFailureHaltsAndNamesNoData` — an unreadable payload and an
  unrouted type reach the same permanent verdict and name no data.

## See also

[[D-121]] [[D-123]] [[D-130]] [[FL-038]] [[UC-032]]
