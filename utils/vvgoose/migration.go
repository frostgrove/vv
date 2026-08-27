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

func createMigration(ctx context.Context, raw vvdb.Config, options createOptions) (string, error) {
	cfg := normalizeConfig(raw)
	if err := cfg.Migration.Validate(); err != nil {
		return "", fmt.Errorf("vvgoose: invalid migration config: %w", err)
	}
	if _, err := dialectFor(cfg.Engine); err != nil {
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
		models, discoverErr := modelscan.Discover(modelscan.Options{Roots: cfg.Migration.Models})
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
	contents, err := renderMigration(cfg.Engine, renderTable, model)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(cfg.Migration.Path, 0o755); err != nil {
		return "", fmt.Errorf("vvgoose: create migration directory %q: %w", cfg.Migration.Path, err)
	}

	now := options.Now
	if now == nil {
		now = time.Now
	}
	baseVersion, err := nextMigrationVersion(cfg.Migration.Path, now())
	if err != nil {
		return "", err
	}
	for offset := range 1000 {
		version := fmt.Sprintf("%014d", baseVersion+int64(offset))
		path := filepath.Join(cfg.Migration.Path, version+"_"+fileSlug+".sql")
		created, err := writeVersionedMigration(cfg.Migration.Path, version, path, contents)
		if err != nil {
			return "", err
		}
		if created {
			return path, nil
		}
	}
	return "", fmt.Errorf("vvgoose: could not allocate a unique migration version in %q", cfg.Migration.Path)
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
	selected := 0
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
