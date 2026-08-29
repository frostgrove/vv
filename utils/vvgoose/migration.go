package vvgoose

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/huh"
	"github.com/frostgrove/vv/utils/vvdb"
	"github.com/frostgrove/vv/utils/vvgoose/internal/modelscan"
)

type createOptions struct {
	Name        string
	Empty       bool
	Model       string
	Interactive bool
	In          io.Reader
	Out         io.Writer
	Now         func() time.Time
}

type migrationOptions struct {
	Name        string
	Tables      []string
	Interactive bool
	In          io.Reader
	Out         io.Writer
	Now         func() time.Time
}

// createTableMigration creates one conventional create_<table>_table
// migration, inferring its columns from the matching source model.
func createTableMigration(ctx context.Context, raw vvdb.Config, options createOptions) (string, error) {
	config := normalizeConfig(&raw)
	if err := config.Migration.Validate(); err != nil {
		return "", fmt.Errorf("vvgoose: invalid migration config: %w", err)
	}
	if _, err := dialectFor(config.Engine); err != nil {
		return "", err
	}

	fileSlug, table, err := migrationNames(options.Name)
	if err != nil {
		return "", err
	}

	var model *modelscan.Model
	// A source model describes a complete CREATE TABLE statement. Reusing it
	// for names such as add_email_to_users would silently turn an ALTER intent
	// into a destructive duplicate CREATE, so non-create names stay editable
	// Goose skeletons.
	if !options.Empty && createsTable(fileSlug) {
		models, discoverErr := modelscan.Discover(&modelscan.Options{Roots: config.Migration.Models})
		if discoverErr != nil {
			return "", discoverErr
		}
		model, err = chooseModel(ctx, models, table, options)
		if err != nil {
			return "", err
		}
	}

	renderTable := table
	if model != nil && model.Table != "" {
		renderTable = model.Table
	}
	contents, err := renderMigration(config.Engine, renderTable, model)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(config.Migration.Path, 0o755); err != nil {
		return "", fmt.Errorf("vvgoose: create migration directory %q: %w", config.Migration.Path, err)
	}

	return writeNewMigration(config.Migration.Path, fileSlug, contents, options.Now)
}

