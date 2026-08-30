package codegen

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

func testGenerator(dir string) *generator {
	return &generator{
		dir:      dir,
		depth:    2,
		withDTO:  true,
		withMeta: true,
		withRepo: true,
		specsPkg: "github.com/frostgrove/vv/crud/decorators/specs",
		crudPkg:  "github.com/frostgrove/vv/crud",
		utilsPkg: DefaultUtilsPkg,
		portPkg:  DefaultPortPkg,
		errsPkg:  DefaultErrsPkg,
		netPkg:   DefaultNetPkg,
		binding:  "net",
	}
}

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
	g := testGenerator(dir)
	if tweak != nil {
		tweak(g)
	}
	outDir := dir
	if g.into != "" {
		outDir = g.into
	}
	out := filepath.Join(outDir, "vv_gen.go")
	if err := g.run(out); err != nil {
		t.Fatalf("generating: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func genError(t *testing.T, source string) error {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(tags(source)), 0o644); err != nil {
		t.Fatal(err)
	}
	return testGenerator(dir).run(filepath.Join(dir, "vv_gen.go"))
}

func writeSafetyModel(t *testing.T, dir string) {
	t.Helper()
	source := tags(`package safe

type Product struct {
	ID   int64  @db:"id,pk"@
	Name string @db:"name"@
}
`)
	if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
}

func safetyOptions(dir, out string) *Options {
	return &Options{Dir: dir, Out: out, WithDTO: true, NoRepo: true, Binding: "none"}
}

