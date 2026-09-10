package errs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	DefaultLocaleFile            = "default"
	MaxCatalogueFileBytes        = 1 << 20
	MaxCatalogueBytes            = 16 << 20
	MaxCatalogueFiles            = 128
	MaxCatalogueDirectoryEntries = 4096
	MaxCatalogueEntries          = 10_000
)

type catalogueEntry struct {
	key      string
	template string
}

func LoadMessages(codes *Codes, fsys fs.FS, dir string) (*Messages, error) {
	m := NewMessages(codes)
	if err := m.Load(fsys, dir); err != nil {
		return nil, err
	}
	return m, nil
}

func (this *Messages) Load(fsys fs.FS, dir string) error {
	if this == nil {
		return errors.New("errs: loading a message catalogue into a nil Messages")
	}
	if nilInterface(fsys) {
		return errors.New("errs: reading the message catalogue: nil filesystem")
	}

	base := this.snapshot()
	staged, err := stageCatalogue(fsys, dir)
	if err != nil {
		return err
	}
	if _, _, _, err := mergeCatalogues(base, staged); err != nil {
		return err
	}

	this.mu.Lock()
	defer this.mu.Unlock()
	merged, entries, size, err := mergeCatalogues(this.templates, staged)
	if err != nil {
		return err
	}
	this.templates = merged
	this.entries = entries
	this.bytes = size
	return nil
}

func stageCatalogue(fsys fs.FS, dir string) (map[string]map[string]string, error) {
	entries, err := readCatalogueDirectory(fsys, dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		if path.Base(name) != name || !fs.ValidPath(name) {
			return nil, fmt.Errorf("errs: invalid catalogue file name %q", name)
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("errs: duplicate catalogue file name %q", name)
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) > MaxCatalogueFiles {
		return nil, fmt.Errorf("errs: the message catalogue has %d JSON files, limit %d", len(names), MaxCatalogueFiles)
	}
	sort.Strings(names)

	staged := map[string]map[string]string{}
	stagedEntries := 0
	stagedBytes := 0
	total := 0
	for _, name := range names {
		locale := localeOf(name)
		if name != DefaultLocaleFile+".json" && locale == "" {
			return nil, fmt.Errorf("errs: %s has an empty locale", name)
		}
		if err := validateLocale(locale); err != nil {
			return nil, fmt.Errorf("errs: invalid locale in %s: %w", name, err)
		}
		raw, err := readCatalogueFile(fsys, path.Join(dir, name), name)
		if err != nil {
			return nil, err
		}
		if len(raw) > MaxCatalogueBytes-total {
			return nil, fmt.Errorf("errs: the message catalogue exceeds %d bytes", MaxCatalogueBytes)
		}
		total += len(raw)
		fields, err := decodeCatalogueFile(name, raw)
		if err != nil {
			return nil, err
		}
		for _, field := range fields {
			if err := addToCatalogue(staged, locale, field.key, field.template, &stagedEntries, &stagedBytes); err != nil {
				return nil, fmt.Errorf("errs: %s: %w", name, err)
			}
		}
	}
	return staged, nil
}

func readCatalogueDirectory(fsys fs.FS, dir string) ([]fs.DirEntry, error) {
	file, err := fsys.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("errs: reading the message catalogue %s: %w", dir, err)
	}
	directory, ok := file.(fs.ReadDirFile)
	if !ok {
		closeErr := file.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("errs: closing the message catalogue %s: %w", dir, closeErr)
		}
		return nil, fmt.Errorf("errs: reading the message catalogue %s: directory does not support bounded reads", dir)
	}
	entries := make([]fs.DirEntry, 0, MaxCatalogueFiles)
	var readErr error
	for {
		batch, batchErr := directory.ReadDir(128)
		if len(batch) > MaxCatalogueDirectoryEntries-len(entries) {
			readErr = fmt.Errorf("errs: the message catalogue directory exceeds %d entries", MaxCatalogueDirectoryEntries)
			break
		}
		entries = append(entries, batch...)
		if batchErr != nil {
			if !errors.Is(batchErr, io.EOF) {
				readErr = fmt.Errorf("errs: reading the message catalogue %s: %w", dir, batchErr)
			}
			break
		}
		if len(batch) == 0 {
			readErr = fmt.Errorf("errs: reading the message catalogue %s: directory returned no entries and no end", dir)
			break
		}
	}
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("errs: closing the message catalogue %s: %w", dir, closeErr)
	}
	return entries, nil
}

