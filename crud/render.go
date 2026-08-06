package crud

// SQL assembles a statement against a dialect and a bound model. It is the
// exported seam that repository implementations build on: predicates resolve
// field references through the schema, bind markers are numbered per dialect,
// and the first resolution failure is remembered instead of panicking.
type SQL struct {
	w writer
}

// NewSQL starts a statement.
func NewSQL(d Dialect, m *Meta) *SQL {
	return &SQL{w: writer{d: d, m: m}}
}

// Alias qualifies every column this statement renders with a table alias, and
// declares that alias as the correlation target for nested paths.
func (b *SQL) Alias(alias string) *SQL {
	b.w.cur = scope{meta: b.w.m, alias: alias}
	return b
}

// Raw appends literal SQL. Nothing is escaped or rewritten.
func (b *SQL) Raw(s string) *SQL { b.w.str(s); return b }

// Ident appends a quoted identifier.
func (b *SQL) Ident(name string) *SQL { b.w.str(b.w.d.Quote(name)); return b }

// Table appends the quoted table name.
func (b *SQL) Table() *SQL { return b.Ident(b.w.m.Table) }

// Column resolves a Go field name (or column name) and appends it quoted.
func (b *SQL) Column(ref string) *SQL { b.w.column(ref); return b }

// Columns appends a comma separated, quoted column list.
func (b *SQL) Columns(fields []*Field) *SQL {
	for i, f := range fields {
		if i > 0 {
			b.w.str(", ")
		}
		b.w.str(b.w.d.Quote(f.Column))
	}
	return b
}

// Bind appends a placeholder and records the argument.
func (b *SQL) Bind(v any) *SQL { b.w.bind(v); return b }

// Binds appends `?, ?, ?` for a whole argument list.
func (b *SQL) Binds(vs []any) *SQL {
	for i, v := range vs {
		if i > 0 {
			b.w.str(", ")
		}
		b.w.bind(v)
	}
	return b
}

// Where appends ` WHERE <p>` when p is non-nil.
func (b *SQL) Where(p Predicate) *SQL {
	if p == nil {
		return b
	}
	b.w.str(" WHERE ")
	p.render(&b.w)
	return b
}

// Predicate appends a predicate without the WHERE keyword.
func (b *SQL) Predicate(p Predicate) *SQL {
	if p != nil {
		p.render(&b.w)
	}
	return b
}

// OrderBy appends ` ORDER BY ...` when there is anything to sort by.
func (b *SQL) OrderBy(orders []Order) *SQL {
	if len(orders) == 0 {
		return b
	}
	b.w.str(" ORDER BY ")
	for i, o := range orders {
		if i > 0 {
			b.w.str(", ")
		}
		o.render(&b.w)
	}
	return b
}

// LimitOffset appends pagination. A limit of zero emits nothing but still
// honours a non-zero offset.
func (b *SQL) LimitOffset(limit, offset int) *SQL {
	if limit > 0 {
		b.w.str(" LIMIT ")
		b.w.str(itoa(limit))
	}
	if offset > 0 {
		if limit <= 0 && b.w.d.Name() == "mysql" {
			// MySQL rejects OFFSET without LIMIT.
			b.w.str(" LIMIT 18446744073709551615")
		}
		b.w.str(" OFFSET ")
		b.w.str(itoa(offset))
	}
	return b
}

// Args returns the collected bind arguments.
func (b *SQL) Args() []any { return b.w.args }

// Err reports the first field-resolution failure, if any.
func (b *SQL) Err() error { return b.w.err }

// String returns the assembled statement.
func (b *SQL) String() string { return b.w.sb.String() }

// Done returns the statement, its arguments and any error in one go.
func (b *SQL) Done() (string, []any, error) {
	return b.w.sb.String(), b.w.args, b.w.err
}

// Dialect exposes the dialect the statement is being built for.
func (b *SQL) Dialect() Dialect { return b.w.d }

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
