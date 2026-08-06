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
)

// TagKey is the struct tag rx-crud reads. `db:"column,option,option"`.
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
	Name      string       // Go field name, as used by predicates and update DTOs
	Column    string       // SQL column name
	Type      reflect.Type // Go type of the field
	Offset    uintptr      // byte offset inside the model struct
	Ordinal   int          // index in Schema.Fields
	PK        bool
	Auto      bool
	Immutable bool
	Generated bool
	Version   bool // optimistic lock counter, owned by the repository
	Optional  bool // the field is a crud.Opt[...]

	noAutoOptOut bool // `noauto` was requested explicitly
}

// pointerTo returns a *T aimed at this field inside the model at base.
func (f *Field) pointerTo(base unsafe.Pointer) any {
	return reflect.NewAt(f.Type, unsafe.Add(base, f.Offset)).Interface()
}

// valueOf reads the field out of the model at base.
func (f *Field) valueOf(base unsafe.Pointer) any {
	return reflect.NewAt(f.Type, unsafe.Add(base, f.Offset)).Elem().Interface()
}

// comparable returns the field value normalised for diffing: nil for NULL,
// the bare element otherwise.
func (f *Field) comparableOf(base unsafe.Pointer) any {
	v := reflect.NewAt(f.Type, unsafe.Add(base, f.Offset)).Elem()
	if f.Optional {
		o := v.Interface().(optional)
		if !o.optDefined() || o.optNull() {
			return nil
		}
		return o.optValue()
	}
	if f.Type.Kind() == reflect.Pointer {
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
func (s *Schema) Field(ref string) *Field {
	if f, ok := s.byName[ref]; ok {
		return f
	}
	if f, ok := s.byCol[ref]; ok {
		return f
	}
	if f, ok := s.byFold[fold(ref)]; ok && f != ambiguousField {
		return f
	}
	return nil
}

// Relation resolves an edge the same way, so `author.name` and `Author.Name`
// are the same path.
func (s *Schema) Relation(ref string) *Relation {
	if r, ok := s.byRel[ref]; ok {
		return r
	}
	if r, ok := s.byRelFold[fold(ref)]; ok && r != ambiguousRel {
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

func (s *Schema) addFold(key string, f *Field) {
	if prev, ok := s.byFold[key]; ok && prev != f {
		s.byFold[key] = ambiguousField
		return
	}
	s.byFold[key] = f
}

func (s *Schema) addRelFold(key string, r *Relation) {
	if prev, ok := s.byRelFold[key]; ok && prev != r {
		s.byRelFold[key] = ambiguousRel
		return
	}
	s.byRelFold[key] = r
}

// Columns returns every mapped column, primary key first.
func (s *Schema) Columns() []string {
	cols := make([]string, len(s.Fields))
	for i, f := range s.Fields {
		cols[i] = f.Column
	}
	return cols
}

var schemaCache sync.Map // reflect.Type -> *Schema (or error)

type schemaResult struct {
	s   *Schema
	err error
}

// SchemaOf reflects over M once and caches the result.
func SchemaOf[M any]() (*Schema, error) {
	var zero M
	t := reflect.TypeOf(&zero).Elem()
	if v, ok := schemaCache.Load(t); ok {
		r := v.(schemaResult)
		return r.s, r.err
	}
	s, err := buildSchema(t)
	schemaCache.Store(t, schemaResult{s, err})
	return s, err
}

// schemaOfType is SchemaOf for a reflect.Type; relations resolve their target
// through it, lazily, so two models may reference each other.
func schemaOfType(t reflect.Type) (*Schema, error) {
	if v, ok := schemaCache.Load(t); ok {
		r := v.(schemaResult)
		return r.s, r.err
	}
	s, err := buildSchema(t)
	schemaCache.Store(t, schemaResult{s, err})
	return s, err
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
		if f.Version {
			if err := checkVersion(t, s, f); err != nil {
				return nil, err
			}
			s.Version = f
		}
		if f.Generated {
			s.HasGen = true
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
		return deny("`version` and `generated` contradict each other: the lock is written by rx-crud, not read back from a default")
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
		if tag == "-" {
			continue
		}
		if sf.Anonymous && !hasTag && deref(sf.Type).Kind() == reflect.Struct && !isOptType(sf.Type) && !isScalarStruct(sf.Type) {
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
		relTag, hasRel := sf.Tag.Lookup(RelTagKey)
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

		name, opts := parseTag(tag)
		f := &Field{
			Name:   sf.Name,
			Column: cmpOr(name, snake(sf.Name)),
			Type:   sf.Type,
			Offset: base + sf.Offset,
		}
		f.Optional = isOptType(sf.Type)
		for _, o := range opts {
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

// Meta binds a schema to a table.
type Meta struct {
	*Schema
	Table string
}

// NewMeta binds M to a table. An empty table name falls back to the snake_case
// plural of the type name.
func NewMeta[M any](table string) (*Meta, error) {
	s, err := SchemaOf[M]()
	if err != nil {
		return nil, err
	}
	if table == "" {
		table = pluralise(snake(s.Name))
	}
	return &Meta{Schema: s, Table: table}, nil
}

func parseTag(tag string) (name string, opts []string) {
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
	optionalTyp         = reflect.TypeOf((*optional)(nil)).Elem()
)

// isOptType reports whether t is a crud.Opt[...]. `optional` has unexported
// methods, so nothing outside this package can pretend to be one.
func isOptType(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t.Implements(optionalTyp)
}

// OptElem returns the element type of a crud.Opt[T], or nil.
func OptElem(t reflect.Type) reflect.Type {
	if !isOptType(t) {
		return nil
	}
	f, ok := t.FieldByName("value")
	if !ok {
		return nil
	}
	return f.Type
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
