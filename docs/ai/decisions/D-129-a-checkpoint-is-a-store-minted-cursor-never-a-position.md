# D-129 — A checkpoint is a store-minted cursor, never a position

**Status:** accepted
**Invariant:** The one value a consumer resumes from is the `event.Cursor` the
log minted. `Checkpoint` carries no position, `Progress` resumes nothing, and
there is no function anywhere in `event/…` from an `event.Position` to an
`event.Cursor`. A cursor is opaque bytes: it is compared for equality and for
emptiness, and never ordered.

## The decision

A checkpoint row has to carry *something* a consumer resumes from, and the
obvious something is the position of the last event it finished with. It is the
number the operator already reads, it sorts, it subtracts, and every dashboard
question — how far behind are we — answers itself from it.

It is refused, because a position is not a resume point on any store this
framework admits.

**`Log.ReadAll` takes a cursor and a cursor is the store's own encoding.**
`eventmemory`'s carries a log identity and a position; `eventpg`'s carries a log
identity and **three** numbers — how far the walk has got, a transaction id
minted after every position up to a reach had been drawn, and that reach
(`event/eventpg/cursor.go`). Two of those three are not derivable from any
position: they are what makes the walk gap-free over an identity column that
hands out positions before commit. A consumer that saved the position and
resumed from `position > p` would deliver a row whose lower neighbour was still
uncommitted, and nothing downstream could see that it had.

So a position-to-cursor function is either impossible or a lie. For `eventpg` it
is impossible — the two settlement numbers are not in the position. For a store
whose cursor *is* its position it is exact, which is worse: the shape compiles,
passes every test written against that store, and skips committed events against
the next one.

**`Progress` is the same question one field out.** `Progress.Highest` is a real
number and a useful one — [[D-128]] makes it a completeness watermark — and it is
an *observation*, published so an operator can read lag. The moment a constructor
turns one into a `Cursor` it has become a second resume authority, and two
authorities disagree the first time a store's cursor stops being a function of
its positions. `Checkpoint.Cursor` is the resume point; `Checkpoint.Advance` is a
fence and not a position; `Progress` is neither.

**Opacity is enforced by absence, not by a wrapper.** `Cursor` stays a `string`
so a checkpoint store can put it in a column, which means `<` compiles. Nothing
in the tree writes one: ordering two cursors answers the byte order of two
encodings, and `eventpg`'s `vve1` cursor is a base64 rendering of a 16-byte log
identity followed by three big-endian numbers, whose byte order is the log
identity's before it is anything of the walk's. The store that minted an encoding
is the one party that may compare what it *parsed* out of one, and it does that
on the numbers.

**What a dashboard gets instead**, so this is a redirection rather than a
refusal: `Progress.Highest`, `Progress.Applied`, `Progress.Quarantined` and
`Progress.At` are persisted columns and are exactly the four numbers an operator
reads. None of them is a resume point and the type says so.

## What it forbids

- Do not add `Checkpoint.Position`, `Progress.Resume`, `CursorAt(Position)`, or
  any other exported spelling from a position to a cursor. `scripts/projection_test.go`
  walks the whole published surface of the extension for that signature.
- Do not order two cursors. `==`, `!=` and the empty test are the whole of what a
  cursor answers outside the store that minted it.
- Do not restate `Progress` as a resume point in a doc, a field name or a
  comment. It is written at the save and read by a person.
- Do not make `Cursor` a struct to enforce the above. It has to fit a column, and
  a type that cannot be stored is one every store base64s by hand.

## Where it lives

- `event/checkpoint.go` — `Checkpoint`, `Progress`, `Checkpoints` and the
  `Tracker` door, which is the only caller of a `Checkpoints`.
- `event/identity.go` — `Cursor`, the string it is.
- `event/eventpg/cursor.go` — the three numbers a cursor of that store carries,
  and the tag that refuses a foreign or retired encoding.
- `scripts/projection_test.go` — the two surface walks and the comparison walk.

## Proven by

- `TestNoExportedFunctionTakesAPositionAndAnswersACursor` and
  `TestNoConstructorTakesAProgressAndAnswersACursor` — a `go/types` walk over
  every package `docs/api/surface.md` lists for the extension. **Control:** the
  same walk must find `event.Read`, which takes a cursor and answers a reader, so
  a walk that resolved nothing is not read as a clean tree.
- `TestCursorIsNeverCompared` — the ordering operators over an `event.Cursor`,
  with a fixture that orders two and must be reported.
- `TestPresenceIsTotalAndAnEmptyCursorIsNeverAResumePoint` and
  `TestAReaderRefusesAnEmptyCursorBesideANonEmptyPage` — the empty cursor is the
  origin of a log, refused at the tracker's door and at the reader's.
- `TestACursorIsTheBackingsAndResumesThroughAnyStoreValueOverIt` and
  `TestACursorAStoreCannotParseIsRefusedRatherThanReadFromTheBeginning` — a
  cursor belongs to a backing, and an unreadable one is never read as the origin.

## See also

[[D-121]] [[D-128]] [[D-133]] [[FL-036]] [[FL-038]] [[UC-032]]