func TestOutputNameCannotEscapeItsControlledDirectory(t *testing.T) {
	dir := t.TempDir()
	writeSafetyModel(t, dir)
	victim := filepath.Join(filepath.Dir(dir), "authored-victim.go")
	if err := os.WriteFile(victim, []byte("authored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{"../authored-victim.go", victim, "nested/vv_gen.go"} {
		if err := Run(safetyOptions(dir, out)); err == nil || !strings.Contains(err.Error(), "-out") {
			t.Fatalf("Run with -out %q = %v, want a basename refusal", out, err)
		}
	}
	b, err := os.ReadFile(victim)
	if err != nil || string(b) != "authored\n" {
		t.Fatalf("victim after refused output = %q, %v", b, err)
	}
}

func TestAuthoredOutputIsNeverOverwritten(t *testing.T) {
	dir := t.TempDir()
	writeSafetyModel(t, dir)
	target := filepath.Join(dir, "manual.go")
	want := "package safe\n\nconst HandWritten = true\n"
	if err := os.WriteFile(target, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(safetyOptions(dir, "manual.go"))
	if err == nil || !strings.Contains(err.Error(), "authored file") {
		t.Fatalf("Run error = %v, want authored-file refusal", err)
	}
	b, readErr := os.ReadFile(target)
	if readErr != nil || string(b) != want {
		t.Fatalf("authored target changed to %q (err %v)", b, readErr)
	}
}

func TestSymlinkOutputIsRefusedWithoutFollowingIt(t *testing.T) {
	dir := t.TempDir()
	writeSafetyModel(t, dir)
	victim := filepath.Join(t.TempDir(), "victim.go")
	if err := os.WriteFile(victim, []byte("do not replace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "vv_gen.go")); err != nil {
		t.Fatal(err)
	}
	err := Run(safetyOptions(dir, "vv_gen.go"))
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Run error = %v, want symlink refusal", err)
	}
	b, readErr := os.ReadFile(victim)
	if readErr != nil || string(b) != "do not replace\n" {
		t.Fatalf("symlink victim changed to %q (err %v)", b, readErr)
	}
}

func TestGeneratedOutputIsAtomicallyReplaceable(t *testing.T) {
	dir := t.TempDir()
	writeSafetyModel(t, dir)
	target := filepath.Join(dir, "vv_gen.go")
	if err := os.WriteFile(target, []byte(generatedHeader+"\n\npackage safe\n\nconst Old = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Run(safetyOptions(dir, "")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), generatedHeader+"\n") || strings.Contains(string(b), "const Old") {
		t.Fatalf("generated target was not replaced completely:\n%s", b)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".vv_gen.go.tmp-") {
			t.Fatalf("temporary output leaked after commit: %s", entry.Name())
		}
	}
}

// decl returns the generated declaration that starts with header, through its
// closing brace, so one type can be compared at a time.
func decl(t *testing.T, source, header string) string {
	t.Helper()
	i := strings.Index(source, header)
	if i < 0 {
		t.Fatalf("the generated file declares no %s:\n%s", header, source)
	}
	rest := source[i:]
	j := strings.Index(rest, "\n}\n")
	if j < 0 {
		t.Fatalf("%s is never closed:\n%s", header, source)
	}
	return rest[:j+2]
}

// comment returns the doc comment block that starts with header, so a test can
// assert what the generator said about a declaration rather than about the file.
func comment(source, header string) string {
	i := strings.Index(source, header)
	if i < 0 {
		return ""
	}
	rest := source[i:]
	j := strings.Index(rest, "\ntype ")
	if j < 0 {
		return rest
	}
	return rest[:j]
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

	"github.com/frostgrove/vv/crud"
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
// gets utils.Opt so an explicit null and an absent key stay different things,
// and a non-nullable one gets a pointer, which only has two states because it
// only needs two.
func TestUpdateDTOFollowsNullability(t *testing.T) {
	out := gen(t, map[string]string{"model.go": blogModel}, nil)
	want := tags(`type ArticleUpdate struct {
	AuthorID    *int64               @json:"authorID,omitempty"@
	Title       *string              @json:"title,omitempty"@
	Views       *int                 @json:"views,omitempty"@
	Rating      utils.Opt[float64]   @json:"rating,omitzero"@
	PublishedAt utils.Opt[time.Time] @json:"publishedAt,omitzero"@
	CreatedAt   *time.Time           @json:"createdAt,omitempty"@
}`)
	if got := decl(t, out, "type ArticleUpdate struct {"); got != want {
		t.Fatalf("the update DTO is\n%s\nwant\n%s", got, want)
	}
}

func TestModelFileNeedsNoDatabaseOrORMTags(t *testing.T) {
	out := gen(t, map[string]string{"product.model.go": `package product

import (
	"time"
	"github.com/google/uuid"
)

type Product struct {
	Id          uuid.UUID
	Name        string
	Description *string
	CreatedAt   time.Time
}
`}, nil)

	if got := decl(t, out, "type ProductUpdate struct {"); !declares(got, "Name") || !declares(got, "Description") {
		t.Fatalf("plain model fields did not reach the update DTO:\n%s", got)
	}
	if strings.Contains(decl(t, out, "type ProductUpdate struct {"), "Id") {
		t.Fatalf("Id was not recognised as the conventional primary key:\n%s", out)
	}
	if !strings.Contains(out, "var ProductRepository = sqlrepo.Define[Product, uuid.UUID, ProductUpdate](\"\")") {
		t.Fatalf("the generated file has no repository blueprint:\n%s", out)
	}
	if !strings.Contains(out, "type ProductRepo = crud.Repo[Product, uuid.UUID, ProductUpdate]") {
		t.Fatalf("the generated file has no short repository alias:\n%s", out)
	}
	if !strings.Contains(out, "func NewProductRepository(src crud.Source) *ProductRepo") {
		t.Fatalf("the generated file has no pointer repository factory:\n%s", out)
	}
}

// The columns a client must never write are the ones a repository would refuse
// anyway: the key, a generated column, an immutable one, and a field the model
// took out of the mapping altogether.
func TestUpdateDTOLeavesOutWhatCannotBeWritten(t *testing.T) {
	out := gen(t, map[string]string{"model.go": blogModel}, nil)
	dataTransferObject := decl(t, out, "type ArticleUpdate struct {")
	attrs := decl(t, out, "type ArticleAttrs struct {")

	for _, f := range []string{"ID", "Slug", "Rendered"} {
		if declares(dataTransferObject, f) {
			t.Fatalf("%s is in the update DTO, so a client could write it:\n%s", f, dataTransferObject)
		}
		if !declares(attrs, f) {
			t.Fatalf("%s left the metamodel too, so it can no longer be filtered or sorted:\n%s", f, attrs)
		}
	}
	// db:"-" is not a column at all, so it is in neither.
	if declares(dataTransferObject, "Secret") || declares(attrs, "Secret") {
		t.Fatalf("an unmapped field reached the generated code:\n%s\n%s", dataTransferObject, attrs)
	}
}

// -readonly is the flag for a column somebody else owns: still filterable and
// sortable, never writable.
func TestReadonlyKeepsAFieldQueryableButNotWritable(t *testing.T) {
	out := gen(t, map[string]string{"model.go": blogModel}, func(g *generator) {
		g.readonly = names("Title,CreatedAt")
	})
	if dataTransferObject := decl(t, out, "type ArticleUpdate struct {"); declares(dataTransferObject, "Title") || declares(dataTransferObject, "CreatedAt") {
		t.Fatalf("a readonly field is writable:\n%s", dataTransferObject)
	}
	attrs := decl(t, out, "type ArticleAttrs struct {")
	if !declares(attrs, "Title") || !declares(attrs, "CreatedAt") {
		t.Fatalf("a readonly field cannot be queried:\n%s", attrs)
	}
}

// -skip is the flag for a field the generated code should not know about — with
// one exception the flag cannot avoid. Reflection reads the struct and never the
// command line, so a skipped column is an ordinary writable column at run time
// and the coverage assertion has to be told about it by name.
func TestSkipRemovesAFieldEverywhere(t *testing.T) {
	out := gen(t, map[string]string{"model.go": blogModel}, func(g *generator) {
		g.skip = names("Title,Comments")
	})
	for _, header := range []string{"type ArticleUpdate struct {", "type ArticleAttrs struct {"} {
		if declares(decl(t, out, header), "Title") {
			t.Fatalf("a skipped field survived in %s:\n%s", header, out)
		}
	}
	if strings.Contains(out, "ArticleCommentsAttrs") {
		t.Fatalf("a skipped relation still has a metamodel:\n%s", out)
	}
	// The exception, asserted rather than tolerated.
	if !strings.Contains(out, `port.MustCoverUpdate[Article, ArticleUpdate]("Title")`) {
		t.Fatalf("the skipped column is not declared as an exclusion, so start-up refuses it:\n%s", out)
	}
	// And its control: a skipped *relation* is not a column, so there is nothing
	// for reflection to disagree about and nothing to declare.
	if strings.Contains(out, `"Comments"`) {
		t.Fatalf("a skipped relation was declared as a column exclusion:\n%s", out)
	}
}

// A relation becomes a nested struct of the *root's* attributes, so
// Article_.Author.Name still filters articles.
func TestRelationsBecomeNestedAttributeStructs(t *testing.T) {
	out := gen(t, map[string]string{"model.go": blogModel}, nil)

	want := `type ArticleAuthorAttrs struct {
	specs.Rel[Article, Author]
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

// A relation group carries its own path as a handle, so sqlrepo.RelationScope,
// crud.Preload and a relation policy take an identifier the compiler resolves
// instead of a string literal.
func TestRelationGroupsCarryATypedPath(t *testing.T) {
	out := gen(t, map[string]string{"model.go": blogModel}, nil)

	for header, want := range map[string]string{
		"type ArticleAuthorAttrs struct {":   "\tspecs.Rel[Article, Author]\n",
		"type ArticleCommentsAttrs struct {": "\tspecs.Rel[Article, Comment]\n",
	} {
		if got := decl(t, out, header); !strings.Contains(got, want) {
			t.Fatalf("%s carries no handle:\n%s", header, got)
		}
	}

	// The control: the root is not reached through a relation, so it has no
	// path to answer and must not be handed one.
	if root := decl(t, out, "type ArticleAttrs struct {"); strings.Contains(root, "specs.Rel[") {
		t.Fatalf("the root metamodel was given a relation handle:\n%s", root)
	}
}

// The handle is embedded, so a column of the *target* called Path sits a level
// nearer and shadows the promoted method. The generated file has to say so
// where a reader is looking, not only in the module doc.
func TestATargetColumnNamedPathIsCalledOut(t *testing.T) {
	const model = `package files

type File struct {
	ID    int64  @db:"id,pk,auto"@
	DirID int64  @db:"dir_id"@
	Path  string @db:"path"@
	Dir   *Dir   @rel:"belongs_to"@
}

type Dir struct {
	ID    int64  @db:"id,pk,auto"@
	Name  string @db:"name"@
	Files []File @rel:"has_many"@
}
`
	out := gen(t, map[string]string{"model.go": model}, nil)

	const note = "spell this relation's path RelPath() here"
	if !strings.Contains(comment(out, "// DirFilesAttrs"), note) {
		t.Fatalf("the shadowed path is not called out where the group is declared:\n%s", out)
	}

	// The control: the other direction of the same schema reaches Dir, which has
	// no such column, and must not carry the note.
	if strings.Contains(comment(out, "// FileDirAttrs"), note) {
		t.Fatalf("a group whose target has no Path column was warned about one:\n%s", out)
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
	dataTransferObject := decl(t, out, "type ProductUpdate struct {")
	if declares(dataTransferObject, "ID") || declares(dataTransferObject, "CreatedAt") {
		t.Fatalf("an embedded key or immutable column became writable:\n%s", dataTransferObject)
	}
	if !declares(dataTransferObject, "Name") {
		t.Fatalf("the model's own column was lost:\n%s", dataTransferObject)
	}
}

// gorm.Model lives in another package, so its fields cannot be read from
// source. It is common enough that the generator knows them by heart.
func TestGormModelIsFlattenedFromTheWellKnownTable(t *testing.T) {
	out := gen(t, map[string]string{"model.go": `package gormstore

import orm "gorm.io/gorm"

type Team struct {
	orm.Model
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
	if !strings.Contains(out, `gorm "gorm.io/gorm"`) {
		t.Fatalf("the generated file does not import gorm:\n%s", out)
	}
	// Nothing gorm manages is writable through the DTO.
	dataTransferObject := decl(t, out, "type TeamUpdate struct {")
	for _, f := range []string{"ID", "CreatedAt", "UpdatedAt", "DeletedAt"} {
		if declares(dataTransferObject, f) {
			t.Fatalf("%s is writable, and gorm owns it:\n%s", f, dataTransferObject)
		}
	}
}

func TestIncompleteExternalEmbedDoesNotPoisonLocalRelationClassification(t *testing.T) {
	out := gen(t, map[string]string{"model.go": `package gormstore

import orm "gorm.io/gorm"

type Team struct {
	orm.Model
	Members []Member @rel:"has_many"@
}

type Member struct {
	orm.Model
	TeamID uint @db:"team_id"@
	Team *Team @rel:"belongs_to"@
}
`}, nil)
	team := decl(t, out, "type TeamAttrs struct {")
	member := decl(t, out, "type MemberAttrs struct {")
	if !declares(team, "Members") || !declares(member, "Team") {
		t.Fatalf("an unresolved external embed poisoned local relation method sets:\n%s\n%s", team, member)
	}
}

func TestNestedIncompleteExternalEmbedFailsLoudBeforeRendering(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(tags(`package store

import "example.com/audit"

type Base struct {
	audit.Fields
}

type Product struct {
	Base
	Name string @db:"name"@
}
`)), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "vv_gen.go")
	err := testGenerator(dir).run(outPath)
	if err == nil || !strings.Contains(err.Error(), "embeds unresolved type audit.Fields") {
		t.Fatalf("nested incomplete embed error = %v", err)
	}
	if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
		t.Fatalf("nested incomplete embed wrote output before refusing it: %v", statErr)
	}
}

func TestFlattenedFieldAndColumnCollisionsFailBeforeRendering(t *testing.T) {
	tests := []struct {
		name, source, want string
	}{
		{
			name: "effective Go field name",
			source: `package store

type base struct { Name string @db:"base_name"@ }

type Product struct {
	ID int64 @db:"id,pk"@
	base
	Name string @db:"name"@
}
`,
			want: "duplicate effective field name Name",
		},
		{
			name: "effective database column",
			source: `package store

type base struct { Legacy string @db:"shared_value"@ }

type Product struct {
	ID int64 @db:"id,pk"@
	base
	Current string @db:"shared_value"@
}
`,
			want: "duplicate effective database column shared_value",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(tags(test.source)), 0o644); err != nil {
				t.Fatal(err)
			}
			outPath := filepath.Join(dir, "vv_gen.go")
			err := testGenerator(dir).run(outPath)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("flattened collision error = %v, want %q", err, test.want)
			}
			if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
				t.Fatalf("flattened collision wrote output before refusing it: %v", statErr)
			}
		})
	}
}

