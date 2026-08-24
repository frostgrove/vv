package crud_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/shardit-io/qq/crud"
)

// schemaErrOf is the table-friendly spelling of SchemaOf: it throws the schema
// away and keeps the complaint.
func schemaErrOf[M any]() func() error {
	return func() error {
		_, err := crud.SchemaOf[M]()
		return err
	}
}

// wantSchemaError insists on the error *identity* a caller can branch on, then
// on the parts of the message a human needs: which field, and why.
func wantSchemaError(t *testing.T, err error, field, reason string) {
	t.Helper()
	var se *crud.SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("err = %v (%T), want a *crud.SchemaError", err, err)
	}
	if field != "" && se.Field != field {
		t.Errorf("error blames %q, want it to name %s", se.Field, field)
	}
	if !strings.Contains(se.Reason, reason) {
		t.Errorf("reason = %q, want it to mention %q", se.Reason, reason)
	}
	if se.Model == "" {
		t.Errorf("error does not say which model it is about: %v", se)
	}
}

// ---------------------------------------------------------------------------
// declaration errors
//
// A broken mapping has to be caught where it is written, not where it is used:
// every one of these is an error at Define time, never a panic and never a
// column that quietly does the wrong thing.

func TestSchemaRefusesBrokenModels(t *testing.T) {
	type NoColumns struct{}
	type NoKey struct {
		Name string `db:"name"`
	}
	type TwoKeys struct {
		A int `db:"a,pk"`
		B int `db:"b,pk"`
	}
	type UnknownOption struct {
		ID int `db:"id,pk,srsly"`
	}
	type DuplicateColumn struct {
		ID int    `db:"id,pk"`
		A  string `db:"x"`
		B  string `db:"x"`
	}
	type Named struct {
		Name string `db:"name"`
	}
	type DuplicateName struct {
		ID int `db:"id,pk"`
		Named
		Name string `db:"other_name"`
	}
	type GeneratedKey struct {
		ID int `db:"id,pk,generated"`
	}
	type EmbeddedPointer struct {
		*Named
		ID int `db:"id,pk"`
	}

	for _, tc := range []struct {
		name   string
		build  func() error
		field  string
		reason string
	}{
		{"a model has to be a struct", schemaErrOf[int](), "", "must be a struct"},
		{"a slice is not a model either", schemaErrOf[[]Author](), "", "must be a struct"},
		{"a struct with nothing to map", schemaErrOf[NoColumns](), "", "no mapped columns"},
		{"no primary key, and nothing called ID to fall back to", schemaErrOf[NoKey](), "", "no primary key"},
		{"two primary keys", schemaErrOf[TwoKeys](), "B", "composite primary keys"},
		{"a tag option nobody knows", schemaErrOf[UnknownOption](), "ID", "unknown tag option srsly"},
		{"two fields claiming the same column", schemaErrOf[DuplicateColumn](), "B", "duplicate column x"},
		{"an embedded field shadowed by an outer one of the same name",
			schemaErrOf[DuplicateName](), "Name", "duplicate field name"},
		// `generated` means "never written"; a key that is never written can
		// never be handed back after an insert. `auto` is the tag that was meant.
		{"a generated primary key", schemaErrOf[GeneratedKey](), "ID", "use `auto`"},
		{"an embedded pointer to a struct", schemaErrOf[EmbeddedPointer](), "Named", "embedded pointer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantSchemaError(t, tc.build(), tc.field, tc.reason)
		})
	}
}

// A field the reflection layer cannot read must not be mapped silently: a
// lower-case letter in a field name would otherwise cost a whole column, and
// the row would come back with a zero in it.
func TestADbTagOnAnUnexportedFieldIsRefused(t *testing.T) {
	type Typo struct {
		ID   int64  `db:"id,pk"`
		name string `db:"name"`
	}
	wantSchemaError(t, schemaErrOf[Typo]()(), "name", "unexported")

	// The documented escape hatch still works: `db:"-"` is how a field says it
	// is none of the mapper's business, whatever its case.
	type Quiet struct {
		ID     int64  `db:"id,pk"`
		secret string `db:"-"`
		Loud   string `db:"loud"`
	}
	s, err := crud.SchemaOf[Quiet]()
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Columns(); !equal(got, []string{"id", "loud"}) {
		t.Fatalf("columns = %v, want the tagged-out field gone and nothing else", got)
	}
}

