# UC-007 — Write typed, compile-checked queries in Go

**Actor:** the application author
**Covered by:** [[FL-010]] [[FL-004]] [[FL-005]]

## Scenario
Not every question arrives over the wire. A background job, a usecase, a report
needs a filter written in Go — and written as a value, so it can be named,
composed, reused and unit-tested without a database. What the author does not
want is a filter addressed by string literals, because a renamed model field then
becomes a runtime 400 discovered by a customer rather than a build failure
discovered by the compiler.

## What must hold

1. A filter is a value. It can be assigned to a variable, returned from a
   function, and combined with `and`, `or`, `not`, all-of and any-of, and the
   result is another filter.
2. A filter is a pure function of its inputs and can be unit-tested without a
   database.
3. Combining is monotone in the right direction: combining a filter with an
   absent one narrows to the present one and never widens to "everything". A
   missing operand is never read as true.
4. An empty filter contributes no clause at all — not a tautology — so a
   statement built from one is the statement without it, with no bound arguments.
5. A filter used against a repository composes with everything else by AND. It
   cannot widen a repository's permanent narrowing or an access-control policy's,
   and the argument order proves which came first.
6. The typed form addresses fields through a generated metamodel, so a field name
   is an identifier the compiler resolves. Renaming a model field and
   regenerating breaks the build at every call site.
7. The metamodel is validated against the model when the package initialises. An
   attribute naming a column the model does not have, an attribute whose element
   type does not match the column, or a nested attribute group that does not name
   a relation each fail at start-up, not on a request.
8. An attribute exposes the operators its type can support, and nothing else:
   equality and null tests for anything, ordering and range for ordered types and
   for timestamps, and the pattern operators for text. Asking for a range on a
   type that has no ordering does not compile.
9. Pattern-matching convenience operators escape the wildcard characters in their
   argument, so a value containing one is matched literally.
10. Attributes reach through relations. A nested attribute group produces a
    predicate over the *root* model — one row in, one row out — and can be sorted
    by through a to-one edge.
11. A relation's *path* is addressable the same way. A nested attribute group
    answers its own canonical path, so a declaration that takes a path rather
    than a predicate — a permanent narrowing on the far side of a relation, a
    preload, a per-principal relation narrowing — is written in identifiers.
    Renaming the relation and regenerating breaks the build at every call site,
    where before it left a declaration that still compiled and narrowed nothing.
12. That handle also carries the model the path lands on, and a handle declaring
    the wrong model fails at package initialisation — a path can be right while
    the predicate beside it is written against the wrong table, and nothing about
    the path would look wrong.
13. There is an executor with the query verbs a specification wants — find one,
    find first, find all, find a page, count, exists, delete by, update by — and
    the plain repository's whole surface remains available through it.
14. Finding exactly one is a distinct operation from finding the first: no match
    is a not-found, several matches is a conflict, and the returned value is the
    zero model rather than an arbitrary row. The conflict is the sentinel a
    transport answers 409 to.
15. A caller's own paging cannot disarm that uniqueness check.
16. Deleting or updating by an *empty* filter is refused and issues no statement.
17. A filter is usable as a plain repository option, so the executor is a
    convenience and never a requirement.
18. The string-addressed form remains available for the cases that genuinely need
    a runtime name — for a field, and for a relation path — and an unknown name
    there produces an error rather than a statement.

## Out of scope

- **Compile-checking the string-addressed form.** By construction it is checked
  when the statement is built, and surfaces as a bad-request error.
- **Nullability in the type.** A nullable column and a non-nullable one of the
  same underlying type are the same attribute type; whether a column can be null
  is not part of the typed contract.
- **Aggregates, grouping and projections.** A filter narrows rows.
- **Writing the metamodel by hand.** Possible, but the guarantee that it matches
  the model is UC-014's.
- **Relations in another package.** A relation whose target model lives elsewhere
  is not expanded into the generated metamodel.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-010]] | where the metamodel comes from, its relation expansion and its depth bound |
| [[FL-004]] | the start-up validation the metamodel shares with the repository declaration |
| [[FL-005]] | a relation attribute becoming a correlated subquery over the root model |

## Status
**covered, with three caveats about how much the compiler actually checks.**

Proven: composition including the nil-operand rule and the empty-filter rule,
each against every shape of "empty" the API admits; the inability to escape a
repository narrowing, with the rendered statement and argument order asserted;
metamodel validation at declaration time for a missing field, a mismatched type,
a non-struct metamodel, a field that is neither an attribute nor a group, a
group that is not a relation, a relation handle at the root and a handle
declaring the wrong target model; a relation path taken from the metamodel
driving a permanent narrowing, with the literal spelling of the same declaration
as the control that both render one statement; relation expansion rendered as SQL for all three
edge kinds plus a related-column sort; the find-one/find-first distinction
including the zero-model return and the fact that a caller's paging cannot
disarm it; and the empty-filter delete refusal across thirteen spellings of
empty.

**Caveat 1 — the metamodel checks the element type, not the attribute kind.**
Declaring a text column as a plain attribute rather than a text attribute binds
happily; the only consequence is that the pattern operators are not available.
Guarantee 8 is therefore enforced by what the generator emits, not by validation.

**Caveat 2 — validation is one-directional.** It checks that every declared
attribute exists on the model. Nothing checks that every model column is
declared, so a *new* column simply does not appear and nothing complains. That
is UC-014's problem, and it is the only way a stale metamodel escapes both the
build and start-up.

**Caveat 3 — one relation in a thousand has no `Path()`.** The handle is
embedded in the attribute group, so a target model with a column called `Path`
declares a field of that name one level nearer and Go resolves the nearer one.
Guarantee 11 then holds for that relation only through `RelPath()`, which
nothing shadows and which the generated file names in that group's doc comment.
The failure is a compile error at the call site, never a wrong path at run time.

Two thinner spots in the test suite: the existence check and the update-by verb
are exercised only in the database-backed suite, and the update-by refusal of an
empty filter is tested for one spelling where its delete counterpart is tested
for thirteen. Both refusals are also plain sentinels wrapping no core error, so
a transport maps them to 500 unless it handles them.
