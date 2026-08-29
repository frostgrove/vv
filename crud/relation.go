package crud

import (
	"reflect"
	"strings"
	"sync"
	"unsafe"
)

// RelTagKey is the struct tag that turns a field into a relation.
//
//	rel:"belongs_to"                     fk inferred as <FieldName>ID on this struct
//	rel:"has_many"                       fk inferred as <ThisModel>ID on the target
//	rel:"has_one"                        same, single row
//	rel:"many_to_many,join=article_tags" fk/ref on the join table inferred from the two models
//	rel:""                               infer the kind from the Go type
//	rel:"-"                              never a relation
//
// Overrides: `fk=Field`, `ref=Field`, `table=name`, `joinFK=col`, `joinRef=col`.
//
// A struct, pointer-to-struct or slice-of-struct field without a rel tag is
// skipped entirely — it is neither a column nor a relation.
const RelTagKey = "rel"

// RelKind is the cardinality and direction of a relation.
type RelKind uint8

const (
	// BelongsTo: this model holds the foreign key. articles.author_id -> authors.id
	BelongsTo RelKind = iota
	// HasOne: the target holds the foreign key, at most one row.
	HasOne
	// HasMany: the target holds the foreign key, any number of rows.
	HasMany
	// ManyToMany: a join table sits between the two.
	ManyToMany
)

func (this RelKind) String() string {
	switch this {
	case BelongsTo:
		return "belongs_to"
	case HasOne:
		return "has_one"
	case HasMany:
		return "has_many"
	case ManyToMany:
		return "many_to_many"
	}
	return "unknown"
}

// ToMany reports whether the relation yields a collection.
func (this RelKind) ToMany() bool { return this == HasMany || this == ManyToMany }

// Relation is one navigable edge out of a model.
type Relation struct {
	Name   string // Go field name, and the path segment used in queries
	Kind   RelKind
	Owner  *Schema      // the schema this relation hangs off
	Offset uintptr      // byte offset of the field inside the owner
	Type   reflect.Type // the Go type of the field: *T, T, []T or []*T
	Elem   reflect.Type // the target model struct type

	// Column names, resolved against the two schemas.
	LocalField  string // field on the side that is joined from
	TargetField string // field on the side that is joined to

	// Join table, for ManyToMany only.
	JoinTable string
	JoinLocal string // join-table column pointing back at the owner
	JoinRef   string // join-table column pointing at the target

	table string // explicit target table override
	once  sync.Once
	meta  *Meta
	err   error

	// defaults guards the second lazy step. Target() resolves the far schema
	// behind `once`; resolveDefaults then *writes* LocalField and TargetField,
	// and it used to do so on every call. Those writes land on the *Relation
	// held by the process-global schema cache, which every repository over the
	// model shares — so two concurrent requests that first crossed the same
	// relation raced on the same two strings, with no lock and no happens-before
	// for the readers that follow.
	//
	// A second Once and not the existing one: resolveDefaults calls Target(),
	// which enters `once`, so reusing it would deadlock. Two steps, two Onces.
	defaults    sync.Once
	defaultsErr error
}

// Target resolves (and caches) the metadata of the model on the other side.
// Resolution is lazy so that models may reference each other in a cycle.
func (this *Relation) Target() (*Meta, error) {
	this.once.Do(func() {
		s, err := schemaOfType(this.Elem)
		if err != nil {
			this.err = err
			return
		}
		table := this.table
		if table == "" {
			table = TableNameOf(this.Elem)
		}
		this.meta = &Meta{Schema: s, Table: table}
	})
	return this.meta, this.err
}

// Local is the field on the owner used to join, resolved through Target so that
// defaults (the primary keys) are available.
func (this *Relation) Local() (*Field, error) {
	if _, err := this.Target(); err != nil {
		return nil, err
	}
	f := this.Owner.Field(this.LocalField)
	if f == nil {
		return nil, &SchemaError{Model: this.Owner.Name, Field: this.Name,
			Reason: "relation references unknown field " + this.LocalField}
	}
	return f, nil
}

// Remote is the field on the target used to join.
func (this *Relation) Remote() (*Field, error) {
	t, err := this.Target()
	if err != nil {
		return nil, err
	}
	f := t.Field(this.TargetField)
	if f == nil {
		return nil, &SchemaError{Model: t.Name, Field: this.Name,
			Reason: "relation references unknown field " + this.TargetField + " on " + t.Name}
	}
	return f, nil
}

// fieldValue returns an addressable reflect.Value of the relation field inside
// the model at base.
func (this *Relation) fieldValue(base unsafe.Pointer) reflect.Value {
	return reflect.NewAt(this.Type, unsafe.Add(base, this.Offset)).Elem()
}