func TestUnknownExternalEmbeddedStructIsRefusedDuringGeneration(t *testing.T) {
	err := genError(t, `package store

import "example.com/audit"

type Product struct {
	audit.Fields
	Name string @db:"name"@
}
`)
	if err == nil || !strings.Contains(err.Error(), "Product embeds unresolved type audit.Fields") {
		t.Fatalf("generation error = %v, want the external embed and model named", err)
	}
	if !strings.Contains(err.Error(), `tag it db:"-"`) {
		t.Fatalf("generation error gives no explicit opt-out: %v", err)
	}
}

func TestEmbeddedPointerIsRefusedLikeRuntimeMetadata(t *testing.T) {
	err := genError(t, `package store

type Base struct {
	ID int64 @db:"id,pk"@
}

type Product struct {
	*Base
	Name string @db:"name"@
}
`)
	if err == nil || !strings.Contains(err.Error(), "Product embeds pointer *Base") {
		t.Fatalf("generation error = %v, want the embedded pointer named", err)
	}
}

func TestUnknownExternalEmbedCanBeExplicitlyExcluded(t *testing.T) {
	out := gen(t, map[string]string{"model.go": `package store

import "example.com/audit"

type Product struct {
	audit.Fields @db:"-"@
	ID   int64  @db:"id,pk"@
	Name string @db:"name"@
}
`}, nil)
	attrs := decl(t, out, "type ProductAttrs struct {")
	if declares(attrs, "Fields") || !declares(attrs, "Name") {
		t.Fatalf("explicitly excluded embed leaked or hid ordinary columns:\n%s", attrs)
	}
}

func TestScalarAnonymousRelationTagMatchesRuntimeRefusal(t *testing.T) {
	err := genError(t, `package store

import "time"

type Product struct {
	ID int64 @db:"id,pk"@
	time.Time @rel:"belongs_to"@
}
`)
	if err == nil || !strings.Contains(err.Error(), "field Time has a rel tag") {
		t.Fatalf("generation error = %v, want the scalar rel tag refused like runtime metadata", err)
	}

	out := gen(t, map[string]string{"model.go": `package store

import "time"

type Product struct {
	ID int64 @db:"id,pk"@
	time.Time @rel:"-"@
}
`}, nil)
	if !declares(decl(t, out, "type ProductAttrs struct {"), "Time") {
		t.Fatalf(`rel:"-" on a scalar should leave the scalar column intact:\n%s`, out)
	}
}

func TestUnexportedFieldsMatchRuntimeVisibilityRules(t *testing.T) {
	out := gen(t, map[string]string{"model.go": `package store

type token int64

type Product struct {
	ID int64 @db:"id,pk"@
	token
	hidden string
}
`}, nil)
	attrs := decl(t, out, "type ProductAttrs struct {")
	if declares(attrs, "token") || declares(attrs, "hidden") {
		t.Fatalf("unexported fields became generated columns:\n%s", attrs)
	}

	for _, source := range []string{
		`package store
type token int64
type Product struct { ID int64 @db:"id,pk"@; token @db:"token"@ }
`,
		`package store
type Product struct { ID int64 @db:"id,pk"@; hidden string @db:"hidden"@ }
`,
	} {
		err := genError(t, source)
		if err == nil || !strings.Contains(err.Error(), "maps unexported") || !strings.Contains(err.Error(), `db:"-"`) {
			t.Fatalf("unexported mapped-field error = %v", err)
		}
	}
}

func TestExternalEmbedWithAnInaccessibleColumnTypeFailsAtGeneration(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store := filepath.Join(dir, "store")
	shared := filepath.Join(dir, "shared")
	for _, path := range []string{store, shared} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(tags(content)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "go.mod"), "module hiddenembed\n\ngo 1.26\n\nrequire github.com/frostgrove/vv v0.0.0\n\nreplace github.com/frostgrove/vv => "+root+"\n")
	write(filepath.Join(shared, "shared.go"), `package shared

type private string

type Public = private

type Safe struct {
	Value Public @db:"value"@
}

type Unsafe struct {
	Value private @db:"value"@
}

type UnsafeArrayShape struct {
	Value [1]struct{ private string } @db:"value"@
}

type UnsafeMapShape struct {
	Value map[string]struct{ private string } @db:"value"@
}

type UnsafeInterfaceShape struct {
	Value interface{ private() } @db:"value"@
}
`)
	write(filepath.Join(store, "model.go"), `package store

import "hiddenembed/shared"

type Product struct {
	ID int64 @db:"id,pk"@
	shared.Unsafe
}
`)
	err = testGenerator(store).run(filepath.Join(store, "vv_gen.go"))
	if err == nil || !strings.Contains(err.Error(), "shared.private is not accessible") {
		t.Fatalf("generation error = %v, want the inaccessible external column type named", err)
	}
	if _, statErr := os.Stat(filepath.Join(store, "vv_gen.go")); !os.IsNotExist(statErr) {
		t.Fatalf("unsafe generation wrote output before refusing it: %v", statErr)
	}
	for _, embedded := range []string{"UnsafeArrayShape", "UnsafeMapShape", "UnsafeInterfaceShape"} {
		write(filepath.Join(store, "model.go"), `package store

import "hiddenembed/shared"

type Product struct {
	ID int64 @db:"id,pk"@
	shared.`+embedded+`
}
`)
		err = testGenerator(store).run(filepath.Join(store, "vv_gen.go"))
		if err == nil || !strings.Contains(err.Error(), "hiddenembed/shared.private is not accessible") {
			t.Fatalf("generation with %s error = %v, want the inaccessible structural member identity named", embedded, err)
		}
	}

	write(filepath.Join(store, "model.go"), `package store

import "hiddenembed/shared"

type Product struct {
	ID int64 @db:"id,pk"@
	shared.Safe
}
`)
	if err := testGenerator(store).run(filepath.Join(store, "vv_gen.go")); err != nil {
		t.Fatalf("an exported alias to a private implementation is still a legal type spelling: %v", err)
	}
	cmd := exec.Command("go", "test", "./store")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off")
	if response, err := cmd.CombinedOutput(); err != nil {
		generated, _ := os.ReadFile(filepath.Join(store, "vv_gen.go"))
		t.Fatalf("generated exported-alias package does not compile: %v\n%s\n--- generated ---\n%s", err, response, generated)
	}
}

