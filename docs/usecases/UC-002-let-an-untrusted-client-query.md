# UC-002 — Let an untrusted client filter, sort, page and search

**Actor:** a client the service does not control — HTTP or gRPC — and the application
author who has to be comfortable exposing the endpoint
**Covered by:** [[FL-001]] [[FL-012]] [[FL-011]]

## Scenario
The front end needs to ask real questions: this status, that date range, sorted
by these two columns, page three, matching this text. Writing an endpoint per
question does not scale, and a hand-rolled filter layer either accepts too
little — so the front end works around it — or too much, and the first typo in a
field name quietly returns the whole table. The author wants one query language,
bounded by a declaration, where every name the client sends is resolved against
the model before any SQL exists.

## What must hold

1. A question can be asked as one JSON document or as flat query-string terms,
   and the two express the same things: filters, boolean combinations, sorting,
   paging, projection, relation loading, and text search.
2. A name the model does not have is **rejected**, not ignored. Unknown filter
   fields, sort fields, select fields, preload paths, relation segments and
   operators are all refusals. So is a key the *document* does not have: a
   misspelled `filter` would otherwise parse as a document with no filter in it
   and answer the whole table. On a query string the same check is narrower on
   purpose — an application reads its own parameters off the same URL, so a
   parameter one edit away from one of ours is refused and an unrelated name
   passes.
3. A rejection produces *no* options at all. A transport cannot log the error and
   run "the good half".
4. A rejection names the path that was wrong, so a client can fix it without
   guessing.
5. What a client may filter by, sort by, select, preload and search is
   declarable, one list per verb, and the five lists are independent: permission
   to filter through a relation is not permission to preload it, and permission
   to preload it is not permission to filter by its columns.
6. A list entry may name a subtree, which covers that path and everything under
   it, whole segment at a time. A prefix that is not a whole segment does not
   match.
7. Allow-list matching happens on the canonical name after the model resolves it,
   case- and separator-insensitively, so no spelling of a denied column routes
   around the list.
8. An empty list allows anything the model maps. Absent configuration is
   permissive about *names* but never about budgets, and never about how much
   comes back.
9. Depth, condition-count and preload-count budgets apply whether or not the
   lists are set, and have non-zero defaults, so an endpoint mounted with no
   configuration at all still cannot be asked an unbounded question.
10. The condition budget is one counter for the whole document, shared with the
    filters a preload carries, so a client cannot spend it twice.
11. A value is typed by the column it is compared against — an integer column
    binds an integer, not a float; a timestamp column parses a string into a
    time; a column with its own text decoding parses through it, so uuid and enum
    types keep their own rules.
12. A value the column cannot hold is a rejection naming the column and the type.
    It is never silently zeroed, and it never wraps: a number too large for the
    column is refused, not truncated.
13. Both doors bind the same Go value for the same logical input.
14. Text search is its own parenthesised node ANDed with the filter. It cannot
    widen a filter, and the wildcard characters in the search term are escaped so
    a client cannot turn a search into a scan.
15. Compilation is deterministic. The same document produces the same statement
    with the same argument positions, however the client's JSON keys happened to
    be ordered.
16. No client input reaches the statement as text. Names resolve to the model's
    own quoted column names; values are always bound. This holds in every name
    position the language has, and in the second statement a preload issues,
    which no caller sees.
17. The page size a caller may request is capped by the repository, and asking to
    be unpaged does not outrank that cap. Asking to be unpaged at all is refused
    unless the endpoint declared that it serves whole result sets — the cap it
    would otherwise be measured against is itself unset by default, so with no
    configuration the request had no ceiling of any kind.
18. A filter across a to-many relation does not multiply rows or inflate the
    total (UC-006).
19. A request body is read under a byte cap before anything parses it, and a
    body past it is refused with a status that says "send less" rather than
    "send something else". The cap is the library's and no binding raises it. A
    server the application owns may refuse earlier with its own answer — a
    framework that carries a body limit of its own applies it before any handler
    runs, and over gRPC the server's message limit does the same job. [[FL-013]]
    carries which is which.
20. How many values one list may carry, and how many terms one sort may carry,
    are bounded with non-zero defaults. Both measure volume rather than names,
    which is why the condition budget cannot see them: a list is one condition
    however long it is, and a sort was charged nothing at all.

