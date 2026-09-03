package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/frostgrove/vv/internal/cachegen"
	"github.com/frostgrove/vv/internal/codegen"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "vv:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) >= 2 && args[0] == "generate" && args[1] == "cache" {
		return runCache(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "generate" && args[1] == "resource" {
		return runResource(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "generate" && args[1] == "routes" {
		return runRoutes(args[2:], stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "generate" && args[1] == "module" {
		return runModule(args[2:], stdout, stderr)
	}
	return runModels(args, stdout, stderr)
}

func runCache(args []string, stdout, stderr io.Writer) error {
	var options cachegen.Options
	flags := flag.NewFlagSet("vv generate cache", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.Dir, "dir", ".", "package directory")
	flags.StringVar(&options.Out, "out", "vv_cache_gen.go", "generated Go file name")
	flags.StringVar(&options.Manifest, "manifest", "cache.manifest.yml", "cache manifest file name")
	flags.StringVar(&options.GOOS, "goos", "", "deployment GOOS; default is the effective go env")
	flags.StringVar(&options.GOARCH, "goarch", "", "deployment GOARCH; default is the effective go env")
	flags.BoolVar(&options.Check, "check", false, "check generated artifacts without writing")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("generate cache accepts no positional arguments")
	}
	options.Log = stdout
	return cachegen.Run(&options)
}

func runResource(args []string, stdout, stderr io.Writer) error {
	var options codegen.ResourceOptions
	flags := flag.NewFlagSet("vv generate resource", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.Dir, "dir", ".", "package directory")
	flags.StringVar(&options.Out, "out", "vv_wire_gen.go", "generated Go file name")
	flags.StringVar(&options.Manifest, "manifest", "resource.manifest.yml", "per-resource manifest file name")
	flags.StringVar(&options.Types, "types", "", "comma-separated model names; default is every model-file struct")
	flags.StringVar(&options.Skip, "skip", "", "comma-separated field names to leave out entirely")
	flags.StringVar(&options.Readonly, "readonly", "", "comma-separated field names to keep out of the update DTO")
	flags.StringVar(&options.Into, "into", "", "write into this directory instead of -dir")
	flags.StringVar(&options.Import, "import", "", "import path of -dir, used to qualify model types written elsewhere")
	flags.BoolVar(&options.Recursive, "recursive", true, "walk model files below -dir and generate beside each package")
	flags.BoolVar(&options.Check, "check", false, "check the generated artefacts without writing")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("generate resource accepts no positional arguments")
	}
	options.Log = stdout
	return codegen.RunResource(&options)
}

func runRoutes(args []string, stdout, stderr io.Writer) error {
	var options codegen.RouteOptions
	flags := flag.NewFlagSet("vv generate routes", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.Dir, "dir", ".", "package directory")
	flags.StringVar(&options.Out, "out", codegen.DefaultRoutesOut, "generated Go file name")
	flags.StringVar(&options.Manifest, "manifest", codegen.DefaultRoutesFile, "route manifest file name")
	flags.StringVar(&options.GuardPkg, "guard", codegen.DefaultGuardPkg, "import path of the package whose guard a use case calls")
	flags.StringVar(&options.GuardFunc, "guard-func", codegen.DefaultGuardFunc, "name of the guard function inside -guard")
	flags.StringVar(&options.DeclarePkg, "declare", codegen.DefaultDeclarePkg, "import path of the package that declares endpoints")
	flags.StringVar(&options.AuthPkg, "auth", codegen.DefaultAuthPkg, "import path of the package that owns Permission")
	flags.BoolVar(&options.Recursive, "recursive", true, "walk guarded packages below -dir and generate beside each one")
	flags.BoolVar(&options.Check, "check", false, "check the generated artefacts without writing")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("generate routes accepts no positional arguments")
	}
	options.Log = stdout
	return codegen.RunRoutes(&options)
}

func runModule(args []string, stdout, stderr io.Writer) error {
	var options codegen.ModuleOptions
	flags := flag.NewFlagSet("vv generate module", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.Dir, "dir", ".", "module directory; its whole package tree is scanned")
	flags.StringVar(&options.Out, "out", codegen.DefaultModuleOut, "generated Go file name")
	flags.StringVar(&options.Manifest, "manifest", codegen.DefaultModuleFile, "module manifest file name")
	flags.StringVar(&options.Name, "name", "", "module name; default is the directory name")
	flags.IntVar(&options.Order, "order", 0, "order this module takes in a catalog")
	flags.StringVar(&options.Import, "import", "", "import path of -dir; default is read from the nearest go.mod")
	flags.StringVar(&options.ModulePkg, "module", codegen.DefaultModulePkg, "import path of the package that owns Definition")
	flags.StringVar(&options.CheckType, "check-type", codegen.DefaultCheckType, "result type that makes a constructor a health check; - to infer none")
	flags.StringVar(&options.RouteType, "route-type", codegen.DefaultRouteType, "result type that makes a constructor a route; - to infer none")
	flags.StringVar(&options.WorkerType, "worker-type", codegen.DefaultWorkerType, "result type that makes a constructor a worker; - to infer none")
	flags.StringVar(&options.SeederType, "seeder-type", codegen.DefaultSeederType, "result type that makes a constructor a seeder; - to infer none")
	flags.BoolVar(&options.Recursive, "recursive", false, "treat every package directly under -dir as its own module")
	flags.BoolVar(&options.Check, "check", false, "check the generated artefacts without writing")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("generate module accepts no positional arguments")
	}
	options.Log = stdout
	return codegen.RunModule(&options)
}

