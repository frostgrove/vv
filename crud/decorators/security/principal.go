package security

import (
	"context"

	"github.com/frostgrove/vv/auth"
)

// The policies in this file are the ones whose answer comes from the
// authenticated caller rather than from something the application looked up
// itself.
//
// They are a layer over the closures the rest of the package already takes, not
// a second mechanism: ScopeAttr builds the func(context.Context) (any, error)
// that ScopeField has always accepted, so it inherits that helper's row check
// and its frozen column with no second copy of either. The import runs one way
// — this package knows about auth, and auth knows nothing about a repository
// ([[D-055]]).
//
// Every one of them fails closed on a context nobody authenticated. That is
// [[UC-004]]'s guarantee 16: a policy that read an absent principal as "no
// narrowing" would widen every query on the one request where the middleware
// was not mounted.

// RequirePermission refuses every operation unless the caller holds all of the
// named permissions.
//
// All of them, because "and" is what a list reads as. RequireAnyPermission is
// the other quantifier, spelled out rather than inferred from the call.
//
// Naming no permission refuses nothing, which is what makes it composable: a
// permission list built from configuration that happens to be empty is a policy
// that adds no rule, not one that locks the table.
func RequirePermission[M any, ID comparable](ps ...auth.Permission) Policy[M, ID] {
	return Policy[M, ID]{
		Authorize: func(ctx context.Context, action Action) error {
			p, err := auth.Require(ctx)
			if err != nil {
				return err
			}
			if !auth.HasAll(p, ps...) {
				return Denied(action, "caller lacks a required permission")
			}
			return nil
		},
	}
}

// RequireAnyPermission refuses unless the caller holds at least one of them.
//
// Naming none refuses everything here, and that asymmetry with
// [RequirePermission] is deliberate: "any of nothing" is not satisfiable, and
// answering yes would turn a rule somebody meant to write into a licence.
func RequireAnyPermission[M any, ID comparable](ps ...auth.Permission) Policy[M, ID] {
	return Policy[M, ID]{
		Authorize: func(ctx context.Context, action Action) error {
			p, err := auth.Require(ctx)
			if err != nil {
				return err
			}
			if !auth.HasAny(p, ps...) {
				return Denied(action, "caller holds none of the required permissions")
			}
			return nil
		},
	}
}

// RequireRole refuses unless the caller is in one of the named roles.
//
// Prefer [RequirePermission]. A rule that names a role has to be edited every
// time the roles are reorganised; a rule that names a permission does not.
// This is for the cases that genuinely are about the role — an admin console,
// a break-glass path.
func RequireRole[M any, ID comparable](rs ...auth.Role) Policy[M, ID] {
	return Policy[M, ID]{
		Authorize: func(ctx context.Context, action Action) error {
			p, err := auth.Require(ctx)
			if err != nil {
				return err
			}
			if !auth.InAny(p, rs...) {
				return Denied(action, "caller is in none of the required roles")
			}
			return nil
		},
	}
}

// PerAction names one permission per verb:
//
//	security.PerAction[Article, int64](map[security.Action]auth.Permission{
//	    security.Read:   "article:read",
//	    security.Create: "article:write",
//	    security.Update: "article:write",
//	    security.Delete: "article:delete",
//	})
//
// An action the map does not name is refused. That is the only safe reading:
// the map is the whole statement of what this principal may do, so a verb
// missing from it is a verb nobody granted — and a new verb added to the seam
// later is then refused rather than silently allowed ([[D-030]]).
func PerAction[M any, ID comparable](m map[Action]auth.Permission) Policy[M, ID] {
	// Copied, so a caller that keeps writing to its map cannot change what is
	// enforced after the repository is bound.
	want := make(map[Action]auth.Permission, len(m))
	for a, p := range m {
		want[a] = p
	}
	return Policy[M, ID]{
		Authorize: func(ctx context.Context, action Action) error {
			p, err := auth.Require(ctx)
			if err != nil {
				return err
			}
			need, named := want[action]
			if !named {
				return Denied(action, "no permission is declared for this action")
			}
			if !p.Has(need) {
				return Denied(action, "caller lacks the permission declared for this action")
			}
			return nil
		},
	}
}

