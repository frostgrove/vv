package errs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

var ErrMessageRedeclared = errors.New("errs: the message key is already declared with different text")

const (
	MaxMessageKeyBytes      = 256
	MaxMessageTemplateBytes = 16 << 10
	MaxLocaleBytes          = 128
)

type Messages struct {
	mu        sync.RWMutex
	codes     *Codes
	templates map[string]map[string]string
	entries   int
	bytes     int
}

var _ LocalizedMessageSource = (*Messages)(nil)

func NewMessages(codes *Codes) *Messages {
	return &Messages{codes: codes, templates: map[string]map[string]string{}}
}

func (this *Messages) Add(locale, key, template string) error {
	if this == nil {
		return errors.New("errs: adding a message to a nil Messages")
	}
	if err := validateMessage(locale, key, template); err != nil {
		return err
	}

	this.mu.Lock()
	defer this.mu.Unlock()
	if this.templates == nil {
		this.templates = map[string]map[string]string{}
	}
	byKey := this.templates[locale]
	if byKey == nil {
		byKey = map[string]string{}
	}
	if have, ok := byKey[key]; ok {
		if have != template {
			return fmt.Errorf("errs: %q in locale %q is already %q: %w", key, locale, have, ErrMessageRedeclared)
		}
		return nil
	}
	if this.entries >= MaxCatalogueEntries {
		return fmt.Errorf("errs: the message catalogue exceeds %d entries", MaxCatalogueEntries)
	}
	size := messageBytes(locale, key, template)
	if size > MaxCatalogueBytes-this.bytes {
		return fmt.Errorf("errs: the message catalogue exceeds %d bytes", MaxCatalogueBytes)
	}
	this.templates[locale] = byKey
	byKey[key] = template
	this.entries++
	this.bytes += size
	return nil
}

func (this *Messages) Message(ctx context.Context, v Violation, locale string) (string, bool) {
	message, _, ok := this.MessageWithLocale(ctx, v, locale)
	return message, ok
}

func (this *Messages) MessageWithLocale(_ context.Context, v Violation, locale string) (string, string, bool) {
	if this == nil {
		return "", "", false
	}
	keys := ladder(v)
	type candidate struct {
		text   string
		locale string
	}
	this.mu.RLock()
	templates := make([]candidate, 0, len(keys)*2)
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
			templates = append(templates, candidate{text: tmpl, locale: loc})
		}
	}
	this.mu.RUnlock()
	for _, template := range templates {
		if message, ok := expand(template.text, v.Params); ok {
			return message, template.locale, true
		}
	}
	if tmpl, ok := this.codes.MessageFor(v.Code); ok {
		if s, ok := expand(tmpl, v.Params); ok {
			return s, "", true
		}
	}
	return "", "", false
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
	if !strings.ContainsAny(tmpl, "{}") {
		return tmpl, true
	}
	var b strings.Builder
	for i := 0; i < len(tmpl); {
		if tmpl[i] == '}' {
			return "", false
		}
		if tmpl[i] != '{' {
			b.WriteByte(tmpl[i])
			i++
			continue
		}
		end := strings.IndexByte(tmpl[i:], '}')
		if end < 1 {
			return "", false
		}
		end += i
		name := tmpl[i+1 : end]
		if !validPlaceholderName(name) {
			return "", false
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

func validateMessage(locale, key, template string) error {
	if err := validateLocale(locale); err != nil {
		return fmt.Errorf("errs: invalid locale %q: %w", locale, err)
	}
	if err := validateMessageKey(key); err != nil {
		return fmt.Errorf("errs: invalid message key %q: %w", key, err)
	}
	if err := validateMessageTemplate(template); err != nil {
		return fmt.Errorf("errs: invalid template for %q in locale %q: %w", key, locale, err)
	}
	return nil
}

func validateLocale(locale string) error {
	if len(locale) > MaxLocaleBytes {
		return fmt.Errorf("it exceeds %d bytes", MaxLocaleBytes)
	}
	if !utf8.ValidString(locale) {
		return errors.New("it is not valid UTF-8")
	}
	return nil
}

func validateMessageKey(key string) error {
	if key == "" {
		return errors.New("it is empty")
	}
	if len(key) > MaxMessageKeyBytes {
		return fmt.Errorf("it exceeds %d bytes", MaxMessageKeyBytes)
	}
	if !utf8.ValidString(key) {
		return errors.New("it is not valid UTF-8")
	}
	return nil
}

func validateMessageTemplate(template string) error {
	if len(template) > MaxMessageTemplateBytes {
		return fmt.Errorf("it exceeds %d bytes", MaxMessageTemplateBytes)
	}
	if !utf8.ValidString(template) {
		return errors.New("it is not valid UTF-8")
	}
	for i := 0; i < len(template); {
		switch template[i] {
		case '}':
			return errors.New("it has an unmatched closing brace")
		case '{':
			end := strings.IndexByte(template[i+1:], '}')
			if end < 0 {
				return errors.New("it has an unmatched opening brace")
			}
			end += i + 1
			if !validPlaceholderName(template[i+1 : end]) {
				return errors.New("it has an invalid placeholder")
			}
			i = end + 1
		default:
			i++
		}
	}
	return nil
}

func validPlaceholderName(name string) bool {
	return name != "" && !strings.ContainsAny(name, "{}")
}

func messageBytes(locale, key, template string) int {
	return len(locale) + len(key) + len(template)
}
