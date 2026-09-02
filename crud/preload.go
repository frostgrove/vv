package crud

import (
	"context"
	"database/sql/driver"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"
	"unsafe"
)

const DefaultPreloadDepth = 5

const preloadBatch = 900

type PreloadSpec struct {
	Path string
	Opts []Option

	MaxRows int
}

func Preload(paths ...string) Option {
	return func(o *Options) {
		for _, p := range paths {
			if p = strings.TrimSpace(p); p != "" {
				o.Preloads = append(o.Preloads, PreloadSpec{Path: p})
			}
		}
	}
}

func PreloadWhere(path string, options ...Option) Option {
	return func(o *Options) {
		o.Preloads = append(o.Preloads, PreloadSpec{Path: path, Opts: options})
	}
}

func PreloadCap(path string, maxRows int, options ...Option) Option {
	return func(o *Options) {
		o.Preloads = append(o.Preloads, PreloadSpec{Path: path, Opts: options, MaxRows: maxRows})
	}
}

type preloadNode struct {
	name     string
	options  *Options
	children []*preloadNode
	whole    bool
	maxRows  int
}

func (this *preloadNode) child(name string) *preloadNode {
	for _, c := range this.children {
		if c.name == name {
			return c
		}
	}
	c := &preloadNode{name: name}
	this.children = append(this.children, c)
	return c
}

func buildPreloadTree(meta *Meta, specs []PreloadSpec, maxDepth int) (*preloadNode, error) {
	root := &preloadNode{}
	for _, spec := range specs {
		if spec.MaxRows < 0 {
			return nil, &SchemaError{Model: meta.Name, Field: spec.Path, Reason: "a preload row cap cannot be negative"}
		}
		segs := strings.Split(spec.Path, ".")
		if len(segs) > maxDepth {
			return nil, &SchemaError{Reason: "preload path " + spec.Path + " is deeper than the allowed " +
				itoa(maxDepth) + " levels"}
		}
		for i := range segs {
			segs[i] = strings.TrimSpace(segs[i])
			if segs[i] == "" {
				return nil, &SchemaError{Reason: "empty segment in preload path " + spec.Path}
			}
		}
		canonical, err := meta.ValidateRelationPath(strings.Join(segs, "."))
		if err != nil {
			return nil, err
		}
		segs = strings.Split(canonical, ".")

		var resolved *Options
		if len(spec.Opts) > 0 {
			resolved, err = buildPreloadOptions(meta.Name, canonical, spec.Opts...)
			if err != nil {
				return nil, err
			}
		}
		maxRows := spec.MaxRows
		if resolved != nil && resolved.PreloadRows > 0 && (maxRows == 0 || resolved.PreloadRows < maxRows) {
			maxRows = resolved.PreloadRows
		}

		cur := root
		for i, seg := range segs {
			cur = cur.child(seg)
			if i < len(segs)-1 {
				cur.whole = true
			}
			if maxRows > 0 && (cur.maxRows == 0 || maxRows < cur.maxRows) {
				cur.maxRows = maxRows
			}
		}
		narrowed := false
		if resolved != nil && len(resolved.Filter) > 0 {
			target, err := preloadTargetMeta(meta, segs)
			if err != nil {
				return nil, err
			}
			narrowed = !IsTautologyFor(target, resolved.Predicate())
		}

		if !narrowed {
			cur.whole = true
		}
		if resolved != nil {
			if cur.options == nil {
				cur.options = &Options{}
			}
			cur.options.Filter = append(cur.options.Filter, resolved.Filter...)
			cur.options.Sort = append(cur.options.Sort, resolved.Sort...)
		}
	}
	clearWholePreloadFilters(root)
	return root, nil
}

