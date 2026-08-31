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

// DefaultPreloadDepth caps how deep a preload path may go. It exists because
// preload paths usually arrive from an HTTP client, and `a.b.a.b.a.b…` should
// not be able to turn one request into a dozen queries.
const DefaultPreloadDepth = 5

// preloadBatch is how many parent keys go into one IN (...) list.
const preloadBatch = 900

// PreloadSpec is one requested relation path, optionally narrowed.
type PreloadSpec struct {
	Path string   // "Comments" or "Comments.Author"
	Opts []Option // extra Where/OrderBy applied to the related query

	// MaxRows caps every relation hop in Path. It is separate from Opts so a
	// request for Comments.Author cannot leave the potentially large Comments
	// hop uncapped while only limiting Author. Zero leaves the direct repository
	// preload uncapped.
	MaxRows int
}

// Preload loads the named relations after the main query. Paths may be nested:
//
//	crud.Preload("Author", "Comments.Author")
//
// Each relation is fetched in one batched statement per level, never per row.
func Preload(paths ...string) Option {
	return func(o *Options) {
		for _, p := range paths {
			if p = strings.TrimSpace(p); p != "" {
				o.Preloads = append(o.Preloads, PreloadSpec{Path: p})
			}
		}
	}
}

// PreloadWhere loads a relation narrowed by extra options. Pagination options
// are refused here: a LIMIT on a batched preload would silently truncate some
// parents' children and not others.
func PreloadWhere(path string, options ...Option) Option {
	return func(o *Options) {
		o.Preloads = append(o.Preloads, PreloadSpec{Path: path, Opts: options})
	}
}

// PreloadCap is PreloadWhere with a materialisation ceiling. The cap applies
// to every hop in a nested path: PreloadCap("Comments.Author", 100) refuses a
// 101st Comment before it can make the nested Author query unbounded by the
// parent relation. A cap is a refusal rather than pagination, so no parent is
// handed a silently partial relation.
func PreloadCap(path string, maxRows int, options ...Option) Option {
	return func(o *Options) {
		o.Preloads = append(o.Preloads, PreloadSpec{Path: path, Opts: options, MaxRows: maxRows})
	}
}

// ---------------------------------------------------------------------------
// the tree

type preloadNode struct {
	name     string
	options  []Option
	children []*preloadNode
	whole    bool // some spec asked for this relation unnarrowed
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

// buildPreloadTree folds the flat paths into a tree, so "Comments" and
// "Comments.Author" share a single query for the comments.
func buildPreloadTree(specs []PreloadSpec, maxDepth int) (*preloadNode, error) {
	root := &preloadNode{}
	for _, spec := range specs {
		segs := strings.Split(spec.Path, ".")
		if len(segs) > maxDepth {
			return nil, &SchemaError{Reason: "preload path " + spec.Path + " is deeper than the allowed " +
				itoa(maxDepth) + " levels"}
		}
		cur := root
		for _, seg := range segs {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				return nil, &SchemaError{Reason: "empty segment in preload path " + spec.Path}
			}
			cur = cur.child(seg)
			if spec.MaxRows > 0 && (cur.maxRows == 0 || spec.MaxRows < cur.maxRows) {
				cur.maxRows = spec.MaxRows
			}
		}
		// Folding two requests for the same path into one query is what makes
		// "Comments" and "Comments.Author" share a statement. Folding their
		// narrowings together is a different thing: a request that asked for
		// all of them and for a subset would receive only the subset, with a
		// 200 and no way to tell. The wider ask wins.
		if len(spec.Opts) == 0 {
			cur.whole, cur.options = true, nil
			continue
		}
		if cur.whole {
			continue
		}
		cur.options = append(cur.options, spec.Opts...)
	}
	return root, nil
}

// ---------------------------------------------------------------------------
// execution

