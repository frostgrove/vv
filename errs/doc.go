// Package errs is the error contract: what a failure is, before anything has
// decided what to do about it.
//
// A [Fault] is one classified failure. Under it is a list of [Violation]s, each
// one thing that was wrong, at one [Path], with one [Code]. A fault wraps the
// errors it describes rather than replacing them, so a caller who branched on a
// crud sentinel keeps that branch and a caller who wants the list reaches it
// with errors.As ([[D-038]]).
//
// # Four refusals
//
// No transport type. The test is whether a non-HTTP transport can implement an
// interface without importing net/http; a renderer returning an http.Header
// cannot, so the renderer seam lives in http/crudhttp and not here ([[D-045]]).
//
// No storage type. A driver error reaches [Detail] as a plain error and is
// never named. Nothing in this package knows what a SQLSTATE is.
//
// No third-party import. [FieldViolation] is satisfied structurally, so a
// validation library and this package translate without either importing the
// other ([[D-033]]).
//
// No init() registry and no package-level table. [Codes], [Messages] and every
// SPI implementation is a value the consumer wires at the call site. Two
// libraries declaring "too_long" with different kinds must not decide each
// other's behaviour by link order, and a go.work joining five modules makes an
// init() in the wrong one invisible.
//
// # Packaging
//
// This is a package of the root module, and it becomes a module of its own —
// on its own version line — in the same change as the first tag ([[D-036]]).
// The timing is a toolchain constraint and not a preference: split earlier, the
// root would require a version of it that does not exist, and every module that
// requires the root then fails to walk its module graph. So the go.mod arrives
// later than the code, and nothing about the contract changes when it does.
//
// What that costs, and it is the reason for the rules above: this package must
// keep an empty require block. make check-tiers seals it — errs and everything
// under it may import the standard library and each other, and nothing else.
//
// # The contract and the implementation
//
// The first tag freezes the contract half. Changing any of it afterwards is a
// consumer's compile error or, worse, a silent change to what a client reads:
//
//   - [Code] and its constants, [Kind] and its eight constants.
//   - [Origin], [Step], [Path] and its three renderings.
//   - The field names and JSON shapes of [Source], [Detail], [Violation] and
//     [Fault]; [Fault.Error] and [Fault.Unwrap].
//   - [Classifier], [Resolver], [CodeMapper], [MessageSource], and [Chain].
//   - [FieldViolation], [FromFieldViolations], [P], and [Builder.Wrapping].
//
// The rest is an implementation of that contract and a consumer may replace it
// wholesale: [Codes] and [StandardCodes], [Messages], the rest of [Builder],
// and [ParsePath].
//
// # Two rules that are not visible in a signature
//
// No contract package constructs a fault. crud may not import errs at all
// ([[D-016]]), and query may — so without the rule a library-origin error would
// have two classification paths and they would disagree. That half is
// mechanical: make check-tiers seals crud to the standard library, which is
// what makes the import impossible rather than merely discouraged.
//
// A [Violation] carries no [Kind]. A kind is one per fault; a violation's is
// derived from its [Code] through the wired [Codes] value, which is why that
// value is wired rather than global.
//
// # Five names that mean something else one import path away
//
// [[D-035]] names packages and does not forbid these, and the collisions are
// worth knowing before reading a file that holds both:
//
//   - errs.Source is storage provenance. crud.Source is a datasource.
//   - errs.Chain composes resolvers. crud.Chain composes repository decorators.
//   - errs.Path is a path into a payload. sqlerr.Path is a filename, in this
//     very subtree, and specs.Path is a criteria attribute.
//   - errs.KindRetryable is a transport class. sqlerr.KindRetryable is an
//     untyped corpus label.
//   - [Classifier.Classify] turns a driver error into a whole [Fault].
//     sqlerr.Classify turns a flattened one into a [Code] and a [Source], and is
//     what an implementation of the former calls. It is not one: a fault needs a
//     [Kind], which comes from the wired [Codes] value a parser cannot see, and
//     [Builder.Wrapping], which is the only door to a sentinel.
//
// A sixth was avoided rather than accepted: sqlerr.Classify takes its dialect as
// a plain string, so there is no sqlerr.Dialect to collide with crud.Dialect. A
// named type would have bought nothing at the one seam where it matters, because
// crud.Dialect.Name() answers "mysql" for MariaDB and so cannot be its source.
//
// None is renamed. Renaming the sqlerr pair would edit [[D-040]] and [[D-046]],
// which cite them by name, and renaming this package's would depart from the
// roadmap for taste.
package errs
