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

			{Name: "docs_slug_partial", Table: "docs", Kind: catalog.KindUniqueIndex, Columns: []string{"slug"}, Partial: true},
			{Name: "docs_email_prefix", Table: "docs", Kind: catalog.KindUniqueIndex, Columns: []string{"email"}, Prefixes: []int{8}},
			{Name: "docs_lower_email", Table: "docs", Kind: catalog.KindUniqueIndex, Columns: []string{""}, Expressions: []string{"lower(email)"}},
			{Name: "docs_zone_def_uk", Table: "docs", Kind: catalog.KindUnique, Columns: []string{"zone"}, Deferrable: true},

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

func (this *fakeCatalog) Dialect() string { return this.dialect }

func (this *fakeCatalog) Table(name string) (*catalog.Table, bool) {
	for i := range this.tables {
		if this.tables[i].Name == name {
			return &this.tables[i], true
		}
	}
	return nil, false
}

func (this *fakeCatalog) Constraint(table, name string) (*catalog.Constraint, bool) {
	t, ok := this.Table(table)
	if !ok {
		return nil, false
	}
	return t.Constraint(name)
}

func (this *fakeCatalog) ReferencedBy(table string) []*catalog.Constraint { return this.refs[table] }

type qualifiedFakeCatalog struct{ *fakeCatalog }

func (this *qualifiedFakeCatalog) TableByRef(ref crud.TableRef) (*catalog.Table, bool) {
	if ref.Schema == "" {
		return this.Table(ref.Name)
	}
	for i := range this.tables {
		if this.tables[i].Schema == ref.Schema && this.tables[i].Name == ref.Name {
			return &this.tables[i], true
		}
	}
	return nil, false
}

func (this *qualifiedFakeCatalog) ConstraintByRef(ref crud.TableRef, name string) (*catalog.Constraint, bool) {
	table, ok := this.TableByRef(ref)
	if !ok {
		return nil, false
	}
	return table.Constraint(name)
}

func (this *qualifiedFakeCatalog) ReferencedByRef(ref crud.TableRef) []*catalog.Constraint {
	var out []*catalog.Constraint
	for i := range this.tables {
		for j := range this.tables[i].Constraints {
			con := &this.tables[i].Constraints[j]
			if con.Kind == catalog.KindForeignKey && con.RefSchema == ref.Schema && con.RefTable == ref.Name {
				out = append(out, con)
			}
		}
	}
	return out
}

func newUnique(name, column string) catalog.Constraint {
	return catalog.Constraint{Name: name, Table: "docs", Kind: catalog.KindUnique, Columns: []string{column}}
}

var (
	_ catalog.Catalog            = (*fakeCatalog)(nil)
	_ catalog.Referrers          = (*fakeCatalog)(nil)
	_ catalog.QualifiedCatalog   = (*qualifiedFakeCatalog)(nil)
	_ catalog.QualifiedReferrers = (*qualifiedFakeCatalog)(nil)
)

func declared(t *testing.T, cat catalog.Catalog, o ...Option) *full {
	t.Helper()
	h, err := Full(cat, o...).(*full).Declare(docMeta(t))
	if err != nil {
		t.Fatalf("declaring the probe: %v", err)
	}
	return h.(*full)
}

func answer(cells ...any) crudtest.Result { return crudtest.Rows(cells) }

func conflict(constraint string, cols ...string) *errs.Fault {
	b := errs.Conflict().Code(errs.CodeUnique).
		General().Code(errs.CodeUnique).Origin(errs.OriginState)
	if constraint != "" || len(cols) > 0 {
		b = b.Source(errs.Source{Table: "docs", Columns: cols, Constraint: constraint})
	}
	return b.Fault()
}

func request(f *errs.Fault, source crud.Source, meta *crud.Meta, rows ...Row) *Request {
	return &Request{
		Op: "Save", Fault: f, Meta: meta, Source: source, Rows: rows,
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
