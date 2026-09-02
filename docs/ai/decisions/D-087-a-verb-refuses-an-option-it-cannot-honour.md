# D-087 — A verb refuses an option it cannot honour

**Status:** accepted
**Invariant:** Every option that reaches a repository verb is either honoured by
the statement that verb runs or refused by name before it runs. No verb drops an
option and answers with a 200.

## The decision

`crud.Option` stays one type, and the verbs keep taking `...crud.Option`. What
is typed is the *verb's* contract, not the option: `crud.ReadOptions`,
`crud.MutationOptions`, `crud.AggregateOptions` and `crud.PreloadOptions` are
`OptionGroup` values that name, field by field of `crud.Options`, what that verb
reads. `OptionGroup.Build` resolves the caller's options and refuses the first
one the verb would not read, with a `*crud.SchemaError` that blames the option
the caller wrote — `Select`, not `Fields`.

| verb | honours | refuses |
|---|---|---|
| a read — `Get` `GetAll` `First` `GetByID` `Count` `Exists` | everything else | `Aggregate` / `GroupBy` |
| a write — `Update` `UpdateAll` `DeleteAll` | `Where` `NarrowRelations` `ForUpdate` `PrimaryOnly` | paging, cursors, sorting, projection, preloads, `Distinct`, aggregates |
| an aggregate — `Aggregate` | `Where` `NarrowRelations` `Aggregate` `GroupBy` `OrderBy` `Unsorted` paging `PrimaryOnly` | cursors, projection, preloads, `SkipTotal`, `ForUpdate`, `Distinct` |
| a preload — `PreloadWhere` `PreloadCap` | `Where` `OrderBy` `PreloadRows` | everything else ([[D-006]]) |

The check walks the whole `Options` value by reflection, so a field added later
is refused by every group that did not classify it, and the group table is
proven complete by a test rather than by memory.

## Why

**Because a dropped option is the one failure a caller cannot see.** This is
[[D-053]]'s argument, one layer down. A client that sends an option the wire
cannot carry is refused by name; a Go caller that passes `crud.Limit(10)` to
`UpdateAll` used to get a well-formed answer, a rows-affected count, and every
row the filter matched rewritten. The two halves of the library disagreed about
the same mistake, and the local half was the one that could actually destroy
data.

**Because that ambiguity was a security hole with its own decision.** [[D-026]]
described `Inspect` being shown ten rows while the `UPDATE` touched all of them,
listed three ways out, and named the third — refuse the option in the SQL
repository — as the most defensible and the one not taken. It is taken here. The
gate's victim fetch had meanwhile stopped honouring the caller's paging
(`inspectionRead` zeroes it), so the divergence D-026 described was already half
closed; what remained was the silent drop, and a refusal closes it in one place
for gated and ungated repositories alike.

**Why the verb is typed and not the option.** The obvious shape is
`type ReadOption`, `type MutationOption` and so on, with each verb taking its
own, so a wrong option is a compile error. Go cannot get there from here without
breaking every caller: two defined function types with the same underlying type
are not assignable to one another, so `crud.Limit` would have to return a
narrower type, and every `[]crud.Option` that collects it — `auth/access`,
`remote`, the integration suite, and consumer code the library cannot see —
stops compiling. [[D-006]] already recorded the same trade-off in the small,
for `PreloadWhere`. A refusal at the door costs one reflection walk per call and
buys the same guarantee for code that already exists.

**Why `*crud.SchemaError`.** A transport already turns it into a 400 that names
a field ([[FL-011]]), and that is exactly what this is: the caller asked for
something this model's verb cannot express.

**Why a read still accepts `ForUpdate`, `PrimaryOnly` and `Unsorted`.** They
change which connection answers and in what order, not which rows come back —
the same line [[D-053]] draws between refusing and documenting. A read consults
all three.

