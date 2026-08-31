package crud

import "fmt"

type SQL struct {
	w writer
}

func NewSQL(d Dialect, m *Meta) *SQL {
	return &SQL{w: writer{d: d, m: m}}
}

func (this *SQL) Alias(alias string) *SQL {
	this.w.cur = scope{meta: this.w.m, alias: alias}
	return this
}

func (this *SQL) RelationScopes(rs *RelationScopes) *SQL {
	if !rs.Empty() {
		this.w.rel = rs
	}
	return this
}

func (this *SQL) Raw(s string) *SQL { this.w.str(s); return this }

func (this *SQL) Ident(name string) *SQL { this.w.str(this.w.d.Quote(name)); return this }

func (this *SQL) TableRef(table TableRef) *SQL {
	if err := table.Validate(); err != nil {
		this.w.fail(err)
		return this
	}
	this.w.str(quoteTable(this.w.d, table))
	return this
}

func (this *SQL) Table() *SQL { return this.TableRef(this.w.m.TableReference()) }

func (this *SQL) Column(ref string) *SQL { this.w.column(ref); return this }

func (this *SQL) Columns(fields []*Field) *SQL {
	for i, f := range fields {
		if i > 0 {
			this.w.str(", ")
		}
		this.w.str(this.w.d.Quote(f.Column))
	}
	return this
}

func (this *SQL) Bind(v any) *SQL { this.w.bind(v); return this }

func (this *SQL) Binds(vs []any) *SQL {
	for i, v := range vs {
		if i > 0 {
			this.w.str(", ")
		}
		this.w.bind(v)
	}
	return this
}

func (this *SQL) Where(p Predicate) *SQL {
	if p == nil {
		return this
	}
	this.w.str(" WHERE ")
	p.render(&this.w)
	return this
}

func (this *SQL) Predicate(p Predicate) *SQL {
	if p != nil {
		p.render(&this.w)
	}
	return this
}

func (this *SQL) OrderBy(orders []Order) *SQL {
	if len(orders) == 0 {
		return this
	}
	this.w.str(" ORDER BY ")
	for i, o := range orders {
		if i > 0 {
			this.w.str(", ")
		}
		o.render(&this.w)
	}
	return this
}

func (this *SQL) LimitOffset(limit, offset int) *SQL {
	if limit > 0 {
		this.w.str(" LIMIT ")
		this.w.str(itoa(limit))
	}
	if offset > 0 {
		if limit <= 0 {
			if d, ok := this.w.d.(OffsetLimiter); ok {
				this.w.str(d.LimitAll())
			}
		}
		this.w.str(" OFFSET ")
		this.w.str(itoa(offset))
	}
	return this
}

func (this *SQL) Args() []any { return this.w.args }

func (this *SQL) Err() error {
	if this.w.err != nil {
		return this.w.err
	}
	limit := BindLimit(this.w.d)
	if len(this.w.args) <= limit {
		return nil
	}
	model := "statement"
	if this.w.m != nil && this.w.m.Name != "" {
		model = this.w.m.Name
	}
	return &SchemaError{Model: model, Reason: fmt.Sprintf(
		"statement needs %d bound values, but dialect %q permits at most %d; reduce the predicate or use a temporary table or bulk API",
		len(this.w.args), this.w.d.Name(), limit)}
}

func (this *SQL) String() string { return this.w.sb.String() }

func (this *SQL) Done() (string, []any, error) {
	return this.w.sb.String(), this.w.args, this.Err()
}

func (this *SQL) Dialect() Dialect { return this.w.d }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
