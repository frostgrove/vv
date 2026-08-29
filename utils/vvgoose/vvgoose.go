// Package vvgoose is the application migration command for vvdb.
//
// A command needs no bootstrap framework of its own:
//
//	func main() {
//		cfg := vvcfg.MustLoad[config.Config]()
//		vvgoose.Execute(&cfg.DB)
//	}
//
// Execute reads os.Args and provides migration generation, applying, status,
// rollback, a reset followed by re-application, and a development database
// flush. SQL files stay compatible with the Goose CLI; the package uses Goose's
// instance Provider internally so two database configurations never share
// process-global migration state.
package vvgoose

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
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
func Execute(config *vvdb.Config) {
	if err := execute(context.Background(), os.Args[1:], normalizeConfig(config), commandIO{
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

func normalizeConfig(config *vvdb.Config) vvdb.Config {
	if config == nil {
		return vvdb.Config{}
	}
	normalized := *config
	if normalized.Migration.Path == "" {
		normalized.Migration.Path = defaultMigrationPath
	}
	if len(normalized.Migration.Models) == 0 {
		normalized.Migration.Models = []string{defaultMigrationModel}
	}
	if normalized.Migration.Table == "" {
		normalized.Migration.Table = defaultMigrationTable
	}
	return normalized
}

func execute(ctx context.Context, args []string, config vvdb.Config, streams commandIO) error {
	config = normalizeConfig(&config)
	root := newRootCommand(config, streams)
	root.SetArgs(args)
	return root.ExecuteContext(ctx)
}

func newRootCommand(config vvdb.Config, streams commandIO) *cobra.Command {
	var ignoredConfigPath string
	var noInteractive bool

	root := &cobra.Command{
		Use:           "migrate",
		Short:         "Generate and run database migrations",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !noInteractive && interactiveTerminal(streams) {
				return runInteractive(cmd.Context(), config, streams)
			}
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
	root.PersistentFlags().BoolVar(&noInteractive, "no-interactive", false, "disable interactive prompts and menus")

	var migrationTables string
	migration := &cobra.Command{
		Use:   "migration <name> [tables]",
		Short: "Create an editable Goose migration, optionally generating explicit tables",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if migrationTables != "" && len(args) == 2 {
				return fmt.Errorf("tables may be supplied either as a positional argument or with --tables, not both")
			}
			tables := []string{migrationTables}
			if len(args) == 2 {
				tables = append(tables, args[1])
			}
			path, err := createMigration(cmd.Context(), config, migrationOptions{
				Name:        args[0],
				Tables:      tables,
				Interactive: !noInteractive && interactiveTerminal(streams),
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
	migration.Flags().StringVarP(&migrationTables, "tables", "t", "", "comma-separated source-model tables to generate in this migration")
	root.AddCommand(migration)

	var tableEmpty bool
	var explicitModel string
	table := &cobra.Command{
		Use:   "table <table[,table...]>",
		Short: "Create one CREATE TABLE migration per source model",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			paths, err := createTableMigrations(cmd.Context(), config, args, createOptions{
				Empty:       tableEmpty,
				Model:       explicitModel,
				Interactive: !noInteractive && interactiveTerminal(streams),
				In:          streams.in,
				Out:         streams.out,
			})
			if err != nil {
				return err
			}
			for _, path := range paths {
				fmt.Fprintf(streams.out, "created %s\n", path)
			}
			return nil
		},
	}
	table.Flags().BoolVar(&tableEmpty, "empty", false, "create an empty editable migration without model columns")
	table.Flags().StringVar(&explicitModel, "model", "", "use this Go struct instead of automatic matching")
	root.AddCommand(table)

	root.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Create or replace the init migration from every discovered model",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			path, err := createInitMigration(config, nil)
			if err != nil {
				return err
			}
			fmt.Fprintf(streams.out, "created %s\n", path)
			return nil
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "migrate",
		Short: "Apply every pending migration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			results, err := runMigrate(cmd.Context(), config)
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
			results, err := runFresh(cmd.Context(), config)
			if err != nil {
				return err
			}
			printResults(streams.out, results)
			return nil
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "flush",
		Short: "Drop every object in the current development database schema",
		Long:  "Drop every application object, including Goose history, from the current database schema. No migrations are applied afterwards. This is destructive and intended for local development only.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := runFlush(cmd.Context(), config); err != nil {
				return err
			}
			fmt.Fprintln(streams.out, "database flushed")
			return nil
		},
	})

	root.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show applied and pending migrations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			statuses, err := runStatus(cmd.Context(), config)
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
			results, err := runRollback(cmd.Context(), config, count)
			if err != nil {
				return err
			}
			printResults(streams.out, results)
			return nil
		},
	})

	return root
}

