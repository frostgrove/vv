package query

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/shardit-io/vv/crud"
)

// Error is a rejected query document. Everything in it is safe to hand back to
// the client: it names the path that was wrong and why, never internals.
type Error struct {
	Path   string
	Reason string
}

func (e *Error) Error() string {
	if e.Path == "" {
		return "query: " + e.Reason
	}
	return "query: " + e.Path + ": " + e.Reason
}

func errf(path, format string, args ...any) *Error {
	return &Error{Path: path, Reason: fmt.Sprintf(format, args...)}
}

// Config bounds what a client may ask for. The zero value allows anything the
// schema maps, with sane depth and size limits — tighten it per endpoint.
type Config struct {
	// MaxDepth limits nesting of and/or/not and the length of a field path.
	MaxDepth int
	// MaxConditions limits how many leaf comparisons one document may contain.
	MaxConditions int
	// MaxPreloads limits how many relations one request may pull in.
	MaxPreloads int

	// MaxInValues limits how many values one `in` or `notIn` list may carry.
	//
	// A list is charged as a single condition however long it is — `{"id":
	// {"in": [...]}}` is one comparison — so MaxConditions never sees it, and
	// every value becomes a bound parameter. PostgreSQL refuses a statement past
	// 65535 of them, so without this the honest 400 arrives from the driver, as
	// a 500, after the statement was built.
	MaxInValues int

	// MaxSort limits how many terms one sort may carry.
	//
	// A sort term that hops a relation renders as a correlated scalar subquery,
	// so the cost of a long list is not linear in the way a projection's is.
	MaxSort int

	// AllowUnpaged lets a request turn pagination off entirely.
	//
	// Off by default, and that default is the one thing in this struct that is
	// not "anything the schema maps". Every other bound here is a bound on what
	// a request may *name*; this one is a bound on how much comes back, and it
	// is the only field a client can set that has no ceiling at all. A
	// repository that declares MaxLimit clamps unpaged down to it, but MaxLimit
	// is itself unset by default — so with both defaults, `?unpaged=true` on a
	// public endpoint is a full table scan and a full table in memory, chosen by
	// whoever sent the request.
	//
	// An endpoint that genuinely serves an export says so here. It reads like
	// security.Policy's AllowUnscopedDeleteAll for the same reason: the
	// dangerous direction is the one that has to be named.
	AllowUnpaged bool

	// Allow-lists of canonical paths. Empty means "anything the model maps".
	// A trailing .* allows a whole subtree: "Comments.*".
	Filterable  []string
	Sortable    []string
	Selectable  []string
	Preloadable []string
	Searchable  []string

	// DefaultSearchFields is used when a search comes in without an explicit
	// field list. Empty means every text column of the root model.
	DefaultSearchFields []string
}

const (
	defaultMaxDepth      = 6
	defaultMaxConditions = 64
	defaultMaxPreloads   = 16
	// Generous on purpose: this is the ceiling that stops a statement no engine
	// will accept, not a page size. PostgreSQL's parameter limit is 65535 for the
	// whole statement, so a list a fifth of that leaves room for everything else
	// the request asked for.
	defaultMaxInValues = 1024
	defaultMaxSort     = 16
)

func (c *Config) maxDepth() int {
	if c != nil && c.MaxDepth > 0 {
		return c.MaxDepth
	}
	return defaultMaxDepth
}

func (c *Config) maxConditions() int {
	if c != nil && c.MaxConditions > 0 {
		return c.MaxConditions
	}
	return defaultMaxConditions
}

func (c *Config) maxPreloads() int {
	if c != nil && c.MaxPreloads > 0 {
		return c.MaxPreloads
	}
	return defaultMaxPreloads
}

func (c *Config) maxInValues() int {
	if c != nil && c.MaxInValues > 0 {
		return c.MaxInValues
	}
	return defaultMaxInValues
}

func (c *Config) maxSort() int {
	if c != nil && c.MaxSort > 0 {
		return c.MaxSort
	}
	return defaultMaxSort
}

