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

// A resource whose ownership is neither a column of its own nor a path into an
// owner implements this rather than bending one of the two that are here. Four of
// its five methods carry an obligation the gate below cannot check, so they are
// written down:
//
//   - Narrow returns the predicate that restricts the resource to the reference,
//     and returns an error rather than a predicate that matches everything.
//   - NarrowsRelations answers whether Relations produces anything. It is asked,
//     never inferred, because a probe answered with an error would read as "no
//     narrowing" — the silence this exists to prevent. A strategy that answers
//     false is given no relation-scope function at all, so a preload of a
//     tenant-owned relation is then read whole: answering false while Relations
//     returns scopes is a cross-tenant read, and nothing below will catch it.
//   - Relations narrows every relation the strategy claims, or errors. Returning
//     nil for a relation it declared is the same leak by a quieter route.
//   - Apply derives or validates ownership for the action, and refuses a model
//     that belongs to another tenant. It is the only place a create may be
//     stamped.
//   - Frozen names the columns a mutation may not change — at least the one the
//     ownership is read from. An empty answer disables immutability, so a row can
//     be moved between tenants by writing to it.
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
	owned := make([]ownedRelation, 0, len(relations))
	for _, relation := range relations {
		resolved := resolveRelation[M](relation.Path, relation.Field)
		owned = append(owned, ownedRelation{path: relation.Path, field: relation.Field,
			reconcile: security.ReconcileValue(resolved.field)})
	}
	return column[M]{schema: schema, owner: owner, mode: mode, value: orDefault(value),
		reconcile: security.ReconcileValue(owner), relations: owned}
}

type ownedRelation struct {
	path      string
	field     string
	reconcile func(any) (any, error)
}

type column[M any] struct {
	schema    *crud.Schema
	owner     *crud.Field
	mode      Mode
	value     Value
	reconcile func(any) (any, error)
	relations []ownedRelation
}

// The ownership value is the deployment's to produce and this package's to check.
// It is checked against the column it will be compared with, because Go makes the
// two dangerous conversions legal: an int becomes a string by way of its rune, and
// a wide integer becomes a narrow one by truncation. Either one narrows on a
// tenant that does not exist and stamps a row with it. A value that is absent in
// any of its spellings — an untyped nil, a nil pointer, a null Opt — is refused
// here rather than compiled, because crud.Eq turns it into IS NULL, which is a
// predicate that matches every unowned row instead of none.
func (this column[M]) owned(reference tenancy.Reference) (any, error) {
	raw, err := this.value(reference)
	if err != nil {
		return nil, tenancy.Classify(err)
	}
	return this.reconcile(raw)
}

func (this column[M]) Narrow(reference tenancy.Reference) (crud.Predicate, error) {
	value, err := this.owned(reference)
	if err != nil {
		return nil, err
	}
	return crud.Eq(this.owner.Name, value), nil
}

func (this column[M]) Relations(reference tenancy.Reference) (*crud.RelationScopes, error) {
	if len(this.relations) == 0 {
		return nil, nil
	}
	raw, err := this.value(reference)
	if err != nil {
		return nil, tenancy.Classify(err)
	}
	scopes := (*crud.RelationScopes)(nil)
	for _, relation := range this.relations {
		value, err := relation.reconcile(raw)
		if err != nil {
			return nil, err
		}
		scopes = scopes.AtPath(relation.path, crud.Eq(relation.field, value))
	}
	return scopes, nil
}

func (this column[M]) NarrowsRelations() bool { return len(this.relations) > 0 }

func (this column[M]) Frozen() []string { return []string{this.owner.Name} }

func (this column[M]) Apply(reference tenancy.Reference, action crud.Action, model *M) error {
	want, err := this.owned(reference)
	if err != nil {
		return err
	}
	pointers, err := this.schema.Pointers(model, []*crud.Field{this.owner})
	if err != nil {
		return err
	}
	held := reflect.ValueOf(pointers[0]).Elem()
	if crud.EqualValues(crud.ElemValue(held.Interface()), want) {
		return nil
	}
	if action != crud.ActionCreate || this.mode == Validate || !held.IsZero() {
		return security.Denied(action, "row is owned by a different tenant")
	}
	return assign(held, want)
}

