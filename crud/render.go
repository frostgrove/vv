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
func (this *SQL) Alias(alias string) *SQL {
	this.w.cur = scope{meta: this.w.m, alias: alias}
	return this
}

// RelationScopes declares the narrowing that follows this statement into every
// table a relation hop opens — the EXISTS a nested filter builds and the scalar
// subquery a nested sort builds. Without it those subqueries read their tables
// raw, whatever the statement's own WHERE says.
func (this *SQL) RelationScopes(rs *RelationScopes) *SQL {
	if !rs.Empty() {
		this.w.rel = rs
	}
	return this
}

// Raw appends literal SQL. Nothing is escaped or rewritten.
func (this *SQL) Raw(s string) *SQL { this.w.str(s); return this }

// Ident appends a quoted identifier.
func (this *SQL) Ident(name string) *SQL { this.w.str(this.w.d.Quote(name)); return this }

// Table appends the quoted table name.
func (this *SQL) Table() *SQL { return this.Ident(this.w.m.Table) }

// Column resolves a Go field name (or column name) and appends it quoted.
func (this *SQL) Column(ref string) *SQL { this.w.column(ref); return this }

// Columns appends a comma separated, quoted column list.
func (this *SQL) Columns(fields []*Field) *SQL {
	for i, f := range fields {
		if i > 0 {
			this.w.str(", ")
		}
		this.w.str(this.w.d.Quote(f.Column))
	}
	return this
}

// Bind appends a placeholder and records the argument.
func (this *SQL) Bind(v any) *SQL { this.w.bind(v); return this }

// Binds appends `?, ?, ?` for a whole argument list.
func (this *SQL) Binds(vs []any) *SQL {
	for i, v := range vs {
		if i > 0 {
			this.w.str(", ")
		}
		this.w.bind(v)
	}
	return this
}

// Where appends ` WHERE <p>` when p is non-nil.
func (this *SQL) Where(p Predicate) *SQL {
	if p == nil {
		return this
	}
	this.w.str(" WHERE ")
	p.render(&this.w)
	return this
}

// Predicate appends a predicate without the WHERE keyword.
func (this *SQL) Predicate(p Predicate) *SQL {
	if p != nil {
		p.render(&this.w)
	}
	return this
}

// OrderBy appends ` ORDER BY ...` when there is anything to sort by.
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

// LimitOffset appends pagination. A limit of zero emits nothing but still
// honours a non-zero offset.
func (this *SQL) LimitOffset(limit, offset int) *SQL {
	if limit > 0 {
		this.w.str(" LIMIT ")
		this.w.str(itoa(limit))
	}
	if offset > 0 {
		if limit <= 0 {
			// Not every grammar has a bare OFFSET, and the ones that do not
			// spell "no limit" differently. Asking the dialect rather than
			// checking its name for "mysql" is what SQLite needs: it used to be
			// handed `OFFSET 5` on its own and answer `near "5": syntax error`,
			// reachable straight from the wire as {"unpaged":true,"offset":5}.
			if d, ok := this.w.d.(OffsetLimiter); ok {
				this.w.str(d.LimitAll())
			}
		}
		this.w.str(" OFFSET ")
		this.w.str(itoa(offset))
	}
	return this
}

// Args returns the collected bind arguments.
func (this *SQL) Args() []any { return this.w.args }

// Err reports the first field-resolution failure, if any.
func (this *SQL) Err() error { return this.w.err }

// String returns the assembled statement.
func (this *SQL) String() string { return this.w.sb.String() }

// Done returns the statement, its arguments and any error in one go.
func (this *SQL) Done() (string, []any, error) {
	return this.w.sb.String(), this.w.args, this.w.err
}

// Dialect exposes the dialect the statement is being built for.
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
