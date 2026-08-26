// Command vv generates the two things you would otherwise copy out of your
// model by hand: the partial-update DTO and the typed metamodel.
//
//	//go:generate go run github.com/frostgrove/vv/cmd/vv
//
// Point it at a package; it reads every struct that carries `db` or `rel` tags
// and writes vv_gen.go next to them:
//
//	type ArticleUpdate struct { … }   // pointers for optional, Opt for nullable
//	type ArticleAttrs  struct { … }   // including nested relation paths
//	var  Article_ = specs.Metamodel[Article, ArticleAttrs]()
//
// After that `Article_.Author.Name.Eq("Ann")` is a compile-time-checked filter
// on a joined column, and a renamed field breaks the build instead of a request.
//
// Flags:
//
//	-dir     package directory (default ".")
//	-out     output file name (default "vv_gen.go")
//	-types   comma-separated list; default is every tagged struct
//	-depth   how far to expand relation paths into the metamodel (default 2)
//	-skip    comma-separated field names to leave out entirely (like db:"-")
//	-readonly comma-separated field names kept out of the update DTO but still
//	         filterable and sortable (like db:",immutable")
//	-into    write into this directory instead of -dir
//	-import  import path of -dir, so model types are qualified when written
//	         somewhere else
//	-no-dto  skip the update DTOs
//	-no-meta skip the metamodels
//	-adapter also generate the resource adapter: the entity-body DTO, the
//	         mapper, its inverse path map, the service shell and the wiring
//	-binding which transport the wiring is written for: net (default) or none
//	-specs   import path of the specs package
//	-crud    import path of the crud package
//
// With -adapter the resource gets a wire shape of its own — <Model>Input, a
// <Model>Mapper onto the model, and <Model>Paths, the inverse of that mapping.
// The inverse is what makes an error body name the key the client sent rather
// than the model's field name, and it is generated rather than written because
// a hand-written one is wrong the first time somebody renames a key.
//
// Whether or not -adapter is on, the generated file asserts at package
// initialisation that the update DTO covers every writable column. Add a column
// and forget to regenerate, and the package refuses to start instead of leaving
// the column silently unpatchable.
//
// With -types the named structs are taken as models even without db tags, which
// is what makes ent's generated entities work as-is. Write the result into your
// own package rather than into ent's, where the names would collide:
//
//	go run github.com/frostgrove/vv/cmd/vv -dir ./ent -types User,Article -skip CreatedAt \
//	    -import myapp/ent -into ./internal/store
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/frostgrove/vv/internal/codegen"
)

func main() {
	var o codegen.Options
	flag.StringVar(&o.Dir, "dir", ".", "package directory")
	flag.StringVar(&o.Out, "out", "vv_gen.go", "output file name")
	flag.StringVar(&o.Types, "types", "", "comma-separated model names; default is every tagged struct")
	flag.StringVar(&o.Skip, "skip", "", "comma-separated field names to leave out entirely")
	flag.StringVar(&o.Readonly, "readonly", "", "comma-separated field names to keep out of the update DTO")
	flag.StringVar(&o.Into, "into", "", "write into this directory instead of -dir")
	flag.StringVar(&o.Import, "import", "", "import path of -dir, used to qualify model types written elsewhere")
	flag.IntVar(&o.Depth, "depth", 2, "how far to expand relation paths into the metamodel")
	flag.StringVar(&o.SpecsPkg, "specs", "github.com/frostgrove/vv/crud/decorators/specs", "import path of the specs package")
	flag.StringVar(&o.CrudPkg, "crud", "github.com/frostgrove/vv/crud", "import path of the crud package")
	flag.BoolVar(&o.Adapter, "adapter", false, "also generate the resource adapter: input DTO, mapper, inverse path map, service and wiring")
	flag.StringVar(&o.Binding, "binding", "net", "which transport the generated wiring is written for: net or none")
	noDTO := flag.Bool("no-dto", false, "skip update DTOs")
	noMeta := flag.Bool("no-meta", false, "skip metamodels")
	flag.Parse()

	o.WithDTO = !*noDTO
	o.WithMeta = !*noMeta
	o.Log = os.Stdout

	if err := codegen.Run(o); err != nil {
		fmt.Fprintln(os.Stderr, "vv:", err)
		os.Exit(1)
	}
}
