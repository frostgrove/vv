package query

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/frostgrove/vv/crud"
)

// Error is a rejected query document. Everything in it is safe to hand back to
// the client: it names the path that was wrong and why, never internals.
type Error struct {
	Path   string
	Reason string
}

func (this *Error) Error() string {
	if this.Path == "" {
		return "query: " + this.Reason
	}
	return "query: " + this.Path + ": " + this.Reason
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

	// MaxSelect limits projection entries from one client document. A projection
	// is not a harmless list: each entry is resolved and becomes an Option, so a
	// repeated JSON array can otherwise allocate work long before sqlrepo later
	// deduplicates its fields.
	MaxSelect int

	// MaxLimit caps the public page size. Zero selects the safe package default.
	// The compiler always emits that limit, even when the client omitted limit
	// or chose cursor paging, so a repository default cannot widen an endpoint.
	MaxLimit int

	// MaxOffset bounds how far a client may page by offset. Page numbers are
	// checked against the same budget using MaxLimit as their worst-case size.
	MaxOffset int

	// MaxBindValues bounds all client values in one document. Per-list limits
	// cannot do that: many individually valid IN lists can still exceed an
	// engine's statement-parameter limit together.
	MaxBindValues int

	// MaxPreloadRows limits the child rows materialised at every requested
	// relation hop. Exceeding it is a refusal, never a partial relation. Zero
	// uses the safe package default.
	MaxPreloadRows int

	// AllowDistinct makes DISTINCT an explicit endpoint decision. DISTINCT may
	// force a full deduplication pass and changes projection identity semantics.
	AllowDistinct bool

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
	defaultMaxInValues    = 1024
	defaultMaxSort        = 16
	defaultMaxSelect      = 64
	defaultMaxLimit       = 100
	defaultMaxOffset      = 10_000
	defaultMaxBinds       = 8_192
	defaultMaxPreloadRows = 1_000
)

func (this *Config) maxDepth() int {
	if this != nil && this.MaxDepth > 0 {
		return this.MaxDepth
	}
	return defaultMaxDepth
}

func (this *Config) maxConditions() int {
	if this != nil && this.MaxConditions > 0 {
		return this.MaxConditions
	}
	return defaultMaxConditions
}

func (this *Config) maxPreloads() int {
	if this != nil && this.MaxPreloads > 0 {
		return this.MaxPreloads
	}
	return defaultMaxPreloads
}

func (this *Config) maxInValues() int {
	if this != nil && this.MaxInValues > 0 {
		return this.MaxInValues
	}
	return defaultMaxInValues
}

func (this *Config) maxSort() int {
	if this != nil && this.MaxSort > 0 {
		return this.MaxSort
	}
	return defaultMaxSort
}

func (this *Config) maxSelect() int {
	if this != nil && this.MaxSelect > 0 {
		return this.MaxSelect
	}
	return defaultMaxSelect
}

func (this *Config) maxLimit() int {
	if this != nil && this.MaxLimit > 0 {
		return this.MaxLimit
	}
	return defaultMaxLimit
}

func (this *Config) maxOffset() int {
	if this != nil && this.MaxOffset > 0 {
		return this.MaxOffset
	}
	return defaultMaxOffset
}

func (this *Config) maxBindValues() int {
	if this != nil && this.MaxBindValues > 0 {
		return this.MaxBindValues
	}
	return defaultMaxBinds
}

