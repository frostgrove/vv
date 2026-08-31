package crud

import "math"

type Options struct {
	Filter   []Predicate
	Sort     []Order
	Preloads []PreloadSpec
	Fields   []string

	PreloadRows int

	Page   int
	Limit  int
	Offset int

	RelScopes *RelationScopes

	Agg AggregateSpec

	After  string
	Before string

	Primary bool

	Unpaged   bool
	NoSort    bool
	NoTotal   bool
	ForUpdate bool
	Distinct  bool
}

func (this *Options) Cursor() (token string, back, ok bool) {
	switch {
	case this.After != "":
		return this.After, false, true
	case this.Before != "":
		return this.Before, true, true
	}
	return "", false, false
}

type Option func(*Options)

func Build(options ...Option) *Options {
	o := &Options{}
	o.Apply(options...)
	return o
}

func (this *Options) Apply(options ...Option) {
	for _, opt := range options {
		if opt != nil {
			opt(this)
		}
	}
}

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

func Where(p Predicate) Option {
	return func(o *Options) {
		if p != nil {
			o.Filter = append(o.Filter, p)
		}
	}
}

func NarrowRelations(rs *RelationScopes) Option {
	return func(o *Options) {
		if !rs.Empty() {
			o.RelScopes = MergeRelationScopes(o.RelScopes, rs)
		}
	}
}

func After(cursor string) Option {
	return func(o *Options) {
		if cursor != "" {
			o.After, o.Before, o.NoTotal = cursor, "", true
		}
	}
}

func Before(cursor string) Option {
	return func(o *Options) {
		if cursor != "" {
			o.Before, o.After, o.NoTotal = cursor, "", true
		}
	}
}

func Page(n int) Option { return func(o *Options) { o.Page = n } }

func Limit(n int) Option { return func(o *Options) { o.Limit = n } }

func Offset(n int) Option { return func(o *Options) { o.Offset = n } }

func OrderBy(orders ...Order) Option {
	return func(o *Options) { o.Sort = append(o.Sort, orders...) }
}

func SortBy(orders ...Order) Option {
	return func(o *Options) { o.Sort = append(o.Sort[:0:0], orders...) }
}

func Select(fields ...string) Option {
	return func(o *Options) { o.Fields = append(o.Fields, fields...) }
}

func SelectAll() Option { return func(o *Options) { o.Fields = nil } }

func PrimaryOnly() Option { return func(o *Options) { o.Primary = true } }

func Unpaged() Option { return func(o *Options) { o.Unpaged = true } }

func Unsorted() Option { return func(o *Options) { o.NoSort = true } }

func SkipTotal() Option { return func(o *Options) { o.NoTotal = true } }

func ForUpdate() Option { return func(o *Options) { o.ForUpdate = true } }

func Distinct() Option { return func(o *Options) { o.Distinct = true } }

func PreloadRows(n int) Option { return func(o *Options) { o.PreloadRows = n } }

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