// createTableMigrations accepts a comma-separated table list and creates one
// conventional migration per table. Separate files keep later rollbacks and
// reviews focused on the table that changed.
func createTableMigrations(ctx context.Context, raw vvdb.Config, tables []string, options createOptions) ([]string, error) {
	targets, err := splitTableList(tables...)
	if err != nil {
		return nil, err
	}
	if options.Model != "" && len(targets) != 1 {
		return nil, fmt.Errorf("vvgoose: --model can only be used with one table")
	}

	paths := make([]string, 0, len(targets))
	for _, target := range targets {
		path, err := createTableMigration(ctx, raw, createOptions{
			Name:        target,
			Empty:       options.Empty,
			Model:       options.Model,
			Interactive: options.Interactive,
			In:          options.In,
			Out:         options.Out,
			Now:         options.Now,
		})
		if err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// createMigration creates an ordinary editable Goose migration. Supplying
// tables makes model generation explicit rather than guessing from the
// migration name, and puts all selected CREATE TABLE statements in this one
// file.
func createMigration(ctx context.Context, raw vvdb.Config, options migrationOptions) (string, error) {
	config := normalizeConfig(&raw)
	if err := config.Migration.Validate(); err != nil {
		return "", fmt.Errorf("vvgoose: invalid migration config: %w", err)
	}
	if _, err := dialectFor(config.Engine); err != nil {
		return "", err
	}

	fileSlug := identifierSlug(options.Name)
	if fileSlug == "" {
		return "", fmt.Errorf("vvgoose: migration name %q contains no letters or digits", options.Name)
	}
	var contents []byte
	tables, err := splitTableList(options.Tables...)
	if err != nil {
		return "", err
	}
	if len(tables) == 0 {
		contents = renderEmptyMigration(fileSlug)
	} else {
		models, err := selectedModels(ctx, config, tables, createOptions{
			Interactive: options.Interactive,
			In:          options.In,
			Out:         options.Out,
		})
		if err != nil {
			return "", err
		}
		contents, err = renderModelsMigration(config.Engine, models)
		if err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(config.Migration.Path, 0o755); err != nil {
		return "", fmt.Errorf("vvgoose: create migration directory %q: %w", config.Migration.Path, err)
	}
	return writeNewMigration(config.Migration.Path, fileSlug, contents, options.Now)
}

// createInitMigration produces one baseline migration from all discovered
// models. Its version is stable: rerunning init replaces the existing *_init
// file instead of adding another competing baseline.
func createInitMigration(raw vvdb.Config, now func() time.Time) (string, error) {
	config := normalizeConfig(&raw)
	if err := config.Migration.Validate(); err != nil {
		return "", fmt.Errorf("vvgoose: invalid migration config: %w", err)
	}
	if _, err := dialectFor(config.Engine); err != nil {
		return "", err
	}
	models, err := modelscan.Discover(&modelscan.Options{Roots: config.Migration.Models})
	if err != nil {
		return "", err
	}
	models, err = initModels(models)
	if err != nil {
		return "", err
	}
	contents, err := renderModelsMigration(config.Engine, models)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(config.Migration.Path, 0o755); err != nil {
		return "", fmt.Errorf("vvgoose: create migration directory %q: %w", config.Migration.Path, err)
	}
	return writeInitMigration(config.Migration.Path, contents, now)
}

func selectedModels(ctx context.Context, config vvdb.Config, tables []string, options createOptions) ([]modelscan.Model, error) {
	models, err := modelscan.Discover(&modelscan.Options{Roots: config.Migration.Models})
	if err != nil {
		return nil, err
	}
	selected := make([]modelscan.Model, 0, len(tables))
	seen := make(map[string]bool, len(tables))
	for _, table := range tables {
		model, err := chooseModel(ctx, models, table, options)
		if err != nil {
			return nil, err
		}
		if model == nil {
			return nil, fmt.Errorf("vvgoose: no unambiguous model found for table %q", table)
		}
		key := strings.ToLower(model.Table)
		if seen[key] {
			return nil, fmt.Errorf("vvgoose: table %q was selected more than once", model.Table)
		}
		seen[key] = true
		selected = append(selected, *model)
	}
	return selected, nil
}

func initModels(models []modelscan.Model) ([]modelscan.Model, error) {
	selected := make([]modelscan.Model, 0, len(models))
	seen := make(map[string]bool, len(models))
	for _, model := range models {
		// A model stub with no columns cannot form a portable CREATE TABLE
		// statement. It is deliberately left out of an init baseline.
		if len(model.Fields) == 0 {
			continue
		}
		key := strings.ToLower(model.Table)
		if seen[key] {
			return nil, fmt.Errorf("vvgoose: init found more than one model for table %q", model.Table)
		}
		seen[key] = true
		selected = append(selected, model)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("vvgoose: init found no models with mapped columns")
	}
	return selected, nil
}

func splitTableList(values ...string) ([]string, error) {
	var tables []string
	seen := map[string]bool{}
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if identifierSlug(item) == "" {
				return nil, fmt.Errorf("vvgoose: table %q contains no letters or digits", item)
			}
			key := strings.ToLower(item)
			if seen[key] {
				continue
			}
			seen[key] = true
			tables = append(tables, item)
		}
	}
	return tables, nil
}

func writeNewMigration(dir, fileSlug string, contents []byte, now func() time.Time) (string, error) {
	if now == nil {
		now = time.Now
	}
	baseVersion, err := nextMigrationVersion(dir, now())
	if err != nil {
		return "", err
	}
	for offset := range 1000 {
		version := fmt.Sprintf("%014d", baseVersion+int64(offset))
		path := filepath.Join(dir, version+"_"+fileSlug+".sql")
		created, err := writeVersionedMigration(dir, version, path, contents)
		if err != nil {
			return "", err
		}
		if created {
			return path, nil
		}
	}
	return "", fmt.Errorf("vvgoose: could not allocate a unique migration version in %q", dir)
}

func writeInitMigration(dir string, contents []byte, now func() time.Time) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("vvgoose: inspect migration directory %q: %w", dir, err)
	}
	var existing string
	for _, entry := range entries {
		if entry.IsDir() || !isInitMigration(entry.Name()) {
			continue
		}
		if existing != "" {
			return "", fmt.Errorf("vvgoose: more than one init migration exists in %q", dir)
		}
		existing = filepath.Join(dir, entry.Name())
	}
	if existing == "" {
		return writeNewMigration(dir, "init", contents, now)
	}
	if err := overwriteFile(existing, contents); err != nil {
		return "", err
	}
	return existing, nil
}

