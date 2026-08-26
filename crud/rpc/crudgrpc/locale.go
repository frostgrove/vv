package crudgrpc

import (
	"context"

	"google.golang.org/grpc/metadata"

	"github.com/frostgrove/vv/port"
)

// LocaleKeys are the metadata keys read for the language a caller asked for, in
// order, first match wins.
//
// gRPC standardises none of them. grpc-accept-language is what grpc-gateway
// forwards an HTTP Accept-Language header as, so a service behind a gateway
// answers in the caller's language with no wiring; accept-language is what a
// native client tends to send by analogy; x-locale is the spelling in the wild
// that is neither.
//
// The value is a language-range list, read by port.FirstLanguageTag — the same
// parser the HTTP bindings use, so `fr-CA,fr;q=0.9` means the same thing on
// both transports.
var LocaleKeys = []string{"grpc-accept-language", "accept-language", "x-locale"}

// WithLocale carries a language to the renderer explicitly, for a caller that
// gets it from somewhere other than metadata — a token claim, a tenant record.
// It wins over the metadata, because it was said on purpose.
func WithLocale(ctx context.Context, locale string) context.Context {
	return port.WithLocale(ctx, locale)
}

// withRequestLocale installs the caller's language, unless one is already
// there.
func withRequestLocale(ctx context.Context) context.Context {
	if port.LocaleFrom(ctx) != "" {
		return ctx
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	for _, key := range LocaleKeys {
		for _, v := range md.Get(key) {
			if tag := port.FirstLanguageTag(v); tag != "" {
				return port.WithLocale(ctx, tag)
			}
		}
	}
	return ctx
}
