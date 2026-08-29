# D-071 — A path map may be derived from the model's tags, and every gap in the derivation is a refusal

**Status:** accepted
**Invariant:** `port.Paths[M]` builds an entry only from a tag the caller named. A column no named tag gives a key for is a start-up refusal naming that column — never a key invented from the Go field name. The field-name fallback is a separate explicit call, and it never covers a column whose tag took it off the wire. The result goes through `NewPathMap` like any hand-written map.

## The decision

`port.Paths[M]()` answers a builder that derives a [PathMap](../../modules/en/port.md#the-path-chain)
from the model's own wire tags:

```go
var ContractPaths = port.Paths[Contract]().
    From("json", "form").
    Override(port.PathMap{"SourceHash": port.At(FieldFile)}).
    MustBuild()
```

Four rules, and each of them is a refusal rather than a default:

| Situation | Answer |
|---|---|
| a named tag gives a key | that key, options cut off — `json:"weight,omitempty"` is `weight` |
| a named tag gives only options | the Go field name, because that is what `encoding/json` does with `json:",omitempty"` |
| no named tag is on the field | **refusal**, unless `OrFieldName()` was called |
| a named tag is `-` | **refusal**, and `OrFieldName()` does *not* cover it — `Except` or `Override` do |
| an override names a column the model does not have, or one no request carries | **refusal**, from `NewPathMap` unchanged |
| a column is both overridden and excepted | **refusal** |

`From` takes tag *names*, not a fixed set, so `wire`, `api` or `my_custom_tag`
work with no change here. Order is preference and not a merge: the first tag
that names a field wins.

This does not touch the generated resource. `cmd/vv -adapter` names its input
fields `lowerFirst(FieldName)` rather than by the model's tag ([[D-050]]), so
its map is generated with the mapper it inverts and stays that way.

## Why

**Because the map was already derived from the model — by a person, by hand.**
Every entry in a written-out `PathMap` for a directly-mounted resource is its
`json` tag transcribed. Transcription is where `sourceFileName` gets typed for a
struct that says `sourceFilename`: the map is total, exact, passes `MustPathMap`,
and reports a key no client ever sent. Nothing catches it — there is no test
that would, because the wrong key is as well-formed as the right one. Deriving
removes the only step where the two could disagree.

**Because the totality check was never the interesting half.** `MustPathMap`
answers two questions: is the map total, and is it exact. A derived map is total
by construction — it is built over the same domain the check validates — so what
survives is the exactness arm, and that is the arm that still has work to do:
every `Override` is hand-written and every one of them is checked. The feature
narrows what a person can get wrong from *n* entries to the one or two they
deliberately typed.

**Because a fallback that guesses is worse than no feature.** The obvious
convenience is to fall back to the Go field name for anything untagged. That
produces `SourceFilename` where the wire says `sourceFilename` — a map that looks
complete, passes every check, and is wrong in exactly the way this exists to
prevent. So it is a separate call, and the consumer asking for it is the consumer
saying they have a model with no tags rather than a model missing one.

**Because `-` and silence are two different statements.** A field with no tag
might well be decoded under its own name. A field tagged `json:"-"` is the
author saying it is not on the wire at all, and giving it a key would point a
violation at something the client cannot find in its own body — the same wrong
answer [[D-050]] refuses an entry for a `generated` column over. So
`OrFieldName` answers for silence only, and the refusal names `Except` and
`Override` as the two ways out.

**Because the decoder's rules are honoured rather than reinvented.**
`json:",omitempty"` keeps the field name as the key and `json:"-,"` is a field
literally called `-`. A map that disagreed with the decoder would be wrong in
the one direction nobody checks, so both spellings are read the way
`encoding/json` reads them.

**Because only a column may lend a tag to a column.** A `db:"-"` field and a
relation can each share a Go field name with a promoted column without `crud`
objecting — it never mapped the other one — and both carry a tag of their own.
Indexing either would hand the column a key from a field that is not it.

**Because a misuse must name the model and not the builder.** `From()` with no
tag is held until `Build`, so the refusal reads as a sentence about the model a
package-level `var` is declaring rather than as a panic inside a method chain.

## What it forbids

- Do not make `OrFieldName` the default, and do not make it cover a `-` tag.
  Both turn the refusal this is built around into a guess.
- Do not use it to replace a *generated* resource's map. That map inverts a
  mapper with a wire shape of its own ([[D-050]]); deriving from the model's tags
  would answer keys the adapter does not use.
- Do not let `Build` skip an `Override` for something outside the domain.
  Silently dropping an entry somebody wrote is how a typo in one survives — the
  map would be valid and the line would do nothing.
- Do not resolve `Override` and `Except` naming the same column in favour of
  either. They are opposite instructions and half of what was written would do
  nothing.
- Do not merge two tags into one entry. A column with two wire spellings has one
  key a violation should be reported at, and the map can only hold one.
- Do not skip `NewPathMap` at the end because the map was derived. It is what
  checks the hand-written half.

## Where it lives

- `port/paths.go` — `Paths`, `PathBuilder`, `From`, `Override`, `Except`,
  `OrFieldName`, `Build`, `MustBuild`; `keyOf` and the `tagVerdict` split;
  `wireTags`, `unmapped`, `structOf`, `tagFrame`.
- `port/pathmap.go` — `NewPathMap`, which the builder finishes through, and the
  domain rule it shares.
- `crud/meta.go` — `Schema.Insert` and `Field.Version`, which are the domain.

## Proven by

- `TestADerivedMapIsTheOneSomebodyWouldHaveTyped` in `port/paths_test.go` — the
  whole map, against the literal it replaces. `Origin`'s tag is deliberately not
  any mechanical transform of its field name, so a derivation that read the
  field name could not pass; the lock and the generated column both carry a tag
  and neither gets an entry.
- `TestATagWithOptionsAndNoNameMeansTheFieldName` — `json:",omitempty"`, read
  the way the decoder reads it.
- `TestAColumnNoTagNamesIsRefusedRatherThanGuessed`, with the `OrFieldName`
  control that keeps it from passing on a builder that can never derive
  anything.
- `TestAColumnTakenOffTheWireIsNotRescuedByTheFieldNameFallback` — the
  distinction between `-` and silence, with `Except` and `Override` as the two
  cures.
- `TestTheFirstTagThatNamesAColumnWins`, with the reversed-order control that
  keeps it from passing on a builder that read whichever tag came first in the
  struct.
- `TestAnyTagKeyCanBeTheSource` — a house tag, no list of blessed names.
- `TestAnOverrideBeatsTheTagAndIsStillChecked` — both refusal arms: an override
  naming no column, and one naming a column no request carries.
- `TestOverridingAndExceptingTheSameColumnIsRefused`.
- `TestAPromotedColumnCarriesTheTagOfTheFieldItIs` — two embeddings deep.
- `TestOnlyAColumnLendsItsTagToAColumn` — a `db:"-"` field and a relation, each
  shadowing a promoted column's name.
- `TestADerivedMapResolvesLikeTheHandWrittenOne` — it is a `PathMap`: the tail
  rides along, and an undeclared head declines.
- `TestABuilderMisuseIsAnswerLater` and `TestMustBuildRefusesAtDeclaration`.

## See also

[[D-050]] — the generated map this does not replace, and where the domain rule
and the exactness refusal come from.
[[D-043]] — the hop this map is.
[[D-021]] — why the magic is allowed to be this magical, and why it has to fail
at start-up.
[[FL-015]] — where the hop sits in the request.
