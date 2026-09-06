package tenancyrow

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/decorators/security"
	"github.com/frostgrove/vv/tenancy"
)

type Mode uint8

const (
	Derive Mode = iota
	Validate
)

type Ownership[M any] interface {
	Narrow(reference tenancy.Reference) (crud.Predicate, error)
	Relations(reference tenancy.Reference) (*crud.RelationScopes, error)
	NarrowsRelations() bool
	Apply(reference tenancy.Reference, action crud.Action, model *M) error
	Frozen() []string
}

type Value func(tenancy.Reference) (any, error)

func orDefault(value Value) Value {
	if value != nil {
		return value
	}
	return func(reference tenancy.Reference) (any, error) { return reference.Value(), nil }
}

type Relation struct {
	Path  string
	Field string
}

// A relation nobody declared is read whole — that is the base gate's contract and
// this does not change it. What it does change is who says so: a tenant-owned
// resource declares which of its relations are tenant-owned, because a preload
// is a second statement against a second table and the root's WHERE clause does
// not reach it. The foreign key is ordinary business data the gate does not
// freeze, so an undeclared relation on a tenanted parent is a cross-tenant read
// reachable from unprivileged input.
func Column[M any](field string, mode Mode, value Value, relations ...Relation) Ownership[M] {
	schema := crud.MustSchemaOf[M]()
	owner := schema.Field(field)
	if owner == nil {
		panic("tenancy: model " + schema.Name + " has no field " + field +
			" — a resource whose ownership names nothing is a resource with no tenancy")
	}
	for _, relation := range relations {
		resolveRelation[M](relation.Path, relation.Field)
	}
	return column[M]{schema: schema, owner: owner, mode: mode, value: orDefault(value),
		relations: append([]Relation(nil), relations...)}
}

type column[M any] struct {
	schema    *crud.Schema
	owner     *crud.Field
	mode      Mode
	value     Value
	relations []Relation
}

func (this column[M]) Narrow(reference tenancy.Reference) (crud.Predicate, error) {
	value, err := this.value(reference)
	if err != nil {
		return nil, err
	}
	return crud.Eq(this.owner.Name, value), nil
}

func (this column[M]) Relations(reference tenancy.Reference) (*crud.RelationScopes, error) {
	if len(this.relations) == 0 {
		return nil, nil
	}
	value, err := this.value(reference)
	if err != nil {
		return nil, err
	}
	scopes := (*crud.RelationScopes)(nil)
	for _, relation := range this.relations {
		scopes = scopes.AtPath(relation.Path, crud.Eq(relation.Field, value))
	}
	return scopes, nil
}

func (this column[M]) NarrowsRelations() bool { return len(this.relations) > 0 }

func (this column[M]) Frozen() []string { return []string{this.owner.Name} }

func (this column[M]) Apply(reference tenancy.Reference, action crud.Action, model *M) error {
	want, err := this.value(reference)
	if err != nil {
		return err
	}
	pointers, err := this.schema.Pointers(model, []*crud.Field{this.owner})
	if err != nil {
		return err
	}
	held := reflect.ValueOf(pointers[0]).Elem()
	if crud.EqualValues(crud.ElemValue(held.Interface()), crud.ElemValue(want)) {
		return nil
	}
	if action != crud.ActionCreate || this.mode == Validate || !held.IsZero() {
		return security.Denied(action, "row is owned by a different tenant")
	}
	return assign(held, want)
}

func assign(target reflect.Value, value any) error {
	incoming := reflect.ValueOf(value)
	if !incoming.IsValid() {
		return security.Denied(crud.ActionCreate, "the ownership value is empty")
	}
	if !incoming.Type().ConvertibleTo(target.Type()) {
		return &crud.SchemaError{
			Field:  target.Type().String(),
			Reason: fmt.Sprintf("an ownership value of type %s cannot be stored in it", incoming.Type()),
		}
	}
	target.Set(incoming.Convert(target.Type()))
	return nil
}

func Through[M any](path, field string, value Value) Ownership[M] {
	if path == "" || field == "" {
		panic("tenancy: ownership through a relation needs both a path and a field")
	}
	local := resolveRelation[M](path, field)
	if local == "" {
		panic("tenancy: the relation " + path + " declares no local key, so nothing freezes the link that carries ownership")
	}
	return through[M]{path: path, field: field, local: local, value: orDefault(value)}
}