// Check resolves every allow-list entry against the model, so a misspelling
// fails where it was written.
//
// The lists are plain strings and `allowed` is pure string matching, so an entry
// naming a field the model does not have is inert: it never matches, the field it
// was meant to expose stays closed, and every request asking for that field is
// refused as the *client's* mistake. `Filterable: []string{"CreatedAt"}` on a
// model whose field is `Created` is a filter nobody can use and an error message
// that blames the caller, forever, with nothing anywhere saying otherwise.
//
// It is a method rather than a check inside Compile because a Config is built
// once and used per request: paying for it per request would be the wrong trade,
// and refusing at request time is the failure [[D-021]] exists to move to
// start-up. Call it where the config is declared:
//
//	var articleQuery = &query.Config{Filterable: []string{"Title", "Views"}}
//
//	func init() { must(articleQuery.Check(Articles.Meta())) }
//
// A bare "*" and an empty list mean "anything the model maps" and are always
// legal. A trailing ".*" is a subtree: the prefix must resolve, the rest is what
// it covers.
func (c *Config) Check(meta *crud.Meta) error {
	if c == nil || meta == nil {
		return nil
	}
	field := func(list []string, where string) error {
		for _, entry := range list {
			name, _ := strings.CutSuffix(entry, ".*")
			if name == "" || name == "*" {
				continue
			}
			if _, _, err := meta.FieldAt(name); err != nil {
				// A subtree entry may name a relation rather than a column —
				// "Comments.*" covers the columns under it.
				if _, _, rerr := meta.RelationAt(name); rerr == nil {
					continue
				}
				return errf(where, "%s names nothing on %s, so it exposes nothing and every request naming it is refused as the client's mistake",
					entry, meta.Name)
			}
		}
		return nil
	}
	for _, l := range []struct {
		list  []string
		where string
	}{
		{c.Filterable, "filterable"},
		{c.Sortable, "sortable"},
		{c.Selectable, "selectable"},
		{c.Searchable, "searchable"},
		{c.DefaultSearchFields, "defaultSearchFields"},
	} {
		if err := field(l.list, l.where); err != nil {
			return err
		}
	}
	for _, entry := range c.Preloadable {
		name, _ := strings.CutSuffix(entry, ".*")
		if name == "" || name == "*" {
			continue
		}
		if _, _, err := meta.RelationAt(name); err != nil {
			return errf("preloadable", "%s is not a relation of %s, so it can never be preloaded",
				entry, meta.Name)
		}
	}
	return nil
}

// MustCheck is [Check] for a package-level declaration, where there is nowhere to
// return an error to.
func (c *Config) MustCheck(meta *crud.Meta) *Config {
	if err := c.Check(meta); err != nil {
		panic("query: " + err.Error())
	}
	return c
}