**Why a write still accepts `ForUpdate` and `PrimaryOnly`.** `Update` reads the
row before it diffs it ([[D-010]]), and that read is the one a caller locks. A
set-based `UPDATE` or `DELETE` locks what it touches anyway. `PrimaryOnly` on a
write is a true statement rather than a dropped one — a write never goes
anywhere else — and it is spelled out loud where a caller composes one option
list over a read and a write, as `auth/access.Store.FenceSessionIssue` does.
Refusing it would be [[D-053]]'s third row read backwards.

## What it forbids

- Do not add an option to a group's allow-list without making the verb read it.
  The list is a claim about the statement, not a way to quiet a refusal.
- Do not answer an unsupported option with a log line, a zeroed field or a
  silently narrowed statement. Those are the three shapes of the failure this
  decision exists to remove.
- Do not make `repository.UpdateAll` or `repository.DeleteAll` honour `Limit`
  instead. [[D-026]] forbids it and the reason has not changed: a partially
  applied filtered write is worse than either answer, and no dialect spells
  `DELETE … LIMIT` portably.
- Do not turn the groups into types on the verbs' signatures without a written
  plan for every `[]crud.Option` in the tree and outside it. Reversing this
  decision is a breaking change to the seam, not a refactor.

## Where it lives

- `crud/optiongroup.go:OptionGroup` — the four groups, the allow-lists, the
  refusal texts, and `Build`/`Check`.
- `crud/optiongroup.go:OptionSpelling` — the `Options` field a refusal was found
  in, spelled as the constructor the caller wrote.
- `crud/preload.go:validatePreloadOptions` — the preload group, which is where
  this mechanism started ([[D-006]]) and which keeps its own error shape because
  a preload refusal names the path.
- `crud/sqlrepo/repository.go:repository.Get` / `:GetAll` / `:First` /
  `:GetByID` / `:Count` / `:Exists` — `ReadOptions.Build`.
- `crud/sqlrepo/repository.go:repository.Update` / `:UpdateAll` / `:DeleteAll` —
  `MutationOptions.Build`, before any statement is planned.
- `crud/sqlrepo/repository.go:repository.Aggregate` — `AggregateOptions.Build`.
- `remote/options.go:ToRequest` — the same rule for a transport, and the older
  half of it ([[D-053]]).

## Proven by

- `TestAFilteredWriteRefusesTheOptionsItWouldNotApply` in
  `crud/sqlrepo/optiongroup_test.go` — seven options across `UpdateAll`,
  `DeleteAll` and `Update`, each blamed by the name the caller wrote, with the
  recorder asserted to have run no statement at all. Its control is the last
  subtest: a filter and a lock still go through, so the test cannot pass for a
  repository that refuses everything.
- `TestAnAggregateRefusesTheOptionsItWouldNotApply` and
  `TestAReadRefusesAnAggregateItCannotAnswer` in the same file, each with the
  same shape of control.
- `TestAGatedFilteredWriteRefusesPagingRatherThanWritingEveryRowItShowedTheRule`
  in `crud/decorators/security/updateall_test.go` — the test [[D-026]] asked for.
  Its control asserts the same write without the paging option succeeds and that
  `Inspect` saw both rows.
- `TestAVerbRefusesTheOptionItCannotHonour` and
  `TestAVerbAcceptsWhatItActuallyHonours` in `crud/optiongroup_test.go` — the
  group contract itself, refusal and acceptance side by side.
- `TestCheckAndBuildGiveTheSameAnswerOverAnOptionsValue` in the same file — the
  explicit `Check` over an `*Options` a caller resolved themselves says exactly
  what the resolving `Build` says.
- `TestEveryQueryOptionIsClassifiedByEveryVerb` and
  `TestAnOptionIsEitherHonouredOrRefusedButNeverBoth` in
  `crud/optiongroup_internal_test.go` — a new field of `crud.Options` cannot be
  added without saying what each verb does with it.
- `TestPreloadRefusesEveryUnsupportedGenericOptionBeforeRowsOrSQL` in
  `crud/preload_test.go` — unchanged, and now the same table.

## See also

[[D-053]] [[D-026]] [[D-006]] [[D-010]] [[D-029]] [[FL-011]]