func preloadTargetMeta(root *Meta, canonicalSegments []string) (*Meta, error) {
	schema := root.Schema
	for _, segment := range canonicalSegments {
		relation := schema.Relation(segment)
		if relation == nil {
			return nil, &UnknownFieldError{Model: schema.Name, Field: strings.Join(canonicalSegments, ".")}
		}
		var err error
		schema, err = schemaOfType(relation.Elem)
		if err != nil {
			return nil, err
		}
	}
	return &Meta{Schema: schema}, nil
}

func clearWholePreloadFilters(node *preloadNode) {
	for _, child := range node.children {
		if child.whole && child.options != nil {
			child.options.Filter = nil
		}
		clearWholePreloadFilters(child)
	}
}

func BuildPreloadOptions(path string, options ...Option) (*Options, error) {
	return buildPreloadOptions("preload", path, options...)
}

func buildPreloadOptions(model, path string, options ...Option) (*Options, error) {
	o := Build(options...)
	if err := validatePreloadOptions(model, path, o); err != nil {
		return nil, err
	}
	return o, nil
}

func validatePreloadOptions(model, path string, o *Options) error {
	if o.PreloadRows < 0 {
		return &SchemaError{Model: model, Field: path, Reason: "a preload row cap cannot be negative"}
	}
	if _, reason := PreloadOptions.refused(o); reason != "" {
		return &SchemaError{Model: model, Field: path, Reason: reason}
	}
	return nil
}

func unsupportedPreloadOption(field string) string {
	switch field {
	case "Page", "Limit", "Offset", "Unpaged":
		return "a preload cannot be paginated; it is loaded for every parent at once"
	case "After", "Before":
		return "a preload cannot use a cursor; it is loaded for every parent at once"
	case "Fields":
		return "a preload cannot change its projection; related models are scanned as complete rows"
	case "Preloads":
		return "a preload option cannot declare nested preloads; use one dotted top-level preload path"
	case "RelScopes":
		return "relation scopes inside a preload option are not supported; narrow relations on the containing query"
	case "Agg":
		return "a preload cannot be an aggregate query"
	case "Primary":
		return "a preload cannot select its own datasource; force the containing read onto primary"
	case "NoSort":
		return "a preload cannot disable deterministic relation ordering"
	case "NoTotal":
		return "a preload has no separate total query to skip"
	case "ForUpdate":
		return "a preload cannot lock related rows"
	case "Distinct":
		return "a preload cannot apply DISTINCT to complete related rows"
	}
	return ""
}

func RunPreloads(ctx context.Context, ex Executor, d Dialect, m *Meta, items any, specs []PreloadSpec, maxDepth int, scopes *RelationScopes) error {
	if len(specs) == 0 {
		return nil
	}
	if maxDepth <= 0 {
		maxDepth = DefaultPreloadDepth
	}
	tree, err := buildPreloadTree(m, specs, maxDepth)
	if err != nil {
		return err
	}
	rows := addressableRows(reflect.ValueOf(items))
	if len(rows) == 0 {
		return nil
	}
	p := &preloader{ctx: ctx, ex: ex, d: d, scopes: scopes}
	return p.level(m, rows, tree.children, "")
}

func addressableRows(v reflect.Value) []reflect.Value {
	if !v.IsValid() || v.Kind() != reflect.Slice {
		return nil
	}
	out := make([]reflect.Value, 0, v.Len())
	for i := range v.Len() {
		e := v.Index(i)
		if e.Kind() == reflect.Pointer {
			if e.IsNil() {
				continue
			}
			out = append(out, e)
			continue
		}
		out = append(out, e.Addr())
	}
	return out
}

type preloader struct {
	ctx    context.Context
	ex     Executor
	d      Dialect
	scopes *RelationScopes
}