type through[M any] struct {
	path  string
	field string
	local string
	value Value
}

func (this through[M]) Narrow(reference tenancy.Reference) (crud.Predicate, error) {
	value, err := this.value(reference)
	if err != nil {
		return nil, err
	}
	return crud.Eq(this.path+"."+this.field, value), nil
}

func (this through[M]) Relations(reference tenancy.Reference) (*crud.RelationScopes, error) {
	value, err := this.value(reference)
	if err != nil {
		return nil, err
	}
	return (*crud.RelationScopes)(nil).AtPath(this.path, crud.Eq(this.field, value)), nil
}

func (this through[M]) NarrowsRelations() bool { return true }

// The key that points at the owner is frozen, because repointing it is how a row
// leaves one tenant for another without any column of its own ever changing.
func (this through[M]) Frozen() []string {
	if this.local == "" {
		return nil
	}
	return []string{this.local}
}

// A row that carries no owner of its own cannot be judged from the row. Reads and
// mutations of rows already found through the relation are narrowed by the
// correlated subquery the path compiles to; a create is refused here rather than
// admitted unchecked, because the owner it would attach to has not been read. An
// application that can check the owner supplies its own Inspect and composes the
// two policies with security.Combine.
func (this through[M]) Apply(_ tenancy.Reference, action crud.Action, _ *M) error {
	if action != crud.ActionCreate {
		return nil
	}
	return security.Denied(action, "ownership is held through a relation, so a create cannot be judged from the row alone")
}

func resolveRelation[M any](path, field string) string {
	if path == "" || field == "" {
		panic("tenancy: a tenant-owned relation needs both a path and a field")
	}
	at, local := crud.MustSchemaOf[M](), ""
	for segment := range strings.SplitSeq(path, ".") {
		relation := at.Relation(segment)
		if relation == nil {
			panic("tenancy: " + at.Name + " has no relation " + segment + " (in path " + path + ")")
		}
		if local == "" {
			local = relation.LocalField
		}
		target, err := crud.SchemaOfType(relation.Elem)
		if err != nil {
			panic("tenancy: resolving " + path + ": " + err.Error())
		}
		at = target
	}
	if at.Field(field) == nil {
		panic("tenancy: " + at.Name + " (at " + path + ") has no field " + field)
	}
	return local
}

func Policy[M any, ID comparable](authority *tenancy.Authority, ownership Ownership[M]) security.Policy[M, ID] {
	if authority == nil || ownership == nil {
		panic("tenancy: a tenant-owned resource needs both an authority and an ownership strategy")
	}
	return security.Policy[M, ID]{
		Scope: func(ctx context.Context) (crud.Predicate, error) {
			scope, err := authority.Scope(ctx, tenancy.ClassRead)
			if err != nil {
				return nil, err
			}
			return ownership.Narrow(scope.Reference())
		},
		RelationScopes: relationScopes[M](authority, ownership),
		Inspect: func(ctx context.Context, action crud.Action, model *M) error {
			scope, err := authority.Scope(ctx, classOf(action))
			if err != nil {
				return err
			}
			return ownership.Apply(scope.Reference(), action, model)
		},
		Immutable: ownership.Frozen(),
	}
}

// The gate refuses a relation-scope function that returns nothing, which is the
// guard against a policy that thinks it narrowed a preload and did not. A
// strategy that declares no tenant-owned relation therefore supplies no function
// at all rather than one that returns nothing behind a disabled guard — and it
// says so itself rather than being probed with a fabricated reference, because a
// probe that a strategy answered with an error would disable the narrowing in
// exactly the silence this arrangement exists to prevent.
func relationScopes[M any](authority *tenancy.Authority, ownership Ownership[M]) func(context.Context) (*crud.RelationScopes, error) {
	if !ownership.NarrowsRelations() {
		return nil
	}
	return func(ctx context.Context) (*crud.RelationScopes, error) {
		scope, err := authority.Scope(ctx, tenancy.ClassRead)
		if err != nil {
			return nil, err
		}
		return ownership.Relations(scope.Reference())
	}
}

func classOf(action crud.Action) tenancy.Class {
	if action == crud.ActionRead {
		return tenancy.ClassRead
	}
	return tenancy.ClassWrite
}

func Repository[M any, ID comparable](authority *tenancy.Authority, ownership Ownership[M]) crud.Middleware[M, ID] {
	return security.Gate(Policy[M, ID](authority, ownership))
}
