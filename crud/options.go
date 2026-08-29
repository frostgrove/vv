package crud

import "math"

// Options is the resolved query shape. Decorators receive and may inspect it;
// the fields are exported for exactly that reason.
type Options struct {
	// Filter is ANDed together. Where appends, it never replaces — that is what
	// lets a security decorator inject a scope the caller cannot remove.
	Filter   []Predicate
	Sort     []Order
	Preloads []PreloadSpec
	Fields   []string // projection; empty means every column

	// PreloadRows is the maximum number of child rows one preload relation may
	// materialise. Zero means no per-relation cap. Query endpoints set it from
	// their Config; the preloader refuses rather than silently truncating a
	// relation when the cap is exceeded.
	PreloadRows int

	Page   int // 1-based; 0 means the first page
	Limit  int // 0 means "repository default"
	Offset int // explicit offset wins over Page

	// RelScopes narrows the far side of a relation for this query only. Filter
	// constrains the statement's own FROM and nothing else; this is what follows
	// a preload, a nested filter's EXISTS and a nested sort's subquery. A
	// decorator whose narrowing depends on the caller — an access-control gate,
	// which cannot bake anything into the blueprint — puts it here.
	RelScopes *RelationScopes

	// Agg carries the summary columns and the grouping for an aggregate read.
	Agg AggregateSpec

	// After and Before page by cursor rather than by offset. They are opaque
	// strings a previous page handed back; at most one may be set.
	After  string
	Before string

	// Primary forces this read onto the writable datasource even when a replica
	// is configured. It is what a read that decides a write must set.
	Primary bool

	Unpaged   bool // ignore Page/Limit and return everything
	NoSort    bool // drop the default sort and the stable-pagination tiebreaker
	NoTotal   bool // skip the COUNT query; Total is then the page length
	ForUpdate bool // SELECT ... FOR UPDATE
	Distinct  bool
}

// Cursor reports whether this query pages by cursor, and in which direction.
func (this *Options) Cursor() (token string, back, ok bool) {
	switch {
	case this.After != "":
		return this.After, false, true
	case this.Before != "":
		return this.Before, true, true
	}
	return "", false, false
}

// Option mutates Options. Options are applied left to right.
type Option func(*Options)

// Build resolves a list of options into an Options value.
func Build(options ...Option) *Options {
	o := &Options{}
	o.Apply(options...)
	return o
}

// Apply runs more options over an existing Options.
func (this *Options) Apply(options ...Option) {
	for _, opt := range options {
		if opt != nil {
			opt(this)
		}
	}
}

// Predicate folds Filter into a single AND node, or nil when empty.
func (this *Options) Predicate() Predicate {
	switch len(this.Filter) {
	case 0:
		return nil
	case 1:
		return this.Filter[0]
	default:
		return And(this.Filter...)
	}
}

// Where ANDs a predicate into the query. Repeated use accumulates.
func Where(p Predicate) Option {
	return func(o *Options) {
		if p != nil {
			o.Filter = append(o.Filter, p)
		}
	}
}

// NarrowRelations carries a narrowing across relation boundaries for this query.
// Like Where it accumulates: repeated use ANDs, so nothing a decorator declared
// can be lifted by a later option.
//
// Where only ever constrains the statement's own FROM, which is why this exists
// separately — a scope that hides rows of the root table hides nothing when the
// same rows are reached through a preload.
func NarrowRelations(rs *RelationScopes) Option {
	return func(o *Options) {
		if !rs.Empty() {
			o.RelScopes = MergeRelationScopes(o.RelScopes, rs)
		}
	}
}

// After pages forward from a cursor a previous page returned.
//
// It replaces the offset rather than adding to it: "the rows after this one" is
// a question a concurrent insert cannot change the answer to, which is the whole
// reason to use it. It also skips the COUNT — a cursor walk has no page number
// for a total to divide into — so Total is the length of the page and
// TotalPages is zero. Call Count separately if a client really needs the number.
func After(cursor string) Option {
	return func(o *Options) {
		if cursor != "" {
			o.After, o.Before, o.NoTotal = cursor, "", true
		}
	}
}

// Before pages backward from a cursor. The page still arrives in the sort's own
// order; only the boundary comparison is inverted.
func Before(cursor string) Option {
	return func(o *Options) {
		if cursor != "" {
			o.Before, o.After, o.NoTotal = cursor, "", true
		}
	}
}

// Page selects a 1-based page.
func Page(n int) Option { return func(o *Options) { o.Page = n } }

// Limit sets the page size. It is clamped to the repository's MaxLimit.
func Limit(n int) Option { return func(o *Options) { o.Limit = n } }

// Offset sets an explicit row offset, overriding Page.
func Offset(n int) Option { return func(o *Options) { o.Offset = n } }

// OrderBy appends sort terms.
func OrderBy(orders ...Order) Option {
	return func(o *Options) { o.Sort = append(o.Sort, orders...) }
}

// SortBy replaces the sort terms.
func SortBy(orders ...Order) Option {
	return func(o *Options) { o.Sort = append(o.Sort[:0:0], orders...) }
}