func (this *Config) maxPreloadRows() int {
	if this != nil && this.MaxPreloadRows > 0 {
		return this.MaxPreloadRows
	}
	return defaultMaxPreloadRows
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
func (this *Config) Check(meta *crud.Meta) error {
	if this == nil {
		return nil
	}
	for _, bound := range []struct {
		name  string
		value int
	}{
		{"maxDepth", this.MaxDepth},
		{"maxConditions", this.MaxConditions},
		{"maxPreloads", this.MaxPreloads},
		{"maxInValues", this.MaxInValues},
		{"maxSort", this.MaxSort},
		{"maxSelect", this.MaxSelect},
		{"maxLimit", this.MaxLimit},
		{"maxOffset", this.MaxOffset},
		{"maxBindValues", this.MaxBindValues},
		{"maxPreloadRows", this.MaxPreloadRows},
	} {
		if bound.value < 0 {
			return errf(bound.name, "must not be negative; zero uses the safe default")
		}
	}
	if meta == nil {
		return nil
	}
	field := func(list []string, where string) error {
		for _, entry := range list {
			if entry == "*" {
				continue
			}
			name, subtree := strings.CutSuffix(entry, ".*")
			if name == "" {
				return errf(where, "empty declaration cannot grant any field")
			}
			if name == "*" {
				return errf(where, "%s is not a wildcard declaration; use * by itself", entry)
			}
			if subtree {
				// Only a relation has a subtree. Letting a bare relation through
				// here makes a declaration look valid while every field operation
				// later rejects it.
				if _, _, err := meta.RelationAt(name); err == nil {
					continue
				}
				return errf(where, "%s must name a relation subtree on %s", entry, meta.Name)
			}
			if _, _, err := meta.FieldAt(name); err != nil {
				return errf(where, "%s must name a field on %s, so it cannot leave an ineffective declaration behind",
					entry, meta.Name)
			}
		}
		return nil
	}
	for _, l := range []struct {
		list  []string
		where string
	}{
		{this.Filterable, "filterable"},
		{this.Sortable, "sortable"},
		{this.Selectable, "selectable"},
		{this.Searchable, "searchable"},
	} {
		if err := field(l.list, l.where); err != nil {
			return err
		}
	}
	// Defaults are an actual list passed to search, not an allow-list. A
	// wildcard would therefore become a literal field named "*" at request
	// time. Resolve every direct field now, and ensure the default is inside
	// the endpoint's searchable vocabulary rather than saving a route mistake
	// for the first client who happens to search it.
	for _, entry := range this.DefaultSearchFields {
		if entry == "*" || strings.HasSuffix(entry, ".*") {
			return errf("defaultSearchFields", "%s is an allow-list wildcard, not a search field", entry)
		}
		if entry == "" {
			return errf("defaultSearchFields", "empty declaration cannot name a search field")
		}
		_, canonical, err := meta.FieldAt(entry)
		if err != nil {
			return errf("defaultSearchFields", "%s must name a field on %s, so it can search when the route starts", entry, meta.Name)
		}
		if !allowed(this.Searchable, canonical) {
			return errf("defaultSearchFields", "%s is not granted by searchable", canonical)
		}
	}
	for _, entry := range this.Preloadable {
		if entry == "*" {
			continue
		}
		name, _ := strings.CutSuffix(entry, ".*")
		if name == "" {
			return errf("preloadable", "empty declaration cannot grant any relation")
		}
		if name == "*" {
			return errf("preloadable", "%s is not a wildcard declaration; use * by itself", entry)
		}
		if _, _, err := meta.RelationAt(name); err != nil {
			return errf("preloadable", "%s is not a relation of %s, so it can never be preloaded",
				entry, meta.Name)
		}
		for i := range strings.Split(name, ".") {
			hop := strings.Join(strings.Split(name, ".")[:i+1], ".")
			if !allowed(this.Preloadable, hop) {
				return errf("preloadable", "%s loads %s too, so %s must be declared explicitly", entry, hop, hop)
			}
		}
	}
	return nil
}

// MustCheck is [Check] for a package-level declaration, where there is nowhere to
// return an error to.
func (this *Config) MustCheck(meta *crud.Meta) *Config {
	if err := this.Check(meta); err != nil {
		panic("query: " + err.Error())
	}
	return this
}

// allowed matches a canonical path against an allow-list. An empty list allows
// everything; "Comments.*" allows the whole subtree. Declarations use the
// same case- and separator-insensitive spelling as request paths: a config
// author should not have to remember whether the model called it PublishedAt or
// published_at while the client is allowed to use either.
func allowed(list []string, canonical string) bool {
	if len(list) == 0 {
		return true
	}
	for _, entry := range list {
		if sub, ok := strings.CutSuffix(entry, ".*"); ok {
			if equalPathFold(canonical, sub) || hasPathPrefixFold(canonical, sub) {
				return true
			}
			continue
		}
		if entry == "*" || equalPathFold(entry, canonical) {
			return true
		}
	}
	return false
}

// equalPathFold compares each dotted path segment in the same forgiving way
// Meta resolves identifiers: case-insensitively and without separators. Dots
// remain structural, so "Comment.Slug" never grants "Comments.Slug".
func equalPathFold(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	if len(as) != len(bs) {
		return false
	}
	for i := range as {
		if foldSegment(as[i]) != foldSegment(bs[i]) {
			return false
		}
	}
	return true
}

func hasPathPrefixFold(path, prefix string) bool {
	ps, ss := strings.Split(path, "."), strings.Split(prefix, ".")
	if len(ps) < len(ss) {
		return false
	}
	for i := range ss {
		if foldSegment(ps[i]) != foldSegment(ss[i]) {
			return false
		}
	}
	return true
}

func foldSegment(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '_', '-', ' ':
			continue
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}

// Compile turns the request into repository options, validating every path
// against the model on the way. Nothing reaches SQL that did not resolve.
func (this *Request) Compile(meta *crud.Meta, config *Config) ([]crud.Option, error) {
	if this == nil {
		return nil, nil
	}
	c := &compiler{meta: meta, config: config, conds: new(int), binds: new(int)}
	var options []crud.Option
	cursoring := this.After != "" || this.Before != ""
	if this.Page < 0 {
		return nil, errf("page", "must not be negative")
	}
	if this.Limit < 0 {
		return nil, errf("limit", "must not be negative")
	}
	if this.Offset < 0 {
		return nil, errf("offset", "must not be negative")
	}
	if this.afterSet && strings.TrimSpace(this.After) == "" {
		return nil, errf("after", "must not be empty")
	}
	if this.beforeSet && strings.TrimSpace(this.Before) == "" {
		return nil, errf("before", "must not be empty")
	}
	if (this.After != "" || this.afterSet) && (this.Before != "" || this.beforeSet) {
		return nil, errf("after", "cannot be combined with before")
	}

	// filter
	if !this.Filter.IsZero() {
		p, err := c.node(this.Filter.raw, "filter", 1)
		if err != nil {
			return nil, err
		}
		if p != nil {
			options = append(options, crud.Where(p))
		}
	}

	// flat terms, ANDed with the structured filter
	if len(this.Terms) > 0 {
		p, err := c.terms(this.Terms)
		if err != nil {
			return nil, err
		}
		if p != nil {
			options = append(options, crud.Where(p))
		}
	}

	// search
	if s := strings.TrimSpace(this.Search); s != "" {
		p, err := c.search(s, this.SearchFields)
		if err != nil {
			return nil, err
		}
		if p != nil {
			options = append(options, crud.Where(p))
		}
	}

	// sort
	if len(this.Sort) > c.config.maxSort() {
		return nil, errf("sort", "at most %d sort terms per query, got %d",
			c.config.maxSort(), len(this.Sort))
	}
	// A repeated canonical path is dropped rather than rendered twice: crud.OrderBy
	// appends, and the second ORDER BY term over a column already sorted decides
	// nothing while costing whatever the term costs — which for a relation hop is
	// a correlated subquery ([[D-005]]).
	sorted := make(map[string]crud.Order, len(this.Sort))
	sortedPaths := make([]string, 0, len(this.Sort))
	for _, s := range this.Sort {
		f, canonical, err := c.path(s.Field, "sort")
		if err != nil {
			return nil, err
		}
		if !allowed(c.config.sortable(), canonical) {
			return nil, errf("sort", "%s is not sortable", canonical)
		}
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
		if prior, exists := sorted[canonical]; exists {
			if prior != o {
				return nil, errf("sort", "conflicting order for %s", canonical)
			}
			continue
		}
		sorted[canonical] = o
		sortedPaths = append(sortedPaths, canonical)
		_ = f
		options = append(options, crud.OrderBy(o))
	}

	// projection
	if len(this.Select) > c.config.maxSelect() {
		return nil, errf("select", "at most %d fields may be selected", c.config.maxSelect())
	}
	selected := make(map[string]bool, len(this.Select))
	for _, name := range this.Select {
		f, canonical, err := c.path(name, "select")
		if err != nil {
			return nil, err
		}
		if strings.Contains(canonical, ".") {
			return nil, errf("select", "%s crosses a relation; use preload instead", canonical)
		}
		if !allowed(c.config.selectable(), canonical) {
			return nil, errf("select", "%s cannot be selected", canonical)
		}
		if selected[canonical] {
			continue
		}
		selected[canonical] = true
		_ = f
		options = append(options, crud.Select(canonical))
	}

	// preloads
	if len(this.Preload) > c.config.maxPreloads() {
		return nil, errf("preload", "at most %d relations may be preloaded", c.config.maxPreloads())
	}
	canonicalPreloads := make([]string, len(this.Preload))
	loaded := make(map[string]struct{}, len(this.Preload))
	for i, p := range this.Preload {
		_, canonical, err := meta.RelationAt(p.Path)
		if err != nil {
			return nil, errf("preload", "%s", cleanErr(err))
		}
		canonicalPreloads[i] = canonical
		if strings.Count(canonical, ".")+1 > c.config.maxDepth() {
			return nil, errf("preload", "path %s is deeper than the allowed %d segments", canonical, c.config.maxDepth())
		}
		// A nested preload materialises every relation it walks. Requiring an
		// explicit grant for every hop keeps a declaration of Comments.Author
		// from quietly exposing Comments as well.
		segments := strings.Split(canonical, ".")
		for i := range segments {
			hop := strings.Join(segments[:i+1], ".")
			if !allowed(c.config.preloadable(), hop) {
				return nil, errf("preload", "%s cannot be preloaded", hop)
			}
			loaded[hop] = struct{}{}
		}
	}
	if len(loaded) > c.config.maxPreloads() {
		return nil, errf("preload", "at most %d relations may be preloaded", c.config.maxPreloads())
	}
	for i, p := range this.Preload {
		canonical := canonicalPreloads[i]
		if p.MaxRows < 0 {
			return nil, errf("preload."+canonical+".maxRows", "must not be negative")
		}
		sub, err := c.preloadOpts(meta, canonical, p)
		if err != nil {
			return nil, err
		}
		// A client may tighten an endpoint's cap, never widen it. The cap is a
		// refusal rather than pagination, so neither value permits a partial
		// relation.
		maxRows := c.config.maxPreloadRows()
		if p.MaxRows > 0 && p.MaxRows < maxRows {
			maxRows = p.MaxRows
		}
		options = append(options, crud.PreloadCap(canonical, maxRows, sub...))
	}

	// paging
	//
	// A cursor replaces page and offset. Apart from matching sqlrepo's
	// behaviour, keeping the ignored knobs out of Options makes the contract
	// hold for every Repository implementation.
	// Query.Config owns the public page budget. Always materialise it as a
	// Limit, including when the request omitted limit and when it pages by
	// cursor; otherwise sqlrepo falls back to its independently configured
	// DefaultLimit and a small endpoint budget is only aspirational.
	limit := this.Limit
	if limit == 0 || limit > c.config.maxLimit() {
		limit = c.config.maxLimit()
	}
	if !cursoring && !this.omitPaging {
		if this.Offset > c.config.maxOffset() {
			return nil, errf("offset", "must not exceed %d", c.config.maxOffset())
		}
		if this.Page > 0 {
			// Compare before multiplying. At the public boundary an overflowing
			// page number must be a normal depth refusal, not an enormous offset
			// that a lower layer happens to saturate.
			if this.Offset == 0 && this.Page > 1 && this.Page-1 > c.config.maxOffset()/limit {
				return nil, errf("page", "is deeper than the allowed offset of %d", c.config.maxOffset())
			}
			options = append(options, crud.Page(this.Page))
		}
		if this.Offset > 0 {
			options = append(options, crud.Offset(this.Offset))
		}
	}
	if !this.omitPaging {
		options = append(options, crud.Limit(limit))
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
	if cursoring {
		if len(sortedPaths) == 0 {
			return nil, errf("after", "cursor pagination requires an explicit sort; a repository default is not part of this endpoint's filter policy")
		}
		// sqlrepo adds this tiebreaker whenever a page sort does not already
		// name it. It participates in the cursor predicate just as much as a
		// client-supplied order does, so it needs the same Filterable grant.
		if _, named := sorted[meta.PK.Name]; !named {
			sortedPaths = append(sortedPaths, meta.PK.Name)
		}
		for _, name := range sortedPaths {
			if strings.Contains(name, ".") {
				return nil, errf("after", "cursor pagination cannot sort through relation %s; use a root-model sort", name)
			}
			if !allowed(c.config.filterable(), name) {
				return nil, errf("after", "a cursor compares %s, which this endpoint does not expose to filtering; "+
					"sort by a filterable column or page by offset", name)
			}
		}
		// CursorPredicate expands an N-column lexicographic comparison into
		// 1+2+...+N leaves, each with its own bound value. Charge the expansion
		// here, while the public query budget is still in force, rather than
		// letting one cursor manufacture a statement larger than the endpoint
		// permits for an equivalent explicit filter.
		cost, ok := triangular(uint64(len(sortedPaths)))
		if !ok {
			return nil, errf("after", "cursor sort is too large")
		}
		if err := c.countConditions(cost, "after"); err != nil {
			return nil, err
		}
		if err := c.countBindValues(cost, "after"); err != nil {
			return nil, err
		}
	}
	if this.After != "" {
		options = append(options, crud.After(this.After))
	}
	if this.Before != "" {
		options = append(options, crud.Before(this.Before))
	}
	if this.Unpaged {
		if c.config == nil || !c.config.AllowUnpaged {
			return nil, errf(this.UnpagedParam(), "this endpoint does not serve unpaged results; "+
				"ask for a page, or declare query.Config{AllowUnpaged: true} for it")
		}
		options = append(options, crud.Unpaged())
	}
	if this.SkipTotal {
		options = append(options, crud.SkipTotal())
	}
	if this.Distinct {
		if c.config == nil || !c.config.AllowDistinct {
			return nil, errf("distinct", "this endpoint does not serve distinct results; declare query.Config{AllowDistinct: true} to enable it")
		}
		options = append(options, crud.Distinct())
	}
	return options, nil
}

// preloadOpts compiles a per-relation filter and sort against the *target*
// model, so `{"path":"comments","filter":{"approved":true}}` validates against
// Comment rather than against the root.
func (this *compiler) preloadOpts(root *crud.Meta, canonical string, p Preload) ([]crud.Option, error) {
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
	sub := &compiler{meta: target, config: this.config, conds: this.conds, binds: this.binds, prefix: canonical}
	var options []crud.Option
	if !p.Filter.IsZero() {
		pred, err := sub.node(p.Filter.raw, "preload."+canonical+".filter", 1)
		if err != nil {
			return nil, err
		}
		if pred != nil {
			options = append(options, crud.Where(pred))
		}
	}
	if len(p.Sort) > this.config.maxSort() {
		return nil, errf("preload."+canonical+".sort", "at most %d sort terms per query, got %d",
			this.config.maxSort(), len(p.Sort))
	}
	sorted := make(map[string]crud.Order, len(p.Sort))
	for _, s := range p.Sort {
		_, sortPath, err := sub.path(s.Field, "preload."+canonical+".sort")
		if err != nil {
			return nil, err
		}
		// A preload's sort used to skip both of these, so a column the config
		// never named was sortable through a relation and an invalid `nulls`
		// was accepted and thrown away — on the same axis the root sort guards.
		if !allowed(this.config.sortable(), sub.qualify(sortPath)) {
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
		fullPath := sub.qualify(sortPath)
		if prior, exists := sorted[fullPath]; exists {
			if prior != o {
				return nil, errf("preload."+canonical+".sort", "conflicting order for %s", fullPath)
			}
			continue
		}
		sorted[fullPath] = o
		options = append(options, crud.OrderBy(o))
	}
	return options, nil
}

func (this *Config) sortable() []string {
	if this == nil {
		return nil
	}
	return this.Sortable
}

func (this *Config) selectable() []string {
	if this == nil {
		return nil
	}
	return this.Selectable
}

func (this *Config) preloadable() []string {
	if this == nil {
		return nil
	}
	return this.Preloadable
}

func (this *Config) filterable() []string {
	if this == nil {
		return nil
	}
	return this.Filterable
}

func (this *Config) searchable() []string {
	if this == nil {
		return nil
	}
	return this.Searchable
}

func (this *Config) defaultSearchFields() []string {
	if this == nil {
		return nil
	}
	return this.DefaultSearchFields
}

// compiler carries the schema and limits through the recursion. A preload
// compiles against its own model but shares the condition counter, because the
// budget is the whole document's.
type compiler struct {
	meta   *crud.Meta
	config *Config
	conds  *int
	binds  *int

	// prefix is where this compiler sits relative to the root, because the
	// allow-lists are the root's and are spelled from there.
	prefix string
}

// qualify spells a path the way the allow-lists do.
func (this *compiler) qualify(canonical string) string {
	if this.prefix == "" {
		return canonical
	}
	return this.prefix + "." + canonical
}

// count charges one leaf comparison against the document's budget. Every shape
// a comparison can take goes through here, so a client cannot buy itself more
// conditions by spelling them as shorthand equalities.
func (this *compiler) count(where string) error {
	return this.countConditions(1, where)
}

func (this *compiler) countConditions(n uint64, where string) error {
	max := uint64(this.config.maxConditions())
	used := uint64(*this.conds)
	if n > max || used > max-n {
		return errf(where, "at most %d conditions per query", this.config.maxConditions())
	}
	*this.conds += int(n)
	return nil
}

// countValues bounds one `in` or `notIn` list.
//
// Separate from count because the two measure different things: a list is one
// condition however long it is, so the condition budget never sees its length,
// and every element becomes a bound parameter in the statement.
func (this *compiler) countValues(n int, where string) error {
	if n == 0 {
		return errf(where, "needs at least one value")
	}
	if n > this.config.maxInValues() {
		return errf(where, "at most %d values per list, got %d", this.config.maxInValues(), n)
	}
	return this.countBinds(n, where)
}

// countBinds accounts for every value that becomes a SQL parameter. It is a
// document-wide budget: a request can spell values through nested filters,
// flat terms, search fields and preload filters, but all of them land in one
// statement (or one family of statements) the server still has to carry.
func (this *compiler) countBinds(n int, where string) error {
	return this.countBindValues(uint64(n), where)
}

func (this *compiler) countBindValues(n uint64, where string) error {
	max := uint64(this.config.maxBindValues())
	used := uint64(*this.binds)
	if n > max || used > max-n {
		return errf(where, "at most %d bound values per query", this.config.maxBindValues())
	}
	*this.binds += int(n)
	return nil
}

// triangular returns 1+2+...+n without wrapping on a pathological request.
func triangular(n uint64) (uint64, bool) {
	a, b := n, n+1
	if a%2 == 0 {
		a /= 2
	} else {
		b /= 2
	}
	if b != 0 && a > ^uint64(0)/b {
		return 0, false
	}
	return a * b, true
}

// path resolves and validates a dotted field path, returning the field and its
// canonical spelling.
func (this *compiler) path(raw, where string) (*crud.Field, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, "", errf(where, "empty field path")
	}
	depth := strings.Count(raw, ".") + 1
	if this.prefix != "" {
		depth += strings.Count(this.prefix, ".") + 1
	}
	if depth > this.config.maxDepth() {
		return nil, "", errf(where, "path %s is deeper than the allowed %d segments", raw, this.config.maxDepth())
	}
	f, canonical, err := this.meta.FieldAt(raw)
	if err != nil {
		return nil, "", errf(where, "%s", cleanErr(err))
	}
	return f, canonical, nil
}

// search builds a case-insensitive OR across the searchable text columns. It is
// wrapped in its own node, so it can never leak out of the surrounding AND —
// the precedence trap that a hand-built "a LIKE ? OR b LIKE ?" string falls
// straight into.
func (this *compiler) search(term string, fields []string) (crud.Predicate, error) {
	names := fields
	if len(names) == 0 {
		names = this.config.defaultSearchFields()
	}
	var preds []crud.Predicate

	if len(names) == 0 {
		// Everything textual on the root model.
		for _, f := range this.meta.Fields {
			if crud.ElemType(f.Type).Kind() == reflect.String && allowed(this.config.searchable(), f.Name) {
				if err := this.count("search"); err != nil {
					return nil, err
				}
				if err := this.countBinds(1, "search"); err != nil {
					return nil, err
				}
				preds = append(preds, crud.ContainsIgnoreCase(f.Name, term))
			}
		}
		if len(preds) == 0 {
			return nil, errf("search", "this endpoint has no searchable text fields")
		}
		return crud.Or(preds...), nil
	}

	// The fourth spelling. A search field list is client-supplied, each entry
	// becomes its own LIKE with its own bind, and nothing charged it: the same
	// unbounded parameter count the `in` caps exist to stop, spelled
	// `searchFields` instead of `in`. The sort list is bounded by MaxSort for the
	// same reason; this one shares MaxSort's budget rather than growing a
	// twelfth cap for a list of column names.
	if len(names) > this.config.maxSort() {
		return nil, errf("searchFields", "at most %d search fields per query, got %d",
			this.config.maxSort(), len(names))
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		f, canonical, err := this.path(name, "searchFields")
		if err != nil {
			return nil, err
		}
		if !allowed(this.config.searchable(), canonical) {
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
			if err := this.count("searchFields"); err != nil {
				return nil, err
			}
			if err := this.countBinds(1, "searchFields"); err != nil {
				return nil, err
			}
			preds = append(preds, crud.ContainsIgnoreCase(canonical, term))
			continue
		}
		// A non-text column only joins the search when the term fits it.
		if v, err := coerceString(term, crud.ElemType(f.Type)); err == nil {
			if err := this.count("searchFields"); err != nil {
				return nil, err
			}
			if err := this.countBinds(1, "searchFields"); err != nil {
				return nil, err
			}
			preds = append(preds, crud.Eq(canonical, v))
		}
	}
	if len(preds) == 0 {
		return nil, errf("searchFields", "none of the requested fields can search %q", term)
	}
	return crud.Or(preds...), nil
}

// cleanErr strips the package prefix so query errors read as one voice.
func cleanErr(err error) string {
	return strings.TrimPrefix(err.Error(), "crud: ")
}