type interactiveCommand string

const (
	interactiveMigration interactiveCommand = "migration"
	interactiveTable     interactiveCommand = "table"
	interactiveInit      interactiveCommand = "init"
	interactiveMigrate   interactiveCommand = "migrate"
	interactiveStatus    interactiveCommand = "status"
	interactiveRollback  interactiveCommand = "rollback"
	interactiveFresh     interactiveCommand = "fresh"
	interactiveFlush     interactiveCommand = "flush"
	interactiveExit      interactiveCommand = "exit"
)

func runInteractive(ctx context.Context, config vvdb.Config, streams commandIO) error {
	// Exit is the safe default if the terminal disappears while the menu is
	// waiting for input. A disconnected terminal must never start a database
	// operation merely because the first option happened to be selected.
	selected := interactiveExit
	menu := huh.NewSelect[interactiveCommand]().
		Title("What do you want to do?").
		Options(
			huh.NewOption("Create a migration", interactiveMigration),
			huh.NewOption("Create table migrations from models", interactiveTable),
			huh.NewOption("Create or replace init migration", interactiveInit),
			huh.NewOption("Apply pending migrations", interactiveMigrate),
			huh.NewOption("Show migration status", interactiveStatus),
			huh.NewOption("Roll back migrations", interactiveRollback),
			huh.NewOption("Fresh: down all, then migrate", interactiveFresh),
			huh.NewOption("Flush: drop every database object", interactiveFlush),
			huh.NewOption("Exit", interactiveExit),
		).
		Value(&selected).
		Filtering(true)
	if err := runForm(ctx, streams, huh.NewForm(huh.NewGroup(menu))); err != nil {
		return fmt.Errorf("vvgoose: choose command: %w", err)
	}

	switch selected {
	case interactiveMigration:
		return runInteractiveMigration(ctx, config, streams)
	case interactiveTable:
		return runInteractiveTable(ctx, config, streams)
	case interactiveInit:
		return runInteractiveInit(ctx, config, streams)
	case interactiveMigrate:
		results, err := runMigrate(ctx, config)
		if err == nil {
			printResults(streams.out, results)
		}
		return err
	case interactiveStatus:
		statuses, err := runStatus(ctx, config)
		if err == nil {
			printStatus(streams.out, statuses)
		}
		return err
	case interactiveRollback:
		return runInteractiveRollback(ctx, config, streams)
	case interactiveFresh:
		return runInteractiveFresh(ctx, config, streams)
	case interactiveFlush:
		return runInteractiveFlush(ctx, config, streams)
	case interactiveExit:
		return nil
	default:
		return fmt.Errorf("vvgoose: unknown interactive command %q", selected)
	}
}

func runInteractiveMigration(ctx context.Context, config vvdb.Config, streams commandIO) error {
	var name string
	var tables string
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Migration name").
			Placeholder("init_permission_tables").
			Value(&name).
			Validate(func(value string) error {
				if strings.TrimSpace(value) == "" {
					return errors.New("migration name is required")
				}
				return nil
			}),
		huh.NewInput().
			Title("Tables to generate (optional, comma-separated)").
			Placeholder("permissions,roles").
			Value(&tables),
	))
	if err := runForm(ctx, streams, form); err != nil {
		return fmt.Errorf("vvgoose: migration form: %w", err)
	}
	path, err := createMigration(ctx, config, migrationOptions{
		Name: name, Tables: []string{tables}, Interactive: true, In: streams.in, Out: streams.out,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(streams.out, "created %s\n", path)
	return nil
}

func runInteractiveTable(ctx context.Context, config vvdb.Config, streams commandIO) error {
	var tables string
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Tables to create (comma-separated)").
			Placeholder("users,products").
			Value(&tables).
			Validate(func(value string) error {
				if strings.TrimSpace(value) == "" {
					return errors.New("at least one table is required")
				}
				return nil
			}),
	))
	if err := runForm(ctx, streams, form); err != nil {
		return fmt.Errorf("vvgoose: table form: %w", err)
	}
	paths, err := createTableMigrations(ctx, config, []string{tables}, createOptions{
		Interactive: true, In: streams.in, Out: streams.out,
	})
	if err != nil {
		return err
	}
	for _, path := range paths {
		fmt.Fprintf(streams.out, "created %s\n", path)
	}
	return nil
}

