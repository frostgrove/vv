package port

import (
	"context"
	"strings"
)

type ctxKey int

const localeKey ctxKey = iota

// WithLocale carries the request's language to the renderer. The locale is a
// rendering parameter and never a field on the fault: a fault crossing a queue
// must not carry the locale of the request that made it.
//
// One key for every transport, and that is the reason it is here rather than in
// each binding. A second key left in an HTTP package would be invisible to a
// gRPC renderer and vice versa: both packages' own tests would still pass, and
// the catalogue would silently answer in the default locale on one protocol.
func WithLocale(ctx context.Context, locale string) context.Context {
	if locale == "" {
		return ctx
	}
	return context.WithValue(ctx, localeKey, locale)
}

// LocaleFrom answers the request's language, or "".
func LocaleFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	s, _ := ctx.Value(localeKey).(string)
	return s
}

// FirstLanguageTag reads the first language tag out of a language-range list —
// what an Accept-Language header carries, and what gRPC metadata carries under
// grpc-accept-language because that is the header a gateway forwards verbatim.
//
// The first tag only: q-values pick between translations this library does not
// have, and the ladder either finds the entry or falls through. It is here
// rather than in each binding because all three HTTP ones had the same four
// lines and the gRPC one would have made it four copies — and a copy in a
// transport package would be a string split that four packages could disagree
// about ([[D-045]]).
func FirstLanguageTag(list string) string {
	if i := strings.IndexAny(list, ",;"); i >= 0 {
		list = list[:i]
	}
	return strings.TrimSpace(list)
}