// allowed matches a canonical path against an allow-list. An empty list allows
// everything; "Comments.*" allows the whole subtree.
func allowed(list []string, canonical string) bool {
	if len(list) == 0 {
		return true
	}
	for _, entry := range list {
		if sub, ok := strings.CutSuffix(entry, ".*"); ok {
			if strings.EqualFold(canonical, sub) || hasPrefixFold(canonical, sub+".") {
				return true
			}
			continue
		}
		if entry == "*" || strings.EqualFold(entry, canonical) {
			return true
		}
	}
	return false
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// Compile turns the request into repository options, validating every path
// against the model on the way. Nothing reaches SQL that did not resolve.
func (r *Request) Compile(meta *crud.Meta, cfg *Config) ([]crud.Option, error) {
	if r == nil {
		return nil, nil
	}
	c := &compiler{meta: meta, cfg: cfg, conds: new(int)}
	var opts []crud.Option

	// filter
	if !r.Filter.IsZero() {
		p, err := c.node(r.Filter.raw, "filter", 1)
		if err != nil {
			return nil, err
		}
		if p != nil {
			opts = append(opts, crud.Where(p))
		}
	}

	// flat terms, ANDed with the structured filter
	if len(r.Terms) > 0 {
		p, err := c.terms(r.Terms)
		if err != nil {
			return nil, err
		}
		if p != nil {
			opts = append(opts, crud.Where(p))
		}
	}

	// search
	if s := strings.TrimSpace(r.Search); s != "" {
		p, err := c.search(s, r.SearchFields)
		if err != nil {
			return nil, err
		}
		if p != nil {
			opts = append(opts, crud.Where(p))
		}
	}

	// sort
	if len(r.Sort) > c.cfg.maxSort() {
		return nil, errf("sort", "at most %d sort terms per query, got %d",
			c.cfg.maxSort(), len(r.Sort))
	}
	// A repeated canonical path is dropped rather than rendered twice: crud.OrderBy
	// appends, and the second ORDER BY term over a column already sorted decides
	// nothing while costing whatever the term costs — which for a relation hop is
	// a correlated subquery ([[D-005]]).
	sorted := make(map[string]bool, len(r.Sort))
	sortedPaths := make([]string, 0, len(r.Sort))
	for _, s := range r.Sort {
		f, canonical, err := c.path(s.Field, "sort")
		if err != nil {
			return nil, err
		}
		if !allowed(c.cfg.sortable(), canonical) {
			return nil, errf("sort", "%s is not sortable", canonical)
		}
		if sorted[canonical] {
			continue
		}
		sorted[canonical] = true
		sortedPaths = append(sortedPaths, canonical)
		_ = f
		o := crud.Order{Field: canonical, Desc: s.Desc}
		switch strings.ToLower(s.Nulls) {
		case "first":
			o = o.WithNullsFirst()
		case "last":
			o = o.WithNullsLast()
		case "":
		default:
			return nil, errf("sort", "nulls must be first or last, got %q", s.Nulls)
		}
		opts = append(opts, crud.OrderBy(o))
	}

	// projection
	for _, name := range r.Select {
		f, canonical, err := c.path(name, "select")
		if err != nil {
			return nil, err
		}
		if strings.Contains(canonical, ".") {
			return nil, errf("select", "%s crosses a relation; use preload instead", canonical)
		}
		if !allowed(c.cfg.selectable(), canonical) {
			return nil, errf("select", "%s cannot be selected", canonical)
		}
		_ = f
		opts = append(opts, crud.Select(canonical))
	}

	// preloads
	if len(r.Preload) > c.cfg.maxPreloads() {
		return nil, errf("preload", "at most %d relations may be preloaded", c.cfg.maxPreloads())
	}
	for _, p := range r.Preload {
		_, canonical, err := meta.RelationAt(p.Path)
		if err != nil {
			return nil, errf("preload", "%s", cleanErr(err))
		}
		if !allowed(c.cfg.preloadable(), canonical) {
			return nil, errf("preload", "%s cannot be preloaded", canonical)
		}
		sub, err := c.preloadOpts(meta, canonical, p)
		if err != nil {
			return nil, err
		}
		opts = append(opts, crud.PreloadWhere(canonical, sub...))
	}

	// paging
	if r.Page > 0 {
		opts = append(opts, crud.Page(r.Page))
	}
	if r.Limit > 0 {
		opts = append(opts, crud.Limit(r.Limit))
	}
	if r.Offset > 0 {
		opts = append(opts, crud.Offset(r.Offset))
	}
	// A cursor is checked against the sort by the repository, which is the only
	// place the sort that actually runs is known. What the repository cannot
	// check is this endpoint's allow-lists, so that happens here.
	//
	// **A cursor is a filter.** Its payload is the sort tuple, and the repository
	// turns it into an inequality over exactly those columns ([[D-028]]) — so a
	// column that is Sortable and not Filterable became comparable with `>` and
	// `<` by forging a two-element token, while the same comparison written as a
	// filter was refused by name. The token is opaque and unsigned, which is
	// fine: it carries no authority, only a position. What it must not do is
	// reach a column this endpoint declined to expose.
	//
	// Checked through the sort rather than by decoding the token, because the two
	// are required to be the same list and the sort is already resolved here.
	if r.After != "" || r.Before != "" {
		for _, name := range sortedPaths {
			if !allowed(c.cfg.filterable(), name) {
				return nil, errf("after", "a cursor compares %s, which this endpoint does not expose to filtering; "+
					"sort by a filterable column or page by offset", name)
			}
		}
	}
	if r.After != "" {
		opts = append(opts, crud.After(r.After))
	}
	if r.Before != "" {
		opts = append(opts, crud.Before(r.Before))
	}
	if r.Unpaged {
		if c.cfg == nil || !c.cfg.AllowUnpaged {
			return nil, errf(r.UnpagedParam(), "this endpoint does not serve unpaged results; "+
				"ask for a page, or declare query.Config{AllowUnpaged: true} for it")
		}
		opts = append(opts, crud.Unpaged())
	}
	if r.SkipTotal {
		opts = append(opts, crud.SkipTotal())
	}
	if r.Distinct {
		opts = append(opts, crud.Distinct())
	}
	return opts, nil
}

// preloadOpts compiles a per-relation filter and sort against the *target*
// model, so `{"path":"comments","filter":{"approved":true}}` validates against
// Comment rather than against the root.
func (c *compiler) preloadOpts(root *crud.Meta, canonical string, p Preload) ([]crud.Option, error) {
	if p.Filter.IsZero() && len(p.Sort) == 0 {
		return nil, nil
	}
	rel, _, err := root.RelationAt(canonical)
	if err != nil {
		return nil, errf("preload", "%s", cleanErr(err))
	}
	target, _, _, err := rel.Resolve()
	if err != nil {
		return nil, errf("preload."+canonical, "%s", cleanErr(err))
	}

	// The sub-compiler resolves paths against the target but keeps the root's
	// allow-lists, so every entry it matches has to be spelled the way the root
	// spells it: `Comments.Body`, not `Body`. Without the prefix a grant on the
	// root's own Body silently authorised every preloaded relation's Body, and
	// the documented `Comments.Body` spelling authorised the root path while
	// refusing the preload route that looks exactly like it.
	sub := &compiler{meta: target, cfg: c.cfg, conds: c.conds, prefix: canonical}
	var opts []crud.Option
	if !p.Filter.IsZero() {
		pred, err := sub.node(p.Filter.raw, "preload."+canonical+".filter", 1)
		if err != nil {
			return nil, err
		}
		if pred != nil {
			opts = append(opts, crud.Where(pred))
		}
	}
	for _, s := range p.Sort {
		_, sortPath, err := sub.path(s.Field, "preload."+canonical+".sort")
		if err != nil {
			return nil, err
		}
		// A preload's sort used to skip both of these, so a column the config
		// never named was sortable through a relation and an invalid `nulls`
		// was accepted and thrown away — on the same axis the root sort guards.
		if !allowed(c.cfg.sortable(), sub.qualify(sortPath)) {
			return nil, errf("preload."+canonical+".sort", "%s is not sortable", sub.qualify(sortPath))
		}
		o := crud.Order{Field: sortPath, Desc: s.Desc}
		switch strings.ToLower(s.Nulls) {
		case "first":
			o = o.WithNullsFirst()
		case "last":
			o = o.WithNullsLast()
		case "":
		default:
			return nil, errf("preload."+canonical+".sort", "nulls must be first or last, got %q", s.Nulls)
		}
		opts = append(opts, crud.OrderBy(o))
	}
	return opts, nil
}

func (c *Config) sortable() []string {
	if c == nil {
		return nil
	}
	return c.Sortable
}

func (c *Config) selectable() []string {
	if c == nil {
		return nil
	}
	return c.Selectable
}

func (c *Config) preloadable() []string {
	if c == nil {
		return nil
	}
	return c.Preloadable
}

func (c *Config) filterable() []string {
	if c == nil {
		return nil
	}
	return c.Filterable
}

func (c *Config) searchable() []string {
	if c == nil {
		return nil
	}
	return c.Searchable
}

func (c *Config) defaultSearchFields() []string {
	if c == nil {
		return nil
	}
	return c.DefaultSearchFields
}

// compiler carries the schema and limits through the recursion. A preload
// compiles against its own model but shares the condition counter, because the
// budget is the whole document's.
type compiler struct {
	meta  *crud.Meta
	cfg   *Config
	conds *int

	// prefix is where this compiler sits relative to the root, because the
	// allow-lists are the root's and are spelled from there.
	prefix string
}

// qualify spells a path the way the allow-lists do.
func (c *compiler) qualify(canonical string) string {
	if c.prefix == "" {
		return canonical
	}
	return c.prefix + "." + canonical
}

// count charges one leaf comparison against the document's budget. Every shape
// a comparison can take goes through here, so a client cannot buy itself more
// conditions by spelling them as shorthand equalities.
func (c *compiler) count(where string) error {
	*c.conds++
	if *c.conds > c.cfg.maxConditions() {
		return errf(where, "at most %d conditions per query", c.cfg.maxConditions())
	}
	return nil
}

// countValues bounds one `in` or `notIn` list.
//
// Separate from count because the two measure different things: a list is one
// condition however long it is, so the condition budget never sees its length,
// and every element becomes a bound parameter in the statement.
func (c *compiler) countValues(n int, where string) error {
	if n > c.cfg.maxInValues() {
		return errf(where, "at most %d values per list, got %d", c.cfg.maxInValues(), n)
	}
	return nil
}

// path resolves and validates a dotted field path, returning the field and its
// canonical spelling.
func (c *compiler) path(raw, where string) (*crud.Field, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, "", errf(where, "empty field path")
	}
	if strings.Count(raw, ".")+1 > c.cfg.maxDepth() {
		return nil, "", errf(where, "path %s is deeper than the allowed %d segments", raw, c.cfg.maxDepth())
	}
	f, canonical, err := c.meta.FieldAt(raw)
	if err != nil {
		return nil, "", errf(where, "%s", cleanErr(err))
	}
	return f, canonical, nil
}

