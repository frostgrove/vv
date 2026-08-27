// Package vvgoose is the application migration command for vvdb.
//
// A command needs no bootstrap framework of its own:
//
//	func main() {
//		cfg := vvcfg.MustLoad[config.Config]()
//		vvgoose.Execute(cfg.DB)
//	}
//
// Execute reads os.Args and provides migration generation, applying, status,
// rollback and a reset followed by re-application. SQL files stay compatible
// with the Goose CLI; the package uses Goose's instance Provider internally so
// two database configurations never share process-global migration state.
package vvgoose

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/frostgrove/vv/utils/vvdb"
	"github.com/pressly/goose/v3"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const (
	defaultMigrationPath  = "./migrations"
	defaultMigrationModel = "."
	defaultMigrationTable = "goose_db_version"
)

// Execute runs the migration CLI using cfg. It is deliberately a terminal
// entrypoint rather than a library-shaped error return: the intended caller is
// cmd/migrate/main.go, and failures must result in a non-zero process status
// even when that minimal main does not contain error handling.
func Execute(cfg vvdb.Config) {
	if err := execute(context.Background(), os.Args[1:], cfg, commandIO{
		in: os.Stdin, out: os.Stdout, err: os.Stderr,
	}); err != nil {
		if strings.HasPrefix(err.Error(), "vvgoose:") {
			fmt.Fprintln(os.Stderr, err)
		} else {
			fmt.Fprintf(os.Stderr, "vvgoose: %v\n", err)
		}
		os.Exit(1)
	}
}

type commandIO struct {
	in       io.Reader
	out, err io.Writer
}

func normalizeConfig(cfg vvdb.Config) vvdb.Config {
	if cfg.Migration.Path == "" {
		cfg.Migration.Path = defaultMigrationPath
	}
	if len(cfg.Migration.Models) == 0 {
		cfg.Migration.Models = []string{defaultMigrationModel}
	}
	if cfg.Migration.Table == "" {
		cfg.Migration.Table = defaultMigrationTable
	}
	return cfg
}

func execute(ctx context.Context, args []string, cfg vvdb.Config, streams commandIO) error {
	cfg = normalizeConfig(cfg)
	root := newRootCommand(cfg, streams)
	root.SetArgs(args)
	return root.ExecuteContext(ctx)
}

func newRootCommand(cfg vvdb.Config, streams commandIO) *cobra.Command {
	var ignoredConfigPath string
	var noInteractive bool

	root := &cobra.Command{
		Use:           "migrate",
		Short:         "Generate and run database migrations",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	root.SetIn(streams.in)
	root.SetOut(streams.out)
	root.SetErr(streams.err)
	root.CompletionOptions.DisableDefaultCmd = true

	// vvcfg reads this flag directly from os.Args. Keeping a hidden declaration
	// here prevents the same, still-present flag from becoming an unknown option
	// when control moves from configuration loading to this CLI.
	root.PersistentFlags().StringVar(&ignoredConfigPath, "config-path", "", "configuration file (consumed by vvcfg)")
	_ = root.PersistentFlags().MarkHidden("config-path")
	root.PersistentFlags().BoolVar(&noInteractive, "no-interactive", false, "never prompt for a model")

	var empty bool
	var explicitModel string
	generate := &cobra.Command{
		Use:   "migration <name>",
		Short: "Create a Goose SQL migration, inferring columns from a Go model",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := createMigration(cmd.Context(), cfg, createOptions{
				Name:        args[0],
				Empty:       empty,
				Model:       explicitModel,
				Interactive: !noInteractive && terminalReader(streams.in),
				In:          streams.in,
				Out:         streams.out,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(streams.out, "created %s\n", path)
			return nil
		},
	}
	generate.Flags().BoolVar(&empty, "empty", false, "create an empty editable migration without model columns")
	generate.Flags().StringVar(&explicitModel, "model", "", "use this Go struct instead of automatic matching")
	root.AddCommand(generate)

	root.AddCommand(&cobra.Command{
		Use:   "migrate",
		Short: "Apply every pending migration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			results, err := runMigrate(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			printResults(streams.out, results)
			return nil
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "fresh",
		Short: "Roll all migrations down, then apply them again",
		Long:  "Roll all tracked migrations down, then apply them again. This uses each migration's Down section; it does not drop untracked tables.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			results, err := runFresh(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			printResults(streams.out, results)
			return nil
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show applied and pending migrations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			statuses, err := runStatus(cmd.Context(), cfg)
			if err != nil {
				return err
			}
			printStatus(streams.out, statuses)
			return nil
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "rollback [count]",
		Short: "Roll back the latest migrations (default: 1)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			count := 1
			if len(args) == 1 {
				var err error
				count, err = strconv.Atoi(args[0])
				if err != nil || count < 1 {
					return fmt.Errorf("rollback count must be a positive integer, got %q", args[0])
				}
			}
			results, err := runRollback(cmd.Context(), cfg, count)
			if err != nil {
				return err
			}
			printResults(streams.out, results)
			return nil
		},
	})

	return root
}

func terminalReader(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func printResults(w io.Writer, results []*goose.MigrationResult) {
	if len(results) == 0 {
		fmt.Fprintln(w, "nothing to do")
		return
	}
	for _, result := range results {
		fmt.Fprintln(w, result)
	}
}

func printStatus(w io.Writer, statuses []*goose.MigrationStatus) {
	if len(statuses) == 0 {
		fmt.Fprintln(w, "no migrations")
		return
	}
	fmt.Fprintln(w, "STATE\tMIGRATION\tAPPLIED AT")
	for _, status := range statuses {
		applied := "-"
		if !status.AppliedAt.IsZero() {
			applied = status.AppliedAt.Format("2006-01-02 15:04:05")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", status.State, filepath.Base(status.Source.Path), applied)
	}
}