// Select narrows the projection to the named fields. The primary key is always
// included, because it is what preloads and identity depend on.
func Select(fields ...string) Option {
	return func(o *Options) { o.Fields = append(o.Fields, fields...) }
}

// SelectAll drops any projection applied before it, so the query reads every
// column again. It exists for the layers that cannot work with half a row: a
// row-level check reading a column the client did not select would compare
// against a zero value and believe it.
func SelectAll() Option { return func(o *Options) { o.Fields = nil } }

// PrimaryOnly keeps this read off any replica.
//
// A replica is behind, and "behind" is only harmless for a read whose answer is
// displayed. A read whose answer decides a write — load-then-diff, an
// authorisation check, a uniqueness probe — must not be served stale, or the
// decision is made against a row that no longer exists in that shape.
func PrimaryOnly() Option { return func(o *Options) { o.Primary = true } }

// Unpaged disables pagination for this call.
func Unpaged() Option { return func(o *Options) { o.Unpaged = true } }

// Unsorted drops the repository's default sort and the primary-key tiebreaker.
// Worth using for lookups that can only match one row anyway.
func Unsorted() Option { return func(o *Options) { o.NoSort = true } }

// SkipTotal drops the COUNT round trip; PaginatedResponse.Total then reports
// only what was fetched and TotalPages is 0.
func SkipTotal() Option { return func(o *Options) { o.NoTotal = true } }

// ForUpdate locks the selected rows.
func ForUpdate() Option { return func(o *Options) { o.ForUpdate = true } }

// Distinct adds SELECT DISTINCT.
func Distinct() Option { return func(o *Options) { o.Distinct = true } }

// PreloadRows caps one preloaded relation's materialised children. It is not
// pagination — the cap is an error when exceeded, so no parent quietly loses
// part of its relation. Zero disables the cap for trusted direct repository
// work; public query endpoints declare a positive value in query.Config.
func PreloadRows(n int) Option { return func(o *Options) { o.PreloadRows = n } }

// With replays a prebuilt Options as an Option, so callers can pass a stored
// query shape around.
//
// It carries the relation narrowings, and that is not a detail. `Get` computes
// its total with `Count(ctx, With(o))`; the security gate's narrowings arrive
// only in `o.RelScopes`, so a `With` that dropped them built the COUNT from a
// narrowing-free Options while the SELECT beside it was narrowed. The page then
// showed the rows the caller may see and a `Total` counted over the rows the
// gate hides — a wrong number, and a count oracle over another tenant's rows
// ([[D-007]], [[D-029]]).
//
// After, Before and Agg are deliberately not replayed: a cursor belongs to the
// sort it was made for ([[D-028]]) and an aggregate is a different statement, so
// replaying either into a second query would be carrying state across a boundary
// rather than reusing a shape.
func With(source *Options) Option {
	return func(o *Options) {
		if source == nil {
			return
		}
		o.Filter = append(o.Filter, source.Filter...)
		o.Sort = append(o.Sort, source.Sort...)
		o.Preloads = append(o.Preloads, source.Preloads...)
		o.Fields = append(o.Fields, source.Fields...)
		if source.Page != 0 {
			o.Page = source.Page
		}
		if source.Limit != 0 {
			o.Limit = source.Limit
		}
		if source.Offset != 0 {
			o.Offset = source.Offset
		}
		o.Primary = o.Primary || source.Primary
		o.Unpaged = o.Unpaged || source.Unpaged
		o.NoSort = o.NoSort || source.NoSort
		o.NoTotal = o.NoTotal || source.NoTotal
		o.ForUpdate = o.ForUpdate || source.ForUpdate
		o.Distinct = o.Distinct || source.Distinct
		if source.PreloadRows > 0 {
			o.PreloadRows = source.PreloadRows
		}
		o.RelScopes = MergeRelationScopes(o.RelScopes, source.RelScopes)
	}
}

// Resolved computes the effective limit, offset and 1-based page number for a
// repository's default and maximum page size.
//
// Unpaged is honoured only as far as maxLimit: a repository that declares a
// maximum page size must not be talked out of it by one flag arriving from the
// wire. With no maximum declared, Unpaged really does mean everything.
func (this *Options) Resolved(defLimit, maxLimit int) (limit, offset, page int) {
	if this.Unpaged {
		if maxLimit > 0 {
			return maxLimit, max(this.Offset, 0), 1
		}
		return 0, max(this.Offset, 0), 1
	}
	limit = this.Limit
	if limit <= 0 {
		limit = defLimit
	}
	if maxLimit > 0 && limit > maxLimit {
		limit = maxLimit
	}
	page = this.Page
	if page <= 0 {
		page = 1
	}
	// A page number arrives from a client and is multiplied by the page size,
	// so it is one of the few places an int can wrap. It used to: a wrapped
	// offset is dropped as non-positive, and the caller was handed page one
	// labelled as page 9223372036854775807. Saturating instead asks the
	// database for a page past the end, which is what was requested.
	var off int64
	if limit > 0 && page > 1 {
		if int64(page-1) > math.MaxInt/int64(limit) {
			off = math.MaxInt
		} else {
			off = int64(page-1) * int64(limit)
		}
	}
	offset = int(off)
	if this.Offset > 0 {
		offset = this.Offset
	}
	return limit, offset, page
}