// ScopeAttr is the multi-tenant one-liner, driven by the token rather than by a
// context key the application invented:
//
//	policy := security.ScopeAttr[Doc, int64]("TenantID", "tenant")
//	docs := Docs.Bind(db, security.Gate(policy))
//
// It is [ScopeField] with the extractor filled in, so everything that helper
// does still happens: reads are narrowed in SQL, a create into another tenant
// is refused by the row check it installs, and the column is frozen against
// updates. That last part is why this is a wrapper and not a fresh policy — a
// principal-driven scope written from scratch is exactly the shape [[UC-004]]
// records as Gap 1, a policy that narrows reads and leaves creates open.
//
// The field is resolved when the policy is declared, so a typo panics at
// start-up rather than narrowing nothing in production.
func ScopeAttr[M any, ID comparable](field, attr string) Policy[M, ID] {
	return ScopeField[M, ID](field, attrOf(attr))
}

// ScopeRelationAttr carries the same claim across a relation, and it has to be
// declared separately for [ScopeRelationField]'s reason: that the far side
// spells its tenant column the same way is a fact about the other model that
// only the author knows.
//
// Without it, ?preload=comments reads every tenant's comments and hands them
// back attached to rows the caller was allowed to see ([[D-007]]).
func ScopeRelationAttr[M any, ID comparable](path, field, attr string) Policy[M, ID] {
	return ScopeRelationField[M, ID](path, field, attrOf(attr))
}

// ScopeSubject narrows to rows the caller owns — the field holds the
// principal's subject.
func ScopeSubject[M any, ID comparable](field string) Policy[M, ID] {
	return ScopeField[M, ID](field, subject)
}

// ScopeRelationSubject is [ScopeSubject] for the far side of a relation.
func ScopeRelationSubject[M any, ID comparable](path, field string) Policy[M, ID] {
	return ScopeRelationField[M, ID](path, field, subject)
}

// attrOf reads one claim off the principal.
//
// A claim that is not there is a denial and not an empty value. A missing
// tenant read as the zero value would compile to `WHERE tenant_id = 0`, which
// matches no rows on most schemas and every row on a schema where 0 is a real
// tenant — a rule that fails open on exactly the deployment where it matters.
func attrOf(name string) func(context.Context) (any, error) {
	return func(ctx context.Context) (any, error) {
		p, err := auth.Require(ctx)
		if err != nil {
			return nil, err
		}
		v, ok := p.Attr(name)
		if !ok || v == nil {
			return nil, Denied(Read, "the caller carries no "+name+" claim")
		}
		return v, nil
	}
}

// subject reads the principal's subject, refusing an empty one for attrOf's
// reason.
func subject(ctx context.Context) (any, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return nil, err
	}
	if p.Subject() == "" {
		return nil, Denied(Read, "the caller has no subject")
	}
	return p.Subject(), nil
}

// InspectOwner is the row-level check for a rule the scope cannot express,
// where owning a row is not the same as the row naming you — a shared
// document, a row visible to a whole team.
//
// It sees the row itself, so it runs after the read rather than inside it, and
// the gate cancels any projection before calling it: a policy that decided on a
// column the caller did not select would compare against a zero value and
// believe it.
func InspectOwner[M any, ID comparable](allow func(p auth.Principal, action Action, m *M) bool) Policy[M, ID] {
	return Policy[M, ID]{
		Inspect: func(ctx context.Context, action Action, m *M) error {
			p, err := auth.Require(ctx)
			if err != nil {
				return err
			}
			if !allow(p, action, m) {
				return Denied(action, "the caller may not reach this row")
			}
			return nil
		},
	}
}