func TestAnonymousTypeClassificationMatchesRuntimeAndGeneratedPackageCompiles(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store := filepath.Join(dir, "store")
	value := filepath.Join(dir, "value")
	for _, path := range []string{store, value} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(tags(content)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "go.mod"), "module genembeds\n\ngo 1.26\n\nrequire github.com/frostgrove/vv v0.0.0\n\nreplace github.com/frostgrove/vv => "+root+"\n")
	write(filepath.Join(value, "value.go"), `package value

type Amount int64

type Audit[T any] struct {
	External T @db:"external"@
}
`)
	write(filepath.Join(store, "types.go"), `package store

import (
	"database/sql/driver"
	"genembeds/value"
)

type Decimal struct{ raw string }

func (Decimal) Value() (driver.Value, error) { return nil, nil }
func (*Decimal) Scan(any) error              { return nil }

type Token struct{ raw string }

func (Token) MarshalText() ([]byte, error) { return nil, nil }
func (*Token) UnmarshalText([]byte) error  { return nil }

type pointerMarshalerBase struct {
	Inner string @db:"inner"@
}

func (*pointerMarshalerBase) MarshalText() ([]byte, error) { return nil, nil }

type Digest [16]byte
type Payload interface{ Payload() }

type genericBase[T any] struct {
	GenericValue T @db:"generic_value"@
}

type genericAlias = genericBase[value.Amount]
type auditAlias = value.Audit[string]
`)
	write(filepath.Join(store, "models.model.go"), `package store

import (
	"genembeds/value"
	"time"
)

type Scalars struct {
	ID int64 @db:"id,pk"@
	time.Time @db:"observed_at"@
	Decimal @db:"decimal"@
}

type PointerScalar struct {
	ID int64 @db:"id,pk"@
	*Decimal
}

type TextScalar struct {
	ID int64 @db:"id,pk"@
	Token @db:"token"@
}

type NamedScalar struct {
	ID int64 @db:"id,pk"@
	value.Amount @db:"amount"@
}

type ArrayScalar struct {
	ID int64 @db:"id,pk"@
	Digest @db:"digest"@
}

type InterfaceScalar struct {
	ID int64 @db:"id,pk"@
	Payload @db:"payload"@
}

type PointerMarshalerModel struct {
	ID int64 @db:"id,pk"@
	pointerMarshalerBase
}

type GenericModel struct {
	ID int64 @db:"id,pk"@
	genericAlias
	auditAlias
}

type Author struct {
	ID int64 @db:"id,pk"@
	Name string @db:"name"@
}

type Writer = Author

type Article struct {
	ID int64 @db:"id,pk"@
	AuthorID int64 @db:"author_id"@
	Author @rel:"belongs_to"@
}

type InferredArticle struct {
	ID int64 @db:"id,pk"@
	AuthorID int64 @db:"author_id"@
	Author Author @rel:""@
}

type InferredEmbeddedArticle struct {
	ID int64 @db:"id,pk"@
	AuthorID int64 @db:"author_id"@
	Author @rel:""@
}

type AliasNamedArticle struct {
	ID int64 @db:"id,pk"@
	WriterID int64 @db:"writer_id"@
	Writer Writer @rel:""@
}

type AliasEmbeddedArticle struct {
	ID int64 @db:"id,pk"@
	WriterID int64 @db:"writer_id"@
	Writer @rel:"belongs_to"@
}

type IgnoredAuthor struct {
	ID int64 @db:"id,pk"@
	Author @rel:"-"@
}
`)
	outPath := filepath.Join(store, "vv_gen.go")
	if err := testGenerator(store).run(outPath); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	out := string(generated)
	for declaration, fields := range map[string][]string{
		"type ScalarsAttrs struct {":               {"Time", "Decimal"},
		"type PointerScalarAttrs struct {":         {"Decimal"},
		"type TextScalarAttrs struct {":            {"Token"},
		"type NamedScalarAttrs struct {":           {"Amount"},
		"type ArrayScalarAttrs struct {":           {"Digest"},
		"type InterfaceScalarAttrs struct {":       {"Payload"},
		"type PointerMarshalerModelAttrs struct {": {"Inner"},
		"type GenericModelAttrs struct {":          {"GenericValue", "External"},
	} {
		block := decl(t, out, declaration)
		for _, name := range fields {
			if !declares(block, name) {
				t.Fatalf("%s did not classify %s as a column:\n%s", declaration, name, block)
			}
		}
	}
	article := decl(t, out, "type ArticleAttrs struct {")
	if !declares(article, "Author") || declares(article, "Name") {
		t.Fatalf("anonymous relation was flattened instead of declared:\n%s", article)
	}
	for _, declaration := range []string{
		"type InferredArticleAttrs struct {",
		"type InferredEmbeddedArticleAttrs struct {",
		"type AliasNamedArticleAttrs struct {",
		"type AliasEmbeddedArticleAttrs struct {",
	} {
		block := decl(t, out, declaration)
		relation := "Author"
		if strings.Contains(declaration, "Alias") {
			relation = "Writer"
		}
		if !declares(block, relation) || declares(block, "Name") {
			t.Fatalf(`rel:"" lost tag presence for %s:\n%s`, declaration, block)
		}
	}
	for _, declaration := range []string{
		"type AliasNamedArticleWriterAttrs struct {",
		"type AliasEmbeddedArticleWriterAttrs struct {",
	} {
		if block := decl(t, out, declaration); !strings.Contains(block, "specs.Rel[") ||
			!strings.Contains(block, ", Author]") {
			t.Fatalf("local relation alias did not expand through canonical Author:\n%s", block)
		}
	}
	ignored := decl(t, out, "type IgnoredAuthorAttrs struct {")
	if declares(ignored, "Author") || declares(ignored, "Name") {
		t.Fatalf(`anonymous rel:"-" leaked into metamodel:\n%s`, ignored)
	}

	cmd := exec.Command("go", "test", "./store")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off")
	if response, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated anonymous-type package does not compile: %v\n%s\n--- generated ---\n%s", err, response, generated)
	}
}