## Out of scope

- **Who may see which rows.** The language bounds what can be *asked*; it says
  nothing about which rows come back. That is UC-004.
- **Cost.** A permitted question can still be slow. There is no query planner
  budget, no timeout, no complexity score beyond depth and condition counts.
- **Aggregates, grouping, joins, arbitrary expressions.** Not expressible, by
  design.
- **Schema confidentiality.** A rejection distinguishes "no such field" from "not
  filterable" and names the Go model. A client can enumerate the schema through
  error text.
- **Per-relation filters and null placement from a query string.** Those need the
  JSON document.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-001]] | the document becoming repository options and then a statement |
| [[FL-012]] | resolving a name against the model and coercing a value to its column's type |
| [[FL-011]] | a rejection becoming a 400 that names the path |
| [[FL-013]] | that both front doors behave the same under the second binding — including repeated query-string terms, the body cap, and the refusal of an undeclared `unpaged` |

## Status
**partially covered.** The core claim — a name is resolved before any SQL exists,
and an unknown one is a rejection — holds for every *name position the language
defines*, and the hostile-input suite is genuinely hostile: fifteen payloads
through twelve name positions, values bound in every position including a
preload's own filter, six spellings of one denied column, and a byte-for-byte
determinism check re-run with shuffled keys. Allow-list independence, subtree
matching, the default budgets, the shared condition counter, cross-door value
identity, overflow refusal, search parenthesisation and wildcard escaping all
have tests.

Guarantees 17, 19 and 20 were the places absent configuration *was* permissive
about volume, and all three are closed: an endpoint declares that it serves whole
result sets rather than a request asking for it ([[D-060]]), the list and sort
caps have non-zero defaults, and every body is read under a byte cap
([[D-063]]) — each with a control test that the permitted case still works. "Cost" stays out of scope — these bound how much a request may
*ask for*, not how long answering it takes.

**The gap that contradicted the headline guarantee is closed.** An unknown
top-level key in the JSON document is refused by name, with the accepted set
offered back, and a query-string parameter one edit away from a real one is
refused the same way. The query-string half stops there rather than closing the
set, and that is deliberate: a handler is free to read its own parameters off
the same URL — `?includeArchived=1` driving a scope option is the documented
pattern — so an unrelated name has to pass. Both halves have tests, each with
the control that the legitimate case still gets through.

The gaps below are what stops this being "covered".

**Gap 2 — the preload allow-list is not checked hop by hop.** An entry naming a
deep path authorises loading every relation on the way to it, because reaching
the far end requires reading the near end. Listing only the deep path therefore
grants more than it appears to.

**Gap 3 — the two doors disagree in four places.** A query-string `isNull` term
whose value does not parse as a boolean silently becomes `IS NOT NULL`, where
the JSON door rejects it. A scalar term silently keeps only the first
comma-separated value, and there is no way to write a literal comma. Byte-slice
columns decode base64 from JSON and raw text from the query string. And a
`greater-than` compared against `null` binds a nil rather than being refused
like every other null operand.

**Gap 4 — a "not in" over an empty list widens to everything**, and a
LIKE-family operator against a non-text column reaches the database and fails
there, so it is a 500 rather than the 400 every other bad value produces.

**Gap 5 — the budgets still have holes.** Search predicates and select entries
are not charged against the condition budget, and the search-field list has no
length cap. The depth budget does not bound a preload path — that is capped
separately, at execution — and inside a preload's own filter both the nesting
counter and the path-length check restart relative to the target model. Sort
terms were in this list and are not any more: they have a cap of their own,
because charging them to the condition budget would have measured the wrong
thing.

**Gap 6 — the page cap is not part of the query configuration.** It is a
repository setting, so an endpoint reviewed only through its query configuration
is reviewed through the wrong file. This is narrower than it was: an endpoint
that says nothing now refuses `unpaged` outright, so the two open defaults no
longer combine into no ceiling at all. What is left is that an endpoint which
*does* serve whole result sets is bounded by a number declared somewhere else.

One smaller asymmetry, behaviourally proven and worth knowing rather than
fixing: a search field named explicitly but not permitted is a rejection, while a
search that falls back to "every text column" and finds none permitted disappears
silently and the request succeeds unfiltered by the search.
