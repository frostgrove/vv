package crud

import (
	"database/sql"
	"database/sql/driver"
	"encoding"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode"
	"unsafe"

	"github.com/frostgrove/vv/utils"
)

// NowFunc is the clock a soft delete stamps with. Swap it in a test rather than
// reaching for a fake database.
var NowFunc = time.Now

// TagKey is the struct tag vv reads. `db:"column,option,option"`.
//
// Options:
//
//	pk         this column is the primary key
//	auto       the database generates the value on insert (serial / identity /
//	           auto_increment). Integer primary keys get this by default.
//	noauto     opt an integer primary key out of the default above
//	immutable  written on insert, never on update (created_at, tenant_id)
//	generated  never written at all; read back after every write (computed
//	           columns, DB-side defaults, updated_at triggers)
//	serverowned never written by generic create/replace/patch paths. A dedicated
//	           repository lifecycle operation may own it instead
//	tombstone  nullable soft-delete timestamp owned by the repository. It implies
//	           serverowned and makes sqlrepo soft delete the declarative default
//	version    optimistic lock: an integer the repository advances on every
//	           update and checks in the update's own WHERE, so a write against a
//	           row somebody else has changed since it was read is refused with
//	           ErrStaleVersion rather than silently overwriting them
//	-          ignore the field completely
//
// The column name may be omitted (`db:",pk"`), in which case it is derived from
// the Go field name in snake_case. Without any tag at all a field is still
// mapped, so a plain struct works out of the box.
const TagKey = "db"

// Field is one mapped column.
type Field struct {
	Name        string       // Go field name, as used by predicates and update DTOs
	Column      string       // SQL column name
	Type        reflect.Type // Go type of the field
	Offset      uintptr      // byte offset inside the model struct
	Ordinal     int          // index in Schema.Fields
	PK          bool
	Auto        bool
	Immutable   bool
	Generated   bool
	ServerOwned bool // generic writes never accept or persist caller values
	Tombstone   bool // soft-delete state; implies ServerOwned
	Version     bool // optimistic lock counter, owned by the repository
	Optional    bool // the field is a crud.Opt[...]

	noAutoOptOut bool // `noauto` was requested explicitly
}

// Nullable reports whether the field can represent SQL NULL. Besides pointers
// and Opt values it recognises Scanner/Valuer wrappers such as sql.NullTime and
// gorm.DeletedAt.
func (this *Field) Nullable() bool { return nullableField(this) }

// AcceptsTombstoneTimestamp reports whether Delete can stamp time.Time into the
// column and a later read can scan it back. Besides *time.Time and Opt[time.Time]
// it accepts Scanner/Valuer wrappers such as sql.NullTime and gorm.DeletedAt,
// but rejects merely nullable *string/Opt[int] fields that would fail only on
// the first delete (or rely on database-specific coercion).
func (this *Field) AcceptsTombstoneTimestamp() bool {
	if this == nil {
		return false
	}
	if elem := OptElem(this.Type); elem != nil {
		return elem == timeType
	}
	t := this.Type
	if t.Kind() == reflect.Pointer {
		if t.Elem() == timeType {
			return true
		}
		t = t.Elem()
	}
	if t == timeType {
		return true
	}
	return scannerValuerCarriesTime(t)
}

// pointerTo returns a *T aimed at this field inside the model at base.
func (this *Field) pointerTo(base unsafe.Pointer) any {
	return reflect.NewAt(this.Type, unsafe.Add(base, this.Offset)).Interface()
}

// valueOf reads the field out of the model at base.
func (this *Field) valueOf(base unsafe.Pointer) any {
	return reflect.NewAt(this.Type, unsafe.Add(base, this.Offset)).Elem().Interface()
}

// comparable returns the field value normalised for diffing: nil for NULL,
// the bare element otherwise.
func (this *Field) comparableOf(base unsafe.Pointer) any {
	v := reflect.NewAt(this.Type, unsafe.Add(base, this.Offset)).Elem()
	if this.Optional {
		value, defined, null, _ := utils.Inspect(v.Interface())
		if !defined || null {
			return nil
		}
		return value
	}
	if this.Type.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		return v.Elem().Interface()
	}
	return v.Interface()
}

