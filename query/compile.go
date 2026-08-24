package query

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/shardit-io/go-rx-crud/crud"
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
	for _, s := range r.Sort {
		f, canonical, err := c.path(s.Field, "sort")
		if err != nil {
			return nil, err
		}
		if !allowed(c.cfg.sortable(), canonical) {
			return nil, errf("sort", "%s is not sortable", canonical)
		}
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
	// place the sort that actually runs is known.
	if r.After != "" {
		opts = append(opts, crud.After(r.After))
	}
	if r.Before != "" {
		opts = append(opts, crud.Before(r.Before))
	}
	if r.Unpaged {
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

	for _, name := range names {
		f, canonical, err := c.path(name, "searchFields")
		if err != nil {
			return nil, err
		}
		if !allowed(c.cfg.searchable(), canonical) {
			return nil, errf("searchFields", "%s is not searchable", canonical)
		}
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
