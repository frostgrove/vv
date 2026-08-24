package security

import (
	"context"
	"strings"

	"github.com/shardit-io/vv/crud"
)

// ScopeField is the multi-tenant one-liner: every query is narrowed to rows
// whose field equals whatever the extractor pulls out of the context, and the
// same field is frozen against updates.
//
//	type ctxTenant struct{}
//
//	var policy = security.ScopeField[Doc, int64]("TenantID",
//	    func(ctx context.Context) (any, error) {
//	        t, ok := ctx.Value(ctxTenant{}).(int64)
//	        if !ok {
//	            return nil, security.Denied(security.Read, "no tenant in context")
//	        }
//	        return t, nil
//	    })
//
//	docs := Docs.Bind(db, security.Gate(policy))
//
// Reads and updates are filtered by SQL. Creates are checked in Go, because an
// INSERT has no WHERE clause to narrow: a row whose field does not match the
// context tenant is refused.
func ScopeField[M any, ID comparable](field string, value func(context.Context) (any, error)) Policy[M, ID] {
	schema := crud.MustSchemaOf[M]()
	f := schema.Field(field)
	if f == nil {
		panic("security: model " + schema.Name + " has no field " + field)
	}
	return Policy[M, ID]{
		Scope: func(ctx context.Context) (crud.Predicate, error) {
			v, err := value(ctx)
			if err != nil {
				return nil, err
			}
			return crud.Eq(f.Name, v), nil
		},
		Inspect: func(ctx context.Context, action Action, m *M) error {
			want, err := value(ctx)
			if err != nil {
				return err
			}
			got, err := schema.Values(m, []*crud.Field{f})
			if err != nil {
				return err
			}
			if !crud.EqualValues(crud.ElemValue(got[0]), crud.ElemValue(want)) {
				return Denied(action, "row belongs to a different "+f.Name)
			}
			return nil
		},
		Immutable: []string{f.Name},
	}
}

// ScopeRelationField is ScopeField for the far side of a relation: rows reached
// through path are narrowed to those whose field equals the same principal
// value.
//
// It is a separate declaration rather than something ScopeField could infer,
// because "the tenant column is called TenantID here too" is a fact about the
// other model that only you know:
//
//	var policy = security.Combine(
//	    security.ScopeField[Article, int64]("TenantID", tenantOf),
//	    security.ScopeRelationField[Article, int64]("Comments", "TenantID", tenantOf),
//	)
//
// Without the second line, `?preload=comments` reads every tenant's comments
// and attaches them to the articles this caller was allowed to see. The path is
// spelled from the repository's own model and may be several hops
// ("Comments.Author"); it is resolved at declaration time, so a typo panics
// here rather than leaking rows in production.
func ScopeRelationField[M any, ID comparable](path, field string, value func(context.Context) (any, error)) Policy[M, ID] {
	name := relationFieldName[M](path, field)
	return Policy[M, ID]{
		RelationScopes: func(ctx context.Context) (*crud.RelationScopes, error) {
			v, err := value(ctx)
			if err != nil {
				return nil, err
			}
			return (*crud.RelationScopes)(nil).AtPath(path, crud.Eq(name, v)), nil
		},
	}
}

// relationFieldName walks the path one relation at a time and returns the
// canonical name of the field on the model it lands on.
//
// It deliberately resolves nothing about tables. Relation.Target caches the
// table it computes on first call, so walking a path this early — policies are
// package variables, and Go's initialisation order does not promise the
// blueprint ran first — would cache a guessed table name and keep it. Stepping
// through the element types answers the only question there is here, which is
// whether the path and the field exist.
func relationFieldName[M any](path, field string) string {
	schema := crud.MustSchemaOf[M]()
	at := schema
	for seg := range strings.SplitSeq(path, ".") {
		rel := at.Relation(seg)
		if rel == nil {
			panic("security: " + at.Name + " has no relation " + seg + " (in path " + path + ")")
		}
		target, err := crud.SchemaOfType(rel.Elem)
		if err != nil {
			panic("security: resolving " + path + ": " + err.Error())
		}
		at = target
	}
	f := at.Field(field)
	if f == nil {
		panic("security: " + at.Name + " (at " + path + ") has no field " + field)
	}
	return f.Name
}

// ReadOnly denies every write. Compose it over a repository that a request
// handler should only ever query.
func ReadOnly[M any, ID comparable]() Policy[M, ID] {
	return Policy[M, ID]{
		Authorize: func(_ context.Context, a Action) error {
			if a == Read {
				return nil
			}
			return Denied(a, "repository is read-only")
		},
	}
}

// Freeze denies updates to the named fields and nothing else.
func Freeze[M any, ID comparable](fields ...string) Policy[M, ID] {
	return Policy[M, ID]{Immutable: fields}
}

// Combine merges policies left to right: scopes are ANDed, relation narrowings
// are merged (and ANDed where two policies narrow the same path), checks run in
// order, immutable field lists concatenate, and the two unscoped-write
// permissions must hold for every policy.
func Combine[M any, ID comparable](ps ...Policy[M, ID]) Policy[M, ID] {
	// "every policy allows it" is vacuously true of no policies, and that is the
	// wrong answer for the one guard here that fails closed: Combine of nothing
	// — which is what a policy list built from a role produces for a role with
	// no policies — must be exactly the zero Policy, not a licence to truncate
	// the table.
	out := Policy[M, ID]{AllowUnscopedDeleteAll: len(ps) > 0, AllowUnscopedUpdateAll: len(ps) > 0}
	var scopes []func(context.Context) (crud.Predicate, error)
	var relScopes []func(context.Context) (*crud.RelationScopes, error)
	var authz []func(context.Context, Action) error
	var inspect []func(context.Context, Action, *M) error

	for _, p := range ps {
		if p.Scope != nil {
			scopes = append(scopes, p.Scope)
		}
		if p.RelationScopes != nil {
			relScopes = append(relScopes, p.RelationScopes)
		}
		if p.Authorize != nil {
			authz = append(authz, p.Authorize)
		}
		if p.Inspect != nil {
			inspect = append(inspect, p.Inspect)
		}
		out.Immutable = append(out.Immutable, p.Immutable...)
		out.InspectReads = out.InspectReads || p.InspectReads
		out.AllowUnscopedDeleteAll = out.AllowUnscopedDeleteAll && p.AllowUnscopedDeleteAll
		out.AllowUnscopedUpdateAll = out.AllowUnscopedUpdateAll && p.AllowUnscopedUpdateAll
	}
	if len(scopes) > 0 {
		out.Scope = func(ctx context.Context) (crud.Predicate, error) {
			ps := make([]crud.Predicate, 0, len(scopes))
			for _, s := range scopes {
				p, err := s(ctx)
				if err != nil {
					return nil, err
				}
				if p != nil {
					ps = append(ps, p)
				}
			}
			if len(ps) == 0 {
				return nil, nil
			}
			return crud.And(ps...), nil
		}
	}
	if len(relScopes) > 0 {
		out.RelationScopes = func(ctx context.Context) (*crud.RelationScopes, error) {
			var merged *crud.RelationScopes
			for _, f := range relScopes {
				rs, err := f(ctx)
				if err != nil {
					return nil, err
				}
				merged = crud.MergeRelationScopes(merged, rs)
			}
			return merged, nil
		}
	}
	if len(authz) > 0 {
		out.Authorize = func(ctx context.Context, a Action) error {
			for _, f := range authz {
				if err := f(ctx, a); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if len(inspect) > 0 {
		out.Inspect = func(ctx context.Context, a Action, m *M) error {
			for _, f := range inspect {
				if err := f(ctx, a, m); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return out
}