// Schema is the reflective description of a model type. It is independent of
// the table it is bound to and cached per type.
type Schema struct {
	Type   reflect.Type
	Name   string
	Fields []*Field
	PK     *Field

	Insert    []*Field // written by INSERT, including the PK
	InsertGen []*Field // written by INSERT when the PK is DB-generated
	Update    []*Field // eligible for UPDATE SET
	HasGen    bool     // has at least one `generated` column
	Version   *Field   // the optimistic-lock column, or nil
	Tombstone *Field   // repository-owned soft-delete timestamp, or nil

	// Relations are navigable edges: they never become columns, but query
	// paths, sorts and preloads can walk them.
	Relations []*Relation

	byName    map[string]*Field
	byCol     map[string]*Field
	byRel     map[string]*Relation
	byFold    map[string]*Field
	byRelFold map[string]*Relation
}

// Field resolves a reference by Go field name, then by column name, then by a
// case- and separator-insensitive match. That last step is what lets an HTTP
// client send `createdAt`, `created_at` or `CreatedAt` and mean the same
// column. An alias that would be ambiguous is not registered at all.
func (this *Schema) Field(ref string) *Field {
	if f, ok := this.byName[ref]; ok {
		return f
	}
	if f, ok := this.byCol[ref]; ok {
		return f
	}
	if f, ok := this.byFold[fold(ref)]; ok && f != ambiguousField {
		return f
	}
	return nil
}

// Relation resolves an edge the same way, so `author.name` and `Author.Name`
// are the same path.
func (this *Schema) Relation(ref string) *Relation {
	if r, ok := this.byRel[ref]; ok {
		return r
	}
	if r, ok := this.byRelFold[fold(ref)]; ok && r != ambiguousRel {
		return r
	}
	return nil
}

// fold normalises an identifier for forgiving lookups: lower case, no
// separators. CreatedAt, created_at, createdAt and CREATED-AT all fold to
// "createdat".
func fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '_', '-', ' ':
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// ambiguous marks a folded alias as unusable rather than picking a winner.
var ambiguousField = &Field{}
var ambiguousRel = &Relation{}

func (this *Schema) addFold(key string, f *Field) {
	if prev, ok := this.byFold[key]; ok && prev != f {
		this.byFold[key] = ambiguousField
		return
	}
	this.byFold[key] = f
}

func (this *Schema) addRelFold(key string, r *Relation) {
	if prev, ok := this.byRelFold[key]; ok && prev != r {
		this.byRelFold[key] = ambiguousRel
		return
	}
	this.byRelFold[key] = r
}

// Columns returns every mapped column, primary key first.
func (this *Schema) Columns() []string {
	cols := make([]string, len(this.Fields))
	for i, f := range this.Fields {
		cols[i] = f.Column
	}
	return cols
}

var schemaCache sync.Map // reflect.Type -> *schemaFuture

type schemaResult struct {
	s   *Schema
	err error
}

type schemaFuture struct {
	done chan struct{}
	schemaResult
}

// SchemaOf reflects over M once and caches the result.
func SchemaOf[M any]() (*Schema, error) {
	var zero M
	t := reflect.TypeOf(&zero).Elem()
	return schemaOfType(t)
}

// schemaOfType is SchemaOf for a reflect.Type; relations resolve their target
// through it, lazily, so two models may reference each other.
func schemaOfType(t reflect.Type) (*Schema, error) {
	candidate := &schemaFuture{done: make(chan struct{})}
	value, loaded := schemaCache.LoadOrStore(t, candidate)
	future := value.(*schemaFuture)
	if !loaded {
		future.s, future.err = buildSchema(t)
		close(future.done)
	}
	<-future.done
	return future.s, future.err
}

// SchemaOfType exposes schema resolution for a reflect.Type.
func SchemaOfType(t reflect.Type) (*Schema, error) { return schemaOfType(t) }

// MustSchemaOf is SchemaOf, panicking on a broken declaration. Use it in
// package-level initialisers so a bad mapping fails at start-up.
func MustSchemaOf[M any]() *Schema {
	s, err := SchemaOf[M]()
	if err != nil {
		panic(err)
	}
	return s
}