func (this *preloader) level(m *Meta, parents []reflect.Value, nodes []*preloadNode, prefix string) error {
	for _, node := range nodes {
		rel := m.Relation(node.name)
		if rel == nil {
			return &UnknownFieldError{Model: m.Name, Field: node.name}
		}
		path := joinPath(prefix, rel.Name)
		children, err := this.load(m, rel, parents, node.options, node.maxRows, path)
		if err != nil {
			return err
		}
		if len(node.children) == 0 || len(children) == 0 {
			continue
		}
		target, _, _, err := rel.Resolve()
		if err != nil {
			return err
		}
		if err := this.level(target, children, node.children, path); err != nil {
			return err
		}
	}
	return nil
}

func (this *preloader) load(m *Meta, rel *Relation, parents []reflect.Value, options *Options, maxRows int, path string) ([]reflect.Value, error) {
	target, local, remote, err := rel.Resolve()
	if err != nil {
		return nil, err
	}
	o := &Options{}
	if options != nil {
		*o = *options
	}
	if maxRows > 0 && (o.PreloadRows == 0 || maxRows < o.PreloadRows) {
		o.PreloadRows = maxRows
	}

	keys := make([]any, 0, len(parents))
	seen := make(map[any]struct{}, len(parents))
	parentKeys := make([]any, len(parents))
	parentHasKey := make([]bool, len(parents))
	for i, parent := range parents {
		v := local.valueOf(parent.UnsafePointer())
		if v == nil {
			continue
		}
		canonical, err := canonicalPreloadKey(m.Name, local, v)
		if err != nil {
			return nil, err
		}
		if canonical.key == nil {
			continue
		}
		parentKeys[i], parentHasKey[i] = canonical.key, true
		if _, dup := seen[canonical.key]; dup {
			continue
		}
		seen[canonical.key] = struct{}{}
		keys = append(keys, canonical.bind)
	}
	if len(keys) == 0 {
		this.assignEmpty(rel, parents)
		return nil, nil
	}

	index := make(map[any][]reflect.Value, len(keys))
	total := 0
	for chunk := range slices(keys, preloadBatch) {
		remaining := -1
		if o.PreloadRows > 0 {
			remaining = o.PreloadRows - total
		}
		rows, owners, err := this.fetch(target, rel, local, remote, chunk, o, path, remaining)
		if err != nil {
			return nil, err
		}
		for i, row := range rows {
			index[owners[i]] = append(index[owners[i]], row)
			total++
		}
	}

	stored := make([]reflect.Value, 0, total)
	for i, parent := range parents {
		var kids []reflect.Value
		if parentHasKey[i] {
			kids = index[parentKeys[i]]
		}
		placed, err := assignRelation(rel, parent, kids)
		if err != nil {
			return nil, err
		}
		stored = append(stored, placed...)
	}
	return stored, nil
}

func (this *preloader) assignEmpty(rel *Relation, parents []reflect.Value) {
	for _, parent := range parents {
		_, _ = assignRelation(rel, parent, nil)
	}
}

