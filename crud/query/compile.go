package query

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/frostgrove/vv/crud"
)

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

type Config struct {
	MaxDepth int

	MaxConditions int

	MaxPreloads int

	MaxInValues int

	MaxSort int

	MaxSelect int

	MaxLimit int

	MaxOffset int

	MaxBindValues int

	MaxPreloadRows int

	AllowDistinct bool

	AllowUnpaged bool

	Filterable  []string
	Sortable    []string
	Selectable  []string
	Preloadable []string
	Searchable  []string

	DefaultSearchFields []string
}

const (
	defaultMaxDepth      = 6
	defaultMaxConditions = 64
	defaultMaxPreloads   = 16

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

func (this *Config) MustCheck(meta *crud.Meta) *Config {
	if err := this.Check(meta); err != nil {
		panic("query: " + err.Error())
	}
	return this
}

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

	if !this.Filter.IsZero() {
		p, err := c.node(this.Filter.raw, "filter", 1)
		if err != nil {
			return nil, err
		}
		if p != nil {
			options = append(options, crud.Where(p))
		}
	}

	if len(this.Terms) > 0 {
		p, err := c.terms(this.Terms)
		if err != nil {
			return nil, err
		}
		if p != nil {
			options = append(options, crud.Where(p))
		}
	}

	if s := strings.TrimSpace(this.Search); s != "" {
		p, err := c.search(s, this.SearchFields)
		if err != nil {
			return nil, err
		}
		if p != nil {
			options = append(options, crud.Where(p))
		}
	}

	if len(this.Sort) > c.config.maxSort() {
		return nil, errf("sort", "at most %d sort terms per query, got %d",
			c.config.maxSort(), len(this.Sort))
	}

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

		maxRows := c.config.maxPreloadRows()
		if p.MaxRows > 0 && p.MaxRows < maxRows {
			maxRows = p.MaxRows
		}
		options = append(options, crud.PreloadCap(canonical, maxRows, sub...))
	}

	limit := this.Limit
	if limit == 0 || limit > c.config.maxLimit() {
		limit = c.config.maxLimit()
	}
	if !cursoring && !this.omitPaging {
		if this.Offset > c.config.maxOffset() {
			return nil, errf("offset", "must not exceed %d", c.config.maxOffset())
		}
		if this.Page > 0 {
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

	if cursoring {
		if len(sortedPaths) == 0 {
			return nil, errf("after", "cursor pagination requires an explicit sort; a repository default is not part of this endpoint's filter policy")
		}

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

type compiler struct {
	meta   *crud.Meta
	config *Config
	conds  *int
	binds  *int

	prefix string
}

func (this *compiler) qualify(canonical string) string {
	if this.prefix == "" {
		return canonical
	}
	return this.prefix + "." + canonical
}

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

func (this *compiler) countValues(n int, where string) error {
	if n == 0 {
		return errf(where, "needs at least one value")
	}
	if n > this.config.maxInValues() {
		return errf(where, "at most %d values per list, got %d", this.config.maxInValues(), n)
	}
	return this.countBinds(n, where)
}

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

func (this *compiler) search(term string, fields []string) (crud.Predicate, error) {
	names := fields
	if len(names) == 0 {
		names = this.config.defaultSearchFields()
	}
	var preds []crud.Predicate

	if len(names) == 0 {
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

func cleanErr(err error) string {
	return strings.TrimPrefix(err.Error(), "crud: ")
}