// ---------------------------------------------------------------------------
// table names

var tableRegistry sync.Map // reflect.Type -> string

// RegisterTable pins the table name of a model type. sqlrepo.Define calls it, so
// declaring a repository is usually enough for relations to resolve.
func RegisterTable[M any](table string) {
	var zero M
	tableRegistry.Store(reflect.TypeOf(&zero).Elem(), table)
}

// RegisterTableType is RegisterTable for a reflect.Type.
func RegisterTableType(t reflect.Type, table string) { tableRegistry.Store(t, table) }

// Tabler lets a model name its own table.
type Tabler interface{ TableName() string }

var tablerType = reflect.TypeOf((*Tabler)(nil)).Elem()

// TableNameOf resolves the table of a model type: an explicit registration
// first, then a TableName method, then the snake_case plural of the type name.
func TableNameOf(t reflect.Type) string {
	if v, ok := tableRegistry.Load(t); ok {
		return v.(string)
	}
	if t.Implements(tablerType) {
		return reflect.New(t).Elem().Interface().(Tabler).TableName()
	}
	if pt := reflect.PointerTo(t); pt.Implements(tablerType) {
		return reflect.New(t).Interface().(Tabler).TableName()
	}
	return pluralise(snake(t.Name()))
}

// ---------------------------------------------------------------------------
// declaration