func isInitMigration(name string) bool {
	version, rest, found := strings.Cut(name, "_")
	if !found || rest != "init.sql" {
		return false
	}
	_, err := strconv.ParseInt(version, 10, 64)
	return err == nil
}

func overwriteFile(path string, contents []byte) (err error) {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("vvgoose: inspect init migration %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("vvgoose: init migration %q is not a regular file", path)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vvgoose-init-*.sql")
	if err != nil {
		return fmt.Errorf("vvgoose: create temporary init migration: %w", err)
	}
	tmpPath := tmp.Name()
	completed := false
	defer func() {
		if !completed {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		return fmt.Errorf("vvgoose: set init migration permissions: %w", err)
	}
	if _, err := tmp.Write(contents); err != nil {
		return fmt.Errorf("vvgoose: write init migration %q: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("vvgoose: close temporary init migration: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("vvgoose: replace init migration %q: %w", path, err)
	}
	completed = true
	return nil
}

func nextMigrationVersion(dir string, now time.Time) (int64, error) {
	base, err := strconv.ParseInt(now.UTC().Format("20060102150405"), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("vvgoose: format migration time: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("vvgoose: inspect migration directory %q: %w", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, _, found := strings.Cut(entry.Name(), "_")
		if !found {
			continue
		}
		version, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr == nil && version >= base {
			base = version + 1
		}
	}
	return base, nil
}

func createsTable(fileSlug string) bool {
	return strings.HasPrefix(fileSlug, "create_") && strings.HasSuffix(fileSlug, "_table")
}

// writeVersionedMigration reserves the Goose version, not merely the final
// filename. Goose identifies migrations by the timestamp prefix, so two
// different names created in the same second must not both receive it.
func writeVersionedMigration(dir, version, path string, contents []byte) (bool, error) {
	lockPath := filepath.Join(dir, ".vvgoose-"+version+".lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("vvgoose: reserve migration version %s: %w", version, err)
	}
	defer func() {
		_ = lock.Close()
		_ = os.Remove(lockPath)
	}()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("vvgoose: inspect migration directory %q: %w", dir, err)
	}
	prefix := version + "_"
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), prefix) && strings.HasSuffix(entry.Name(), ".sql") {
			return false, nil
		}
	}
	return writeExclusive(path, contents)
}

func writeExclusive(path string, contents []byte) (created bool, err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if os.IsExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("vvgoose: create migration %q: %w", path, err)
	}
	ok := false
	defer func() {
		closeErr := f.Close()
		if err == nil && closeErr != nil {
			err = closeErr
		}
		if !ok || err != nil {
			_ = os.Remove(path)
		}
	}()
	if _, err = f.Write(contents); err != nil {
		return false, fmt.Errorf("vvgoose: write migration %q: %w", path, err)
	}
	ok = true
	return true, nil
}

