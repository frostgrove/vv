package security

import (
	"context"
	"reflect"
	"strings"

	"github.com/frostgrove/vv/crud"
)

func ScopeField[M any, ID comparable](field string, value func(context.Context) (any, error)) Policy[M, ID] {
	schema := crud.MustSchemaOf[M]()
	f := schema.Field(field)
	if f == nil {
		panic("security: model " + schema.Name + " has no field " + field)
	}

	reconcile := reconcileFieldValue(f)

	return Policy[M, ID]{
		Scope: func(ctx context.Context) (crud.Predicate, error) {
			v, err := value(ctx)
			if err != nil {
				return nil, err
			}
			v, err = reconcile(v)
			if err != nil {
				return nil, err
			}
			return crud.Eq(f.Name, v), nil
		},
		Inspect: func(ctx context.Context, action Action, m *M) error {
			raw, err := value(ctx)
			if err != nil {
				return err
			}
			w, err := reconcile(raw)
			if err != nil {
				return err
			}
			got, err := schema.Values(m, []*crud.Field{f})
			if err != nil {
				return err
			}
			if !crud.EqualValues(crud.ElemValue(got[0]), crud.ElemValue(w)) {
				return Denied(action, "row belongs to a different "+f.Name)
			}
			return nil
		},
		Immutable: []string{f.Name},
	}
}

func ScopeRelationField[M any, ID comparable](path, field string, value func(context.Context) (any, error)) Policy[M, ID] {
	f := relationField[M](path, field)
	reconcile := reconcileFieldValue(f)
	return Policy[M, ID]{
		RelationScopes: func(ctx context.Context) (*crud.RelationScopes, error) {
			v, err := value(ctx)
			if err != nil {
				return nil, err
			}
			v, err = reconcile(v)
			if err != nil {
				return nil, err
			}
			return (*crud.RelationScopes)(nil).AtPath(path, crud.Eq(f.Name, v)), nil
		},
	}
}

func reconcileFieldValue(f *crud.Field) func(any) (any, error) {
	want := crud.ElemType(f.Type)
	return func(v any) (any, error) {
		if v == nil {
			return nil, Denied(Read, f.Name+" extractor answered no value")
		}
		got := reflect.TypeOf(v)
		if got == want {
			return v, nil
		}
		if converted, ok := safelyConvert(reflect.ValueOf(v), want); ok {
			return converted.Interface(), nil
		}
		return nil, Denied(Read, "the value for "+f.Name+" cannot be safely converted to the column type")
	}
}

func safelyConvert(v reflect.Value, want reflect.Type) (reflect.Value, bool) {
	got := v.Type()
	if got.Kind() == want.Kind() && got.ConvertibleTo(want) {
		if got != want && (got.Kind() == reflect.Float32 || got.Kind() == reflect.Float64) {
			return reflect.Value{}, false
		}
		return v.Convert(want), true
	}

	isSigned := func(k reflect.Kind) bool {
		return k >= reflect.Int && k <= reflect.Int64
	}
	isUnsigned := func(k reflect.Kind) bool {
		return k >= reflect.Uint && k <= reflect.Uint64
	}
	if (!isSigned(got.Kind()) && !isUnsigned(got.Kind())) || (!isSigned(want.Kind()) && !isUnsigned(want.Kind())) {
		return reflect.Value{}, false
	}

	out := reflect.New(want).Elem()
	switch {
	case isSigned(got.Kind()) && isSigned(want.Kind()):
		n := v.Int()
		if out.OverflowInt(n) {
			return reflect.Value{}, false
		}
		out.SetInt(n)
	case isUnsigned(got.Kind()) && isUnsigned(want.Kind()):
		n := v.Uint()
		if out.OverflowUint(n) {
			return reflect.Value{}, false
		}
		out.SetUint(n)
	case isSigned(got.Kind()):
		n := v.Int()
		if n < 0 || out.OverflowUint(uint64(n)) {
			return reflect.Value{}, false
		}
		out.SetUint(uint64(n))
	case isUnsigned(got.Kind()):
		n := v.Uint()
		if n > uint64(^uint64(0)>>1) || out.OverflowInt(int64(n)) {
			return reflect.Value{}, false
		}
		out.SetInt(int64(n))
	default:
		return reflect.Value{}, false
	}
	return out, true
}

func relationField[M any](path, field string) *crud.Field {
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
	return f
}

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

func Freeze[M any, ID comparable](fields ...string) Policy[M, ID] {
	return Policy[M, ID]{Immutable: fields}
}

func Combine[M any, ID comparable](ps ...Policy[M, ID]) Policy[M, ID] {
	out := Policy[M, ID]{AllowUnscopedDeleteAll: len(ps) > 0, AllowUnscopedUpdateAll: len(ps) > 0}
	type scopeRule struct {
		fn    func(context.Context) (crud.Predicate, error)
		allow bool
	}
	var scopes []scopeRule
	type relationScopeRule struct {
		fn    func(context.Context) (*crud.RelationScopes, error)
		allow bool
	}
	var relScopes []relationScopeRule
	var authz []func(context.Context, Action) error
	var inspect []func(context.Context, Action, *M) error
	allowUnscopedScope := true
	allowUnscopedRelationScopes := true

	for _, p := range ps {
		if p.Scope != nil {
			scopes = append(scopes, scopeRule{fn: p.Scope, allow: p.AllowUnscopedScope})
			allowUnscopedScope = allowUnscopedScope && p.AllowUnscopedScope
		}
		if p.RelationScopes != nil {
			relScopes = append(relScopes, relationScopeRule{fn: p.RelationScopes, allow: p.AllowUnscopedRelationScopes})
			allowUnscopedRelationScopes = allowUnscopedRelationScopes && p.AllowUnscopedRelationScopes
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
		out.AllowUnscopedScope = allowUnscopedScope
		out.Scope = func(ctx context.Context) (crud.Predicate, error) {
			ps := make([]crud.Predicate, 0, len(scopes))
			for _, s := range scopes {
				p, err := s.fn(ctx)
				if err != nil {
					return nil, err
				}
				if crud.IsTautology(p) && !s.allow {
					return nil, Denied(Read, "one combined scope returned no narrowing; set AllowUnscopedScope only for an intentional unrestricted principal")
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
		out.AllowUnscopedRelationScopes = allowUnscopedRelationScopes
		out.RelationScopes = func(ctx context.Context) (*crud.RelationScopes, error) {
			var merged *crud.RelationScopes
			for _, rule := range relScopes {
				rs, err := rule.fn(ctx)
				if err != nil {
					return nil, err
				}
				if rs.Empty() && !rule.allow {
					return nil, Denied(Read, "one combined relation scope returned no narrowing; set AllowUnscopedRelationScopes only for an intentional unrestricted principal")
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
