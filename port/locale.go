package port

import (
	"context"
	"strings"
)

type ctxKey int

const localeKey ctxKey = iota

func WithLocale(ctx context.Context, locale string) context.Context {
	if locale == "" {
		return ctx
	}
	return context.WithValue(ctx, localeKey, locale)
}

func LocaleFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(localeKey).(string)
	return s
}

func FirstLanguageTag(list string) string {
	if i := strings.IndexAny(list, ",;"); i >= 0 {
		list = list[:i]
	}
	return strings.TrimSpace(list)
}