func buildSchema(t reflect.Type) (*Schema, error) {
	if t.Kind() != reflect.Struct {
		return nil, &SchemaError{Model: t.String(), Reason: "model must be a struct"}
	}
	s := &Schema{
		Type:      t,
		Name:      t.Name(),
		byName:    map[string]*Field{},
		byCol:     map[string]*Field{},
		byRel:     map[string]*Relation{},
		byFold:    map[string]*Field{},
		byRelFold: map[string]*Relation{},
	}
	if err := collectFields(s, t, 0, nil); err != nil {
		return nil, err
	}
	if len(s.Fields) == 0 {
		return nil, &SchemaError{Model: t.String(), Reason: "no mapped columns"}
	}
	if s.PK == nil {
		// Fall back to a field called ID, then to a column called id.
		if f := s.Field("ID"); f != nil {
			f.PK = true
			s.PK = f
		} else if f := s.byCol["id"]; f != nil {
			f.PK = true
			s.PK = f
		} else {
			return nil, &SchemaError{Model: t.String(), Reason: `no primary key: tag one field with db:",pk"`}
		}
		if isIntKind(s.PK.Type.Kind()) && !s.PK.noAutoOptOut {
			s.PK.Auto = true
		}
	}
	// The primary key sorts first; the rest keep declaration order.
	if s.PK.Ordinal != 0 {
		ordered := make([]*Field, 0, len(s.Fields))
		ordered = append(ordered, s.PK)
		for _, f := range s.Fields {
			if f != s.PK {
				ordered = append(ordered, f)
			}
		}
		s.Fields = ordered
	}
	for i, f := range s.Fields {
		f.Ordinal = i
		if f.Tombstone {
			f.ServerOwned = true
			if err := checkTombstone(t, s, f); err != nil {
				return nil, err
			}
			s.Tombstone = f
		}
		if f.Version {
			if err := checkVersion(t, s, f); err != nil {
				return nil, err
			}
			s.Version = f
		}
		if f.Generated || f.ServerOwned {
			if f.Generated {
				s.HasGen = true
			}
			continue
		}
		s.Insert = append(s.Insert, f)
		if !f.PK {
			s.InsertGen = append(s.InsertGen, f)
			// The version column is left out of the UPDATE list for the same
			// reason an immutable one is: it is not the caller's to set. The
			// repository writes `version = version + 1` itself, and Save's
			// conflict clause leaves it alone, so an upsert built from a stale
			// model cannot wind the counter back.
			if !f.Immutable && !f.Version {
				s.Update = append(s.Update, f)
			}
		}
	}
	if s.PK.Generated {
		return nil, &SchemaError{Model: t.String(), Field: s.PK.Name, Reason: "primary key cannot be `generated`; use `auto`"}
	}
	for _, f := range s.Fields {
		s.addFold(fold(f.Name), f)
		s.addFold(fold(f.Column), f)
	}
	for _, r := range s.Relations {
		s.addRelFold(fold(r.Name), r)
	}
	return s, nil
}

// checkTombstone makes the lifecycle declaration complete at schema-build
// time. A tombstone cannot share ownership with another generic write policy,
// and one model cannot have two competing notions of being deleted.
func checkTombstone(t reflect.Type, s *Schema, f *Field) error {
	deny := func(reason string) error {
		return &SchemaError{Model: t.String(), Field: f.Name, Reason: reason}
	}
	switch {
	case s.Tombstone != nil:
		return deny("a model can carry only one `tombstone` column, and " + s.Tombstone.Name + " already is one")
	case f.PK:
		return deny("the primary key cannot be the `tombstone` column")
	case f.Immutable:
		return deny("`tombstone` and `immutable` contradict each other: delete and restore have to write the tombstone")
	case f.Generated:
		return deny("`tombstone` and `generated` contradict each other: the repository, not a database default, owns the lifecycle")
	case f.Version:
		return deny("`tombstone` and `version` are distinct repository-owned columns")
	case !nullableField(f):
		return deny("a `tombstone` column has to be nullable, or there is no value that means not deleted")
	case !f.AcceptsTombstoneTimestamp():
		return deny("a `tombstone` column must carry time.Time: use *time.Time, Opt[time.Time], or a Scanner/Valuer timestamp wrapper")
	}
	return nil
}

