package security

import (
	"context"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/crud"
)

func Requiring[M any, ID comparable](requires map[Action][]auth.Permission) Policy[M, ID] {
	declared := copyRequirements(requires)
	return Policy[M, ID]{
		Requires: declared,
		Authorize: func(ctx context.Context, action Action) error {
			return requireDeclared(ctx, declared, action)
		},
	}
}

func RequirePermission[M any, ID comparable](permissions ...auth.Permission) Policy[M, ID] {
	requires := map[Action][]auth.Permission{}
	for _, action := range crud.Actions() {
		requires[action] = permissions
	}
	return Requiring[M, ID](requires)
}

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

func PerAction[M any, ID comparable](m map[Action]auth.Permission) Policy[M, ID] {
	requires := make(map[Action][]auth.Permission, len(m))
	for action, permission := range m {
		requires[action] = []auth.Permission{permission}
	}
	return Requiring[M, ID](requires)
}

func requireDeclared(ctx context.Context, requires map[Action][]auth.Permission, action Action) error {
	principal, err := auth.Require(ctx)
	if err != nil {
		return err
	}
	need, declared := requires[action]
	if !declared {
		return Denied(action, "no permission is declared for this action")
	}
	if !auth.HasAll(principal, need...) {
		return Denied(action, "caller lacks the permission declared for this action")
	}
	return nil
}

func copyRequirements(requires map[Action][]auth.Permission) map[Action][]auth.Permission {
	out := make(map[Action][]auth.Permission, len(requires))
	for action, permissions := range requires {
		out[action] = append([]auth.Permission{}, permissions...)
	}
	return out
}

func ScopeAttr[M any, ID comparable](field, attr string) Policy[M, ID] {
	return ScopeField[M, ID](field, attrOf(attr))
}

func ScopeRelationAttr[M any, ID comparable](path, field, attr string) Policy[M, ID] {
	return ScopeRelationField[M, ID](path, field, attrOf(attr))
}

func ScopeSubject[M any, ID comparable](field string) Policy[M, ID] {
	return ScopeField[M, ID](field, subject)
}

func ScopeRelationSubject[M any, ID comparable](path, field string) Policy[M, ID] {
	return ScopeRelationField[M, ID](path, field, subject)
}

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
