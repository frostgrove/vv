package probe

import (
	"context"
	"strings"
	"testing"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/catalog"
	"github.com/frostgrove/vv/crud/crudtest"
	"github.com/frostgrove/vv/errs"
)

// The fixture every unit test in this package reads.
//
// It is built by hand rather than loaded, because what is under test here is
// what the probe does with a schema and not how a schema is read — that is
// catalog's own suite, live on four engines. Every shape the probe has a rule
// for is present with its opposite beside it, so an assertion is a control
// rather than a statement: a reproducible unique key beside a partial one, a
// prefix one, an expression one and a deferrable one; a nullable foreign key
// beside a composite one; a restricting inbound key beside a cascading one.

// Doc is the model. The columns exist so that each has exactly one job.
type Doc struct {
	ID       int64            `db:"id,pk,auto"`
	TenantID int64            `db:"tenant_id"`
	Slug     string           `db:"slug"`
	Email    string           `db:"email"`
	OrgID    crud.Opt[int64]  `db:"org_id"`
	RegionID crud.Opt[int64]  `db:"region_id"`
	Zone     crud.Opt[string] `db:"zone"`
	Code     string           `db:"code"`
}

func docMeta(t *testing.T) *crud.Meta {
	t.Helper()
	m, err := crud.NewMeta[Doc]("docs")
	if err != nil {
		t.Fatalf("building the model metadata: %v", err)
	}
	return m
}

func col(name string, nullable bool) catalog.Column {
	return catalog.Column{Name: name, Type: "text", Nullable: nullable}
}

func fixture() *fakeCatalog {
	docs := catalog.Table{
		Name:   "docs",
		Schema: "public",
		Columns: []catalog.Column{
			col("id", false), col("tenant_id", false), col("slug", false),
			col("email", false), col("org_id", true), col("region_id", true),
			col("zone", true), col("code", false),
		},
		PrimaryKey: []string{"id"},
		Constraints: []catalog.Constraint{
			{Name: "docs_pkey", Table: "docs", Kind: catalog.KindPrimaryKey, Columns: []string{"id"}},
			{Name: "docs_email_uk", Table: "docs", Kind: catalog.KindUnique, Columns: []string{"email"}},
			{Name: "docs_tenant_slug_uk", Table: "docs", Kind: catalog.KindUnique, Columns: []string{"tenant_id", "slug"}},
			{Name: "docs_code_uk", Table: "docs", Kind: catalog.KindUnique, Columns: []string{"code"}},
			// The four the probe must never replay, each beside a twin above
			// that it must.
			{Name: "docs_slug_partial", Table: "docs", Kind: catalog.KindUniqueIndex, Columns: []string{"slug"}, Partial: true},
			{Name: "docs_email_prefix", Table: "docs", Kind: catalog.KindUniqueIndex, Columns: []string{"email"}, Prefixes: []int{8}},
			{Name: "docs_lower_email", Table: "docs", Kind: catalog.KindUniqueIndex, Columns: []string{""}, Expressions: []string{"lower(email)"}},
			{Name: "docs_zone_def_uk", Table: "docs", Kind: catalog.KindUnique, Columns: []string{"zone"}, Deferrable: true},
			// A nullable single foreign key and a composite one.
			{Name: "docs_org_fk", Table: "docs", Kind: catalog.KindForeignKey,
				Columns: []string{"org_id"}, RefTable: "orgs", RefColumns: []string{"id"}},
			{Name: "docs_region_fk", Table: "docs", Kind: catalog.KindForeignKey,
				Columns: []string{"region_id", "zone"}, RefTable: "regions", RefColumns: []string{"id", "zone"}},
		},
	}
	orgs := catalog.Table{Name: "orgs", Schema: "public",
		Columns: []catalog.Column{col("id", false)}, PrimaryKey: []string{"id"}}
	regions := catalog.Table{Name: "regions", Schema: "public",
		Columns: []catalog.Column{col("id", false), col("zone", false)}, PrimaryKey: []string{"id"}}
	// notes points at docs.code and refuses; audits points at it and cascades,
	// which is the control for the restrict rule.
	notes := catalog.Table{Name: "notes", Schema: "public",
		Columns:    []catalog.Column{col("id", false), col("doc_code", false)},
		PrimaryKey: []string{"id"},
		Constraints: []catalog.Constraint{
			{Name: "notes_code_fk", Table: "notes", Kind: catalog.KindForeignKey,
				Columns: []string{"doc_code"}, RefTable: "docs", RefColumns: []string{"code"},
				OnUpdate: "NO ACTION", OnDelete: "NO ACTION"},
		}}
	audits := catalog.Table{Name: "audits", Schema: "public",
		Columns:    []catalog.Column{col("id", false), col("doc_code", false)},
		PrimaryKey: []string{"id"},
		Constraints: []catalog.Constraint{
			{Name: "audits_code_fk", Table: "audits", Kind: catalog.KindForeignKey,
				Columns: []string{"doc_code"}, RefTable: "docs", RefColumns: []string{"code"},
				OnUpdate: "CASCADE", OnDelete: "CASCADE"},
		}}
	return newFakeCatalog("postgres", docs, orgs, regions, notes, audits)
}