// An untagged unexported field is not a mistake — it is how Go structs carry
// state — so it is skipped, not complained about.
func TestAnUntaggedUnexportedFieldIsSimplyIgnored(t *testing.T) {
	type WithState struct {
		ID    int64 `db:"id,pk"`
		Name  string
		dirty bool
	}
	s, err := crud.SchemaOf[WithState]()
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Columns(); !equal(got, []string{"id", "name"}) {
		t.Fatalf("columns = %v, want unexported state left out", got)
	}
}

// The primary key may be declared by convention rather than by tag, and both
// conventions have to keep working — plenty of models never write `pk` at all.
func TestPrimaryKeyFallsBackToIDByNameThenByColumn(t *testing.T) {
	type ByName struct {
		ID   int64
		Name string
	}
	type ByColumn struct {
		Key  int64 `db:"id"`
		Name string
	}
	type ByNameNotAuto struct {
		ID   string
		Name string
	}

	for _, tc := range []struct {
		name  string
		build func() (*crud.Schema, error)
		pk    string
		auto  bool
	}{
		{"a field called ID", func() (*crud.Schema, error) { return crud.SchemaOf[ByName]() }, "ID", true},
		{"a column called id", func() (*crud.Schema, error) { return crud.SchemaOf[ByColumn]() }, "Key", true},
		{"a string key is assigned, not generated",
			func() (*crud.Schema, error) { return crud.SchemaOf[ByNameNotAuto]() }, "ID", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := tc.build()
			if err != nil {
				t.Fatal(err)
			}
			if s.PK == nil || s.PK.Name != tc.pk {
				t.Fatalf("pk = %v, want %s", s.PK, tc.pk)
			}
			if s.PK.Auto != tc.auto {
				t.Fatalf("pk.Auto = %v, want %v", s.PK.Auto, tc.auto)
			}
			if s.PK.Ordinal != 0 || s.Columns()[0] != s.PK.Column {
				t.Fatalf("columns = %v, want the key first whatever order it was declared in", s.Columns())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// relation declaration errors

func TestRelationTagsAreCheckedWhenTheyAreDeclared(t *testing.T) {
	type NotAStruct struct {
		ID    int64 `db:"id,pk"`
		Count int   `rel:"has_many"`
	}
	type UnknownKind struct {
		ID   int64   `db:"id,pk"`
		Peer *Author `rel:"belongs_to_ish"`
	}
	type UnknownOption struct {
		ID   int64   `db:"id,pk"`
		Peer *Author `rel:"belongs_to,onDelete=cascade"`
	}
	type CollectionOnASingleField struct {
		ID   int64   `db:"id,pk"`
		Peer *Author `rel:"has_many"`
	}
	type SingleOnASlice struct {
		ID    int64    `db:"id,pk"`
		Peers []Author `rel:"belongs_to"`
	}
	type M2MWithoutAJoinTable struct {
		ID    int64    `db:"id,pk"`
		Peers []Author `rel:"many_to_many"`
	}
	type Sidecar struct {
		Peer *Author `rel:"belongs_to,fk=ID"`
	}
	type DuplicateRelation struct {
		ID int64 `db:"id,pk"`
		Sidecar
		Peer *Author `rel:"belongs_to,fk=ID"`
	}

	for _, tc := range []struct {
		name   string
		build  func() error
		field  string
		reason string
	}{
		{"a rel tag on an int", schemaErrOf[NotAStruct](), "Count", "not a struct"},
		{"a relation kind that does not exist", schemaErrOf[UnknownKind](), "Peer", "unknown relation kind"},
		{"a rel option that does not exist", schemaErrOf[UnknownOption](), "Peer", "unknown rel option onDelete"},
		{"has_many on a single value", schemaErrOf[CollectionOnASingleField](), "Peer", "must be declared on a slice"},
		{"belongs_to on a slice", schemaErrOf[SingleOnASlice](), "Peers", "cannot be declared on a slice"},
		{"many_to_many without a join table", schemaErrOf[M2MWithoutAJoinTable](), "Peers", "needs a join table"},
		{"two relations of the same name, one of them embedded",
			schemaErrOf[DuplicateRelation](), "Peer", "duplicate relation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantSchemaError(t, tc.build(), tc.field, tc.reason)
		})
	}
}

// A relation names fields on two models, and the far one may not be buildable
// when the near one is. That is why these are reported on first use instead of
// at declaration — but they are still reported, in the model's own words, and
// the statement they were going to appear in never renders them.
func TestARelationPointingAtAMissingFieldIsReportedOnUse(t *testing.T) {
	type BadLocal struct {
		ID   int64   `db:"id,pk"`
		Peer *Author `rel:"belongs_to,fk=NoSuchColumn"`
	}
	type BadRemote struct {
		ID     int64   `db:"id,pk"`
		PeerID int64   `db:"peer_id"`
		Peer   *Author `rel:"belongs_to,fk=PeerID,ref=NoSuchColumn"`
	}
	type Unmappable struct {
		Name string `db:"name"` // no primary key: this model cannot be built at all
	}
	type BadTarget struct {
		ID   int64       `db:"id,pk"`
		Peer *Unmappable `rel:"has_one"`
	}

	for _, tc := range []struct {
		name   string
		meta   *crud.Meta
		reason string
	}{
		{"the near side", metaOf[BadLocal](t, "bad_locals"), "unknown field NoSuchColumn"},
		{"the far side", metaOf[BadRemote](t, "bad_remotes"), "unknown field NoSuchColumn"},
		{"a target that is not a model at all", metaOf[BadTarget](t, "bad_targets"), "no primary key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := tc.meta.Relation("Peer").Resolve()
			wantSchemaError(t, err, "", tc.reason)

			// The same complaint reaches whoever tries to use the relation, and
			// no half-resolved column reaches the statement.
			sql, args, err := crud.NewSQL(crud.Postgres{}, tc.meta).Predicate(crud.Eq("Peer.Name", "Ann")).Done()
			wantSchemaError(t, err, "", tc.reason)
			if strings.Contains(sql, "name") || len(args) != 0 {
				t.Fatalf("sql = %q args = %v, want the broken path to render nothing but a constant", sql, args)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// forgiving lookups and their limits

// An alias that could mean two different fields is not registered at all:
// guessing which one the client meant would be a silent wrong answer. The
// unambiguous spellings keep working.
func TestAnAmbiguousAliasResolvesToNothing(t *testing.T) {
	type Muddle struct {
		ID     int64  `db:"id,pk"`
		UserId int64  `db:"user_id"`
		UserID int64  `db:"user_ident"`
		Name   string `db:"name"`
	}
	m := metaOf[Muddle](t, "muddles")

	if f := m.Field("userid"); f != nil {
		t.Fatalf("the folded alias userid resolved to %s; two fields answer to it", f.Name)
	}
	if _, _, _, err := m.WalkPath("userid"); !errors.As(err, new(*crud.UnknownFieldError)) {
		t.Fatalf("WalkPath(userid) = %v, want an UnknownFieldError rather than a coin toss", err)
	}
	for _, ref := range []string{"UserId", "user_id", "UserID", "user_ident"} {
		if m.Field(ref) == nil {
			t.Errorf("the exact spelling %q stopped resolving because of the ambiguity", ref)
		}
	}
	// Only the collision is lost; the rest of the model is still forgiving.
	if m.Field("NAME") != m.Field("Name") {
		t.Error("an unrelated field lost its case-insensitive alias")
	}
}

// ---------------------------------------------------------------------------
// WalkPath

// WalkPath is the one resolver every layer shares, so what it refuses is what
// the HTTP DSL, the SQL writer and the preloader all refuse.
func TestWalkPathRefusesMalformedPaths(t *testing.T) {
	m := articleMeta(t)

	for _, tc := range []struct {
		name string
		path string
	}{
		{"the empty path names nothing", ""},
		{"a trailing dot leaves a segment with no name", "Author."},
		{"a leading dot has the same problem at the front", ".Title"},
		{"a dot on its own", "."},
		{"a column cannot be walked through", "Title.Nope"},
		{"a relation that does not exist", "Nope.Name"},
		{"a field that does not exist behind a relation that does", "Author.Nope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hops, field, canonical, err := m.WalkPath(tc.path)
			var unknown *crud.UnknownFieldError
			if !errors.As(err, &unknown) {
				t.Fatalf("WalkPath(%q) = (%v, %v, %q, %v), want an UnknownFieldError",
					tc.path, hopNames(hops), field, canonical, err)
			}
			if unknown.Field != tc.path || unknown.Model == "" {
				t.Errorf("error = %v, want it to quote the path the caller sent and the model it failed on", unknown)
			}
			if hops != nil || field != nil || canonical != "" {
				t.Errorf("a failed walk still returned hops=%v field=%v canonical=%q",
					hopNames(hops), field, canonical)
			}
		})
	}
}

// Two to-many hops in one path stay two nested EXISTS. A pair of joins here
// would multiply the driving rows by both collections at once, and every LIMIT
// and COUNT downstream would be wrong.
func TestAPathThroughTwoToManyHopsNestsRatherThanJoins(t *testing.T) {
	m := metaOf[Person](t, "persons")

	hops, field, canonical, err := m.WalkPath("Reports.Reports.Name")
	if err != nil {
		t.Fatal(err)
	}
	if len(hops) != 2 || !hops[0].Rel.Kind.ToMany() || !hops[1].Rel.Kind.ToMany() {
		t.Fatalf("hops = %v, want two to-many hops", hopNames(hops))
	}
	if field == nil || field.Column != "name" || canonical != "Reports.Reports.Name" {
		t.Fatalf("walk ended at %v (%q), want the name column", field, canonical)
	}

	checkRender(t, crud.Postgres{}, m, crud.Eq("Reports.Reports.Name", "Ann"),
		`EXISTS (SELECT 1 FROM "persons" AS rx1 WHERE rx1."manager_no" = "persons"."id" `+
			`AND EXISTS (SELECT 1 FROM "persons" AS rx2 WHERE rx2."manager_no" = rx1."id" AND rx2."name" = $1))`,
		[]any{"Ann"})
}

// A path may stop on a relation — that is what a preload is — but then there is
// no column, and whoever needed one has to say so.
func TestAPathThatStopsOnARelationHasNoField(t *testing.T) {
	m := articleMeta(t)

	hops, field, canonical, err := m.WalkPath("comments.author")
	if err != nil {
		t.Fatal(err)
	}
	if field != nil {
		t.Fatalf("field = %v, want nothing: the path names an edge", field.Name)
	}
	if len(hops) != 2 || canonical != "Comments.Author" {
		t.Fatalf("walk = %v (%q), want both hops and the canonical spelling", hopNames(hops), canonical)
	}

	if _, _, err := m.FieldAt("comments.author"); !errors.As(err, new(*crud.SchemaError)) {
		t.Fatalf("FieldAt = %v, want a SchemaError: there is no column to compare", err)
	}
	// The other direction: a column is not something to preload.
	if _, _, err := m.RelationAt("Title"); !errors.As(err, new(*crud.SchemaError)) {
		t.Fatalf("RelationAt = %v, want a SchemaError", err)
	}
	// ...and neither is a path that never leaves the model.
	if _, _, err := m.RelationAt("Views"); !errors.As(err, new(*crud.SchemaError)) {
		t.Fatalf("RelationAt = %v, want a SchemaError", err)
	}
}

// The schema is cached per type, and the cache hands back the very same value:
// a *Field is compared by pointer all over the query layer.
func TestSchemaOfIsCachedByType(t *testing.T) {
	a, err := crud.SchemaOf[Article]()
	if err != nil {
		t.Fatal(err)
	}
	b, err := crud.SchemaOfType(reflect.TypeFor[Article]())
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("SchemaOf and SchemaOfType built two different schemas for one type")
	}
	// Two Metas over one schema differ only in the table they name.
	one, two := metaOf[Article](t, "articles"), metaOf[Article](t, "archived_articles")
	if one.Schema != two.Schema || one.Table == two.Table {
		t.Fatalf("binding a second table rebuilt the schema: %v / %v", one.Table, two.Table)
	}
}
