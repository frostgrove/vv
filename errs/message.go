package errs

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var ErrMessageRedeclared = errors.New("errs: the message key is already declared with different text")

type Messages struct {
	codes     *Codes
	templates map[string]map[string]string
}

func NewMessages(codes *Codes) *Messages {
	return &Messages{codes: codes, templates: map[string]map[string]string{}}
}

func (this *Messages) Add(locale, key, template string) error {
	if this.templates == nil {
		this.templates = map[string]map[string]string{}
	}
	byKey := this.templates[locale]
	if byKey == nil {
		byKey = map[string]string{}
		this.templates[locale] = byKey
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

func (this *Messages) Message(_ context.Context, v Violation, locale string) (string, bool) {
	keys := ladder(v)
	for _, loc := range locales(locale) {
		byKey := this.templates[loc]
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
	if tmpl, ok := this.codes.MessageFor(v.Code); ok {
		if s, ok := expand(tmpl, v.Params); ok {
			return s, true
		}
	}
	return "", false
}

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