// scannerValuerCarriesTime validates the actual driver contract rather than a
// struct's name. The copy is addressable so wrappers with pointer-only methods
// are supported. A hostile/broken declaration becomes false here and a normal
// SchemaError at the caller, not a panic escaping TryDefine.
func scannerValuerCarriesTime(t reflect.Type) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	holder := reflect.New(t)
	scanner, ok := holder.Interface().(sql.Scanner)
	if !ok {
		if scanner, ok = holder.Elem().Interface().(sql.Scanner); !ok {
			return false
		}
	}
	sample := time.Date(2001, 2, 3, 4, 5, 6, 7, time.UTC)
	if err := scanner.Scan(sample); err != nil {
		return false
	}
	var valuer driver.Valuer
	if candidate, yes := holder.Elem().Interface().(driver.Valuer); yes {
		valuer = candidate
	} else if candidate, yes := holder.Interface().(driver.Valuer); yes {
		valuer = candidate
	} else {
		return false
	}
	value, err := valuer.Value()
	if err != nil {
		return false
	}
	_, ok = value.(time.Time)
	return ok
}

func nullableField(f *Field) bool {
	if f == nil {
		return false
	}
	if f.Optional || f.Type.Kind() == reflect.Pointer {
		return true
	}
	// Scanner/Valuer wrappers such as sql.NullTime and gorm.DeletedAt carry
	// NULL in a value struct rather than in a Go pointer.
	return reflect.PointerTo(f.Type).Implements(scannerType) &&
		(f.Type.Implements(valuerType) || reflect.PointerTo(f.Type).Implements(valuerType))
}

// checkVersion refuses the declarations an optimistic lock cannot be built on.
// Each of them fails silently at run time otherwise: a version the caller can
// set is not a lock, a version nobody writes never advances, and a version on
// the primary key would be checked against the row it identifies.
func checkVersion(t reflect.Type, s *Schema, f *Field) error {
	deny := func(reason string) error {
		return &SchemaError{Model: t.String(), Field: f.Name, Reason: reason}
	}
	switch {
	case s.Version != nil:
		return deny("a model can carry only one `version` column, and " + s.Version.Name + " already is one")
	case f.PK:
		return deny("the primary key cannot be the `version` column")
	case f.Immutable:
		return deny("`version` and `immutable` contradict each other: the lock has to advance")
	case f.Generated:
		return deny("`version` and `generated` contradict each other: the lock is written by vv, not read back from a default")
	case !isIntKind(ElemType(f.Type).Kind()):
		// A timestamp version would need a clock, and two application servers
		// do not share one. An integer counter needs nothing but the row.
		return deny("a `version` column must be an integer; " + f.Type.String() + " is not")
	}
	return nil
}

