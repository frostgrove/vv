package errs

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

const DefaultLocaleFile = "default"

func LoadMessages(codes *Codes, fsys fs.FS, dir string) (*Messages, error) {
	m := NewMessages(codes)
	if err := m.Load(fsys, dir); err != nil {
		return nil, err
	}
	return m, nil
}

func (this *Messages) Load(fsys fs.FS, dir string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return fmt.Errorf("errs: reading the message catalogue %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}

	sort.Strings(names)

	for _, name := range names {
		raw, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			return fmt.Errorf("errs: reading %s: %w", name, err)
		}
		if err := this.loadFile(localeOf(name), name, raw); err != nil {
			return err
		}
	}
	return nil
}

func localeOf(name string) string {
	base := strings.TrimSuffix(name, ".json")
	if base == DefaultLocaleFile {
		return ""
	}
	return base
}

func (this *Messages) loadFile(locale, name string, raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("errs: %s is not a flat object of message keys: %w", name, err)
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		var template string
		if err := json.Unmarshal(fields[k], &template); err != nil {
			return fmt.Errorf("errs: %s: %q is not a string — a catalogue file is flat, and a nested object produces keys the ladder never asks for", name, k)
		}
		if err := this.Add(locale, k, template); err != nil {
			return fmt.Errorf("errs: %s: %w", name, err)
		}
	}
	return nil
}

func (this *Messages) Locales() []string {
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
	rungs := locales(locale)
	var out []Code
	for code := range this.codes.defs {
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
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
