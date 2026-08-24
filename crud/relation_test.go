package crud_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/shardit-io/qq/crud"
)

// ---------------------------------------------------------------------------
// The blog fixture. relation_test, predicate_test, render_test and preload_test
// all query this shape, so the SQL they assert reads like the SQL a real caller
// would get.

type Author struct {
	ID   int64            `db:"id,pk,auto"`
	Name string           `db:"name"`
	City crud.Opt[string] `db:"city"`
}

type Tag struct {
	ID   int64  `db:"id,pk,auto"`
	Slug string `db:"slug"`
}

type Comment struct {
	ID        int64  `db:"id,pk,auto"`
	ArticleID int64  `db:"article_id"`
	AuthorID  int64  `db:"author_id"`
	Body      string `db:"body"`
	Approved  bool   `db:"approved"`

	Author *Author `rel:"belongs_to"`
}

type Article struct {
	ID       int64  `db:"id,pk,auto"`
	AuthorID int64  `db:"author_id"`
	Title    string `db:"title"`
	Views    int    `db:"views"`

	Author   *Author   `rel:"belongs_to"`
	Comments []Comment `rel:"has_many"`
	Tags     []Tag     `rel:"many_to_many,join=article_tags"`
	Draft    *Author   `rel:"-"` // opted out: neither a column nor an edge
}

func articleMeta(t *testing.T) *crud.Meta {
	t.Helper()
	return metaOf[Article](t, "articles")
}

func metaOf[M any](t *testing.T, table string) *crud.Meta {
	t.Helper()
	m, err := crud.NewMeta[M](table)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// ---------------------------------------------------------------------------
// tag forms

// A relation kind may be stated outright, and the two column names it joins on
// then default to the conventional ones.
func TestRelationTagStatesTheKind(t *testing.T) {
	m := articleMeta(t)

	for _, tc := range []struct {
		name        string
		kind        crud.RelKind
		toMany      bool
		local       string // field on the article
		remote      string // field on the target
		targetTable string
		join        string
		joinLocal   string
		joinRef     string
	}{
		{name: "Author", kind: crud.BelongsTo, local: "AuthorID", remote: "ID", targetTable: "authors"},
		{name: "Comments", kind: crud.HasMany, toMany: true, local: "ID", remote: "ArticleID", targetTable: "comments"},
		{name: "Tags", kind: crud.ManyToMany, toMany: true, local: "ID", remote: "ID", targetTable: "tags",
			join: "article_tags", joinLocal: "article_id", joinRef: "tag_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rel := m.Relation(tc.name)
			if rel == nil {
				t.Fatalf("no relation named %s", tc.name)
			}
			if rel.Kind != tc.kind {
				t.Fatalf("kind = %s, want %s", rel.Kind, tc.kind)
			}
			if rel.Kind.ToMany() != tc.toMany {
				t.Errorf("%s.ToMany() = %v, want %v", rel.Kind, rel.Kind.ToMany(), tc.toMany)
			}
			target, local, remote, err := rel.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			if local.Name != tc.local || remote.Name != tc.remote {
				t.Errorf("joins %s -> %s, want %s -> %s", local.Name, remote.Name, tc.local, tc.remote)
			}
			if target.Table != tc.targetTable {
				t.Errorf("target table = %q, want %q", target.Table, tc.targetTable)
			}
			if rel.JoinTable != tc.join || rel.JoinLocal != tc.joinLocal || rel.JoinRef != tc.joinRef {
				t.Errorf("join table %q(%q, %q), want %q(%q, %q)",
					rel.JoinTable, rel.JoinLocal, rel.JoinRef, tc.join, tc.joinLocal, tc.joinRef)
			}
		})
	}
}

// Ticket states no kind at all: every edge is inferred from the Go type and
// from whether this struct carries the foreign key.
type Ticket struct {
	ID      int64 `db:"id,pk,auto"`
	OwnerID int64 `db:"owner_id"`

	Owner *Author `rel:""`                  // an OwnerID column exists here -> belongs_to
	Stamp *Stamp  `rel:""`                  // no StampID column -> the far side holds the key
	Lines []Line  `rel:""`                  // a slice -> has_many
	Tags  []Tag   `rel:",join=ticket_tags"` // a slice plus a join table -> many_to_many
}

