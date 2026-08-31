package crudhttp

import (
	"context"
	"io"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port/porthttp"
)

type Renderer = porthttp.Renderer

type EnvelopeRenderer = porthttp.EnvelopeRenderer

type RenderOption = porthttp.RenderOption

type Envelope = porthttp.Envelope

type Groups = porthttp.Groups

const (
	MaxViolations = porthttp.MaxViolations

	DefaultRetryAfter = porthttp.DefaultRetryAfter

	MaxKeptBody = porthttp.MaxKeptBody

	MaxBody = porthttp.MaxBody
)

var ErrBadRequest = porthttp.ErrBadRequest

func NewRenderer(options ...RenderOption) *EnvelopeRenderer { return porthttp.NewRenderer(options...) }

func WithCodes(c *errs.Codes) RenderOption { return porthttp.WithCodes(c) }

func WithMessages(m errs.MessageSource) RenderOption { return porthttp.WithMessages(m) }

func WithResolvers(rs ...errs.Resolver) RenderOption { return porthttp.WithResolvers(rs...) }

func WithMaxViolations(n int) RenderOption { return porthttp.WithMaxViolations(n) }

func WithRetryAfter(seconds int) RenderOption { return porthttp.WithRetryAfter(seconds) }

func Internal() Envelope { return porthttp.Internal() }

func Status(err error) int { return porthttp.Status(err) }

func StatusFor(k errs.Kind) int { return porthttp.StatusFor(k) }

func KindForStatus(code int) errs.Kind { return porthttp.KindForStatus(code) }

func KindOf(err error) errs.Kind { return porthttp.KindOf(err) }

func ParseEnvelope(body []byte) (Envelope, bool) { return porthttp.ParseEnvelope(body) }

func BadRequest(err error) error { return porthttp.BadRequest(err) }

func BadRequestf(format string, args ...any) error { return porthttp.BadRequestf(format, args...) }

func BadRequestAs(code errs.Code, path errs.Path, format string, args ...any) error {
	return porthttp.BadRequestAs(code, path, format, args...)
}

func MalformedBody(err error) error { return porthttp.MalformedBody(err) }

func TooLarge(limit int) error { return porthttp.TooLarge(limit) }

func BodyResolver(raw []byte) errs.Resolver { return porthttp.BodyResolver(raw) }

func DecodeJSON(r io.Reader, v any) error { return porthttp.DecodeJSON(r, v) }

func DecodeJSONKeep(r io.Reader, v any) ([]byte, error) { return porthttp.DecodeJSONKeep(r, v) }

func DecodeJSONKeepLimit(r io.Reader, v any, limit int) ([]byte, error) {
	return porthttp.DecodeJSONKeepLimit(r, v, limit)
}

func KeepBody(b []byte) []byte { return porthttp.KeepBody(b) }

func WithBody(ctx context.Context, body []byte) context.Context {
	return porthttp.WithBody(ctx, body)
}

func BodyFrom(ctx context.Context) []byte { return porthttp.BodyFrom(ctx) }

func WithLocale(ctx context.Context, locale string) context.Context {
	return porthttp.WithLocale(ctx, locale)
}

func LocaleFrom(ctx context.Context) string { return porthttp.LocaleFrom(ctx) }

func AcceptLanguage(header string) string { return porthttp.AcceptLanguage(header) }

func Routed(status int) error { return porthttp.Routed(status) }
