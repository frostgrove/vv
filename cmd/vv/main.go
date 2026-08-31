package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/frostgrove/vv/internal/codegen"
)

func main() {
	var o codegen.Options
	if len(os.Args) > 1 && os.Args[1] == "generate" {
		o.Recursive = true
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}
	flag.StringVar(&o.Dir, "dir", ".", "package directory")
	flag.StringVar(&o.Out, "out", "vv_gen.go", "output file name")
	flag.StringVar(&o.Types, "types", "", "comma-separated model names; default is every model-file struct")
	flag.StringVar(&o.Skip, "skip", "", "comma-separated field names to leave out entirely")
	flag.StringVar(&o.Readonly, "readonly", "", "comma-separated field names to keep out of the update DTO")
	flag.StringVar(&o.Into, "into", "", "write into this directory instead of -dir")
	flag.StringVar(&o.Import, "import", "", "import path of -dir, used to qualify model types written elsewhere")
	flag.IntVar(&o.Depth, "depth", 2, "how far to expand relation paths into the metamodel")
	flag.StringVar(&o.SpecsPkg, "specs", "github.com/frostgrove/vv/crud/decorators/specs", "import path of the specs package")
	flag.StringVar(&o.CrudPkg, "crud", "github.com/frostgrove/vv/crud", "import path of the crud package")
	flag.StringVar(&o.UtilsPkg, "utils", "github.com/frostgrove/vv/utils", "import path of the shared utils package")
	flag.BoolVar(&o.Adapter, "adapter", false, "also generate the resource adapter: input DTO, mapper, inverse path map, service and wiring")
	flag.StringVar(&o.Binding, "binding", "net", "which transport the generated wiring is written for: net or none")
	flag.BoolVar(&o.Recursive, "recursive", o.Recursive, "walk model files below -dir and generate beside each package")
	noDTO := flag.Bool("no-dto", false, "skip update DTOs")
	noMeta := flag.Bool("no-meta", false, "skip metamodels")
	flag.BoolVar(&o.NoRepo, "no-repo", false, "skip repository blueprints and binding factories")
	flag.Parse()

	o.WithDTO = !*noDTO
	o.WithMeta = !*noMeta
	o.Log = os.Stdout

	if err := codegen.Run(&o); err != nil {
		fmt.Fprintln(os.Stderr, "vv:", err)
		os.Exit(1)
	}
}