func collectFields(s *Schema, t reflect.Type, base uintptr, seen []reflect.Type) error {
	for i := range t.NumField() {
		sf := t.Field(i)
		tag, hasTag := sf.Tag.Lookup(TagKey)
		relTag, hasRel := sf.Tag.Lookup(RelTagKey)
		if tag == "-" {
			continue
		}
		// An explicit relation declaration belongs to the anonymous field itself,
		// exactly as it does on a named field. Flattening it used to discard the
		// declaration and expose the embedded struct's columns instead. Only a
		// completely untagged non-scalar struct is a mixin to flatten.
		if sf.Anonymous && !hasTag && !hasRel && deref(sf.Type).Kind() == reflect.Struct && !isOptType(sf.Type) && !isScalarStruct(sf.Type) {
			if sf.Type.Kind() == reflect.Pointer {
				return &SchemaError{Model: s.Type.String(), Field: sf.Name, Reason: "embedded pointer structs are not supported"}
			}
			for _, st := range seen {
				if st == sf.Type {
					return &SchemaError{Model: s.Type.String(), Field: sf.Name, Reason: "recursive embedding"}
				}
			}
			if err := collectFields(s, sf.Type, base+sf.Offset, append(seen, sf.Type)); err != nil {
				return err
			}
			continue
		}
		if !sf.IsExported() {
			// Reflection cannot read the field, so a tag asking for it to be
			// mapped can only ever be a typo — and silently dropping the column
			// would show up as a zero in the row rather than as an error here.
			if hasTag {
				return &SchemaError{Model: s.Type.String(), Field: sf.Name,
					Reason: `unexported fields cannot be mapped; rename it or tag it db:"-"`}
			}
			continue
		}

		// A relation is anything tagged `rel`, and any struct-shaped field is a
		// relation candidate: mapping one as a column is never what was meant.
		if _, _, candidate := relCandidate(sf.Type); candidate {
			if !hasRel || relTag == "-" {
				continue // neither a column nor an edge
			}
			r, err := parseRelation(s, sf, base, relTag)
			if err != nil {
				return err
			}
			if _, dup := s.byRel[r.Name]; dup {
				return &SchemaError{Model: s.Type.String(), Field: r.Name, Reason: "duplicate relation"}
			}
			s.Relations = append(s.Relations, r)
			s.byRel[r.Name] = r
			continue
		}
		if hasRel && relTag != "-" {
			return &SchemaError{Model: s.Type.String(), Field: sf.Name,
				Reason: "rel tag on a type that is not a struct, *struct or []struct"}
		}

		name, options := parseTag(tag)
		f := &Field{
			Name:   sf.Name,
			Column: cmpOr(name, snake(sf.Name)),
			Type:   sf.Type,
			Offset: base + sf.Offset,
		}
		f.Optional = isOptType(sf.Type)
		for _, o := range options {
			switch o {
			case "pk", "primarykey", "primary_key":
				f.PK = true
			case "auto", "autoincrement", "identity", "serial":
				f.Auto = true
			case "noauto":
				f.noAutoOptOut = true
			case "immutable", "readonly", "insertonly", "insert_only":
				f.Immutable = true
			case "generated", "computed":
				f.Generated = true
			case "serverowned", "server_owned":
				f.ServerOwned = true
			case "tombstone", "softdelete", "soft_delete":
				f.ServerOwned = true
				f.Tombstone = true
			case "version", "lock":
				f.Version = true
			case "":
			default:
				return &SchemaError{Model: s.Type.String(), Field: sf.Name, Reason: "unknown tag option " + o}
			}
		}
		if _, dup := s.byName[f.Name]; dup {
			return &SchemaError{Model: s.Type.String(), Field: f.Name, Reason: "duplicate field name"}
		}
		if _, dup := s.byCol[f.Column]; dup {
			return &SchemaError{Model: s.Type.String(), Field: f.Name, Reason: "duplicate column " + f.Column}
		}
		if f.PK {
			if s.PK != nil {
				return &SchemaError{Model: s.Type.String(), Field: f.Name, Reason: "composite primary keys are not supported"}
			}
			if !f.Auto && !f.noAutoOptOut && isIntKind(f.Type.Kind()) {
				f.Auto = true
			}
			s.PK = f
		}
		f.Ordinal = len(s.Fields)
		s.Fields = append(s.Fields, f)
		s.byName[f.Name] = f
		s.byCol[f.Column] = f
	}
	return nil
}

// Meta binds a schema to a physical table.
type Meta struct {
	*Schema
	// Table is the conventional diagnostic spelling kept for compatibility.
	// SQL and relation identity use the private structured reference, so callers
	// cannot retarget validated metadata by mutating an exported field.
	Table string

	tableRef TableRef

	relations *relationContext
}

// NewMeta binds M to a table. An empty name asks the model first, through
// TableName(), then falls back to the snake_case plural of its type name. The
// string form accepts exactly one identifier component; use NewMetaInSchema or
// NewMetaRef for a qualified table.
func NewMeta[M any](table string) (*Meta, error) {
	s, err := SchemaOf[M]()
	if err != nil {
		return nil, err
	}
	if table == "" {
		ref, err := TableRefOf(s.Type)
		if err != nil {
			return nil, tableRefSchemaError(s.Type, err)
		}
		return bindMeta(s, ref), nil
	}
	ref, err := NewTableRef(table)
	if err != nil {
		return nil, tableRefSchemaError(s.Type, err)
	}
	return bindMeta(s, ref), nil
}

// NewMetaInSchema binds M to a qualified physical table. Schema is the first
// identifier component: a PostgreSQL schema, MySQL database, or SQLite attached
// database.
func NewMetaInSchema[M any](schema, table string) (*Meta, error) {
	ref, err := NewTableRefInSchema(schema, table)
	if err != nil {
		var zero M
		return nil, tableRefSchemaError(reflect.TypeOf(&zero).Elem(), err)
	}
	return NewMetaRef[M](ref)
}