// relCandidate reports whether a field's type could carry a relation: a struct,
// a pointer to one, or a slice of either.
func relCandidate(t reflect.Type) (elem reflect.Type, slice bool, ok bool) {
	if t.Kind() == reflect.Slice {
		slice = true
		t = t.Elem()
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || isOptType(t) || isScalarStruct(t) {
		return nil, false, false
	}
	return t, slice, true
}

func parseRelation(s *Schema, sf reflect.StructField, base uintptr, tag string) (*Relation, error) {
	elem, slice, ok := relCandidate(sf.Type)
	if !ok {
		return nil, &SchemaError{Model: s.Type.String(), Field: sf.Name,
			Reason: "rel tag on a type that is not a struct, *struct or []struct"}
	}
	fail := func(reason string) error {
		return &SchemaError{Model: s.Type.String(), Field: sf.Name, Reason: reason}
	}

	kind, options := parseTag(tag)
	r := &Relation{
		Name:   sf.Name,
		Owner:  s,
		Offset: base + sf.Offset,
		Type:   sf.Type,
		Elem:   elem,
	}
	var fk, ref string
	for _, o := range options {
		k, v, _ := strings.Cut(strings.TrimSpace(o), "=")
		switch k {
		case "fk":
			fk = v
		case "ref":
			ref = v
		case "table":
			r.table = v
		case "join":
			r.JoinTable = v
		case "joinFK", "joinfk":
			r.JoinLocal = v
		case "joinRef", "joinref":
			r.JoinRef = v
		case "":
		default:
			return nil, fail("unknown rel option " + k)
		}
	}

	switch kind {
	case "belongs_to", "belongsTo":
		r.Kind = BelongsTo
	case "has_one", "hasOne":
		r.Kind = HasOne
	case "has_many", "hasMany":
		r.Kind = HasMany
	case "many_to_many", "manyToMany", "m2m":
		r.Kind = ManyToMany
	case "", "auto":
		// Infer: a slice is a collection, a single value is a belongs_to when
		// this struct carries the foreign key and a has_one otherwise.
		switch {
		case slice && r.JoinTable != "":
			r.Kind = ManyToMany
		case slice:
			r.Kind = HasMany
		case fk != "" && s.byName[fk] != nil:
			r.Kind = BelongsTo
		case s.byName[sf.Name+"ID"] != nil:
			r.Kind = BelongsTo
			fk = sf.Name + "ID"
		default:
			r.Kind = HasOne
		}
	default:
		return nil, fail("unknown relation kind " + kind)
	}

	if r.Kind.ToMany() && !slice {
		return nil, fail(r.Kind.String() + " must be declared on a slice field")
	}
	if !r.Kind.ToMany() && slice {
		return nil, fail(r.Kind.String() + " cannot be declared on a slice field")
	}

	switch r.Kind {
	case BelongsTo:
		// The foreign key lives here and points at the target's key.
		r.LocalField = cmpOr(fk, sf.Name+"ID")
		r.TargetField = cmpOr(ref, "")
	case HasOne, HasMany:
		// The foreign key lives on the target and points back at our key.
		r.LocalField = cmpOr(ref, "")
		r.TargetField = cmpOr(fk, s.Name+"ID")
	case ManyToMany:
		if r.JoinTable == "" {
			return nil, fail("many_to_many needs a join table: rel:\"many_to_many,join=...\"")
		}
		r.LocalField = cmpOr(ref, "")
		r.TargetField = cmpOr(fk, "")
		if r.JoinLocal == "" {
			r.JoinLocal = snake(s.Name) + "_id"
		}
		if r.JoinRef == "" {
			r.JoinRef = snake(elem.Name()) + "_id"
		}
	}
	return r, nil
}

// resolveDefaults fills the join fields that default to a primary key. It runs
// on first use rather than at build time, because the target schema may not be
// buildable yet when models reference each other.
func (this *Relation) resolveDefaults() error {
	this.defaults.Do(func() {
		t, err := this.Target()
		if err != nil {
			this.defaultsErr = err
			return
		}
		if this.LocalField == "" {
			this.LocalField = this.Owner.PK.Name
		}
		if this.TargetField == "" {
			this.TargetField = t.PK.Name
		}
	})
	return this.defaultsErr
}

// Resolve returns everything the query layer needs in one call.
func (this *Relation) Resolve() (target *Meta, local, remote *Field, err error) {
	if err = this.resolveDefaults(); err != nil {
		return nil, nil, nil, err
	}
	if target, err = this.Target(); err != nil {
		return nil, nil, nil, err
	}
	if local, err = this.Local(); err != nil {
		return nil, nil, nil, err
	}
	if remote, err = this.Remote(); err != nil {
		return nil, nil, nil, err
	}
	return target, local, remote, nil
}

// ---------------------------------------------------------------------------
// paths

// PathHop is one relation traversal on the way to a field.
type PathHop struct {
	Rel    *Relation
	Target *Meta
	Local  *Field // on the near side
	Remote *Field // on the far side
}

// WalkPath resolves a dotted path such as "Comments.Author.Name" against this
// model. It returns the relations crossed, the terminal field, and the
// canonical spelling of the path — the one form every layer agrees on, so a
// client may send `comments.author.name` and still get errors and echoes back
// in the model's own vocabulary.
//
// It is the single source of truth for path resolution: the SQL writer, the
// preloader and the HTTP DSL all go through it.
func (this *Meta) WalkPath(path string) (hops []PathHop, field *Field, canonical string, err error) {
	segs := strings.Split(path, ".")
	cur := this
	names := make([]string, 0, len(segs))

	for i, seg := range segs {
		last := i == len(segs)-1
		if last {
			if f := cur.Field(seg); f != nil {
				names = append(names, f.Name)
				return hops, f, strings.Join(names, "."), nil
			}
			// A path may also stop on a relation — a preload does exactly that.
			if rel := cur.Relation(seg); rel != nil {
				target, local, remote, rerr := rel.Resolve()
				if rerr != nil {
					return nil, nil, "", rerr
				}
				hops = append(hops, PathHop{Rel: rel, Target: target, Local: local, Remote: remote})
				names = append(names, rel.Name)
				return hops, nil, strings.Join(names, "."), nil
			}
			return nil, nil, "", &UnknownFieldError{Model: cur.Name, Field: path}
		}

		rel := cur.Relation(seg)
		if rel == nil {
			return nil, nil, "", &UnknownFieldError{Model: cur.Name, Field: path}
		}
		target, local, remote, rerr := rel.Resolve()
		if rerr != nil {
			return nil, nil, "", rerr
		}
		hops = append(hops, PathHop{Rel: rel, Target: target, Local: local, Remote: remote})
		names = append(names, rel.Name)
		cur = target
	}
	return nil, nil, "", &UnknownFieldError{Model: this.Name, Field: path}
}

// FieldAt resolves a path to its terminal column, refusing paths that stop on a
// relation.
func (this *Meta) FieldAt(path string) (*Field, string, error) {
	_, f, canonical, err := this.WalkPath(path)
	if err != nil {
		return nil, "", err
	}
	if f == nil {
		return nil, "", &SchemaError{Model: this.Name, Field: path,
			Reason: "path names a relation, not a column"}
	}
	return f, canonical, nil
}

// RelationAt resolves a path that ends on a relation, e.g. a preload target.
func (this *Meta) RelationAt(path string) (*Relation, string, error) {
	hops, f, canonical, err := this.WalkPath(path)
	if err != nil {
		return nil, "", err
	}
	if f != nil || len(hops) == 0 {
		return nil, "", &SchemaError{Model: this.Name, Field: path,
			Reason: "path names a column, not a relation"}
	}
	return hops[len(hops)-1].Rel, canonical, nil
}