func chooseModel(ctx context.Context, models []modelscan.Model, target string, options createOptions) (*modelscan.Model, error) {
	if options.Model != "" {
		matches := explicitlyNamedModels(models, options.Model)
		switch len(matches) {
		case 0:
			return nil, fmt.Errorf("vvgoose: model %q was not found in the configured model roots", options.Model)
		case 1:
			return &matches[0], nil
		default:
			if !options.Interactive {
				return nil, fmt.Errorf("vvgoose: model %q is ambiguous; use package.Type", options.Model)
			}
			return selectModel(ctx, matches, options.In, options.Out)
		}
	}

	candidates := modelscan.Candidates(models, target)
	switch len(candidates) {
	case 0:
		return nil, nil
	case 1:
		return &candidates[0], nil
	default:
		if !options.Interactive {
			return nil, nil
		}
		return selectModel(ctx, candidates, options.In, options.Out)
	}
}

func explicitlyNamedModels(models []modelscan.Model, name string) []modelscan.Model {
	name = strings.TrimSpace(name)
	var matches []modelscan.Model
	for _, model := range models {
		qualified := model.Package + "." + model.Name
		if strings.EqualFold(name, model.Name) || strings.EqualFold(name, qualified) || strings.EqualFold(name, model.Table) {
			matches = append(matches, model)
		}
	}
	return matches
}

func selectModel(ctx context.Context, models []modelscan.Model, in io.Reader, out io.Writer) (*modelscan.Model, error) {
	// Empty is deliberately the default. In accessible mode Huh returns the
	// current value when input reaches EOF, so a disconnected terminal must not
	// silently turn the first candidate into a schema decision.
	selected := len(models)
	options := make([]huh.Option[int], 0, len(models)+1)
	for i, model := range models {
		options = append(options, huh.NewOption(model.Label(), i))
	}
	options = append(options, huh.NewOption("Empty migration (do not use a model)", len(models)))
	field := huh.NewSelect[int]().
		Title("Several Go models match. Choose one (type to search)").
		Options(options...).
		Value(&selected).
		Filtering(true)
	form := huh.NewForm(huh.NewGroup(field)).WithInput(in).WithOutput(out)
	if err := form.RunWithContext(ctx); err != nil {
		return nil, fmt.Errorf("vvgoose: choose model: %w", err)
	}
	if selected == len(models) {
		return nil, nil
	}
	return &models[selected], nil
}

func migrationNames(raw string) (fileSlug, table string, err error) {
	slug := identifierSlug(raw)
	if slug == "" {
		return "", "", fmt.Errorf("vvgoose: migration name %q contains no letters or digits", raw)
	}
	if strings.HasPrefix(slug, "create_") && strings.HasSuffix(slug, "_table") {
		table = strings.TrimSuffix(strings.TrimPrefix(slug, "create_"), "_table")
		if table == "" {
			return "", "", fmt.Errorf("vvgoose: migration name %q has no table", raw)
		}
		return slug, table, nil
	}
	for _, action := range []string{"add_", "drop_", "remove_", "rename_", "alter_", "update_"} {
		if !strings.HasPrefix(slug, action) {
			continue
		}
		for _, separator := range []string{"_to_", "_from_", "_in_"} {
			if i := strings.LastIndex(slug, separator); i >= 0 && i+len(separator) < len(slug) {
				return slug, slug[i+len(separator):], nil
			}
		}
		return slug, slug, nil
	}
	return "create_" + slug + "_table", slug, nil
}

func identifierSlug(raw string) string {
	var b strings.Builder
	lastUnderscore := false
	var previous rune
	for _, r := range strings.TrimSpace(raw) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if unicode.IsUpper(r) && b.Len() > 0 && unicode.IsLower(previous) && !lastUnderscore {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			lastUnderscore = false
		case r == '_' || r == '-' || unicode.IsSpace(r):
			if b.Len() > 0 && !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		default:
			// A migration name becomes both a filename and SQL identifier. Drop
			// punctuation instead of ever copying syntax into either context.
			if b.Len() > 0 && !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
		previous = r
	}
	return strings.Trim(b.String(), "_")
}
