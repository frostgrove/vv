package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// tags turns @ into the backtick a struct tag needs, so a model can be written
// as a raw string in the test that reads it.
func tags(s string) string { return strings.ReplaceAll(s, "@", "`") }

// gen runs the generator over a scratch package built from the given sources
// and returns the file it wrote.
func gen(t *testing.T, files map[string]string, tweak func(*generator)) string {
	t.Helper()
	dir := t.TempDir()
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(tags(source)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	g := &generator{
		dir:      dir,
		depth:    2,
		withDTO:  true,
		withMeta: true,
		specsPkg: "github.com/shardit-io/rx/repo/decorators/specs",
		crudPkg:  "github.com/shardit-io/rx/crud",
	}
	if tweak != nil {
		tweak(g)
	}
	outDir := dir
	if g.into != "" {
		outDir = g.into
	}
	out := filepath.Join(outDir, "rxcrud_gen.go")
	defer quiet(t)()
	if err := g.run(out); err != nil {
		t.Fatalf("rxcrud: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// quiet swallows the "wrote …" line the command prints for its human user; a
// test does not need to hear about it.
func quiet(t *testing.T) func() {
	t.Helper()
	saved := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = devnull
	return func() { os.Stdout = saved; devnull.Close() }
}

// decl returns the generated declaration that starts with header, through its
// closing brace, so one type can be compared at a time.
func decl(t *testing.T, src, header string) string {
	t.Helper()
	i := strings.Index(src, header)
	if i < 0 {
		t.Fatalf("the generated file declares no %s:\n%s", header, src)
	}
	rest := src[i:]
	j := strings.Index(rest, "\n}\n")
	if j < 0 {
		t.Fatalf("%s is never closed:\n%s", header, src)
	}
	return rest[:j+2]
}

// declares reports whether a generated struct has a field of that name.
func declares(block, name string) bool {
	for _, line := range strings.Split(block, "\n") {
		if f, _, _ := strings.Cut(strings.TrimSpace(line), " "); f == name {
			return true
		}
	}
	return false
}

const blogModel = `package blog

import (
	"time"

	"github.com/shardit-io/rx/crud"
)

type Author struct {
	ID       int64     @db:"id,pk,auto"@
	Name     string    @db:"name"@
	Articles []Article @rel:"has_many"@
}

type Comment struct {
	ID        int64   @db:"id,pk,auto"@
	ArticleID int64   @db:"article_id"@
	AuthorID  int64   @db:"author_id"@
	Body      string  @db:"body"@
	Author    *Author @rel:"belongs_to"@
}

type Article struct {
	ID          int64               @db:"id,pk,auto"@
	AuthorID    int64               @db:"author_id"@
	Title       string              @db:"title"@
	Views       int                 @db:"views"@
	Rating      *float64            @db:"rating"@
	PublishedAt crud.Opt[time.Time] @db:"published_at"@
	Slug        string              @db:"slug,immutable"@
	Rendered    string              @db:"rendered,generated"@
	Secret      string              @db:"-"@
	CreatedAt   time.Time           @db:"created_at"@

	Author   *Author   @rel:"belongs_to"@
	Comments []Comment @rel:"has_many"@
}
`

// The DTO's field types are the whole point of generating it: a nullable column
// gets crud.Opt so an explicit null and an absent key stay different things,
// and a non-nullable one gets a pointer, which only has two states because it
// only needs two.
func TestUpdateDTOFollowsNullability(t *testing.T) {
	out := gen(t, map[string]string{"model.go": blogModel}, nil)
	want := tags(`type ArticleUpdate struct {
	AuthorID    *int64              @json:"authorID,omitempty"@
	Title       *string             @json:"title,omitempty"@
	Views       *int                @json:"views,omitempty"@
	Rating      crud.Opt[float64]   @json:"rating,omitzero"@
	PublishedAt crud.Opt[time.Time] @json:"publishedAt,omitzero"@
	CreatedAt   *time.Time          @json:"createdAt,omitempty"@
}`)
	if got := decl(t, out, "type ArticleUpdate struct {"); got != want {
		t.Fatalf("the update DTO is\n%s\nwant\n%s", got, want)
	}
}

// The columns a client must never write are the ones a repository would refuse
// anyway: the key, a generated column, an immutable one, and a field the model
// took out of the mapping altogether.
func TestUpdateDTOLeavesOutWhatCannotBeWritten(t *testing.T) {
	out := gen(t, map[string]string{"model.go": blogModel}, nil)
	dto := decl(t, out, "type ArticleUpdate struct {")
	attrs := decl(t, out, "type ArticleAttrs struct {")

	for _, f := range []string{"ID", "Slug", "Rendered"} {
		if declares(dto, f) {
			t.Fatalf("%s is in the update DTO, so a client could write it:\n%s", f, dto)
		}
		if !declares(attrs, f) {
			t.Fatalf("%s left the metamodel too, so it can no longer be filtered or sorted:\n%s", f, attrs)
		}
	}
	// db:"-" is not a column at all, so it is in neither.
	if declares(dto, "Secret") || declares(attrs, "Secret") {
		t.Fatalf("an unmapped field reached the generated code:\n%s\n%s", dto, attrs)
	}
}

// -readonly is the flag for a column somebody else owns: still filterable and
// sortable, never writable.
func TestReadonlyKeepsAFieldQueryableButNotWritable(t *testing.T) {
	out := gen(t, map[string]string{"model.go": blogModel}, func(g *generator) {
		g.readonly = names("Title,CreatedAt")
	})
	if dto := decl(t, out, "type ArticleUpdate struct {"); declares(dto, "Title") || declares(dto, "CreatedAt") {
		t.Fatalf("a readonly field is writable:\n%s", dto)
	}
	attrs := decl(t, out, "type ArticleAttrs struct {")
	if !declares(attrs, "Title") || !declares(attrs, "CreatedAt") {
		t.Fatalf("a readonly field cannot be queried:\n%s", attrs)
	}
}

// -skip is the flag for a field the generated code should not know about.
func TestSkipRemovesAFieldEverywhere(t *testing.T) {
	out := gen(t, map[string]string{"model.go": blogModel}, func(g *generator) {
		g.skip = names("Title,Comments")
	})
	if strings.Contains(out, "Title") {
		t.Fatalf("a skipped field survived:\n%s", out)
	}
	if strings.Contains(out, "ArticleCommentsAttrs") {
		t.Fatalf("a skipped relation still has a metamodel:\n%s", out)
	}
}

// A relation becomes a nested struct of the *root's* attributes, so
// Article_.Author.Name still filters articles.
func TestRelationsBecomeNestedAttributeStructs(t *testing.T) {
	out := gen(t, map[string]string{"model.go": blogModel}, nil)

	want := `type ArticleAuthorAttrs struct {
	ID   specs.Ord[Article, int64]
	Name specs.Str[Article]
}`
	if got := decl(t, out, "type ArticleAuthorAttrs struct {"); got != want {
		t.Fatalf("the nested metamodel is\n%s\nwant\n%s", got, want)
	}
	if !strings.Contains(out, "// ArticleAuthorAttrs reaches Article through Author.") {
		t.Fatalf("the nested struct is not documented as a path:\n%s", out)
	}

	root := decl(t, out, "type ArticleAttrs struct {")
	for _, line := range []string{
		"\tAuthor      ArticleAuthorAttrs\n",
		"\tComments    ArticleCommentsAttrs\n",
	} {
		if !strings.Contains(root, line) {
			t.Fatalf("the root metamodel is missing %q:\n%s", strings.TrimSpace(line), root)
		}
	}
	// A column keeps the attribute type its Go type earns: text is searchable,
	// ordered types compare, everything else only equals.
	for _, want := range []string{
		"\tTitle       specs.Str[Article]\n",
		"\tViews       specs.Ord[Article, int]\n",
		"\tRating      specs.Ord[Article, float64]\n",
		"\tPublishedAt specs.Cmp[Article, time.Time]\n",
	} {
		if !strings.Contains(root, want) {
			t.Fatalf("the root metamodel is missing %q:\n%s", strings.TrimSpace(want), root)
		}
	}
}

// Article -> Author -> Articles -> Author -> … has no end, so the walk stops at
// a model it has already passed through.
func TestRelationCyclesAreCutShort(t *testing.T) {
	out := gen(t, map[string]string{"model.go": blogModel}, func(g *generator) { g.depth = 6 })
	if declares(decl(t, out, "type ArticleAuthorAttrs struct {"), "Articles") {
		t.Fatalf("the walk went back into Article and would never stop:\n%s", out)
	}
	if strings.Contains(out, "ArticleAuthorArticlesAttrs") {
		t.Fatalf("a cyclic path was expanded:\n%s", out)
	}
	// Comment -> Author is not a cycle, so with the depth for it, it expands.
	if !strings.Contains(out, "type ArticleCommentsAuthorAttrs struct {") {
		t.Fatalf("a second hop that is not a cycle was dropped:\n%s", out)
	}
	if !strings.Contains(out, "// ArticleCommentsAuthorAttrs reaches Article through Comments.Author.") {
		t.Fatalf("the two-hop path is not documented:\n%s", out)
	}
}

// Depth is what bounds the expansion; one hop is the default.
func TestDepthBoundsHowFarRelationsExpand(t *testing.T) {
	shallow := gen(t, map[string]string{"model.go": blogModel}, func(g *generator) { g.depth = 1 })
	if strings.Contains(shallow, "ArticleAuthorAttrs") {
		t.Fatalf("depth 1 still expanded a relation:\n%s", shallow)
	}
	if declares(decl(t, shallow, "type ArticleAttrs struct {"), "Author") {
		t.Fatalf("depth 1 left a relation in the metamodel with nothing to point at:\n%s", shallow)
	}

	one := gen(t, map[string]string{"model.go": blogModel}, nil)
	if !strings.Contains(one, "type ArticleCommentsAttrs struct {") ||
		strings.Contains(one, "ArticleCommentsAuthorAttrs") {
		t.Fatalf("the default depth is not one hop:\n%s", one)
	}
}

// The runtime flattens an embedded struct into its parent's columns, so the
// generator has to as well — otherwise the shared audit columns would be
// missing from every DTO and metamodel in the package.
func TestEmbeddedStructsAreFlattened(t *testing.T) {
	out := gen(t, map[string]string{"model.go": `package store

import "time"

type Base struct {
	ID        int64     @db:"id,pk,auto"@
	CreatedAt time.Time @db:"created_at,immutable"@
}

type Product struct {
	Base
	Name string @db:"name"@
}
`}, nil)

	attrs := decl(t, out, "type ProductAttrs struct {")
	for _, f := range []string{"ID", "CreatedAt", "Name"} {
		if !declares(attrs, f) {
			t.Fatalf("%s did not come through the embedding:\n%s", f, attrs)
		}
	}
	dto := decl(t, out, "type ProductUpdate struct {")
	if declares(dto, "ID") || declares(dto, "CreatedAt") {
		t.Fatalf("an embedded key or immutable column became writable:\n%s", dto)
	}
	if !declares(dto, "Name") {
		t.Fatalf("the model's own column was lost:\n%s", dto)
	}
}

// gorm.Model lives in another package, so its fields cannot be read from
// source. It is common enough that the generator knows them by heart.
func TestGormModelIsFlattenedFromTheWellKnownTable(t *testing.T) {
	out := gen(t, map[string]string{"model.go": `package gormstore

import "gorm.io/gorm"

type Team struct {
	gorm.Model
	Name string @gorm:"size:120"@
}
`}, func(g *generator) { g.readonly = names("UpdatedAt,DeletedAt") })

	want := `type TeamAttrs struct {
	ID        specs.Ord[Team, uint]
	CreatedAt specs.Cmp[Team, time.Time]
	UpdatedAt specs.Cmp[Team, time.Time]
	DeletedAt specs.Attr[Team, gorm.DeletedAt]
	Name      specs.Str[Team]
}`
	if got := decl(t, out, "type TeamAttrs struct {"); got != want {
		t.Fatalf("the metamodel is\n%s\nwant\n%s", got, want)
	}
	// gorm's own package has to be imported, or the DeletedAt attribute dangles.
	if !strings.Contains(out, `"gorm.io/gorm"`) {
		t.Fatalf("the generated file does not import gorm:\n%s", out)
	}
	// Nothing gorm manages is writable through the DTO.
	dto := decl(t, out, "type TeamUpdate struct {")
	for _, f := range []string{"ID", "CreatedAt", "UpdatedAt", "DeletedAt"} {
		if declares(dto, f) {
			t.Fatalf("%s is writable, and gorm owns it:\n%s", f, dto)
		}
	}
}

const entModel = `package ent

import "time"

type UserEdges struct {
	Posts []*Post
}

type Post struct {
	ID int64
}

type User struct {
	ID        int64
	Email     string
	Age       *int
	CreatedAt time.Time
	Edges     UserEdges @json:"edges"@
}
`

// A generated entity from another tool carries no db tags at all, so naming it
// with -types is what makes it a model — and the output goes into a package of
// your own, where the names do not collide with the ones that tool generated.
func TestIntoAnotherPackageQualifiesTheModelTypes(t *testing.T) {
	out := gen(t, map[string]string{"user.go": entModel}, func(g *generator) {
		g.only = map[string]bool{"User": true}
		g.readonly = names("CreatedAt")
		g.into = filepath.Join(t.TempDir(), "store")
		g.modelImport = "example.com/app/ent"
		g.modelAlias = "ent"
	})

	if !strings.HasPrefix(out, "// Code generated by rxcrud. DO NOT EDIT.\n\npackage store\n") {
		t.Fatalf("the output package is not the directory it was written into:\n%s", out)
	}
	if !strings.Contains(out, `"example.com/app/ent"`) {
		t.Fatalf("the model package is not imported:\n%s", out)
	}
	want := `type UserAttrs struct {
	ID        specs.Ord[ent.User, int64]
	Email     specs.Str[ent.User]
	Age       specs.Ord[ent.User, int]
	CreatedAt specs.Cmp[ent.User, time.Time]
}`
	if got := decl(t, out, "type UserAttrs struct {"); got != want {
		t.Fatalf("the metamodel is\n%s\nwant\n%s", got, want)
	}
	if !strings.Contains(out, "var User_ = specs.Metamodel[ent.User, UserAttrs]()") {
		t.Fatalf("the metamodel is not bound to the qualified model type:\n%s", out)
	}
	// Edges is another struct in the same package: bookkeeping for the tool that
	// generated it, never a column.
	if strings.Contains(out, "Edges") {
		t.Fatalf("the entity's own bookkeeping field became a column:\n%s", out)
	}
	// Only the named type is a model; the others in the file are not.
	if strings.Contains(out, "PostAttrs") {
		t.Fatalf("a type nobody asked for was generated:\n%s", out)
	}
}

// When the target directory is already a package, the generated file joins it
// rather than inventing a second package name in the same folder.
func TestIntoAnExistingPackageKeepsItsName(t *testing.T) {
	into := t.TempDir()
	if err := os.WriteFile(filepath.Join(into, "doc.go"), []byte("package entstore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := gen(t, map[string]string{"user.go": entModel}, func(g *generator) {
		g.only = map[string]bool{"User": true}
		g.into = into
		g.modelImport = "example.com/app/ent"
		g.modelAlias = "ent"
	})
	if !strings.Contains(out, "\npackage entstore\n") {
		t.Fatalf("the generated file did not join the package already in the directory:\n%s", out)
	}
}

func TestIntoWithoutImportIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(tags(blogModel)), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &generator{dir: dir, depth: 2, withDTO: true, withMeta: true, into: t.TempDir()}
	err := g.run(filepath.Join(g.into, "rxcrud_gen.go"))
	if err == nil {
		t.Fatal("writing into another package without knowing its import path should be refused")
	}
	if !strings.Contains(err.Error(), "-import") {
		t.Fatalf("err = %v, want it to name the missing flag", err)
	}
}

func TestAPackageWithNothingToGenerateIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(`package empty

type NotAModel struct {
	Name string
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &generator{dir: dir, depth: 2, withDTO: true, withMeta: true}
	if err := g.run(filepath.Join(dir, "rxcrud_gen.go")); err == nil {
		t.Fatal("a package with no tagged models should be an error, not an empty file")
	}
}

func TestGeneratingOnlyOneHalf(t *testing.T) {
	noDTO := gen(t, map[string]string{"model.go": blogModel}, func(g *generator) { g.withDTO = false })
	if strings.Contains(noDTO, "ArticleUpdate") {
		t.Fatalf("-no-dto still wrote a DTO:\n%s", noDTO)
	}
	if !strings.Contains(noDTO, "ArticleAttrs") {
		t.Fatalf("-no-dto took the metamodel with it:\n%s", noDTO)
	}
	// With no DTO nothing needs crud, and the import goes away with it.
	if strings.Contains(noDTO, `"github.com/shardit-io/rx/crud"`) {
		t.Fatalf("an unused import would not compile:\n%s", noDTO)
	}

	noMeta := gen(t, map[string]string{"model.go": blogModel}, func(g *generator) { g.withMeta = false })
	if strings.Contains(noMeta, "Attrs") || strings.Contains(noMeta, "specs.") {
		t.Fatalf("-no-meta still wrote a metamodel:\n%s", noMeta)
	}
	if !strings.Contains(noMeta, "ArticleUpdate") {
		t.Fatalf("-no-meta took the DTO with it:\n%s", noMeta)
	}
}

// Generated code is committed and diffed. Two runs over the same package have
// to produce the same bytes, whatever order the parser handed the files and the
// types back in.
func TestOutputIsByteIdenticalAcrossRuns(t *testing.T) {
	files := map[string]string{
		"article.go": blogModel,
		"extra.go": `package blog

import "time"

type Tag struct {
	ID   int64  @db:"id,pk,auto"@
	Slug string @db:"slug"@
}

type Event struct {
	ID   int64     @db:"id,pk,auto"@
	At   time.Time @db:"at"@
	Name string    @db:"name"@
}
`,
	}
	first := gen(t, files, nil)
	for i := range 8 {
		if got := gen(t, files, nil); got != first {
			t.Fatalf("run %d produced different bytes:\n%s\n---\n%s", i+2, first, got)
		}
	}
	// Models are emitted in a stable order, not the order the parser found them.
	order := []string{"ArticleUpdate", "AuthorUpdate", "CommentUpdate", "EventUpdate", "TagUpdate"}
	at := 0
	for _, name := range order {
		i := strings.Index(first[at:], "type "+name+" struct {")
		if i < 0 {
			t.Fatalf("%s is missing or out of order:\n%s", name, first)
		}
		at += i
	}
}

// The proof that matters: the generated file builds, and the metamodel's
// package-init check agrees with the model it was generated from.
func TestGeneratedCodeCompilesAndValidates(t *testing.T) {
	out := gen(t, map[string]string{"model.go": blogModel}, nil)

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "model"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module gencheck\n\ngo 1.24\n\nrequire github.com/shardit-io/rx v0.0.0\n\nreplace github.com/shardit-io/rx => "+root+"\n")
	write(filepath.Join("model", "model.go"), tags(blogModel))
	write(filepath.Join("model", "rxcrud_gen.go"), out)
	write("main.go", "package main\n\nimport _ \"gencheck/model\"\n\nfunc main() {}\n")

	cmd := exec.Command("go", "run", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off")
	if res, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the generated code does not build and run: %v\n%s\n---- generated ----\n%s", err, res, out)
	}
}

// The optimistic lock is the repository's column: it pins the write to the
// version it read and advances it. crud.PlanFor refuses a DTO that names it, so
// a generator that emitted it produced a package which panicked at Define time —
// the two features shipped in the same change and did not know about each other.
func TestTheVersionColumnIsLeftOutOfTheDTO(t *testing.T) {
	src := tags(`package m

import "time"

type Doc struct {
	ID        int64     @db:"id,pk,auto"@
	Title     string    @db:"title"@
	Version   int       @db:"version,version"@
	Revision  int       @db:"revision,lock"@
	UpdatedAt time.Time @db:"updated_at"@
}
`)
	out := gen(t, map[string]string{"model.go": src}, nil)

	dto := decl(t, out, "type DocUpdate struct {")
	for _, f := range []string{"Version", "Revision"} {
		if declares(dto, f) {
			t.Fatalf("%s is in the update DTO; basic.Define will panic on it:\n%s", f, dto)
		}
	}
	if !declares(dto, "Title") {
		t.Fatalf("an ordinary column left the DTO with it:\n%s", dto)
	}

	// It is still a column, so filtering and sorting by it must keep working —
	// "the repository owns the writes" is not "the column is invisible".
	attrs := decl(t, out, "type DocAttrs struct {")
	for _, f := range []string{"Version", "Revision"} {
		if !declares(attrs, f) {
			t.Fatalf("%s left the metamodel, so it can no longer be filtered or sorted:\n%s", f, attrs)
		}
	}
}
