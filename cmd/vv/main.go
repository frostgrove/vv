package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/frostgrove/vv/internal/cachegen"
	"github.com/frostgrove/vv/internal/codegen"
	"github.com/frostgrove/vv/internal/jobsgen"
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
	if len(args) >= 2 && args[0] == "generate" && args[1] == "jobs" {
		return runJobs(args[2:], stdout, stderr)
	}
	return runModels(args, stdout, stderr)
}

func runJobs(args []string, stdout, stderr io.Writer) error {
	var options jobsgen.Options
	flags := flag.NewFlagSet("vv generate jobs", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&options.Dir, "dir", ".", "package directory")
	flags.StringVar(&options.Out, "out", "vv_jobs_gen.go", "generated Go file name")
	flags.StringVar(&options.Manifest, "manifest", "jobs.manifest.yml", "jobs manifest file name")
	flags.BoolVar(&options.Check, "check", false, "check generated artifacts without writing")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("generate jobs accepts no positional arguments")
	}
	options.Log = stdout
	return jobsgen.Run(&options)
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