type Stamp struct {
	ID       int64  `db:"id,pk,auto"`
	TicketID int64  `db:"ticket_id"`
	Code     string `db:"code"`
}

type Line struct {
	ID       int64  `db:"id,pk,auto"`
	TicketID int64  `db:"ticket_id"`
	Text     string `db:"text"`
}

func TestRelationKindIsInferredFromTheGoType(t *testing.T) {
	m := metaOf[Ticket](t, "tickets")

	for _, tc := range []struct {
		name   string
		kind   crud.RelKind
		local  string
		remote string
	}{
		{"Owner", crud.BelongsTo, "OwnerID", "ID"},
		{"Stamp", crud.HasOne, "ID", "TicketID"},
		{"Lines", crud.HasMany, "ID", "TicketID"},
		{"Tags", crud.ManyToMany, "ID", "ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rel := m.Relation(tc.name)
			if rel == nil {
				t.Fatalf("no relation named %s", tc.name)
			}
			if rel.Kind != tc.kind {
				t.Fatalf("kind = %s, want %s", rel.Kind, tc.kind)
			}
			_, local, remote, err := rel.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			if local.Name != tc.local || remote.Name != tc.remote {
				t.Fatalf("joins %s -> %s, want %s -> %s", local.Name, remote.Name, tc.local, tc.remote)
			}
		})
	}
	if rel := m.Relation("Tags"); rel.JoinLocal != "ticket_id" || rel.JoinRef != "tag_id" {
		t.Errorf("join columns = (%q, %q), want the two snake_cased model names", rel.JoinLocal, rel.JoinRef)
	}
}

// Person overrides every default the tag offers, and points at itself — which
// only works because a target is resolved lazily.
type Person struct {
	ID        int64  `db:"id,pk,auto"`
	ManagerNo int64  `db:"manager_no"`
	Name      string `db:"name"`

	Manager *Person  `rel:"belongs_to,fk=ManagerNo"`
	Reports []Person `rel:"has_many,fk=ManagerNo"`
	Badge   *Badge   `rel:"has_one,fk=HolderID,table=id_badges"`
	Aliases []Tag    `rel:"many_to_many,join=person_aliases,joinFK=who_id,joinRef=alias_id"`
}

type Badge struct {
	ID       int64  `db:"id,pk,auto"`
	HolderID int64  `db:"holder_id"`
	Code     string `db:"code"`
}

// Warehouses join their crates on a natural key instead of the primary one.
type Warehouse struct {
	ID   int64  `db:"id,pk,auto"`
	Code string `db:"code"`

	Crates []Crate `rel:"has_many,fk=WarehouseCode,ref=Code"`
}

type Crate struct {
	ID            int64  `db:"id,pk,auto"`
	WarehouseCode string `db:"warehouse_code"`
	Weight        int    `db:"weight"`
}

func TestRelationTagOverrides(t *testing.T) {
	people := metaOf[Person](t, "persons")

	// fk= names the column that holds the key, on whichever side holds it.
	manager := people.Relation("Manager")
	_, local, remote, err := manager.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if local.Name != "ManagerNo" || remote.Name != "ID" {
		t.Errorf("Manager joins %s -> %s, want ManagerNo -> ID", local.Name, remote.Name)
	}
	reports := people.Relation("Reports")
	_, local, remote, err = reports.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if local.Name != "ID" || remote.Name != "ManagerNo" {
		t.Errorf("Reports joins %s -> %s, want ID -> ManagerNo", local.Name, remote.Name)
	}

	// table= overrides the target's table without touching its schema.
	badge, _, _, err := people.Relation("Badge").Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if badge.Table != "id_badges" {
		t.Errorf("Badge target table = %q, want the override id_badges", badge.Table)
	}
	if plain := crud.TableNameOf(reflect.TypeFor[Badge]()); plain != "badges" {
		t.Errorf("the override leaked into the type's own table name: %q", plain)
	}

	// joinFK=/joinRef= name the two columns of the join table.
	aliases := people.Relation("Aliases")
	if aliases.JoinTable != "person_aliases" || aliases.JoinLocal != "who_id" || aliases.JoinRef != "alias_id" {
		t.Errorf("join = %s(%s, %s), want person_aliases(who_id, alias_id)",
			aliases.JoinTable, aliases.JoinLocal, aliases.JoinRef)
	}

	// ref= names the column on the near side, so a relation can hang off a
	// natural key rather than the primary key.
	_, local, remote, err = metaOf[Warehouse](t, "warehouses").Relation("Crates").Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if local.Name != "Code" || remote.Name != "WarehouseCode" {
		t.Fatalf("Crates joins %s -> %s, want Code -> WarehouseCode", local.Name, remote.Name)
	}
}