func runInteractiveInit(ctx context.Context, config vvdb.Config, streams commandIO) error {
	confirmed := false
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Create or replace the init migration from every model?").
			Affirmative("Generate init").
			Negative("Cancel").
			Value(&confirmed),
	))
	if err := runForm(ctx, streams, form); err != nil {
		return fmt.Errorf("vvgoose: init confirmation: %w", err)
	}
	if !confirmed {
		fmt.Fprintln(streams.out, "cancelled")
		return nil
	}
	path, err := createInitMigration(config, nil)
	if err != nil {
		return err
	}
	fmt.Fprintf(streams.out, "created %s\n", path)
	return nil
}

func runInteractiveRollback(ctx context.Context, config vvdb.Config, streams commandIO) error {
	countText := "1"
	confirmed := false
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("How many migrations should be rolled back?").
			Value(&countText).
			Validate(func(value string) error {
				count, err := strconv.Atoi(strings.TrimSpace(value))
				if err != nil || count < 1 {
					return errors.New("count must be a positive integer")
				}
				return nil
			}),
		huh.NewConfirm().
			Title("Roll back the selected migrations?").
			Affirmative("Rollback").
			Negative("Cancel").
			Value(&confirmed),
	))
	if err := runForm(ctx, streams, form); err != nil {
		return fmt.Errorf("vvgoose: rollback form: %w", err)
	}
	if !confirmed {
		fmt.Fprintln(streams.out, "cancelled")
		return nil
	}
	count, err := strconv.Atoi(strings.TrimSpace(countText))
	if err != nil || count < 1 {
		return fmt.Errorf("vvgoose: rollback count must be a positive integer, got %q", countText)
	}
	results, err := runRollback(ctx, config, count)
	if err == nil {
		printResults(streams.out, results)
	}
	return err
}

func runInteractiveFresh(ctx context.Context, config vvdb.Config, streams commandIO) error {
	confirmed := false
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Roll every tracked migration down and apply it again?").
			Affirmative("Run fresh").
			Negative("Cancel").
			Value(&confirmed),
	))
	if err := runForm(ctx, streams, form); err != nil {
		return fmt.Errorf("vvgoose: fresh confirmation: %w", err)
	}
	if !confirmed {
		fmt.Fprintln(streams.out, "cancelled")
		return nil
	}
	results, err := runFresh(ctx, config)
	if err == nil {
		printResults(streams.out, results)
	}
	return err
}

func runInteractiveFlush(ctx context.Context, config vvdb.Config, streams commandIO) error {
	confirmed := false
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Drop every object, including Goose history, from the current database schema?").
			Affirmative("Flush database").
			Negative("Cancel").
			Value(&confirmed),
	))
	if err := runForm(ctx, streams, form); err != nil {
		return fmt.Errorf("vvgoose: flush confirmation: %w", err)
	}
	if !confirmed {
		fmt.Fprintln(streams.out, "cancelled")
		return nil
	}
	if err := runFlush(ctx, config); err != nil {
		return err
	}
	fmt.Fprintln(streams.out, "database flushed")
	return nil
}

func runForm(ctx context.Context, streams commandIO, form *huh.Form) error {
	return form.WithInput(streams.in).WithOutput(streams.out).RunWithContext(ctx)
}

func interactiveTerminal(streams commandIO) bool {
	return terminalFile(streams.in) && terminalFile(streams.out)
}

func terminalFile(stream any) bool {
	f, ok := stream.(*os.File)
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