// RunPreloads fills the relation fields of items (a []M or []*M) according to
// specs. It issues one query per relation per level, batching parent keys.
//
// scopes narrows the related tables; it may be nil, and then a preload reads
// its table raw — which is the whole reason a repository has to pass its own.
func RunPreloads(ctx context.Context, ex Executor, d Dialect, m *Meta, items any, specs []PreloadSpec, maxDepth int, scopes *RelationScopes) error {
	if len(specs) == 0 {
		return nil
	}
	if maxDepth <= 0 {
		maxDepth = DefaultPreloadDepth
	}
	tree, err := buildPreloadTree(specs, maxDepth)
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

// addressableRows turns a []M or []*M into pointers to each element's struct.
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

// load fetches one relation for a whole set of parents and wires the results
// into their fields, returning the loaded children so nested preloads can
// continue from them.
func (this *preloader) load(m *Meta, rel *Relation, parents []reflect.Value, options []Option, maxRows int, path string) ([]reflect.Value, error) {
	target, local, remote, err := rel.Resolve()
	if err != nil {
		return nil, err
	}
	o := Build(options...)
	if maxRows > 0 && (o.PreloadRows == 0 || maxRows < o.PreloadRows) {
		o.PreloadRows = maxRows
	}
	if o.Limit != 0 || o.Page != 0 || o.Offset != 0 || o.Unpaged {
		return nil, &SchemaError{Model: m.Name, Field: rel.Name,
			Reason: "a preload cannot be paginated; it is loaded for every parent at once"}
	}

	// Collect the distinct parent keys.
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
		if canonical.key == nil { // a Scanner/Valuer NULL cannot own a related row.
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

	// index maps a parent key to the children that belong to it.
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

	// Collect the children *as stored*, not the temporaries: a []T relation
	// copies each row into the parent's slice, and a nested preload has to
	// write into that copy or it writes into nothing.
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

// fetch runs one batched SELECT and returns the scanned children together with
// the parent key each one belongs to.
func (this *preloader) fetch(target *Meta, rel *Relation, local, remote *Field, keys []any, o *Options, path string, remaining int) ([]reflect.Value, []any, error) {
	var (
		b       *SQL
		ownerAt = -1 // index of the owner-key column in the result, -1 = derive from the row
	)
	// The narrowing the parent's repository declared for this relation. It is
	// ANDed in here, not left to the caller: a preload is a second statement
	// against a second table, so the parent query's WHERE does nothing for it.
	extra := this.scopes.At(path, target)
	if sub := o.Predicate(); sub != nil {
		extra = And(extra, sub)
	}

	if rel.Kind == ManyToMany {
		// SELECT j.<owner>, t.<cols> FROM target t JOIN join j ON j.ref = t.pk
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
		// A has_one promises at most one row, and only a unique index on the
		// foreign key can keep that promise. When the schema does not, the
		// statement matches several rows and the first one wins — so without an
		// ORDER BY the winner is whichever row the engine felt like returning,
		// and it can differ between two runs of the same query. The schema is
		// what is wrong there, but the answer still has to be the same twice.
		sort = []Order{Asc(target.PK.Name)}
	}
	b.OrderBy(sort)
	// Fetch one row beyond the remaining budget. It is enough to make the
	// failure exact while keeping the driver from materialising an unbounded
	// child table merely to discover the cap was crossed.
	if remaining >= 0 {
		b.LimitOffset(remaining+1, 0)
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
			// The join table's owner column holds the parent's key, so it has
			// to be read as the parent's type: the index is looked up with the
			// parent's own value, and int32(1) does not equal int64(1).
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

// assignRelation writes the loaded children into the parent's relation field,
// adapting to whether it is declared as T, *T, []T or []*T. It returns pointers
// to the children *where they now live*, which is what a nested preload has to
// keep filling.
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
		// Every parent gets its own copy. Two parents pointing at the same row
		// used to share one object, so a presenter that rewrote a field on one
		// rewrote it on all its siblings — and which spelling of the relation
		// field was used decided whether that happened, because the value form
		// below has always copied.
		own := reflect.New(rel.Type.Elem())
		own.Elem().Set(kids[0].Elem())
		destination.Set(own)
		return []reflect.Value{own}, nil
	}
	destination.Set(kids[0].Elem())
	return []reflect.Value{destination.Addr()}, nil
}

// preloadMapKey normalises a value so it can index a map, and — the part that earns
// its keep — so that the two ends of a relation agree on it. A parent's key and
// a child's foreign key are separate struct fields that need not share a Go
// type: int32(1) and int64(1) are different map keys but the same row, and a
// mismatch here is silent, because the children simply end up filed under a key
// no parent looks for. So every integer key is widened, every string kind is
// flattened, and byte slices become strings the way a text driver hands them
// over. Unsupported values fail rather than sharing reflect.Value.String's
// generic "<T Value>" spelling with every other value of the same type.
type canonicalRelationKey struct {
	key  any
	bind any
}

// canonicalPreloadKey resolves a custom driver value exactly once and derives
// both identities from that one immutable snapshot. A stateful Valuer must not
// be able to make the IN predicate ask for one value while the child index is
// keyed under another.
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
		// Snapshot before deriving either representation. A Valuer may return a
		// buffer it owns and mutate later; the driver argument and map identity
		// still have to describe the same observed value.
		bytes := make([]byte, rv.Len())
		copy(bytes, rv.Bytes())
		return canonicalRelationKey{key: string(bytes), bind: bytes}, nil
	}
	key := value
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		key = rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		// Signed and unsigned meet here too: a key that fits in an int64 is
		// keyed as one, and a key that does not could never have equalled a
		// signed one anyway.
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
		// SQL compares both zero spellings equal. NaN equality is dialect- and
		// column-dependent, so guessing one shared identity could attach a row
		// to the wrong parent; refuse that actual value instead.
		if value == 0 {
			value = 0 // canonicalise -0 to +0.
		} else if math.IsNaN(value) {
			return canonicalRelationKey{}, preloadKeyRuntimeError(model, field,
				fmt.Errorf("NaN relation keys do not have portable SQL equality"))
		}
		key = preloadFloatKey(math.Float64bits(value))
	}
	// Value.Comparable, unlike Type.Comparable, follows interface members. A
	// struct{ Part any } is statically comparable but is not a valid map key
	// when Part currently contains []byte.
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

// unwrapRelationKeyValue mirrors database/sql's pointer conversion but stops
// at a pointer that is itself a driver.Valuer. relationKeyValue has already
// unwrapped the framework's explicit Opt state; relation fields can still be
// **T or Opt[*T], and using the remaining pointer address as a map key would
// make separately scanned equal values look different.
func unwrapRelationKeyValue(value any) any {
	if value == nil {
		return nil
	}
	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Pointer {
		if rv.Type().Implements(valuerType) {
			// Match database/sql's nil-pointer exception for the wrapper Go
			// generates when a value-receiver Valuer is used through *T. A
			// genuinely pointer-only Valuer still gets to define nil itself.
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

// relationDriverValue supports both value and pointer-only Valuer methods. The
// latter receives an addressable copy because a field placed in an interface is
// not addressable. A broken custom implementation becomes a declaration/use
// error instead of a process panic in the preloader.
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

// slices yields consecutive chunks of at most n elements.
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
