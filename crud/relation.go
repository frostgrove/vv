package crud

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"unsafe"
)

const RelTagKey = "rel"

type RelKind uint8

const (
	BelongsTo RelKind = iota

	HasOne

	HasMany

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

func (this RelKind) ToMany() bool { return this == HasMany || this == ManyToMany }

type Relation struct {
	Name   string
	Kind   RelKind
	Owner  *Schema
	Offset uintptr
	Type   reflect.Type
	Elem   reflect.Type

	LocalField  string
	TargetField string

	JoinTable string
	JoinLocal string
	JoinRef   string

	table     TableRef
	joinTable TableRef

	context *relationContext

	declaredLocal  string
	declaredTarget string

	once sync.Once
	meta *Meta
	err  error

	defaults    sync.Once
	defaultsErr error
}

func (this *Relation) JoinTableReference() TableRef {
	if this == nil {
		return TableRef{}
	}
	if this.joinTable.Name != "" || this.joinTable.Schema != "" {
		return this.joinTable
	}
	return TableRef{Name: this.JoinTable}
}

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

func (this *Relation) fieldValue(base unsafe.Pointer) reflect.Value {
	return reflect.NewAt(this.Type, unsafe.Add(base, this.Offset)).Elem()
}

type tableRegistration struct {
	mu           sync.Mutex
	table        TableRef
	resolved     bool
	resolvedErr  error
	fallbackOnce sync.Once
	fallback     TableRef
	fallbackErr  error
}

var tableRegistry sync.Map

type relationContext struct {
	tables    map[reflect.Type]TableRef
	relations sync.Map
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

func RegisterTable[M any](table string) {
	if err := TryRegisterTable[M](table); err != nil {
		panic(err)
	}
}

func TryRegisterTable[M any](table string) error {
	var zero M
	return TryRegisterTableType(reflect.TypeOf(&zero).Elem(), table)
}

func RegisterTableRef[M any](table TableRef) {
	if err := TryRegisterTableRef[M](table); err != nil {
		panic(err)
	}
}

func TryRegisterTableRef[M any](table TableRef) error {
	var zero M
	return TryRegisterTableRefType(reflect.TypeOf(&zero).Elem(), table)
}

func RegisterTableType(t reflect.Type, table string) {
	if err := TryRegisterTableType(t, table); err != nil {
		panic(err)
	}
}

func RegisterTableRefType(t reflect.Type, table TableRef) {
	if err := TryRegisterTableRefType(t, table); err != nil {
		panic(err)
	}
}

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

type Tabler interface{ TableName() string }

var tablerType = reflect.TypeOf((*Tabler)(nil)).Elem()

func TableNameOf(t reflect.Type) string {
	ref, err := TableRefOf(t)
	if err != nil {
		return ""
	}
	return ref.String()
}

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
		r.LocalField = cmpOr(fk, sf.Name+"ID")
		r.TargetField = cmpOr(ref, "")
	case HasOne, HasMany:
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

	if err = validateRelationKeyType(this.Owner.Name, local); err != nil {
		return nil, nil, nil, err
	}
	if err = validateRelationKeyType(target.Name, remote); err != nil {
		return nil, nil, nil, err
	}
	return target, local, remote, nil
}

func validateRelationKeyType(model string, field *Field) error {
	if field == nil {
		return nil
	}
	if relationKeyTypeSupported(field.Type) {
		return nil
	}
	return &SchemaError{Model: model, Field: field.Name, Reason: fmt.Sprintf(
		"relation key type %s is not comparable; use []byte or implement driver.Valuer with a stable scalar value", field.Type)}
}

func relationKeyTypeSupported(t reflect.Type) bool {
	if elem := OptElem(t); elem != nil {
		t = elem
	}
	for t.Kind() == reflect.Pointer {
		if t.Implements(valuerType) {
			return true
		}
		t = t.Elem()
	}
	if isByteSliceType(t) || t.Implements(valuerType) {
		return true
	}
	if t.Kind() != reflect.Pointer {
		pointer := reflect.PointerTo(t)
		if pointer.Implements(valuerType) {
			return true
		}
	}
	return t.Comparable()
}

func isByteSliceType(t reflect.Type) bool {
	return t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8
}

type PathHop struct {
	Rel    *Relation
	Target *Meta
	Local  *Field
	Remote *Field
}

func (this *Meta) Relation(ref string) *Relation {
	if this == nil || this.Schema == nil {
		return nil
	}
	return this.relations.bind(this.Schema.Relation(ref))
}

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

func (this *Meta) ValidateRelationPath(path string) (string, error) {
	if this == nil || this.Schema == nil {
		return "", &SchemaError{Reason: "relation path needs a root model"}
	}

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
		local := rel.Owner.Field(localName)
		if local == nil {
			return "", &SchemaError{Model: rel.Owner.Name, Field: rel.Name,
				Reason: "relation references unknown field " + localName}
		}
		if err := validateRelationKeyType(rel.Owner.Name, local); err != nil {
			return "", err
		}

		target, err := schemaOfType(rel.Elem)
		if err != nil {
			return "", err
		}
		remoteName := rel.declaredTarget
		if remoteName == "" {
			remoteName = target.PK.Name
		}
		remote := target.Field(remoteName)
		if remote == nil {
			return "", &SchemaError{Model: target.Name, Field: rel.Name,
				Reason: "relation references unknown field " + remoteName + " on " + target.Name}
		}
		if err := validateRelationKeyType(target.Name, remote); err != nil {
			return "", err
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