// A model may reference itself: targets resolve on first use, not at build
// time, so a cycle never deadlocks the schema builder.
func TestSelfReferencingRelationResolves(t *testing.T) {
	people := metaOf[Person](t, "persons")
	target, err := people.Relation("Manager").Target()
	if err != nil {
		t.Fatal(err)
	}
	if target.Type != reflect.TypeFor[Person]() {
		t.Fatalf("Manager targets %s, want Person itself", target.Type)
	}
	if again, _ := people.Relation("Manager").Target(); again != target {
		t.Error("Target must cache: a second call handed back a different *Meta")
	}
}

// A struct-shaped field is never guessed into a column: without a rel tag it is
// invisible, and with rel:"-" it stays invisible even though it looks like one.
func TestStructFieldsWithoutARelTagAreIgnored(t *testing.T) {
	m := articleMeta(t)
	if m.Relation("Draft") != nil {
		t.Error(`rel:"-" must not produce a relation`)
	}
	if m.Field("Draft") != nil {
		t.Error("a struct field must never be mapped as a column")
	}

	type Silent struct {
		ID    int64 `db:"id,pk,auto"`
		Extra *Author
	}
	s, err := crud.SchemaOf[Silent]()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Relations) != 0 || s.Field("Extra") != nil {
		t.Errorf("an untagged struct field became %d relations / field %v", len(s.Relations), s.Field("Extra"))
	}
}

// ---------------------------------------------------------------------------
// table names

type Continent struct {
	ID   int64  `db:"id,pk,auto"`
	Name string `db:"name"`
}

func (Continent) TableName() string { return "the_continents" }

type Registered struct {
	ID int64 `db:"id,pk,auto"`
}

type Category struct {
	ID int64 `db:"id,pk,auto"`
}

type Box struct {
	ID int64 `db:"id,pk,auto"`
}

func TestTableNameOf(t *testing.T) {
	crud.RegisterTable[Registered]("pinned_elsewhere")

	for _, tc := range []struct {
		name string
		typ  reflect.Type
		want string
	}{
		{"an explicit registration wins", reflect.TypeFor[Registered](), "pinned_elsewhere"},
		{"a Tabler names itself", reflect.TypeFor[Continent](), "the_continents"},
		{"otherwise snake_case plural", reflect.TypeFor[Author](), "authors"},
		{"...pluralised properly", reflect.TypeFor[Category](), "categories"},
		{"...including the sibilants", reflect.TypeFor[Box](), "boxes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := crud.TableNameOf(tc.typ); got != tc.want {
				t.Fatalf("TableNameOf(%s) = %q, want %q", tc.typ, got, tc.want)
			}
		})
	}
}

// A relation to a Tabler picks the table up without anyone registering it.
func TestRelationTargetUsesTheTablerName(t *testing.T) {
	type Country struct {
		ID          int64 `db:"id,pk,auto"`
		ContinentID int64 `db:"continent_id"`

		Continent *Continent `rel:"belongs_to"`
	}
	target, _, _, err := metaOf[Country](t, "countries").Relation("Continent").Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if target.Table != "the_continents" {
		t.Fatalf("target table = %q, want the name the model gave itself", target.Table)
	}
}

// ---------------------------------------------------------------------------
// paths

