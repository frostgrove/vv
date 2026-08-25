package errs

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrMessageRedeclared reports a second, disagreeing template for one key in
// one locale. Declaring the same text twice is allowed; declaring two different
// texts is a catalogue that renders differently depending on load order.
var ErrMessageRedeclared = errors.New("errs: the message key is already declared with different text")

// Messages is a catalogue with a hierarchical lookup, in the shape Spring's
// MessageSource established: an override is as narrow or as broad as its author
// needs, with no configuration schema to learn.
//
// For a violation at ["user","email"] with code "unique" the ladder is
//
//	user.email.unique  ->  user.unique  ->  email.unique  ->  unique
//
// and then the code's default from the wired [Codes].
//
// Only the first and last named steps take part, so the ladder is four rungs
// deep whatever the path is. A violation at ["order","items","email"] with the
// same code reads
//
//	order.email.unique  ->  order.unique  ->  email.unique  ->  unique
//
// and a key spelling the whole path is never consulted. That failure is silent
// in a way worth naming: Add accepts order.items.email.unique, nothing reaches
// it, and the walk falls through to the code's default — so the response is
// well-formed and the override the consumer wrote just never appears.
//
// The scope is derived from the violation's own path, because that is all a
// [MessageSource] is given: Message receives a [Violation], and a violation
// carries no entity — [Fault.Entity] is one level up and out of reach. The key
// scheme is entity.field.code, and first-and-last is the nearest thing a path
// alone can offer.
//
// Index steps take no part. A violation on row 3 and one on row 7 must not each
// need a catalogue entry, so items[3].email and items[7].email resolve the same
// keys.
type Messages struct {
	codes     *Codes
	templates map[string]map[string]string // locale -> key -> template
}

// NewMessages returns an empty catalogue that falls back to the codes' own
// default messages. A nil *Codes is allowed and means there are none.
func NewMessages(codes *Codes) *Messages {
	return &Messages{codes: codes, templates: map[string]map[string]string{}}
}

// Add declares one template. The empty locale is the default one, used when the
// requested locale and its base language have nothing.
func (m *Messages) Add(locale, key, template string) error {
	if m.templates == nil {
		m.templates = map[string]map[string]string{}
	}
	byKey := m.templates[locale]
	if byKey == nil {
		byKey = map[string]string{}
		m.templates[locale] = byKey
	}
	if have, ok := byKey[key]; ok {
		if have != template {
			return fmt.Errorf("errs: %q in locale %q is already %q: %w", key, locale, have, ErrMessageRedeclared)
		}
		return nil
	}
	byKey[key] = template
	return nil
}

// Message walks the ladder for the requested locale, then for its base language,
// then for the default locale, and finally falls back to the code's own default.
//
// A template whose placeholders [Violation.Params] cannot fill counts as
// unresolved and the walk continues, so a missing parameter produces the
// broader message rather than a body containing {max}.
//
// The context is accepted and unused. It is in the signature for a catalogue
// that reads from somewhere.
func (m *Messages) Message(_ context.Context, v Violation, locale string) (string, bool) {
	keys := ladder(v)
	for _, loc := range locales(locale) {
		byKey := m.templates[loc]
		if byKey == nil {
			continue
		}
		for _, k := range keys {
			tmpl, ok := byKey[k]
			if !ok {
				continue
			}
			if s, ok := expand(tmpl, v.Params); ok {
				return s, true
			}
		}
	}
	if tmpl, ok := m.codes.MessageFor(v.Code); ok {
		if s, ok := expand(tmpl, v.Params); ok {
			return s, true
		}
	}
	return "", false
}

// ladder is the key list, narrowest first, with an index step skipped, every
// named step between the first and the last dropped, and a key that would
// repeat the previous one dropped. The key scheme is entity.field.code and a
// violation carries no entity, so first-and-last is what a path alone can
// supply; a full dotted chain would need a catalogue entry per nesting.
func ladder(v Violation) []string {
	code := string(v.Code)
	if code == "" {
		return nil
	}
	var first, last string
	for _, s := range v.Path {
		if s.IsIndex {
			continue
		}
		if first == "" {
			first = s.Name
		}
		last = s.Name
	}

	var keys []string
	add := func(k string) {
		if len(keys) > 0 && keys[len(keys)-1] == k {
			return
		}
		keys = append(keys, k)
	}
	if first != "" && last != "" && first != last {
		add(first + "." + last + "." + code)
	}
	if first != "" {
		add(first + "." + code)
	}
	if last != "" {
		add(last + "." + code)
	}
	add(code)
	return keys
}

// locales is the requested locale, its base language, and the default, without
// repeats: en-GB reads en-GB, then en, then the default catalogue.
//
// Both separators are read. A locale arrives from an Accept-Language header as
// en-GB and out of a POSIX environment as en_GB, and a catalogue that resolved
// one and not the other would answer differently depending on where the string
// came from.
func locales(locale string) []string {
	out := []string{}
	if locale != "" {
		out = append(out, locale)
		if base, _, ok := strings.Cut(locale, "-"); ok && base != "" {
			out = append(out, base)
		} else if base, _, ok := strings.Cut(locale, "_"); ok && base != "" {
			out = append(out, base)
		}
	}
	return append(out, "")
}

// expand fills {name} from params. It scans the template and never ranges the
// map: a message assembled by iterating a map is not byte-identical run to run,
// which is [[D-014]] applied to what a client reads.
//
// A placeholder with no parameter makes the whole template unresolved, so the
// caller falls back rather than emitting the placeholder.
func expand(tmpl string, params map[string]any) (string, bool) {
	if !strings.ContainsRune(tmpl, '{') {
		return tmpl, true
	}
	var b strings.Builder
	for i := 0; i < len(tmpl); {
		if tmpl[i] != '{' {
			b.WriteByte(tmpl[i])
			i++
			continue
		}
		end := strings.IndexByte(tmpl[i:], '}')
		if end < 1 {
			b.WriteByte(tmpl[i])
			i++
			continue
		}
		end += i
		name := tmpl[i+1 : end]
		if name == "" {
			b.WriteString(tmpl[i : end+1])
			i = end + 1
			continue
		}
		val, ok := params[name]
		if !ok {
			return "", false
		}
		fmt.Fprint(&b, val)
		i = end + 1
	}
	return b.String(), true
}