// search builds a case-insensitive OR across the searchable text columns. It is
// wrapped in its own node, so it can never leak out of the surrounding AND —
// the precedence trap that a hand-built "a LIKE ? OR b LIKE ?" string falls
// straight into.
func (c *compiler) search(term string, fields []string) (crud.Predicate, error) {
	names := fields
	if len(names) == 0 {
		names = c.cfg.defaultSearchFields()
	}
	var preds []crud.Predicate

	if len(names) == 0 {
		// Everything textual on the root model.
		for _, f := range c.meta.Fields {
			if crud.ElemType(f.Type).Kind() == reflect.String && allowed(c.cfg.searchable(), f.Name) {
				preds = append(preds, crud.Contains(f.Name, term))
			}
		}
		if len(preds) == 0 {
			return nil, nil
		}
		return crud.Or(preds...), nil
	}

	// The fourth spelling. A search field list is client-supplied, each entry
	// becomes its own LIKE with its own bind, and nothing charged it: the same
	// unbounded parameter count the `in` caps exist to stop, spelled
	// `searchFields` instead of `in`. The sort list is bounded by MaxSort for the
	// same reason; this one shares MaxSort's budget rather than growing a
	// twelfth cap for a list of column names.
	if len(names) > c.cfg.maxSort() {
		return nil, errf("searchFields", "at most %d search fields per query, got %d",
			c.cfg.maxSort(), len(names))
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		f, canonical, err := c.path(name, "searchFields")
		if err != nil {
			return nil, err
		}
		if !allowed(c.cfg.searchable(), canonical) {
			return nil, errf("searchFields", "%s is not searchable", canonical)
		}
		// A repeated field is one LIKE, not two. Deduplicated the way the sort
		// list is: the second term over a column already searched matches the
		// same rows and costs the same scan.
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		if crud.ElemType(f.Type).Kind() == reflect.String {
			preds = append(preds, crud.Contains(canonical, term))
			continue
		}
		// A non-text column only joins the search when the term fits it.
		if v, err := coerceString(term, crud.ElemType(f.Type)); err == nil {
			preds = append(preds, crud.Eq(canonical, v))
		}
	}
	if len(preds) == 0 {
		return nil, nil
	}
	return crud.Or(preds...), nil
}

// cleanErr strips the package prefix so query errors read as one voice.
func cleanErr(err error) string {
	return strings.TrimPrefix(err.Error(), "crud: ")
}