func (this *preloader) fetch(target *Meta, rel *Relation, local, remote *Field, keys []any, o *Options, path string, remaining int) ([]reflect.Value, []any, error) {
	var (
		b       *SQL
		ownerAt = -1
	)

	extra := this.scopes.At(path, target)
	if sub := o.Predicate(); sub != nil {
		extra = And(extra, sub)
	}

	if rel.Kind == ManyToMany {
		b = NewSQL(this.d, target).Alias("rxt").RelationScopes(this.scopes.under(path))
		b.Raw("SELECT rxj.").Ident(rel.JoinLocal).Raw(", ")
		for i, f := range target.Fields {
			if i > 0 {
				b.Raw(", ")
			}
			b.Raw("rxt.").Ident(f.Column)
		}
		b.Raw(" FROM ").TableRef(target.TableReference()).Raw(" AS rxt JOIN ").TableRef(rel.JoinTableReference()).Raw(" AS rxj ON rxj.").
			Ident(rel.JoinRef).Raw(" = rxt.").Ident(remote.Column).
			Raw(" WHERE rxj.").Ident(rel.JoinLocal).Raw(" IN (").Binds(keys).Raw(")")
		if extra != nil {
			b.Raw(" AND ")
			b.Predicate(extra)
		}
		ownerAt = 0
	} else {
		b = NewSQL(this.d, target).RelationScopes(this.scopes.under(path))
		b.Raw("SELECT ").Columns(target.Fields).Raw(" FROM ").Table().
			Raw(" WHERE ").Ident(remote.Column).Raw(" IN (").Binds(keys).Raw(")")
		if extra != nil {
			b.Raw(" AND ")
			b.Predicate(extra)
		}
	}
	sort := o.Sort
	if len(sort) == 0 && rel.Kind == HasOne {
		sort = []Order{Asc(target.PK.Name)}
	}
	b.OrderBy(sort)

	if remaining >= 0 {
		limit := remaining
		if limit < math.MaxInt {
			limit++
		}
		b.LimitOffset(limit, 0)
	}

	q, args, err := b.Done()
	if err != nil {
		return nil, nil, err
	}
	rows, err := this.ex.Query(this.ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var (
		out    []reflect.Value
		owners []any
	)
	for rows.Next() {
		if remaining >= 0 && len(out) >= remaining {
			return nil, nil, &SchemaError{Model: target.Name, Field: path,
				Reason: "preload exceeds the configured row limit"}
		}
		child := reflect.New(target.Type)
		dest, err := target.Pointers(child.Interface(), target.Fields)
		if err != nil {
			return nil, nil, err
		}
		var ownerKey reflect.Value
		if ownerAt >= 0 {
			ownerKey = reflect.New(local.Type)
			dest = append([]any{ownerKey.Interface()}, dest...)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, nil, err
		}
		out = append(out, child)
		if ownerAt >= 0 {
			key, err := preloadMapKey(rel.Owner.Name, local, ownerKey.Elem().Interface())
			if err != nil {
				return nil, nil, err
			}
			owners = append(owners, key)
		} else {
			key, err := preloadMapKey(target.Name, remote, remote.valueOf(child.UnsafePointer()))
			if err != nil {
				return nil, nil, err
			}
			owners = append(owners, key)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return out, owners, nil
}

func assignRelation(rel *Relation, parent reflect.Value, kids []reflect.Value) ([]reflect.Value, error) {
	destination := rel.fieldValue(parent.UnsafePointer())

	if rel.Kind.ToMany() {
		ptrElem := rel.Type.Elem().Kind() == reflect.Pointer
		slice := reflect.MakeSlice(rel.Type, 0, len(kids))
		for _, k := range kids {
			if ptrElem {
				slice = reflect.Append(slice, k)
			} else {
				slice = reflect.Append(slice, k.Elem())
			}
		}
		destination.Set(slice)
		if ptrElem {
			return kids, nil
		}
		placed := make([]reflect.Value, destination.Len())
		for i := range placed {
			placed[i] = destination.Index(i).Addr()
		}
		return placed, nil
	}

	if len(kids) == 0 {
		destination.SetZero()
		return nil, nil
	}
	if rel.Type.Kind() == reflect.Pointer {
		own := reflect.New(rel.Type.Elem())
		own.Elem().Set(kids[0].Elem())
		destination.Set(own)
		return []reflect.Value{own}, nil
	}
	destination.Set(kids[0].Elem())
	return []reflect.Value{destination.Addr()}, nil
}

type canonicalRelationKey struct {
	key  any
	bind any
}

func canonicalPreloadKey(model string, field *Field, value any) (canonicalRelationKey, error) {
	value = unwrapRelationKeyValue(relationKeyValue(value))
	if value == nil {
		return canonicalRelationKey{}, nil
	}
	resolved, used, err := relationDriverValue(value)
	if err != nil {
		return canonicalRelationKey{}, preloadKeyRuntimeError(model, field, err)
	}
	if used {
		value = resolved
		if value == nil {
			return canonicalRelationKey{}, nil
		}
	}
	rv := reflect.ValueOf(value)
	if nilableKind(rv.Kind()) && rv.IsNil() {
		return canonicalRelationKey{}, nil
	}
	bind := value
	if isByteSliceType(rv.Type()) {
		bytes := make([]byte, rv.Len())
		copy(bytes, rv.Bytes())
		return canonicalRelationKey{key: string(bytes), bind: bytes}, nil
	}
	key := value
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		key = rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if u := rv.Uint(); u <= math.MaxInt64 {
			key = int64(u)
		} else {
			key = rv.Uint()
		}
	case reflect.String:
		key = rv.String()
	case reflect.Bool:
		key = rv.Bool()
	case reflect.Float32, reflect.Float64:
		value := rv.Float()

		if value == 0 {
			value = 0
		} else if math.IsNaN(value) {
			return canonicalRelationKey{}, preloadKeyRuntimeError(model, field,
				fmt.Errorf("NaN relation keys do not have portable SQL equality"))
		}
		key = preloadFloatKey(math.Float64bits(value))
	}

	if !rv.Comparable() {
		return canonicalRelationKey{}, preloadKeyRuntimeError(model, field, fmt.Errorf(
			"relation key value of type %T is not comparable; use []byte or a driver.Valuer with a stable scalar value", value))
	}
	return canonicalRelationKey{key: key, bind: bind}, nil
}

type preloadFloatKey uint64

func preloadMapKey(model string, field *Field, value any) (any, error) {
	canonical, err := canonicalPreloadKey(model, field, value)
	if err != nil {
		return nil, err
	}
	return canonical.key, nil
}

func unwrapRelationKeyValue(value any) any {
	if value == nil {
		return nil
	}
	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Pointer {
		if rv.Type().Implements(valuerType) {
			if rv.IsNil() && rv.Type().Elem().Implements(valuerType) {
				return nil
			}
			return rv.Interface()
		}
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	return rv.Interface()
}

func nilableKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return true
	default:
		return false
	}
}

func validRelationDriverValue(value any) bool {
	if value == nil {
		return true
	}
	switch value.(type) {
	case []byte, bool, float64, int64, string, time.Time:
		return true
	default:
		return false
	}
}

func relationDriverValue(value any) (resolved any, used bool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			resolved, used, err = nil, true, fmt.Errorf("driver.Valuer panicked while canonicalising the relation key")
		}
	}()
	rv := reflect.ValueOf(value)
	valuer, ok := value.(driver.Valuer)
	if ok && rv.Kind() == reflect.Pointer && rv.IsNil() && rv.Type().Elem().Implements(valuerType) {
		return nil, true, nil
	}
	if !ok && rv.IsValid() && rv.Kind() != reflect.Pointer {
		copy := reflect.New(rv.Type())
		copy.Elem().Set(rv)
		valuer, ok = copy.Interface().(driver.Valuer)
	}
	if !ok && rv.IsValid() && nilableKind(rv.Kind()) && rv.IsNil() {
		return nil, false, nil
	}
	if !ok {
		return value, false, nil
	}
	resolved, err = valuer.Value()
	if err != nil {
		return nil, true, fmt.Errorf("driver.Valuer failed while canonicalising the relation key: %w", err)
	}
	if !validRelationDriverValue(resolved) {
		return nil, true, fmt.Errorf("driver.Valuer returned unsupported relation key type %T", resolved)
	}
	return resolved, true, nil
}

func preloadKeyRuntimeError(model string, field *Field, err error) error {
	name := "relation key"
	if field != nil {
		name = field.Name
	}
	return fmt.Errorf("crud: canonicalise relation key %s.%s: %w", model, name, err)
}

func slices[T any](s []T, n int) func(func([]T) bool) {
	return func(yield func([]T) bool) {
		for len(s) > 0 {
			k := min(n, len(s))
			if !yield(s[:k]) {
				return
			}
			s = s[k:]
		}
	}
}

var _ = unsafe.Pointer(nil)