func TestAnonymousScannerOnlyPointerMatchesRuntimePointerRefusal(t *testing.T) {
	err := genError(t, `package store

type ScannerOnly struct{}

func (*ScannerOnly) Scan(any) error { return nil }

type Product struct {
	ID int64 @db:"id,pk"@
	*ScannerOnly
}
`)
	if err == nil || !strings.Contains(err.Error(), "embeds pointer *ScannerOnly") {
		t.Fatalf("scanner-only anonymous pointer classification error = %v", err)
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

	if !strings.HasPrefix(out, "// Code generated by vv. DO NOT EDIT.\n\npackage store\n") {
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

func TestIntoUsesTheDeclaredPackageNameForAVersionedImportPath(t *testing.T) {
	out := gen(t, map[string]string{"user.go": strings.Replace(entModel, "package ent", "package models", 1)}, func(g *generator) {
		g.only = map[string]bool{"User": true}
		g.into = filepath.Join(t.TempDir(), "store")
		g.modelImport = "example.com/app/models/v2"
	})
	if !strings.Contains(out, `models "example.com/app/models/v2"`) {
		t.Fatalf("versioned model import does not use its declared package name:\n%s", out)
	}
	if !strings.Contains(out, "specs.Metamodel[models.User, UserAttrs]()") {
		t.Fatalf("model types are qualified with the path basename instead of the package declaration:\n%s", out)
	}
}

func TestRenamedSourceImportKeepsItsAliasInGeneratedCode(t *testing.T) {
	out := gen(t, map[string]string{"model.go": `package store

import money "example.com/acme/decimal"

type Product struct {
	ID    int64        @db:"id,pk"@
	Price money.Amount @db:"price"@
}
`}, nil)
	if !strings.Contains(out, `money "example.com/acme/decimal"`) {
		t.Fatalf("renamed import lost its source alias:\n%s", out)
	}
	if !strings.Contains(out, "Price *money.Amount") {
		t.Fatalf("column type lost its renamed package qualifier:\n%s", out)
	}
}

func TestVersionedColumnImportReadsTheDeclaredPackageName(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	store := filepath.Join(root, "store")
	for _, dir := range []string{shared, store} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(root, "go.mod"): `module example.com/app

go 1.26

require example.com/shared/v2 v2.0.0
replace example.com/shared/v2 => ./shared
`,
		filepath.Join(shared, "go.mod"):   "module example.com/shared/v2\n\ngo 1.26\n",
		filepath.Join(shared, "money.go"): "package models\n\ntype Money int64\n",
		filepath.Join(store, "model.go"): tags(`package store

import "example.com/shared/v2"

type Product struct {
	ID    int64        @db:"id,pk"@
	Price models.Money @db:"price"@
}
`),
	}
	for path, contents := range files {
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	outPath := filepath.Join(store, "vv_gen.go")
	if err := testGenerator(store).run(outPath); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	if !strings.Contains(out, `models "example.com/shared/v2"`) || !strings.Contains(out, "Price *models.Money") {
		t.Fatalf("versioned column import used v2 instead of declared package models:\n%s", out)
	}
}

func TestLocalSelectorCannotHideVersionedImportDeclarationCollision(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, "store")
	dependency := filepath.Join(root, "value", "v2")
	for _, dir := range []string{store, dependency} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module prefixshadow\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dependency, "value.go"), []byte("package ProductUpdate\n\ntype Value string\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "model.go"), []byte(tags(`package store

import "prefixshadow/value/v2"

type Product struct {
	ID int64 @db:"id,pk"@
	Value ProductUpdate.Value @db:"value"@
}
`)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "helper.go"), []byte("package store\n\nvar v2 = struct{ X int }{}\n\nfunc unrelated() { _ = v2.X }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(store, "vv_gen.go")
	err := testGenerator(store).run(outPath)
	if err == nil || !strings.Contains(err.Error(), `import alias "ProductUpdate"`) ||
		!strings.Contains(err.Error(), "generated declaration ProductUpdate") {
		t.Fatalf("local selector hid versioned import declaration collision: %v", err)
	}
	if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
		t.Fatalf("hidden versioned import collision wrote output: %v", statErr)
	}
}

func TestCrossFileLocalSelectorDoesNotChangeResolvedImportQualifier(t *testing.T) {
	frameworkRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	store := filepath.Join(root, "store")
	dependency := filepath.Join(root, "value", "v2")
	for _, dir := range []string{store, dependency} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	module := "module prefixshadowcontrol\n\ngo 1.26\n\nrequire github.com/frostgrove/vv v0.0.0\n\nreplace github.com/frostgrove/vv => " + frameworkRoot + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dependency, "value.go"), []byte("package ProductUpdate\n\ntype Value string\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "model.go"), []byte(tags(`package store

import "prefixshadowcontrol/value/v2"

type Invoice struct {
	ID int64 @db:"id,pk"@
	Value ProductUpdate.Value @db:"value"@
}
`)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "helper.go"), []byte("package store\n\nvar v2 = struct{ X int }{}\n\nfunc unrelated() { _ = v2.X }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(store, "vv_gen.go")
	if err := testGenerator(store).run(outPath); err != nil {
		t.Fatalf("cross-file local selector changed the resolved import qualifier: %v", err)
	}
	generated, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), `ProductUpdate "prefixshadowcontrol/value/v2"`) {
		t.Fatalf("declared package qualifier was not preserved:\n%s", generated)
	}
	cmd := exec.Command("go", "test", "./store")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off")
	if response, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("resolved cross-file qualifier package does not compile: %v\n%s\n--- generated ---\n%s", err, response, generated)
	}
}

func TestImportAliasCollisionsAcrossSourceFilesAreMadeStable(t *testing.T) {
	out := gen(t, map[string]string{
		"alpha.model.go": `package store

import common "example.com/alpha/common"

type Alpha struct {
	ID    int64        @db:"id,pk"@
	Value common.Value @db:"value"@
}
`,
		"beta.model.go": `package store

import common "example.com/beta/common"

type Beta struct {
	ID    int64        @db:"id,pk"@
	Value common.Value @db:"value"@
}
`,
	}, nil)
	for _, want := range []string{
		`alphaCommon "example.com/alpha/common"`,
		`betaCommon "example.com/beta/common"`,
		"Value *alphaCommon.Value",
		"Value *betaCommon.Value",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("generated collision-safe imports/types lack %q:\n%s", want, out)
		}
	}
	again := gen(t, map[string]string{
		"beta.model.go": `package store
import common "example.com/beta/common"
type Beta struct { ID int64 @db:"id,pk"@; Value common.Value @db:"value"@ }
`,
		"alpha.model.go": `package store
import common "example.com/alpha/common"
type Alpha struct { ID int64 @db:"id,pk"@; Value common.Value @db:"value"@ }
`,
	}, nil)
	if out != again {
		t.Fatal("alias assignment depends on source-file/map iteration order")
	}
}

func TestImportAliasRewriteIsSinglePass(t *testing.T) {
	out := gen(t, map[string]string{"model.go": `package store

	import (
	crud "example.com/alpha"
	alpha "example.com/beta"
)

type Product struct {
	ID    int64       @db:"id,pk"@
	Alpha crud.Value  @db:"alpha"@
	Beta  alpha.Value @db:"beta"@
}
`}, nil)
	for _, want := range []string{
		`alpha "example.com/alpha"`,
		`beta "example.com/beta"`,
		"Alpha *alpha.Value",
		"Beta  *beta.Value",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("single-pass alias rewrite lacks %q:\n%s", want, out)
		}
	}
}

func TestSourceImportAliasCollidingWithGeneratedDeclarationIsRefused(t *testing.T) {
	err := genError(t, `package store

import ProductUpdate "example.com/value"

type Product struct {
	ID    int64               @db:"id,pk"@
	Value ProductUpdate.Value @db:"value"@
}
	`)
	if err == nil || !strings.Contains(err.Error(), `import alias "ProductUpdate"`) ||
		!strings.Contains(err.Error(), "generated declaration ProductUpdate") ||
		!strings.Contains(err.Error(), "rename the source import") {
		t.Fatalf("source/generated declaration collision error = %v", err)
	}
}

func TestGeneratedServiceAndRelationDeclarationsRejectSourceImportCollisions(t *testing.T) {
	tests := []struct {
		name, alias, source string
	}{
		{"relation attrs", "ArticleAuthorAttrs", `package store

import ArticleAuthorAttrs "example.com/relation/value"

type Author struct {
	ID int64 @db:"id,pk"@
}

type Article struct {
	ID int64 @db:"id,pk"@
	RelationValue ArticleAuthorAttrs.Value @db:"relation_value"@
	Author Author @rel:"belongs_to"@
}
		`},
		{"service constructor", "NewArticleService", `package store

import NewArticleService "example.com/service/value"

type Article struct {
	ID int64 @db:"id,pk"@
	ServiceValue NewArticleService.Value @db:"service_value"@
}
		`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(tags(test.source)), 0o644); err != nil {
				t.Fatal(err)
			}
			generator := testGenerator(dir)
			generator.adapter = true
			err := generator.run(filepath.Join(dir, "vv_gen.go"))
			if err == nil || !strings.Contains(err.Error(), `import alias "`+test.alias+`"`) ||
				!strings.Contains(err.Error(), "generated declaration "+test.alias) {
				t.Fatalf("generated declaration collision error = %v", err)
			}
		})
	}
}

func TestGeneratedLocalSelectorDoesNotResurrectAnUnusedSourceImport(t *testing.T) {
	out := gen(t, map[string]string{"model.go": `package store

import out "example.com/value"

var _ = out.Value{}

type Product struct {
	ID      int64     @db:"id,pk"@
	Ignored out.Value @db:"-"@
}
`}, func(g *generator) { g.adapter = true })
	if strings.Contains(out, `"example.com/value"`) {
		t.Fatalf("adapter local out.ID was mistaken for a package selector:\n%s", out)
	}
}

func TestGeneratedSupportImportCollidingWithPackageDeclarationIsRefused(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(tags(`package store

var crud = struct{}{}

type Product struct {
	ID int64 @db:"id,pk"@
}
`)), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "vv_gen.go")
	err := testGenerator(dir).run(outPath)
	if err == nil || !strings.Contains(err.Error(), `generated import alias "crud"`) ||
		!strings.Contains(err.Error(), "package declaration crud") {
		t.Fatalf("generated import/package declaration collision error = %v", err)
	}
	if _, statErr := os.Stat(outPath); !os.IsNotExist(statErr) {
		t.Fatalf("namespace collision wrote output before refusing it: %v", statErr)
	}
}

func TestMethodNameMatchingGeneratedDeclarationDoesNotCauseFalseRefusal(t *testing.T) {
	out := gen(t, map[string]string{"model.go": `package store

type Product struct {
	ID int64 @db:"id,pk"@
}

func (Product) ProductUpdate() {}
`}, nil)
	if !strings.Contains(out, "type ProductUpdate struct {") {
		t.Fatalf("method scope was mistaken for a package declaration:\n%s", out)
	}
}

func TestIntoDestinationImportAliasCollidingWithGeneratedDeclarationIsRefused(t *testing.T) {
	models := t.TempDir()
	into := t.TempDir()
	if err := os.WriteFile(filepath.Join(models, "model.go"), []byte(tags(`package ent

type User struct { ID int64 @db:"id,pk"@ }
`)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(into, "compat.go"), []byte(`package store

import UserUpdate "example.com/value"

var _ = UserUpdate.Value{}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	generator := testGenerator(models)
	generator.into = into
	generator.modelImport = "example.com/app/ent"
	err := generator.run(filepath.Join(into, "vv_gen.go"))
	if err == nil || !strings.Contains(err.Error(), `import alias "UserUpdate"`) ||
		!strings.Contains(err.Error(), "generated declaration UserUpdate") {
		t.Fatalf("destination import/generated declaration collision error = %v", err)
	}
}

func TestIntoDestinationCgoImportDoesNotNeedGoListResolution(t *testing.T) {
	models := t.TempDir()
	into := t.TempDir()
	if err := os.WriteFile(filepath.Join(models, "model.go"), []byte(tags(`package ent

type User struct { ID int64 @db:"id,pk"@ }
`)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(into, "compat.go"), []byte(`package store

/* typedef int vv_int; */
import "C"

var _ = C.vv_int(0)
`), 0o644); err != nil {
		t.Fatal(err)
	}
	generator := testGenerator(models)
	generator.into = into
	generator.modelImport = "example.com/app/ent"
	if err := generator.run(filepath.Join(into, "vv_gen.go")); err != nil {
		t.Fatalf("destination cgo import was treated as an unresolved Go package: %v", err)
	}
}

func TestIntoGeneratedImportAliasCollidingWithDestinationDeclarationIsRefused(t *testing.T) {
	models := t.TempDir()
	into := t.TempDir()
	if err := os.WriteFile(filepath.Join(models, "model.go"), []byte(tags(`package ent

import value "example.com/value"

type User struct {
	ID int64 @db:"id,pk"@
	Value value.Value @db:"value"@
}
`)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(into, "compat.go"), []byte("package store\n\ntype value string\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	generator := testGenerator(models)
	generator.into = into
	generator.modelImport = "example.com/app/ent"
	err := generator.run(filepath.Join(into, "vv_gen.go"))
	if err == nil || !strings.Contains(err.Error(), `generated import alias "value"`) ||
		!strings.Contains(err.Error(), "package declaration value") {
		t.Fatalf("generated import/destination declaration collision error = %v", err)
	}
}

func TestAuthoredDeclarationCollidingWithGeneratedDeclarationIsRefused(t *testing.T) {
	err := genError(t, `package store

type Product struct {
	ID int64 @db:"id,pk"@
}

type ProductUpdate struct {
	Legacy string
}
`)
	if err == nil || !strings.Contains(err.Error(), "package declaration ProductUpdate") ||
		!strings.Contains(err.Error(), "generated declaration ProductUpdate") {
		t.Fatalf("authored/generated declaration collision error = %v", err)
	}
}

func TestIntoKeepsModelPackageDeclarationsOutOfTheOutputCollisionSet(t *testing.T) {
	out := gen(t, map[string]string{"model.go": `package ent

type User struct {
	ID int64
}

type UserUpdate struct {
	Legacy string
}
`}, func(g *generator) {
		g.only = map[string]bool{"User": true}
		g.into = filepath.Join(t.TempDir(), "store")
		g.modelImport = "example.com/app/ent"
	})
	if !strings.Contains(out, "type UserUpdate struct {") ||
		!strings.Contains(out, "specs.Metamodel[ent.User, UserAttrs]") {
		t.Fatalf("-into treated a declaration in the model package as an output collision:\n%s", out)
	}
}

func TestIntoRefusesADeclarationCollisionInTheOutputPackage(t *testing.T) {
	models := t.TempDir()
	into := t.TempDir()
	if err := os.WriteFile(filepath.Join(models, "model.go"), []byte(tags(`package ent

type User struct { ID int64 @db:"id,pk"@ }
`)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(into, "compat.go"), []byte(`package store

type UserUpdate struct{ Legacy string }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	generator := testGenerator(models)
	generator.into = into
	generator.modelImport = "example.com/app/ent"
	err := generator.run(filepath.Join(into, "vv_gen.go"))
	if err == nil || !strings.Contains(err.Error(), "package declaration UserUpdate") ||
		!strings.Contains(err.Error(), "generated declaration UserUpdate") {
		t.Fatalf("output package declaration collision error = %v", err)
	}
}

func TestConcatenatedRelationDeclarationCollisionIsRefused(t *testing.T) {
	dir := t.TempDir()
	source := tags(`package store

type C struct { ID int64 @db:"id,pk"@ }
type BC struct { ID int64 @db:"id,pk"@ }
type A struct { ID int64 @db:"id,pk"@; BC BC @rel:"has_one"@ }
type AB struct { ID int64 @db:"id,pk"@; C C @rel:"has_one"@ }
type Root struct {
	ID int64 @db:"id,pk"@
	A A @rel:"has_one"@
	AB AB @rel:"has_one"@
}
`)
	if err := os.WriteFile(filepath.Join(dir, "model.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	generator := testGenerator(dir)
	generator.depth = 3
	err := generator.run(filepath.Join(dir, "vv_gen.go"))
	if err == nil || !strings.Contains(err.Error(), "generated declaration ABCAttrs") ||
		!strings.Contains(err.Error(), "model A relation BC") ||
		!strings.Contains(err.Error(), "model AB relation C") {
		t.Fatalf("relation declaration collision error = %v", err)
	}
}

func TestFrameworkAndColumnImportsOfTheSamePathAreDeduplicated(t *testing.T) {
	out := gen(t, map[string]string{"model.go": `package store

import fork "example.com/fork/crud"

type Product struct {
	ID    int64      @db:"id,pk"@
	Value fork.Value @db:"value"@
}
`}, func(g *generator) { g.crudPkg = "example.com/fork/crud" })
	if count := strings.Count(out, `"example.com/fork/crud"`); count != 1 {
		t.Fatalf("custom framework/type package was imported %d times:\n%s", count, out)
	}
	if !strings.Contains(out, `crud "example.com/fork/crud"`) || !strings.Contains(out, "Value *crud.Value") {
		t.Fatalf("custom framework import did not keep the generated crud alias:\n%s", out)
	}
}

func TestModelAndFrameworkUseOneAliasForTheSameImportPath(t *testing.T) {
	out := gen(t, map[string]string{"model.go": `package crud

type Product struct {
	ID int64 @db:"id,pk"@
}
`}, func(g *generator) {
		g.into = filepath.Join(t.TempDir(), "store")
		g.modelImport = DefaultCrudPkg
	})
	if count := strings.Count(out, `"`+DefaultCrudPkg+`"`); count != 1 {
		t.Fatalf("model/support package was imported %d times:\n%s", count, out)
	}
	for _, want := range []string{"crud.Product", "crud.Repo[crud.Product"} {
		if !strings.Contains(out, want) {
			t.Fatalf("shared model/framework alias lacks %q:\n%s", want, out)
		}
	}
}

func TestDotImportIsRefusedInsteadOfProducingAnUnqualifiedType(t *testing.T) {
	err := genError(t, `package store

import . "example.com/value"

type Product struct {
	ID    int64 @db:"id,pk"@
	Value UUID  @db:"value"@
}
`)
	if err == nil || !strings.Contains(err.Error(), "dot import") || !strings.Contains(err.Error(), "explicit import alias") {
		t.Fatalf("dot-import error = %v", err)
	}
}

func TestExternalEmbedCarriesARequiredFixedAliasImport(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store := filepath.Join(dir, "store")
	shared := filepath.Join(dir, "shared")
	for _, path := range []string{store, shared} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(tags(content)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "go.mod"), "module fixedaliasembed\n\ngo 1.26\n\nrequire github.com/frostgrove/vv v0.0.0\n\nreplace github.com/frostgrove/vv => "+root+"\n")
	write(filepath.Join(shared, "audit.go"), `package shared

import "github.com/frostgrove/vv/utils"

type Audit struct {
	Value utils.Optional @db:"value"@
}
`)
	write(filepath.Join(store, "model.go"), `package store

import "fixedaliasembed/shared"

type Product struct {
	ID int64 @db:"id,pk"@
	shared.Audit
}
`)
	outPath := filepath.Join(store, "vv_gen.go")
	if err := testGenerator(store).run(outPath); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), `"github.com/frostgrove/vv/utils"`) ||
		!strings.Contains(string(generated), "Value *utils.Optional") {
		t.Fatalf("transitive fixed-alias type lost its import:\n%s", generated)
	}
	cmd := exec.Command("go", "test", "./store")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off")
	if response, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated external-embed package does not compile: %v\n%s\n--- generated ---\n%s", err, response, generated)
	}
}

func TestCompositeAndGenericColumnImportsCompile(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store := filepath.Join(dir, "store")
	for _, path := range []string{store, filepath.Join(dir, "money"), filepath.Join(dir, "box")} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(tags(content)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "go.mod"), "module genimports\n\ngo 1.26\n\nrequire github.com/frostgrove/vv v0.0.0\n\nreplace github.com/frostgrove/vv => "+root+"\n")
	write(filepath.Join(dir, "money", "money.go"), "package money\n\ntype Amount int64\n")
	write(filepath.Join(dir, "box", "box.go"), "package box\n\ntype Box[T any] []T\n")
	write(filepath.Join(store, "model.go"), `package store

import (
	"genimports/box"
	"genimports/money"
)

type Ledger struct {
	ID      int64                   @db:"id,pk"@
	Items   []money.Amount          @db:"items"@
	Lookup  map[string]money.Amount @db:"lookup"@
	Pair    [2]money.Amount         @db:"pair"@
	Wrapped box.Box[money.Amount]   @db:"wrapped"@
}
`)
	if err := testGenerator(store).run(filepath.Join(store, "vv_gen.go")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "test", "./store")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off")
	if response, err := cmd.CombinedOutput(); err != nil {
		generated, _ := os.ReadFile(filepath.Join(store, "vv_gen.go"))
		t.Fatalf("generated composite types do not compile: %v\n%s\n--- generated ---\n%s", err, response, generated)
	}
}

func TestReadableCollisionAliasesCompile(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	directories := []string{
		"alpha/common", "beta/common", "shared", "store", "transitive/product",
	}
	for _, name := range directories {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(tags(content)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module genaliases\n\ngo 1.26\n\nrequire github.com/frostgrove/vv v0.0.0\n\nreplace github.com/frostgrove/vv => "+root+"\n")
	for _, name := range []string{"alpha/common", "beta/common"} {
		packageName := filepath.Base(name)
		write(filepath.Join(name, "value.go"), "package "+packageName+"\n\ntype Value string\n")
	}
	write("transitive/product/value.go", "package ProductUpdate\n\ntype Value string\n")
	write("shared/audit.go", `package shared

import ProductUpdate "genaliases/transitive/product"

type Audit struct {
	Collision ProductUpdate.Value @db:"collision"@
}
`)
	write("store/alpha.model.go", `package store

import common "genaliases/alpha/common"

type Alpha struct {
	ID int64 @db:"id,pk"@
	Value common.Value @db:"value"@
}
`)
	write("store/beta.model.go", `package store

import common "genaliases/beta/common"

type Beta struct {
	ID int64 @db:"id,pk"@
	Value common.Value @db:"value"@
}
`)
	write("store/article.model.go", `package store

type Author struct {
	ID int64 @db:"id,pk"@
}

type Article struct {
	ID int64 @db:"id,pk"@
	AuthorID int64 @db:"author_id"@
	Author Author @rel:"belongs_to"@
}
`)
	write("store/product.model.go", `package store

import "genaliases/shared"

type Product struct {
	ID int64 @db:"id,pk"@
	shared.Audit
}
`)
	generator := testGenerator(filepath.Join(dir, "store"))
	generator.adapter = true
	outPath := filepath.Join(dir, "store", "vv_gen.go")
	if err := generator.run(outPath); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`alphaCommon "genaliases/alpha/common"`,
		`betaCommon "genaliases/beta/common"`,
		`product "genaliases/transitive/product"`,
		"Collision *product.Value",
	} {
		if !strings.Contains(string(generated), want) {
			t.Fatalf("readable collision output lacks %q:\n%s", want, generated)
		}
	}
	cmd := exec.Command("go", "test", "./store")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off")
	if response, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated readable-alias package does not compile: %v\n%s\n--- generated ---\n%s", err, response, generated)
	}
}

func TestGormPackageNameCannotStealTheWellKnownGormAlias(t *testing.T) {
	out := gen(t, map[string]string{"model.go": `package gorm

import orm "gorm.io/gorm"

type Team struct {
	orm.Model
	Name string @db:"name"@
}
`}, func(g *generator) {
		g.into = filepath.Join(t.TempDir(), "store")
		g.modelImport = "example.com/models"
	})
	if !strings.Contains(out, `gorm "gorm.io/gorm"`) || !strings.Contains(out, `models "example.com/models"`) {
		t.Fatalf("model and well-known gorm imports collide:\n%s", out)
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
	err := g.run(filepath.Join(g.into, "vv_gen.go"))
	if err == nil {
		t.Fatal("writing into another package without knowing its import path should be refused")
	}
	if !strings.Contains(err.Error(), "-import") {
		t.Fatalf("err = %v, want it to name the missing flag", err)
	}
}

func TestAPackageWithoutModelFilesIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "helper.go"), []byte(`package empty

type NotAModel struct {
	Name string
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	g := &generator{dir: dir, depth: 2, withDTO: true, withMeta: true}
	if err := g.run(filepath.Join(dir, "vv_gen.go")); err == nil {
		t.Fatal("a package without model files should be an error, not an empty file")
	}
}

func TestRecursiveGenerationFindsModelFilesAndSkipsPrivateHelpers(t *testing.T) {
	root := t.TempDir()
	productDir := filepath.Join(root, "app", "product")
	helperDir := filepath.Join(root, "app", "example")
	if err := os.MkdirAll(productDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(helperDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(productDir, "product.model.go"), []byte(`package product

type Product struct {
	ID   int64
	Name string
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(helperDir, "example.model.go"), []byte(`package example

type placeholder struct{}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Run(&Options{Dir: root, Out: "vv_gen.go", WithDTO: true, WithMeta: true, Recursive: true}); err != nil {
		t.Fatalf("recursive generation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(productDir, "vv_gen.go")); err != nil {
		t.Fatalf("product output: %v", err)
	}
	if _, err := os.Stat(filepath.Join(helperDir, "vv_gen.go")); !os.IsNotExist(err) {
		t.Fatalf("private helper produced output, err = %v", err)
	}
}

func TestGeneratingOnlyOneHalf(t *testing.T) {
	noDTO := gen(t, map[string]string{"model.go": blogModel}, func(g *generator) {
		g.withDTO = false
		g.withRepo = false
	})
	if strings.Contains(noDTO, "ArticleUpdate") {
		t.Fatalf("-no-dto still wrote a DTO:\n%s", noDTO)
	}
	if !strings.Contains(noDTO, "ArticleAttrs") {
		t.Fatalf("-no-dto took the metamodel with it:\n%s", noDTO)
	}
	// With no DTO nothing needs crud, and the import goes away with it.
	if strings.Contains(noDTO, `"github.com/frostgrove/vv/crud"`) {
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

	// The adapter half has two more map iterations in it — the inverse map and
	// the exclusion list — so it gets the same treatment rather than inheriting
	// the claim.
	firstAdapter := gen(t, files, func(g *generator) {
		g.adapter = true
		g.readonly = names("Views,Title")
	})
	for i := range 8 {
		got := gen(t, files, func(g *generator) {
			g.adapter = true
			g.readonly = names("Views,Title")
		})
		if got != firstAdapter {
			t.Fatalf("adapter run %d produced different bytes:\n%s\n---\n%s", i+2, firstAdapter, got)
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

// runGenerated builds a throwaway module holding nothing but a model and the
// file the generator wrote for it, and runs it.
//
// Package initialisation is the whole point: the metamodel's check, the inverse
// path map and the update-coverage assertion all live there, so "it starts" and
// "it refuses to start" are the two answers this can give.
func runGenerated(t *testing.T, model, generated string) (string, error) {
	t.Helper()
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
	write("go.mod", "module gencheck\n\ngo 1.26\n\nrequire github.com/frostgrove/vv v0.0.0\n\nreplace github.com/frostgrove/vv => "+root+"\n")
	write(filepath.Join("model", "model.go"), model)
	write(filepath.Join("model", "vv_gen.go"), generated)
	write("main.go", "package main\n\nimport _ \"gencheck/model\"\n\nfunc main() {}\n")

	cmd := exec.Command("go", "run", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=mod", "GOPROXY=off")
	response, err := cmd.CombinedOutput()
	return string(response), err
}

// The proof that matters: the generated file builds, and the metamodel's
// package-init check agrees with the model it was generated from.
func TestGeneratedCodeCompilesAndValidates(t *testing.T) {
	out := gen(t, map[string]string{"model.go": blogModel}, nil)
	if response, err := runGenerated(t, tags(blogModel), out); err != nil {
		t.Fatalf("the generated code does not build and run: %v\n%s\n---- generated ----\n%s", err, response, out)
	}
}

// resourceModel is the fixture for the adapter half: a key the database
// generates, two ordinary columns and one the database fills.
const resourceModel = `package m

import "time"

type Doc struct {
	ID        int64     @db:"id,pk,auto"@
	Title     string    @db:"title"@
	Body      string    @db:"body"@
	CreatedAt time.Time @db:"created_at,generated"@
}
`

func withAdapter(g *generator) { g.adapter = true }

// The phase's load-bearing test. A column the generated artefacts do not cover
// refuses to start, and it does so with nothing regenerated — which is the half
// regenerate-and-diff cannot reach, because that comparison only ever measures
// the generator against itself.
func TestAGeneratedResourceRefusesToStartWhenAColumnIsMissing(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}

	// The control. Without it a binary that never builds for some unrelated
	// reason would pass both arms below by failing for the wrong cause.
	t.Run("the untampered resource starts", func(t *testing.T) {
		out := gen(t, map[string]string{"model.go": resourceModel}, withAdapter)
		if response, err := runGenerated(t, tags(resourceModel), out); err != nil {
			t.Fatalf("the generated resource refused to start: %v\n%s\n---- generated ----\n%s", err, response, out)
		}
	})

	// The scenario UC-014 gap 1 describes: somebody adds a column and does not
	// regenerate. The generator read the model's source text; the assertion
	// reads the compiled struct. That is what makes this a check rather than a
	// tautology.
	t.Run("a column the model gained without regenerating", func(t *testing.T) {
		out := gen(t, map[string]string{"model.go": resourceModel}, nil)
		grown := strings.Replace(tags(resourceModel),
			"\tBody      string    `db:\"body\"`",
			"\tBody      string    `db:\"body\"`\n\tColour    string    `db:\"colour\"`", 1)
		if grown == tags(resourceModel) {
			t.Fatal("the fixture did not gain a column, so this measures nothing")
		}
		response, err := runGenerated(t, grown, out)
		if err == nil {
			t.Fatalf("a column the DTO does not cover started cleanly; it is silently unpatchable:\n%s", response)
		}
		if !strings.Contains(response, "Colour") {
			t.Fatalf("the refusal does not name the column somebody has to act on:\n%s", response)
		}
	})

	// The other direction: the map is edited by hand, which is what
	// DO NOT EDIT is there to stop and what nothing could catch before.
	t.Run("an entry deleted from the inverse map", func(t *testing.T) {
		out := gen(t, map[string]string{"model.go": resourceModel}, withAdapter)
		cut := strings.Replace(out, "\t\"Title\": port.At(\"title\"),\n", "", 1)
		if cut == out {
			t.Fatalf("the fixture has no Title entry to delete:\n%s", out)
		}
		response, err := runGenerated(t, tags(resourceModel), cut)
		if err == nil {
			t.Fatalf("a map missing a column started cleanly; the wrong path would ship:\n%s", response)
		}
		if !strings.Contains(response, "Title") {
			t.Fatalf("the refusal does not name the column:\n%s", response)
		}
	})
}

// The map's domain, asserted against what a client can and cannot send.
func TestTheGeneratedMapCoversEveryWritableColumn(t *testing.T) {
	out := gen(t, map[string]string{"model.go": resourceModel}, withAdapter)
	block := decl(t, out, "var DocPaths = port.MustPathMap[Doc](port.PathMap{")

	for _, want := range []string{`"Title"`, `"Body"`} {
		if !strings.Contains(block, want) {
			t.Fatalf("the map has no entry for %s:\n%s", want, block)
		}
	}
	if strings.Contains(block, `"ID":`) {
		t.Fatalf("an auto-generated key has a request path although the body cannot carry it:\n%s", block)
	}
	// The control: a column the database fills is deliberately outside the
	// domain, so a generator that emitted every column fails here rather than
	// passing the loop above.
	if strings.Contains(block, `"CreatedAt"`) {
		t.Fatalf("a generated column has an entry; no client sends a key for one:\n%s", block)
	}
	// And the input body agrees with the map, which is what the start-up check
	// measures the two halves against.
	input := decl(t, out, "type DocInput struct {")
	if !declares(input, "Title") || declares(input, "ID") || declares(input, "CreatedAt") {
		t.Fatalf("the entity body and the map disagree:\n%s", input)
	}
	if !strings.Contains(out, `}, "ID")`) {
		t.Fatalf("the generated path-map declaration does not state why the auto key is absent:\n%s", out)
	}
}

func TestAnAssignedPrimaryKeyRemainsInTheGeneratedInput(t *testing.T) {
	source := `package m

type Document struct {
	Key   string @db:"key,pk"@
	Title string @db:"title"@
}
`
	out := gen(t, map[string]string{"model.go": source}, withAdapter)
	input := decl(t, out, "type DocumentInput struct {")
	paths := decl(t, out, "var DocumentPaths = port.MustPathMap[Document](port.PathMap{")
	if !declares(input, "Key") || !strings.Contains(paths, `"Key"`) {
		t.Fatalf("client-owned key disappeared from input or path map:\n%s", out)
	}
}

// The exclusion list is what carries a command-line flag into a file that
// reflection reads. Without it the assertion refuses a column dropped on purpose.
func TestTheGeneratedAssertionNamesTheReadonlyExclusions(t *testing.T) {
	out := gen(t, map[string]string{"model.go": resourceModel}, func(g *generator) {
		g.adapter = true
		g.readonly = names("Body")
	})
	if !strings.Contains(out, `port.MustCoverUpdate[Doc, DocUpdate]("Body")`) {
		t.Fatalf("the coverage assertion does not declare the -readonly column:\n%s", out)
	}
	if !strings.Contains(out, `}, "Body", "ID")`) {
		t.Fatalf("the inverse map does not declare the -readonly column:\n%s", out)
	}
	// The control: with no flag the list is empty, so this is not a generator
	// that names every column whatever it was told.
	plain := gen(t, map[string]string{"model.go": resourceModel}, withAdapter)
	if !strings.Contains(plain, `port.MustCoverUpdate[Doc, DocUpdate]()`) {
		t.Fatalf("an exclusion appeared with nothing declared:\n%s", plain)
	}
}

// The lock, end to end: the artefacts a versioned model produces are ones that
// start. This is the case that had no model in the tree at all.
func TestAVersionedModelGeneratesAResourceThatStarts(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain")
	}
	source := `package m

type Doc struct {
	ID      int64  @db:"id,pk,auto"@
	Title   string @db:"title"@
	Version int    @db:"version,version"@
}
`
	out := gen(t, map[string]string{"model.go": source}, withAdapter)
	if response, err := runGenerated(t, tags(source), out); err != nil {
		t.Fatalf("a versioned model produced a package that does not start: %v\n%s\n---- generated ----\n%s", err, response, out)
	}

	if declares(decl(t, out, "type DocInput struct {"), "Version") {
		t.Fatalf("the lock reached the entity body:\n%s", out)
	}

	// The control: name the lock in the map and the package refuses to start,
	// because no request carries that key. Without it, a validator that accepted
	// anything would pass the arm above.
	named := strings.Replace(out, "\t\"Title\": port.At(\"title\"),\n",
		"\t\"Title\":   port.At(\"title\"),\n\t\"Version\": port.At(\"version\"),\n", 1)
	if named == out {
		t.Fatalf("the fixture has no Title entry to extend:\n%s", out)
	}
	response, err := runGenerated(t, tags(source), named)
	if err == nil {
		t.Fatalf("a map claiming the lock as a request key started cleanly:\n%s", response)
	}
	if !strings.Contains(response, "Version") {
		t.Fatalf("the refusal does not name the entry:\n%s", response)
	}
}

// The optimistic lock is the repository's column: it pins the write to the
// version it read and advances it. crud.PlanFor refuses a DTO that names it, so
// a generator that emitted it produced a package which panicked at Define time —
// the two features shipped in the same change and did not know about each other.
func TestTheVersionColumnIsLeftOutOfTheDTO(t *testing.T) {
	source := tags(`package m

import "time"

type Doc struct {
	ID        int64     @db:"id,pk,auto"@
	Title     string    @db:"title"@
	Version   int       @db:"version,version"@
	Revision  int       @db:"revision,lock"@
	UpdatedAt time.Time @db:"updated_at"@
}
`)
	out := gen(t, map[string]string{"model.go": source}, nil)

	dataTransferObject := decl(t, out, "type DocUpdate struct {")
	for _, f := range []string{"Version", "Revision"} {
		if declares(dataTransferObject, f) {
			t.Fatalf("%s is in the update DTO; sqlrepo.Define will panic on it:\n%s", f, dataTransferObject)
		}
	}
	if !declares(dataTransferObject, "Title") {
		t.Fatalf("an ordinary column left the DTO with it:\n%s", dataTransferObject)
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
