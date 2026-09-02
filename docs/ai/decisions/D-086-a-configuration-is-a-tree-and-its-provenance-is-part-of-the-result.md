# D-086 — A configuration is a tree, and where each value came from is part of the result

**Status:** accepted
**Invariant:** Loading a configuration validates every node of the tree, not
only its root; strictness and the default path are properties of a `Source`
value the caller holds, never of package state; and the report a load produces
names where every value came from and never what the value is.

## The decision

Three things were wrong with a loader whose whole contract was "decode, then
call `Validate()` on the root".

**A root-only hook makes every nested block a forwarder.** An application whose
config holds a database block, a listener block and six others wrote eleven
`if err := this.X.Validate(); err != nil { return err }` lines by hand, returned
at the first one, and learned nothing about the second broken block until the
next restart. A block that is forgotten in that chain is validated by nothing,
and no test can see the omission.

`ValidateTree` walks the exported shape instead. Every node that can validate
itself is asked exactly once, failures are joined so an operator sees all of
them at once, and each one carries the path of the block it came from. Cross-field
rules are a second interface and a second pass: they run only over a tree whose
nodes are individually valid, so a rule that compares two blocks may assume each
block holds on its own.

**Descent stops at the older `Validate`.** That method is as often a
hand-written forwarder as a rule about one node, and a block that merges a
fragment with its parent before checking it refuses the fragment when asked
directly — `vvdb.Config.Replica` is the live case: a replica declaring only a
port is legal in a file and illegal on its own. So a node spelling `Validate`
owns everything under it and the walk goes no deeper; `ValidateSelf` is the
promise that a method is about this node alone, and the walk continues past it.
The migration is a rename, and until it happens an existing configuration
behaves exactly as it did.

**A mistyped key is a silent default.** `adress:` sets nothing, the field keeps
its zero value or its `env-default`, and the service starts on the wrong
address. `LoadStrict` reads the file a second time as a document, compares its
keys against the declared shape and refuses what no field declares, naming the
key with its path. The same comparison runs on a lenient load and lands in the
report instead of an error, so a consumer can log the drift before turning
strictness on. This is [[D-013]]'s rule — an unknown field is a rejection, not a
shrug — applied at start-up rather than at the wire.

**The environment half of that check is narrower on purpose.** A process has
hundreds of variables it did not declare, so only variables under a prefix the
struct itself declares through `env-prefix` are candidates, and a prefix leading
into a block that applies its own environment is excluded outright: that block
owns names the walk cannot see, and guessing there would refuse a correct
deployment.

**The default path was a mutable package variable.** `DefaultCfgPath` made the
answer to "which file is this?" process-global: two configurations loaded in one
process shared it, a test that changed it changed it for every other test, and
under `-race` two concurrent loads were a data race. It is now `Source.DefaultPath`,
beside the arguments to scan, whether no file at all is legal, whether the load
is strict, and which paths must come from the environment. The magic path stays:
`MustLoad` is `DefaultSource()` and a panic. The explicit path is one value the
caller builds and can log.

## Provenance without values

`Load` answers a struct, and a struct does not say that its host came from a
stray variable rather than from the file an operator is looking at. The report
answers that: per field, whether the value came from the file, from a named
environment variable, or from a declared default, plus the path that was read
and which of flag, environment, caller or default named it.

The report carries **no values at all**. A redaction list is a leak waiting for
the first field type that forgets to opt in, and the one line an operator copies
into an incident ticket is exactly the line a password would be in. Where a
value came from is enough to answer the question the report exists for, and it
is safe by construction rather than by review.

**A file the loader cannot open as a document has no provenance, and the report
says so rather than guessing.** `.edn` decodes — cleanenv reads it — and does not
inspect, so a lenient load knows the values in the struct but not which of them
the file set. It reports every field as `OriginUnknown` and carries the refusal
in `Report.NotInspected`, which `Report.String` prints as its own line. Calling
those values defaults was worse than saying nothing: it named the one origin that
means "nobody wrote this down", for values an operator had written down.

`RequireEnvironment` is provenance used as a rule: a path listed there must have
arrived from the environment, so a password committed to the file fails at
start-up rather than at the next audit. A path in that list that names no
declared field is refused as the typo it is.