// WalkPath is the single source of truth every layer resolves through, and the
// canonical spelling it returns is what errors and echoes are phrased in.
func TestWalkPath(t *testing.T) {
	m := articleMeta(t)

	for _, tc := range []struct {
		name      string
		path      string
		hops      []string // relation names crossed, in order
		field     string   // terminal column, empty when the path stops on a relation
		canonical string
	}{
		{"an own column", "Title", nil, "title", "Title"},
		{"one hop", "Author.Name", []string{"Author"}, "name", "Author.Name"},
		{"a to-many hop", "Comments.Body", []string{"Comments"}, "body", "Comments.Body"},
		{"two hops", "Comments.Author.Name", []string{"Comments", "Author"}, "name", "Comments.Author.Name"},
		{"a path may stop on a relation", "Comments", []string{"Comments"}, "", "Comments"},
		{"...even a nested one", "Comments.Author", []string{"Comments", "Author"}, "", "Comments.Author"},

		// The forgiving spelling: a client sends whatever case it likes and
		// still gets the model's own vocabulary back.
		{"column names resolve too", "author_id", nil, "author_id", "AuthorID"},
		{"camelCase", "authorId", nil, "author_id", "AuthorID"},
		{"lower case, all the way down", "comments.author.name", []string{"Comments", "Author"}, "name", "Comments.Author.Name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hops, field, canonical, err := m.WalkPath(tc.path)
			if err != nil {
				t.Fatal(err)
			}
			if got := hopNames(hops); strings.Join(got, ".") != strings.Join(tc.hops, ".") {
				t.Errorf("hops = %v, want %v", got, tc.hops)
			}
			switch {
			case tc.field == "" && field != nil:
				t.Errorf("field = %q, want the path to stop on a relation", field.Column)
			case tc.field != "" && (field == nil || field.Column != tc.field):
				t.Errorf("field = %v, want column %q", field, tc.field)
			}
			if canonical != tc.canonical {
				t.Errorf("canonical = %q, want %q", canonical, tc.canonical)
			}
		})
	}
}

// Every hop carries the two columns it joins on, so the writer never has to
// re-derive them.
func TestWalkPathHopsCarryTheirJoinColumns(t *testing.T) {
	hops, _, _, err := articleMeta(t).WalkPath("Comments.Author.Name")
	if err != nil {
		t.Fatal(err)
	}
	if len(hops) != 2 {
		t.Fatalf("hops = %v, want two", hopNames(hops))
	}
	if hops[0].Local.Column != "id" || hops[0].Remote.Column != "article_id" || hops[0].Target.Table != "comments" {
		t.Errorf("first hop joins %s -> %s.%s, want id -> comments.article_id",
			hops[0].Local.Column, hops[0].Target.Table, hops[0].Remote.Column)
	}
	if hops[1].Local.Column != "author_id" || hops[1].Remote.Column != "id" || hops[1].Target.Table != "authors" {
		t.Errorf("second hop joins %s -> %s.%s, want author_id -> authors.id",
			hops[1].Local.Column, hops[1].Target.Table, hops[1].Remote.Column)
	}
}

func TestFieldAtAndRelationAt(t *testing.T) {
	m := articleMeta(t)

	f, canonical, err := m.FieldAt("comments.author.name")
	if err != nil {
		t.Fatal(err)
	}
	if f.Column != "name" || canonical != "Comments.Author.Name" {
		t.Errorf("FieldAt = (%s, %s), want (name, Comments.Author.Name)", f.Column, canonical)
	}
	if _, _, err := m.FieldAt("Comments"); err == nil {
		t.Error("FieldAt must refuse a path that stops on a relation — there is no column to compare")
	}

	rel, canonical, err := m.RelationAt("comments.author")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Name != "Author" || rel.Owner.Name != "Comment" || canonical != "Comments.Author" {
		t.Errorf("RelationAt = (%s.%s, %s), want (Comment.Author, Comments.Author)", rel.Owner.Name, rel.Name, canonical)
	}
	if _, _, err := m.RelationAt("Title"); err == nil {
		t.Error("RelationAt must refuse a column — there is nothing to preload")
	}
}

func TestWalkPathRejectsUnknownSegments(t *testing.T) {
	m := articleMeta(t)
	for _, path := range []string{"Nope", "Author.Nope", "Title.Nope", "Nope.Name"} {
		if _, _, _, err := m.WalkPath(path); err == nil {
			t.Errorf("WalkPath(%q) was accepted; an unknown path has to be named, not rendered", path)
		}
	}
}

func hopNames(hops []crud.PathHop) []string {
	out := make([]string, len(hops))
	for i, h := range hops {
		out[i] = h.Rel.Name
	}
	return out
}
