package crud

import (
	"fmt"
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
// Overrides: `fk=Field`, `ref=Field`, `table=name`, `schema=name`,
// `join=name`, `joinSchema=name`, `joinFK=col`, `joinRef=col`.
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

	table     TableRef // explicit target table override
	joinTable TableRef // structured form of JoinTable

	// context is present only on the relation view returned by Meta.Relation.
	// Schemas are process-global, but a Meta is one physical view of a schema:
	// an IndependentTable blueprint may bind Node to archived_nodes while the
	// canonical Node repository binds it to nodes. Keeping that choice here
	// makes a self edge, and a cycle that eventually returns to Node, stay in
	// the physical view from which the traversal started.
	context *relationContext

	// declaredLocal/Target are the immutable tag-time values. LocalField and
	// TargetField are filled lazily with primary-key defaults, so copying those
	// public fields from a process-global Relation while it is first resolving
	// would race. Contextual relation views are built from these declarations
	// instead.
	declaredLocal  string
	declaredTarget string

	once sync.Once
	meta *Meta
	err  error

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

// JoinTableReference returns the validated physical join-table identity. The
// value is a copy; changing it or the legacy JoinTable diagnostic string cannot
// retarget relation SQL after schema validation.
func (this *Relation) JoinTableReference() TableRef {
	if this == nil {
		return TableRef{}
	}
	if this.joinTable.Name != "" || this.joinTable.Schema != "" {
		return this.joinTable
	}
	return TableRef{Name: this.JoinTable}
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
		targetContext := this.context
		if table.Name == "" && this.context != nil {
			table, _ = this.context.tableFor(this.Elem)
		}
		if table.Name == "" {
			table, err = relationTableRefOf(this.Elem)
			if err != nil {
				this.err = err
				return
			}
		}
		if targetContext != nil {
			targetContext = targetContext.withTable(this.Elem, table)
		}
		this.meta = &Meta{Schema: s, Table: table.String(), tableRef: table, relations: targetContext}
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

type tableRegistration struct {
	mu           sync.Mutex
	table        TableRef
	resolved     bool
	resolvedErr  error
	fallbackOnce sync.Once
	fallback     TableRef
	fallbackErr  error
}

var tableRegistry sync.Map // reflect.Type -> *tableRegistration

// relationContext belongs to one traversal branch. It starts with the root
// Meta's physical table and remembers the physical view chosen for every model
// reached on that branch. That makes A(archive)->B->A return to A(archive),
// while an explicit `table=live_a` edge starts a child branch whose later
// self-relations remain on live_a. Relation views are cached per context so
// their lazy metadata remains stable and race-free.
type relationContext struct {
	tables    map[reflect.Type]TableRef
	relations sync.Map // *Relation schema declaration -> *Relation contextual view
}

func (this *relationContext) tableFor(t reflect.Type) (TableRef, bool) {
	if this == nil {
		return TableRef{}, false
	}
	table, ok := this.tables[t]
	return table, ok
}

func (this *relationContext) withTable(t reflect.Type, table TableRef) *relationContext {
	if this == nil {
		return nil
	}
	if current, ok := this.tables[t]; ok && current == table {
		return this
	}
	tables := make(map[reflect.Type]TableRef, len(this.tables)+1)
	for model, physicalTable := range this.tables {
		tables[model] = physicalTable
	}
	tables[t] = table
	return &relationContext{tables: tables}
}

func (this *relationContext) bind(declaration *Relation) *Relation {
	if this == nil || declaration == nil {
		return declaration
	}
	candidate := &Relation{
		Name:           declaration.Name,
		Kind:           declaration.Kind,
		Owner:          declaration.Owner,
		Offset:         declaration.Offset,
		Type:           declaration.Type,
		Elem:           declaration.Elem,
		LocalField:     declaration.declaredLocal,
		TargetField:    declaration.declaredTarget,
		JoinTable:      declaration.JoinTable,
		JoinLocal:      declaration.JoinLocal,
		JoinRef:        declaration.JoinRef,
		table:          declaration.table,
		joinTable:      declaration.joinTable,
		context:        this,
		declaredLocal:  declaration.declaredLocal,
		declaredTarget: declaration.declaredTarget,
	}
	actual, _ := this.relations.LoadOrStore(declaration, candidate)
	return actual.(*Relation)
}

func registrationFor(t reflect.Type) *tableRegistration {
	entry := new(tableRegistration)
	actual, _ := tableRegistry.LoadOrStore(t, entry)
	return actual.(*tableRegistration)
}

// RegisterTable pins the table name of a model type. sqlrepo.Define calls it, so
// declaring a repository is usually enough for relations to resolve. It panics
// on a conflicting or late declaration; use TryRegisterTable when declaration
// errors belong to a larger start-up validation result.
func RegisterTable[M any](table string) {
	if err := TryRegisterTable[M](table); err != nil {
		panic(err)
	}
}

// TryRegisterTable is RegisterTable without the panic.
func TryRegisterTable[M any](table string) error {
	var zero M
	return TryRegisterTableType(reflect.TypeOf(&zero).Elem(), table)
}

// RegisterTableRef is RegisterTable for a structured physical identifier.
func RegisterTableRef[M any](table TableRef) {
	if err := TryRegisterTableRef[M](table); err != nil {
		panic(err)
	}
}

// TryRegisterTableRef is RegisterTableRef without the panic.
func TryRegisterTableRef[M any](table TableRef) error {
	var zero M
	return TryRegisterTableRefType(reflect.TypeOf(&zero).Elem(), table)
}

// RegisterTableType is RegisterTable for a reflect.Type.
func RegisterTableType(t reflect.Type, table string) {
	if err := TryRegisterTableType(t, table); err != nil {
		panic(err)
	}
}

// RegisterTableRefType is RegisterTableRef for a reflect.Type.
func RegisterTableRefType(t reflect.Type, table TableRef) {
	if err := TryRegisterTableRefType(t, table); err != nil {
		panic(err)
	}
}

// TryRegisterTableType pins a model table without panicking. A type may be
// registered repeatedly with the same table, but never with two tables. Once
// Relation.Target has published a name to relation metadata, a different late
// name is also refused: accepting it would leave the already-resolved relation
// on a stale table while making the registry claim otherwise.
func TryRegisterTableType(t reflect.Type, table string) error {
	if err := validateTableRegistrationType(t); err != nil {
		return err
	}
	ref, err := NewTableRef(table)
	if err != nil {
		return tableRefSchemaError(t, err)
	}
	return TryRegisterTableRefType(t, ref)
}

// TryRegisterTableRefType pins a structured model table without panicking.
func TryRegisterTableRefType(t reflect.Type, table TableRef) error {
	if err := validateTableRegistrationType(t); err != nil {
		return err
	}
	if err := table.Validate(); err != nil {
		return tableRefSchemaError(t, err)
	}

	entry := registrationFor(t)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.resolved {
		if entry.resolvedErr == nil && entry.table == table {
			return nil
		}
		resolved := entry.table.String()
		if entry.resolvedErr != nil {
			resolved = "<invalid model table>"
		}
		return &SchemaError{Model: t.String(), Reason: fmt.Sprintf(
			"table was already resolved as %q; refusing late registration %q", resolved, table.String())}
	}
	if entry.table.Name == "" {
		entry.table = table
		return nil
	}
	if entry.table == table {
		return nil
	}
	return &SchemaError{Model: t.String(), Reason: fmt.Sprintf(
		"table is already registered as %q; refusing conflicting registration %q", entry.table.String(), table.String())}
}

func validateTableRegistrationType(t reflect.Type) error {
	if t == nil {
		return &SchemaError{Model: "<nil>", Reason: "cannot register a table for a nil type"}
	}
	if t.Kind() != reflect.Struct {
		return &SchemaError{Model: t.String(), Reason: "table registration model must be a struct"}
	}
	return nil
}

// Tabler lets a model name its own table.
type Tabler interface{ TableName() string }

var tablerType = reflect.TypeOf((*Tabler)(nil)).Elem()

// TableNameOf reports the table of a model type: an explicit registration
// first, then a TableName method, then the snake_case plural of the type name.
// Merely asking for the name does not publish relation metadata and therefore
// does not freeze the fallback. Relation.Target performs that separate step.
func TableNameOf(t reflect.Type) string {
	ref, err := TableRefOf(t)
	if err != nil {
		return ""
	}
	return ref.String()
}

// TableRefOf reports the structured table of a model type: an explicit
// registration first, then a one-component TableName result, then convention.
// It is a read-only preview and does not publish relation metadata.
func TableRefOf(t reflect.Type) (TableRef, error) {
	if t == nil || t.Kind() != reflect.Struct {
		return TableRef{}, &SchemaError{Model: fmt.Sprint(t), Reason: "table lookup model must be a struct"}
	}
	entry := registrationFor(t)
	entry.mu.Lock()
	if entry.table.Name != "" {
		table := entry.table
		entry.mu.Unlock()
		return table, nil
	}
	entry.mu.Unlock()
	fallback, err := fallbackTableRef(entry, t)
	// A registration that completed while user TableName code was running wins
	// this read without turning the fallback lookup itself into publication.
	entry.mu.Lock()
	if entry.table.Name != "" {
		fallback = entry.table
		err = nil
	}
	entry.mu.Unlock()
	return fallback, err
}

func fallbackTableRef(entry *tableRegistration, t reflect.Type) (TableRef, error) {
	entry.fallbackOnce.Do(func() {
		name := defaultTableNameOf(t)
		if name == "" {
			entry.fallbackErr = &TableRefError{Component: "name",
				Reason: "table name resolved to empty; TableName must return a name or an explicit table must be supplied"}
			return
		}
		entry.fallback, entry.fallbackErr = NewTableRef(name)
	})
	return entry.fallback, entry.fallbackErr
}

// relationTableRefOf resolves the one process-wide table a relation without
// an explicit `table=` tag may publish. Unlike TableNameOf, this freezes the
// answer: Relation.Target caches the resulting Meta, so accepting a different
// registration afterwards could never update every reader consistently.
func relationTableRefOf(t reflect.Type) (TableRef, error) {
	entry := registrationFor(t)
	entry.mu.Lock()
	if entry.table.Name != "" {
		entry.resolved = true
		table := entry.table
		entry.mu.Unlock()
		return table, nil
	}
	entry.mu.Unlock()

	// A model-owned TableName may be arbitrary consumer code. Do not run it
	// under the registry lock, and do not run it at all when a registration has
	// already won. A concurrent registration is checked again below.
	fallback, fallbackErr := fallbackTableRef(entry, t)
	entry.mu.Lock()
	if entry.table.Name == "" {
		if fallbackErr != nil {
			entry.resolved = true
			entry.resolvedErr = fallbackErr
			entry.mu.Unlock()
			return TableRef{}, tableRefSchemaError(t, fallbackErr)
		}
		entry.table = fallback
	}
	entry.resolved = true
	table := entry.table
	entry.mu.Unlock()
	return table, nil
}

func defaultTableNameOf(t reflect.Type) string {
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
	var fk, ref, tableName, tableSchema, joinName, joinSchema string
	var tableSet, tableSchemaSet, joinSet, joinSchemaSet bool
	for _, o := range options {
		k, v, _ := strings.Cut(strings.TrimSpace(o), "=")
		v = strings.TrimSpace(v)
		switch k {
		case "fk":
			fk = v
		case "ref":
			ref = v
		case "table":
			tableName, tableSet = v, true
		case "schema":
			tableSchema, tableSchemaSet = v, true
		case "join":
			joinName, joinSet = v, true
		case "joinSchema", "joinschema":
			joinSchema, joinSchemaSet = v, true
		case "joinFK", "joinfk":
			r.JoinLocal = v
		case "joinRef", "joinref":
			r.JoinRef = v
		case "":
		default:
			return nil, fail("unknown rel option " + k)
		}
	}
	if tableSet || tableSchemaSet {
		if !tableSet {
			return nil, fail("schema= needs an explicit table= component")
		}
		var err error
		if tableSchemaSet {
			r.table, err = NewTableRefInSchema(tableSchema, tableName)
		} else {
			r.table, err = NewTableRef(tableName)
		}
		if err != nil {
			return nil, fail(strings.TrimPrefix(err.Error(), "crud: "))
		}
	}
	if joinSet || joinSchemaSet {
		if !joinSet {
			return nil, fail("joinSchema= needs an explicit join= component")
		}
		var err error
		if joinSchemaSet {
			r.joinTable, err = NewTableRefInSchema(joinSchema, joinName)
		} else {
			r.joinTable, err = NewTableRef(joinName)
		}
		if err != nil {
			return nil, fail(strings.TrimPrefix(err.Error(), "crud: "))
		}
		r.JoinTable = r.joinTable.String()
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
	r.declaredLocal = r.LocalField
	r.declaredTarget = r.TargetField
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

// Relation resolves an edge in this Meta's physical-table context. Schema is
// shared process-wide; the returned Relation is a stable per-Meta-context view
// so alternate table blueprints cannot leak their choice into one another.
func (this *Meta) Relation(ref string) *Relation {
	if this == nil || this.Schema == nil {
		return nil
	}
	return this.relations.bind(this.Schema.Relation(ref))
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

// ValidateRelationPath validates a path that ends on a relation and returns its
// canonical spelling without resolving or publishing relation metadata. It is
// the declaration-time counterpart of RelationAt: callers can validate every
// fallible option first and cross the table-publication boundary only after the
// whole declaration succeeds.
func (this *Meta) ValidateRelationPath(path string) (string, error) {
	if this == nil || this.Schema == nil {
		return "", &SchemaError{Reason: "relation path needs a root model"}
	}

	// Mirror the immutable physical-table branch carried by Relation.Target,
	// but keep it local to this validation. No contextual Relation is created or
	// cached, and TableRefOf below is a read-only preview.
	var tables map[reflect.Type]TableRef
	if this.relations != nil {
		tables = make(map[reflect.Type]TableRef, len(this.relations.tables))
		for model, table := range this.relations.tables {
			tables[model] = table
		}
	}

	segs := strings.Split(path, ".")
	cur := this.Schema
	names := make([]string, 0, len(segs))
	for i, seg := range segs {
		last := i == len(segs)-1
		if last && cur.Field(seg) != nil {
			return "", &SchemaError{Model: this.Name, Field: path,
				Reason: "path names a column, not a relation"}
		}

		rel := cur.Relation(seg)
		if rel == nil {
			return "", &UnknownFieldError{Model: cur.Name, Field: path}
		}

		localName := rel.declaredLocal
		if localName == "" {
			localName = rel.Owner.PK.Name
		}
		if rel.Owner.Field(localName) == nil {
			return "", &SchemaError{Model: rel.Owner.Name, Field: rel.Name,
				Reason: "relation references unknown field " + localName}
		}

		target, err := schemaOfType(rel.Elem)
		if err != nil {
			return "", err
		}
		remoteName := rel.declaredTarget
		if remoteName == "" {
			remoteName = target.PK.Name
		}
		if target.Field(remoteName) == nil {
			return "", &SchemaError{Model: target.Name, Field: rel.Name,
				Reason: "relation references unknown field " + remoteName + " on " + target.Name}
		}

		table := rel.table
		if table.Name == "" && tables != nil {
			table = tables[rel.Elem]
		}
		if table.Name == "" {
			var err error
			table, err = TableRefOf(rel.Elem)
			if err != nil {
				return "", tableRefSchemaError(rel.Elem, err)
			}
		}
		if tables != nil {
			tables[rel.Elem] = table
		}

		names = append(names, rel.Name)
		if last {
			return strings.Join(names, "."), nil
		}
		cur = target
	}
	return "", &UnknownFieldError{Model: this.Name, Field: path}
}