func readCatalogueFile(fsys fs.FS, file, name string) ([]byte, error) {
	f, err := fsys.Open(file)
	if err != nil {
		return nil, fmt.Errorf("errs: reading %s: %w", name, err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, MaxCatalogueFileBytes+1))
	closeErr := f.Close()
	if readErr != nil {
		return nil, fmt.Errorf("errs: reading %s: %w", name, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("errs: closing %s: %w", name, closeErr)
	}
	if len(raw) > MaxCatalogueFileBytes {
		return nil, fmt.Errorf("errs: %s exceeds %d bytes", name, MaxCatalogueFileBytes)
	}
	return raw, nil
}

func decodeCatalogueFile(name string, raw []byte) ([]catalogueEntry, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("errs: %s is not valid UTF-8", name)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("errs: %s is not a flat object of message keys: %w", name, err)
	}
	if delim, ok := opening.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("errs: %s is not a flat object of message keys", name)
	}

	values := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("errs: %s is not a flat object of message keys: %w", name, err)
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("errs: %s contains a non-string message key", name)
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("errs: %s contains duplicate message key %q", name, key)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("errs: %s: reading %q: %w", name, key, err)
		}
		values[key] = value
		if len(values) > MaxCatalogueEntries {
			return nil, fmt.Errorf("errs: the message catalogue exceeds %d entries", MaxCatalogueEntries)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("errs: %s is not a flat object of message keys: %w", name, err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			err = errors.New("more than one JSON value")
		}
		return nil, fmt.Errorf("errs: %s is not one JSON object: %w", name, err)
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fields := make([]catalogueEntry, 0, len(keys))
	for _, key := range keys {
		if bytes.Equal(bytes.TrimSpace(values[key]), []byte("null")) {
			return nil, fmt.Errorf("errs: %s: %q is not a string — a catalogue file is flat, and a nested object produces keys the ladder never asks for", name, key)
		}
		var template string
		if err := json.Unmarshal(values[key], &template); err != nil {
			return nil, fmt.Errorf("errs: %s: %q is not a string — a catalogue file is flat, and a nested object produces keys the ladder never asks for", name, key)
		}
		fields = append(fields, catalogueEntry{key: key, template: template})
	}
	return fields, nil
}

func localeOf(name string) string {
	base := strings.TrimSuffix(name, ".json")
	if base == DefaultLocaleFile {
		return ""
	}
	return base
}

func (this *Messages) Locales() []string {
	if this == nil {
		return nil
	}
	this.mu.RLock()
	defer this.mu.RUnlock()
	out := make([]string, 0, len(this.templates))
	for locale := range this.templates {
		out = append(out, locale)
	}
	sort.Strings(out)
	return out
}

func (this *Messages) Missing(locale string) []Code {
	if this == nil || this.codes == nil {
		return nil
	}
	codes := this.codes.all()
	sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
	rungs := locales(locale)
	if locale != "" {
		rungs = rungs[:len(rungs)-1]
	}

	this.mu.RLock()
	defer this.mu.RUnlock()
	var out []Code
	for _, code := range codes {
		found := false
		for _, rung := range rungs {
			if _, ok := this.templates[rung][string(code)]; ok {
				found = true
				break
			}
		}
		if !found {
			out = append(out, code)
		}
	}
	return out
}

func (this *Messages) snapshot() map[string]map[string]string {
	this.mu.RLock()
	defer this.mu.RUnlock()
	return cloneCatalogue(this.templates)
}

func cloneCatalogue(source map[string]map[string]string) map[string]map[string]string {
	target := make(map[string]map[string]string, len(source))
	for locale, messages := range source {
		copy := make(map[string]string, len(messages))
		for key, template := range messages {
			copy[key] = template
		}
		target[locale] = copy
	}
	return target
}

func mergeCatalogues(base, additions map[string]map[string]string) (map[string]map[string]string, int, int, error) {
	merged := cloneCatalogue(base)
	entries, size := catalogueSize(merged)
	locales := make([]string, 0, len(additions))
	for locale := range additions {
		locales = append(locales, locale)
	}
	sort.Strings(locales)
	for _, locale := range locales {
		keys := make([]string, 0, len(additions[locale]))
		for key := range additions[locale] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			template := additions[locale][key]
			byKey := merged[locale]
			if have, ok := byKey[key]; ok {
				if have != template {
					return nil, 0, 0, fmt.Errorf("errs: %q in locale %q is already %q: %w", key, locale, have, ErrMessageRedeclared)
				}
				continue
			}
			if entries >= MaxCatalogueEntries {
				return nil, 0, 0, fmt.Errorf("errs: the message catalogue exceeds %d entries", MaxCatalogueEntries)
			}
			added := messageBytes(locale, key, template)
			if added > MaxCatalogueBytes-size {
				return nil, 0, 0, fmt.Errorf("errs: the message catalogue exceeds %d bytes", MaxCatalogueBytes)
			}
			if byKey == nil {
				byKey = map[string]string{}
				merged[locale] = byKey
			}
			byKey[key] = template
			entries++
			size += added
		}
	}
	return merged, entries, size, nil
}

func addToCatalogue(target map[string]map[string]string, locale, key, template string, entries, size *int) error {
	if err := validateMessage(locale, key, template); err != nil {
		return err
	}
	byKey := target[locale]
	if have, ok := byKey[key]; ok {
		if have != template {
			return fmt.Errorf("errs: %q in locale %q is already %q: %w", key, locale, have, ErrMessageRedeclared)
		}
		return nil
	}
	if *entries >= MaxCatalogueEntries {
		return fmt.Errorf("errs: the message catalogue exceeds %d entries", MaxCatalogueEntries)
	}
	added := messageBytes(locale, key, template)
	if added > MaxCatalogueBytes-*size {
		return fmt.Errorf("errs: the message catalogue exceeds %d bytes", MaxCatalogueBytes)
	}
	if byKey == nil {
		byKey = map[string]string{}
		target[locale] = byKey
	}
	byKey[key] = template
	*entries++
	*size += added
	return nil
}

func catalogueSize(catalogue map[string]map[string]string) (int, int) {
	entries := 0
	bytes := 0
	for locale, messages := range catalogue {
		for key, template := range messages {
			entries++
			bytes += messageBytes(locale, key, template)
		}
	}
	return entries, bytes
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}
