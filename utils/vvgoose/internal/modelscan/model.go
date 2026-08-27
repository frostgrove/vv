// Package modelscan discovers database model declarations in application
// source files. It deliberately uses only the Go parser: creating a migration
// should still work while an unrelated package is unfinished, and discovery
// must never execute application code.
package modelscan

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// Options describes the source trees Discover walks. Empty Roots means the
// current directory.
type Options struct {
	Roots []string
}

// Model is the source-level part of a database model needed by migration
// generation. Package is the declared Go package name; Dir and File disambiguate
// equally named models in different packages.
type Model struct {
	Package       string
	Name          string
	Table         string
	ExplicitTable bool
	Dir           string
	File          string
	Line          int
	Fields        []Field
	Tagged        bool
}

// Field is one mapped column. GoType is the type expression exactly as the
// model spells it, which keeps named application types visible to diagnostics.
type Field struct {
	Name       string
	Column     string
	GoType     string
	File       string
	Line       int
	Nullable   bool
	PrimaryKey bool
	Auto       bool
	NoAuto     bool
	Immutable  bool
	Generated  bool
	Version    bool
}

// Label is a stable, human-readable option for an interactive model picker.
func (m Model) Label() string {
	name := m.Name
	if m.Package != "" {
		name = m.Package + "." + name
	}
	where := m.File
	if m.Line > 0 {
		where = fmt.Sprintf("%s:%d", where, m.Line)
	}
	if where == "" {
		return fmt.Sprintf("%s (table %s)", name, m.Table)
	}
	return fmt.Sprintf("%s (table %s) — %s", name, m.Table, where)
}

// Candidates returns the most likely models for target. It intentionally
// returns every equally good answer: the caller decides whether to show an
// interactive picker or create an empty migration in non-interactive mode.
//
// Both `users` and the conventional `create_users_table` spelling select the
// users table. A constant TableName method is stronger evidence than a table
// name inferred from the Go type.
func Candidates(models []Model, target string) []Model {
	target = migrationTarget(target)
	if target == "" {
		return nil
	}

	best := 0
	var out []Model
	for _, m := range models {
		score := candidateScore(m, target)
		switch {
		case score == 0 || score < best:
			continue
		case score > best:
			best = score
			out = out[:0]
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Package != out[j].Package {
			return out[i].Package < out[j].Package
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].File < out[j].File
	})
	return out
}

func candidateScore(m Model, target string) int {
	table := normalizeName(m.Table)
	typeName := normalizeName(snake(m.Name))
	switch {
	case table == target && m.ExplicitTable:
		return 400
	case table == target:
		return 300
	case pluralise(typeName) == target:
		return 250
	case typeName == target || typeName == singularise(target):
		return 200
	default:
		return 0
	}
}

func migrationTarget(name string) string {
	name = normalizeName(name)
	if strings.HasPrefix(name, "create_") && strings.HasSuffix(name, "_table") {
		name = strings.TrimSuffix(strings.TrimPrefix(name, "create_"), "_table")
	}
	return name
}

func normalizeName(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '.'); i >= 0 {
		s = s[i+1:]
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if b.Len() > 0 && !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.ToLower(snake(strings.Trim(b.String(), "_")))
}

// snake mirrors crud's field and default-table convention: UserID becomes
// user_id and HTTPCode becomes http_code.
func snake(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	rs := []rune(s)
	for i, r := range rs {
		if unicode.IsUpper(r) {
			prevLower := i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1]))
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if i > 0 && (prevLower || nextLower) {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func pluralise(s string) string {
	switch {
	case s == "":
		return s
	case strings.HasSuffix(s, "s"), strings.HasSuffix(s, "x"), strings.HasSuffix(s, "z"),
		strings.HasSuffix(s, "ch"), strings.HasSuffix(s, "sh"):
		return s + "es"
	case strings.HasSuffix(s, "y") && len(s) > 1 && !strings.ContainsRune("aeiou", rune(s[len(s)-2])):
		return s[:len(s)-1] + "ies"
	default:
		return s + "s"
	}
}

func singularise(s string) string {
	switch {
	case strings.HasSuffix(s, "ies") && len(s) > 3:
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "ches"), strings.HasSuffix(s, "shes"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "ses"), strings.HasSuffix(s, "xes"), strings.HasSuffix(s, "zes"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss"):
		return strings.TrimSuffix(s, "s")
	default:
		return s
	}
}

func displayPath(path string) string {
	if rel, err := filepath.Rel(".", path); err == nil {
		return rel
	}
	return path
}