## What it costs in dependencies

Reading the file a second time as a document means `vvcfg` decodes YAML, JSON
and TOML itself, so `gopkg.in/yaml.v3` and `github.com/BurntSushi/toml` are
imported by non-test code here. Both were already `require`d by this module —
cleanenv brings them, and nobody takes cleanenv without both — so under [[D-051]]
this is the same one decision the module already carried, not a new one, and
`go mod tidy` moves no line. The root module is untouched ([[D-036]]): `vvcfg`
is a satellite precisely so that a consumer binding its own configuration pays
for none of it.

## What it forbids

- Do not reintroduce package-level state for the path, the format or strictness.
  A second configuration in one process must be able to disagree.
- Do not put a value, redacted or otherwise, into the report.
- Do not descend past a node whose type spells `Validate`, and do not stop at
  one that spells `ValidateSelf`.
- Do not run a cross rule over a tree whose nodes have not all validated.
- Do not report an unused variable under a prefix owned by a block that applies
  its own environment.
- Do not make an uninspectable file format silently non-strict: a strict load of
  a format the loader cannot read as a document is a refusal, and a lenient one
  admits in the report that it never looked.
- Do not report an origin the loader did not establish. A field whose file was
  never inspected is `OriginUnknown`, never `OriginDefault`.

## Where it lives

- `utils/vvcfg/validate.go` — `ValidateTree`, the two interfaces, the cycle and
  depth bounds, `ValidationError` and its path.
- `utils/vvcfg/source.go` — `Source`, `DefaultSource`, `Resolve` and `PathOrigin`.
- `utils/vvcfg/schema.go` — the declared shape: paths, environment names,
  prefixes, deprecations, and which subtrees own their own environment.
- `utils/vvcfg/document.go` — the file read as a document, and the key comparison.
- `utils/vvcfg/report.go` — `Report`, `Origin`, `Report.NotInspected`, and the
  three refusal types.
- `utils/vvcfg/validate_test.go`, `utils/vvcfg/report_test.go` — the proofs.

## Proven by

- `TestEveryBlockThatCanRefuseItselfIsAskedWithoutAForwarder` and
  `TestAFileWithTwoBrokenBlocksReportsBoth` — both blocks, one restart.
- `TestANodeReachableTwiceIsAskedToValidateItselfOnce` — the cycle bound.
- `TestCrossRulesRunAfterEveryNodeHasValidatedItselfAndNotOverABrokenTree`.
- `TestABlockSpellingTheOlderValidateOwnsWhatIsUnderIt` — with its control on
  the `ValidateSelf` shape, so the rule cannot pass vacuously.
- `TestAMistypedKeyStopsAStrictLoadAndNamesItself`,
  `TestANestedKeyNoFieldDeclaresIsNamedWithItsPath`,
  `TestAMistypedKeyIsReportedEvenWhenTheLoadIsNotStrict`.
- `TestAVariableUnderADeclaredPrefixThatNoFieldReadsIsRefused` and
  `TestABlockThatAppliesItsOwnEnvironmentIsNotSecondGuessed`.
- `TestTheReportSaysWhereEachValueCameFrom` and
  `TestTheReportNamesWhereAValueCameFromAndNeverTheValue`.
- `TestAValueRequiredFromTheEnvironmentRefusesAFileThatCarriesIt` and
  `TestRequiringAnUndeclaredPathIsATypoAndRefusedAsOne`.
- `TestAFileFormatThatCannotBeInspectedIsRefusedOnlyWhenStrictnessNeedsIt` — both
  halves through the real door: `LoadStrict` refuses, `LoadFrom` loads the file
  anyway, and an inspectable format is the control.
- `TestAFileTheLoaderCouldNotInspectIsSaidSoAndItsValuesAreNotCalledDefaults` —
  the values reach the struct, every origin is `OriginUnknown`, the rendered block
  says the file was not inspected, and the same struct in YAML is the control.
- `TestTheDefaultPathIsCarriedByTheSourceAndNotByThePackage` — two sources with
  different defaults loaded concurrently under `-race`.

## See also

[[D-013]] [[D-021]] [[D-036]] [[D-051]] [[D-057]] [[D-081]] [[FL-026]]