func runModels(args []string, stdout, stderr io.Writer) error {
	var options codegen.Options
	if len(args) > 0 && args[0] == "generate" {
		options.Recursive = true
		args = args[1:]
	}
	flags := flag.NewFlagSet("vv", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.Dir, "dir", ".", "package directory")
	flags.StringVar(&options.Out, "out", "vv_gen.go", "output file name")
	flags.StringVar(&options.Types, "types", "", "comma-separated model names; default is every model-file struct")
	flags.StringVar(&options.Skip, "skip", "", "comma-separated field names to leave out entirely")
	flags.StringVar(&options.Readonly, "readonly", "", "comma-separated field names to keep out of the update DTO")
	flags.StringVar(&options.Into, "into", "", "write into this directory instead of -dir")
	flags.StringVar(&options.Import, "import", "", "import path of -dir, used to qualify model types written elsewhere")
	flags.IntVar(&options.Depth, "depth", 2, "how far to expand relation paths into the metamodel")
	flags.StringVar(&options.SpecsPkg, "specs", "github.com/frostgrove/vv/crud/decorators/specs", "import path of the specs package")
	flags.StringVar(&options.CrudPkg, "crud", "github.com/frostgrove/vv/crud", "import path of the crud package")
	flags.StringVar(&options.UtilsPkg, "utils", "github.com/frostgrove/vv/utils", "import path of the shared utils package")
	flags.BoolVar(&options.Adapter, "adapter", false, "also generate the resource adapter: input DTO, mapper, inverse path map, service and wiring")
	flags.StringVar(&options.Binding, "binding", "net", "which transport the generated wiring is written for: net or none")
	flags.BoolVar(&options.Recursive, "recursive", options.Recursive, "walk model files below -dir and generate beside each package")
	flags.BoolVar(&options.Check, "check", false, "check the generated artefacts without writing")
	noDTO := flags.Bool("no-dto", false, "skip update DTOs")
	noMeta := flags.Bool("no-meta", false, "skip metamodels")
	flags.BoolVar(&options.NoRepo, "no-repo", false, "skip repository blueprints and binding factories")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	options.WithDTO = !*noDTO
	options.WithMeta = !*noMeta
	options.Log = stdout
	return codegen.Run(&options)
}
