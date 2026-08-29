package errs

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// A catalogue on disk, one file per locale, loaded into the [Messages] this
// package already has.
//
// This is not a subsystem and there is no i18n package. [MessageSource] is the
// interface, [Messages] is the implementation and the four-rung ladder is
// already written; what was missing was a way to get the templates out of files
// instead of out of Go code. That is this file, and it is why the contract
// manifest gained nothing ([[D-048]]).

// DefaultLocaleFile is the file whose templates are the default ones — the rung
// used when the requested locale and its base language have nothing. It is
// spelled out rather than left as an empty file name because a file called
// `.json` is not a thing anybody writes on purpose.
const DefaultLocaleFile = "default"

// LoadMessages builds a catalogue from a directory of per-locale files.
//
//	//go:embed messages
//	var messages embed.FS
//
//	cat, err := errs.LoadMessages(errs.StandardCodes(), messages, "messages")
//
// The vocabulary is what the ladder falls through to when no file answers, so
// a partial catalogue is the designed case rather than a broken one.
func LoadMessages(codes *Codes, fsys fs.FS, dir string) (*Messages, error) {
	m := NewMessages(codes)
	if err := m.Load(fsys, dir); err != nil {
		return nil, err
	}
	return m, nil
}

// Load reads every *.json file in dir into this catalogue. The base name is the
// locale — en.json, en-GB.json, pt_BR.json — and default.json is the default
// one. Files are read in sorted order.
//
// Each file is a **flat** object of key to template:
//
//	{"user.email.unique": "that address is already registered"}
//
// Flat and not nested, and the reason is worth writing down because the nested
// shape looks obviously nicer. A nested {"order":{"items":{"email":…}}} invites
// the key order.items.email.unique — which [Add] accepts, which nothing ever
// reaches, and which [Messages] documents by name as the silent failure of this
// design: the ladder is first-and-last, so the response comes out well formed
// and the override the consumer wrote simply never appears. A flat file makes
// the keys exactly what the ladder asks for, and a key that will never be
// consulted is visible in the file itself.
//
// A value that is not a string is refused rather than skipped, naming the key.
// A consumer who wrote the nested shape gets told at start-up, which is where
// [[D-021]] says this class of mistake has to fail.
//
// Loading twice is allowed and the second load adds to the first. A key
// declared twice with different text in one locale is [ErrMessageRedeclared] —
// a catalogue that renders differently depending on load order is not one.
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
	// Sorted here rather than trusted from the directory. fs.ReadDir sorts what
	// it lists itself, and an fs.FS that implements ReadDirFS answers in
	// whatever order it likes — so two loads of one catalogue with a
	// disagreeing key would report a different one of the pair each time.
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

// localeOf reads the locale a file name declares.
func localeOf(name string) string {
	base := strings.TrimSuffix(name, ".json")
	if base == DefaultLocaleFile {
		return ""
	}
	return base
}

// loadFile adds one file's templates, in sorted key order for the same reason
// the files are sorted.
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

// Locales lists the locales this catalogue declares templates for, sorted. The
// default one is the empty string and sorts first.
func (this *Messages) Locales() []string {
	out := make([]string, 0, len(this.templates))
	for locale := range this.templates {
		out = append(out, locale)
	}
	sort.Strings(out)
	return out
}

// Missing reports which of the vocabulary's codes have no template in this
// locale — the codes a caller in that language reads in whatever the code
// declares instead.
//
// A report and not a refusal, and that is the whole judgement in this file.
// Falling through is the designed behaviour of the ladder: a catalogue with
// three overrides and a vocabulary with forty codes is the normal case, and a
// loader that refused to start on it would break the feature it was checking.
// A caller who wants a start-up refusal writes one out of this list, having
// decided for itself which locales are meant to be complete.
//
// The result is sorted and the codes are the vocabulary's own, so it says
// nothing about a code nobody declared.
//
// The locale ladder is walked, not just the locale: a code en-GB leaves to en
// is not missing from en-GB, because that is what the ladder is for. What is
// missing is what falls all the way through.
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
