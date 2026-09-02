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

var NowFunc = time.Now

const TagKey = "db"

type Field struct {
	Name        string
	Column      string
	Type        reflect.Type
	Offset      uintptr
	Ordinal     int
	PK          bool
	Auto        bool
	Immutable   bool
	Generated   bool
	ServerOwned bool
	Tombstone   bool
	Secret      bool
	Version     bool
	Optional    bool

	noAutoOptOut bool
}

func (this *Field) Nullable() bool { return nullableField(this) }

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

func (this *Field) pointerTo(base unsafe.Pointer) any {
	return reflect.NewAt(this.Type, unsafe.Add(base, this.Offset)).Interface()
}

func (this *Field) valueOf(base unsafe.Pointer) any {
	return reflect.NewAt(this.Type, unsafe.Add(base, this.Offset)).Elem().Interface()
}

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

type Schema struct {
	Type   reflect.Type
	Name   string
	Fields []*Field
	PK     *Field

	Insert    []*Field
	InsertGen []*Field
	Update    []*Field
	HasGen    bool
	Version   *Field
	Tombstone *Field

	Relations []*Relation

	byName    map[string]*Field
	byCol     map[string]*Field
	byRel     map[string]*Relation
	byFold    map[string]*Field
	byRelFold map[string]*Relation
}

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

func (this *Schema) Relation(ref string) *Relation {
	if r, ok := this.byRel[ref]; ok {
		return r
	}
	if r, ok := this.byRelFold[fold(ref)]; ok && r != ambiguousRel {
		return r
	}
	return nil
}

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

func (this *Schema) Columns() []string {
	cols := make([]string, len(this.Fields))
	for i, f := range this.Fields {
		cols[i] = f.Column
	}
	return cols
}

var schemaCache sync.Map

type schemaResult struct {
	s   *Schema
	err error
}

type schemaFuture struct {
	done chan struct{}
	schemaResult
}

func SchemaOf[M any]() (*Schema, error) {
	var zero M
	t := reflect.TypeOf(&zero).Elem()
	return schemaOfType(t)
}

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

func SchemaOfType(t reflect.Type) (*Schema, error) { return schemaOfType(t) }

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

	return reflect.PointerTo(f.Type).Implements(scannerType) &&
		(f.Type.Implements(valuerType) || reflect.PointerTo(f.Type).Implements(valuerType))
}

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
			if hasTag {
				return &SchemaError{Model: s.Type.String(), Field: sf.Name,
					Reason: `unexported fields cannot be mapped; rename it or tag it db:"-"`}
			}
			continue
		}

		if _, _, candidate := relCandidate(sf.Type); candidate {
			if !hasRel || relTag == "-" {
				continue
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
			case "secret":
				f.Secret = true
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

type Meta struct {
	*Schema

	Table string

	tableRef TableRef

	relations *relationContext
}

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

func NewMetaInSchema[M any](schema, table string) (*Meta, error) {
	ref, err := NewTableRefInSchema(schema, table)
	if err != nil {
		var zero M
		return nil, tableRefSchemaError(reflect.TypeOf(&zero).Elem(), err)
	}
	return NewMetaRef[M](ref)
}

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

func (this *Meta) TableReference() TableRef {
	if this == nil {
		return TableRef{}
	}
	if this.tableRef.Name != "" || this.tableRef.Schema != "" {
		return this.tableRef
	}
	return TableRef{Name: this.Table}
}

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

func isScalarStruct(t reflect.Type) bool {
	if t == timeType {
		return true
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

func isOptType(t reflect.Type) bool {
	return utils.IsOptType(t)
}

func OptElem(t reflect.Type) reflect.Type {
	return utils.OptElem(t)
}

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