// The value arrives already reconciled against the column's element type, so the
// only conversion left is the wrapper the field itself adds — a pointer, an Opt.
func assign(target reflect.Value, value any) error {
	incoming := reflect.ValueOf(value)
	if !incoming.Type().AssignableTo(target.Type()) {
		return &crud.SchemaError{
			Field: target.Type().String(),
			Reason: fmt.Sprintf("an ownership value of type %s cannot be stored in it; "+
				"a pointer or Opt ownership column cannot be derived, so supply it and declare Validate", incoming.Type()),
		}
	}
	target.Set(incoming)
	return nil
}

// A to-many path is not ownership. It compiles to "at least one child on the far
// side is mine", so a row whose children belong to two tenants answers to both of
// them — for reads and for writes. The link it would freeze is the row's own key
// rather than a key that points at an owner, so nothing stops the row moving
// either. Refusing at wiring is the only place this can be caught: the statement
// it produces is well-formed and the rows it returns look ordinary.
func Through[M any](path, field string, value Value) Ownership[M] {
	if path == "" || field == "" {
		panic("tenancy: ownership through a relation needs both a path and a field")
	}
	resolved := resolveRelation[M](path, field)
	if resolved.toMany {
		panic("tenancy: the relation " + path + " is to-many, so ownership through it would mean " +
			"\"at least one of them is mine\" and would share a row between every tenant on the far side")
	}
	if resolved.local == "" {
		panic("tenancy: the relation " + path + " declares no local key, so nothing freezes the link that carries ownership")
	}
	return through[M]{path: path, field: field, local: resolved.local, value: orDefault(value),
		reconcile: security.ReconcileValue(resolved.field)}
}

type through[M any] struct {
	path      string
	field     string
	local     string
	value     Value
	reconcile func(any) (any, error)
}

func (this through[M]) owned(reference tenancy.Reference) (any, error) {
	raw, err := this.value(reference)
	if err != nil {
		return nil, tenancy.Classify(err)
	}
	return this.reconcile(raw)
}

func (this through[M]) Narrow(reference tenancy.Reference) (crud.Predicate, error) {
	value, err := this.owned(reference)
	if err != nil {
		return nil, err
	}
	return crud.Eq(this.path+"."+this.field, value), nil
}

func (this through[M]) Relations(reference tenancy.Reference) (*crud.RelationScopes, error) {
	value, err := this.owned(reference)
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
// application that can check the owner writes its own Ownership rather than
// composing this one, because security.Combine chains inspections and cannot
// relax this refusal.
func (this through[M]) Apply(_ tenancy.Reference, action crud.Action, _ *M) error {
	if action != crud.ActionCreate {
		return nil
	}
	return security.Denied(action, "ownership is held through a relation, so a create cannot be judged from the row alone")
}

type resolvedRelation struct {
	local  string
	field  *crud.Field
	toMany bool
}

func resolveRelation[M any](path, field string) resolvedRelation {
	if path == "" || field == "" {
		panic("tenancy: a tenant-owned relation needs both a path and a field")
	}
	at, resolved := crud.MustSchemaOf[M](), resolvedRelation{}
	for segment := range strings.SplitSeq(path, ".") {
		relation := at.Relation(segment)
		if relation == nil {
			panic("tenancy: " + at.Name + " has no relation " + segment + " (in path " + path + ")")
		}
		if resolved.local == "" {
			resolved.local = relation.LocalField
		}
		resolved.toMany = resolved.toMany || relation.Kind.ToMany()
		target, err := crud.SchemaOfType(relation.Elem)
		if err != nil {
			panic("tenancy: resolving " + path + ": " + err.Error())
		}
		at = target
	}
	resolved.field = at.Field(field)
	if resolved.field == nil {
		panic("tenancy: " + at.Name + " (at " + path + ") has no field " + field)
	}
	return resolved
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