// NewMetaRef is the low-level structured form of NewMeta. It never interprets
// dots inside a component.
func NewMetaRef[M any](table TableRef) (*Meta, error) {
	s, err := SchemaOf[M]()
	if err != nil {
		return nil, err
	}
	if err := table.Validate(); err != nil {
		return nil, tableRefSchemaError(s.Type, err)
	}
	return bindMeta(s, table), nil
}

func bindMeta(s *Schema, table TableRef) *Meta {
	context := &relationContext{tables: map[reflect.Type]TableRef{s.Type: table}}
	return &Meta{Schema: s, Table: table.String(), tableRef: table, relations: context}
}

func tableRefSchemaError(model reflect.Type, err error) error {
	reason := strings.TrimPrefix(err.Error(), "crud: ")
	if strings.Contains(reason, "dotted string") {
		reason += "; use sqlrepo.DefineInSchema or crud.NewMetaInSchema at a declarative boundary"
	}
	return &SchemaError{Model: model.String(), Reason: reason}
}

// TableReference returns the structured physical identity. The fallback keeps
// manually-constructed legacy Meta values useful; metadata created by NewMeta
// always has TableRef populated and validated.
func (this *Meta) TableReference() TableRef {
	if this == nil {
		return TableRef{}
	}
	if this.tableRef.Name != "" || this.tableRef.Schema != "" {
		return this.tableRef
	}
	return TableRef{Name: this.Table}
}

// QuotedTable renders the validated physical table for d. It is the safe seam
// used by repository implementations that cache statement fragments.
func (this *Meta) QuotedTable(d Dialect) string {
	return quoteTable(d, this.TableReference())
}

func parseTag(tag string) (name string, options []string) {
	if tag == "" {
		return "", nil
	}
	parts := strings.Split(tag, ",")
	return strings.TrimSpace(parts[0]), parts[1:]
}

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// isScalarStruct reports whether a struct type is one column rather than
// something to flatten or navigate into: time.Time, sql.Null[T], crud.Opt[T],
// decimals — anything the driver already knows how to carry.
func isScalarStruct(t reflect.Type) bool {
	if t == timeType {
		return true // database/sql special-cases it; it implements nothing
	}
	pt := reflect.PointerTo(t)
	return t.Implements(valuerType) || pt.Implements(valuerType) ||
		pt.Implements(scannerType) ||
		t.Implements(textMarshalerType) || pt.Implements(textUnmarshalerType)
}

var (
	timeType            = reflect.TypeOf(time.Time{})
	textMarshalerType   = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
	valuerType          = reflect.TypeOf((*driver.Valuer)(nil)).Elem()
	scannerType         = reflect.TypeOf((*sql.Scanner)(nil)).Elem()
)

// isOptType reports whether t is a utils.Opt[...]. Its concrete identity is
// still protected by utils' private marker, so an arbitrary Optional cannot
// accidentally alter CRUD persistence semantics.
func isOptType(t reflect.Type) bool {
	return utils.IsOptType(t)
}

// OptElem returns the element type of a utils.Opt[T], or nil.
func OptElem(t reflect.Type) reflect.Type {
	return utils.OptElem(t)
}

// ElemType strips Opt and pointer wrappers, giving the type a caller actually
// works with: Opt[int] and *int both report int.
func ElemType(t reflect.Type) reflect.Type {
	if e := OptElem(t); e != nil {
		return e
	}
	if t.Kind() == reflect.Pointer {
		return t.Elem()
	}
	return t
}

func isIntKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	}
	return false
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// snake converts a Go identifier to snake_case: UserID -> user_id,
// HTTPCode -> http_code, CreatedAt -> created_at.
func snake(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	rs := []rune(s)
	for i, r := range rs {
		if unicode.IsUpper(r) {
			prevLower := i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1]))
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if i > 0 && (prevLower || nextLower) {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func pluralise(s string) string {
	switch {
	case s == "":
		return s
	case strings.HasSuffix(s, "s"), strings.HasSuffix(s, "x"), strings.HasSuffix(s, "z"),
		strings.HasSuffix(s, "ch"), strings.HasSuffix(s, "sh"):
		return s + "es"
	case strings.HasSuffix(s, "y") && len(s) > 1 && !strings.ContainsRune("aeiou", rune(s[len(s)-2])):
		return s[:len(s)-1] + "ies"
	default:
		return s + "s"
	}
}
