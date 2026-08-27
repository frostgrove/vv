# UC-020 — Authorize by role and permission without writing a policy per endpoint

**Actor:** the application author, protecting every resource the service exposes
**Covered by:** [[FL-020]] [[FL-007]] [[FL-008]] [[FL-011]]

## Scenario
The identity is established (UC-019). Now the author has to say what it may do,
for every resource, without that turning into a hand-written rule per endpoint —
which is the thing that gets forgotten on the twelfth one, and the thing that is
subtly different on the thirteenth.

Two shapes of rule, and both are needed. *Coarse*: this caller may read articles
at all, or may not. *Row-level*: this caller may read the articles of their own
tenant, which has to be a `WHERE` clause rather than a check in Go, because a
check in Go has already fetched the rows.

The author also wants a bad configuration to be loud. A rule that names a column
that does not exist, or a verb nobody declared, should fail at start-up or fail
closed — never narrow nothing and look like it worked.

## What must hold

1. A rule is declared next to the repository, once, and applies to every entry
   point that repository has.
2. Authorization is expressible against permissions, and a permission is the
   unit a rule names. Roles are accepted where a rule genuinely is about the
   role.
3. Roles expand to permissions once, when the identity is established. The same
   token grants the same permissions everywhere in the process.
4. A rule may name one permission per verb. A verb the declaration does not name
   is refused, including a verb added to the library later.
5. Row-level narrowing can be driven by a claim, and it is narrowing in SQL: the
   filter is in the statement, on every read, count, existence check and
   filtered write.
6. A claim-driven narrowing also refuses a create that would write outside it,
   and freezes the column against updates. Declaring the narrowing is enough;
   there is no second thing to remember.
7. A claim the identity does not carry is a refusal, never a zero value. A
   missing tenant must not compile to a filter that matches row zero — or every
   row.
8. An absent identity is a refusal at every rule, with no statement executed.
9. A refusal is the transport's "forbidden"; a row hidden by a narrowing is
   still "not found". Learning that a row exists is not something a denial may
   do.
10. A refusal names no reason to the client.
11. Rules compose. Two of them, or ten, applied to one repository, all hold, and
    the narrowings combine by AND rather than replacing one another.
12. A rule that names a column the model does not have fails when it is
    declared, not when a query runs.
13. An empty rule is a no-op where that is the safe reading and a refusal where
    it is not, and which is which is stated rather than inferred.

## Out of scope

- **Establishing identity.** UC-019.
- **A permission model.** What "article:write" means, whether permissions nest,
  whether a role can inherit another — all of that is the application's. This is
  a vocabulary and a place to hang it, not a rule engine.
- **Field-level read masking.** Hiding a column from the wire is a presenter,
  UC-013.
- **Enforcement below the application.** These are predicates ANDed into
  statements, not database row-level security. UC-004's exclusion applies
  unchanged.
- **Auditing.** Recording who did what is the application's; the identity is
  available for it.

## Covered by
| Flow | What it contributes |
|---|---|
| [[FL-020]] | the identity reaching a rule, and the rule's answer |
| [[FL-007]] | a claim-driven narrowing entering a read |
| [[FL-008]] | the same narrowing entering a write, and the frozen column |
| [[FL-011]] | the refusal becoming a status |

## Status
**covered.** The claim-driven narrowing is built on the existing helper rather
than beside it, so it inherits its row check and frozen column; a refused create
reaches no statement. The underlying tenant use case now also pins the hidden-ID
answer, relation-aware page total and bulk inspection, so this helper does not
leave those earlier seams behind.

Guarantee 13's two answers, spelled out because the asymmetry is deliberate:
naming no permission in an all-of rule refuses nothing, so a list built from
configuration that happens to be empty adds no rule; naming none in an any-of
rule refuses everything, because "any of nothing" is not satisfiable and
answering otherwise would turn a rule somebody meant to write into a licence.
