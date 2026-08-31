package crudgrpc

import (
	"context"

	"google.golang.org/grpc/metadata"

	"github.com/frostgrove/vv/port"
)

var LocaleKeys = []string{"grpc-accept-language", "accept-language", "x-locale"}

func WithLocale(ctx context.Context, locale string) context.Context {
	return port.WithLocale(ctx, locale)
}

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
