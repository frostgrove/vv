package errs

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

var ErrMessageRedeclared = errors.New("errs: the message key is already declared with different text")

const (
	MaxMessageKeyBytes      = 256
	MaxMessageTemplateBytes = 16 << 10
	MaxMessageOutputBytes   = 16 << 10
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

func (this *Messages) MessageWithLocale(ctx context.Context, v Violation, locale string) (string, string, bool) {
	if this == nil || contextDone(ctx) {
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
		if contextDone(ctx) {
			this.mu.RUnlock()
			return "", "", false
		}
		byKey := this.templates[loc]
		if byKey == nil {
			continue
		}
		for _, k := range keys {
			if contextDone(ctx) {
				this.mu.RUnlock()
				return "", "", false
			}
			tmpl, ok := byKey[k]
			if !ok {
				continue
			}
			templates = append(templates, candidate{text: tmpl, locale: loc})
		}
	}
	this.mu.RUnlock()
	for _, template := range templates {
		if contextDone(ctx) {
			return "", "", false
		}
		if message, ok := expand(ctx, template.text, v.Params); ok {
			return message, template.locale, true
		}
	}
	if contextDone(ctx) {
		return "", "", false
	}
	if tmpl, ok := this.codes.MessageFor(v.Code); ok {
		if s, ok := expand(ctx, tmpl, v.Params); ok {
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

func expand(ctx context.Context, tmpl string, params map[string]any) (string, bool) {
	if contextDone(ctx) || len(tmpl) > MaxMessageOutputBytes {
		return "", false
	}
	if !strings.ContainsAny(tmpl, "{}") {
		return tmpl, true
	}
	var b strings.Builder
	b.Grow(len(tmpl))
	for i := 0; i < len(tmpl); {
		if contextDone(ctx) {
			return "", false
		}
		if tmpl[i] == '}' {
			return "", false
		}
		if tmpl[i] != '{' {
			if b.Len() == MaxMessageOutputBytes {
				return "", false
			}
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
		text, ok := scalarText(val)
		if !ok || len(text) > MaxMessageOutputBytes-b.Len() {
			return "", false
		}
		b.WriteString(text)
		i = end + 1
	}
	if contextDone(ctx) {
		return "", false
	}
	return b.String(), true
}

func scalarText(value any) (string, bool) {
	v := reflect.ValueOf(value)
	if !v.IsValid() {
		return "", false
	}
	switch v.Kind() {
	case reflect.String:
		text := v.String()
		return text, utf8.ValidString(text)
	case reflect.Bool:
		return strconv.FormatBool(v.Bool()), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.FormatUint(v.Uint(), 10), true
	case reflect.Float32, reflect.Float64:
		n := v.Float()
		if math.IsInf(n, 0) || math.IsNaN(n) {
			return "", false
		}
		return strconv.FormatFloat(n, 'g', -1, v.Type().Bits()), true
	default:
		return "", false
	}
}

func contextDone(ctx context.Context) bool {
	return ctx != nil && ctx.Err() != nil
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
