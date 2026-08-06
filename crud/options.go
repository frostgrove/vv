package crud

// Options is the resolved query shape. Decorators receive and may inspect it;
// the fields are exported for exactly that reason.
type Options struct {
	// Filter is ANDed together. Where appends, it never replaces — that is what
	// lets a security decorator inject a scope the caller cannot remove.
	Filter   []Predicate
	Sort     []Order
	Preloads []PreloadSpec
	Fields   []string // projection; empty means every column

	Page   int // 1-based; 0 means the first page
	Limit  int // 0 means "repository default"
	Offset int // explicit offset wins over Page

	Unpaged   bool // ignore Page/Limit and return everything
	NoSort    bool // drop the default sort and the stable-pagination tiebreaker
	NoTotal   bool // skip the COUNT query; Total is then the page length
	ForUpdate bool // SELECT ... FOR UPDATE
	Distinct  bool
}

// Option mutates Options. Options are applied left to right.
type Option func(*Options)

// Build resolves a list of options into an Options value.
func Build(opts ...Option) *Options {
	o := &Options{}
	o.Apply(opts...)
	return o
}

// Apply runs more options over an existing Options.
func (o *Options) Apply(opts ...Option) {
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
}

// Predicate folds Filter into a single AND node, or nil when empty.
func (o *Options) Predicate() Predicate {
	switch len(o.Filter) {
	case 0:
		return nil
	case 1:
		return o.Filter[0]
	default:
		return And(o.Filter...)
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

// With replays a prebuilt Options as an Option, so callers can pass a stored
// query shape around.
func With(src *Options) Option {
	return func(o *Options) {
		if src == nil {
			return
		}
		o.Filter = append(o.Filter, src.Filter...)
		o.Sort = append(o.Sort, src.Sort...)
		o.Preloads = append(o.Preloads, src.Preloads...)
		o.Fields = append(o.Fields, src.Fields...)
		if src.Page != 0 {
			o.Page = src.Page
		}
		if src.Limit != 0 {
			o.Limit = src.Limit
		}
		if src.Offset != 0 {
			o.Offset = src.Offset
		}
		o.Unpaged = o.Unpaged || src.Unpaged
		o.NoSort = o.NoSort || src.NoSort
		o.NoTotal = o.NoTotal || src.NoTotal
		o.ForUpdate = o.ForUpdate || src.ForUpdate
		o.Distinct = o.Distinct || src.Distinct
	}
}

// Resolved computes the effective limit, offset and 1-based page number for a
// repository's default and maximum page size.
func (o *Options) Resolved(defLimit, maxLimit int) (limit, offset, page int) {
	if o.Unpaged {
		return 0, max(o.Offset, 0), 1
	}
	limit = o.Limit
	if limit <= 0 {
		limit = defLimit
	}
	if maxLimit > 0 && limit > maxLimit {
		limit = maxLimit
	}
	page = o.Page
	if page <= 0 {
		page = 1
	}
	offset = (page - 1) * limit
	if o.Offset > 0 {
		offset = o.Offset
	}
	return limit, offset, page
}