// fakeCatalog is a Catalog and a Referrers over a fixed list of tables.
type fakeCatalog struct {
	dialect string
	tables  []catalog.Table
	refs    map[string][]*catalog.Constraint
}

func newFakeCatalog(dialect string, tables ...catalog.Table) *fakeCatalog {
	c := &fakeCatalog{dialect: dialect, tables: tables, refs: map[string][]*catalog.Constraint{}}
	for i := range c.tables {
		for j := range c.tables[i].Constraints {
			k := &c.tables[i].Constraints[j]
			if k.Kind == catalog.KindForeignKey && k.RefTable != "" {
				c.refs[k.RefTable] = append(c.refs[k.RefTable], k)
			}
		}
	}
	return c
}

func (c *fakeCatalog) Dialect() string { return c.dialect }

func (c *fakeCatalog) Table(name string) (*catalog.Table, bool) {
	for i := range c.tables {
		if c.tables[i].Name == name {
			return &c.tables[i], true
		}
	}
	return nil, false
}

func (c *fakeCatalog) Constraint(table, name string) (*catalog.Constraint, bool) {
	t, ok := c.Table(table)
	if !ok {
		return nil, false
	}
	return t.Constraint(name)
}

func (c *fakeCatalog) ReferencedBy(table string) []*catalog.Constraint { return c.refs[table] }

// newUnique is a plain single-column unique key, for a test that needs one more.
func newUnique(name, column string) catalog.Constraint {
	return catalog.Constraint{Name: name, Table: "docs", Kind: catalog.KindUnique, Columns: []string{column}}
}

var (
	_ catalog.Catalog   = (*fakeCatalog)(nil)
	_ catalog.Referrers = (*fakeCatalog)(nil)
)

// declared builds a probe bound to the Doc model, failing the test rather than
// returning an error nobody would look at.
func declared(t *testing.T, cat catalog.Catalog, o ...Option) *full {
	t.Helper()
	h, err := Full(cat, o...).(*full).Declare(docMeta(t))
	if err != nil {
		t.Fatalf("declaring the probe: %v", err)
	}
	return h.(*full)
}

// answer builds a result set for one probe row: the cells, in term order.
func answer(cells ...any) crudtest.Result { return crudtest.Rows(cells) }

// conflict is what an adapter hands the decorator: a classified fault carrying
// one violation and whatever the driver managed to name.
func conflict(constraint string, cols ...string) *errs.Fault {
	b := errs.Conflict().Code(errs.CodeUnique).
		General().Code(errs.CodeUnique).Origin(errs.OriginState)
	if constraint != "" || len(cols) > 0 {
		b = b.Source(errs.Source{Table: "docs", Columns: cols, Constraint: constraint})
	}
	return b.Fault()
}

// request is the shape the faults decorator hands over, with the resolve hop
// wired to the Doc model.
func request(f *errs.Fault, src crud.Source, meta *crud.Meta, rows ...Row) Request {
	return Request{
		Op: "Save", Fault: f, Meta: meta, Source: src, Rows: rows,
		Resolve: func(table string, columns []string) (errs.Path, bool) {
			if table != "docs" {
				return nil, false
			}
			var out []errs.Path
			for _, c := range columns {
				fld := meta.Schema.Field(c)
				if fld == nil {
					return nil, false
				}
				out = append(out, errs.Path{errs.Named(fld.Name)})
			}
			if len(out) == 1 {
				return out[0], true
			}
			return nil, true
		},
	}
}

func row(vals map[string]any) Row { return Row{Values: vals} }

func idRow(id any, vals map[string]any) Row { return Row{Values: vals, ID: id, HasID: true} }

// codesAt renders the answer as the set of (code, path) pairs a client sees.
func codesAt(f *errs.Fault) []string {
	out := make([]string, 0, len(f.Violations))
	for _, v := range f.Violations {
		out = append(out, string(v.Code)+"@"+v.Path.String())
	}
	return out
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// only fails unless the fault carries exactly the listed (code, path) pairs.
func only(t *testing.T, f *errs.Fault, want ...string) {
	t.Helper()
	got := codesAt(f)
	if len(got) != len(want) {
		t.Fatalf("the answer carries %d violations, want %d: %v", len(got), len(want), got)
	}
	for _, w := range want {
		if !has(got, w) {
			t.Fatalf("the answer is missing %s: %v", w, got)
		}
	}
}

func lastSQL(rec *crudtest.Recorder) string { return crudtest.Normalize(rec.Last().SQL) }

func contains(s, sub string) bool { return strings.Contains(s, sub) }

var ctx = context.Background()
