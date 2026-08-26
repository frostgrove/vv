package crudhttp

import (
	"context"
	"io"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port/porthttp"
)

// Everything below moved to port/porthttp when the HTTP projection of the error
// contract stopped belonging to CRUD ([[D-059]]). It is still exported from
// here, as an alias or a one-line forwarder, for the same reason [[D-034]]'s
// move was: re-pointing an alias is not a breaking change, and a consumer who
// writes their own create route should not have to learn a new import path to
// keep compiling.
//
// Nothing here has behaviour. If a symbol in this file ever grows a body, it
// has stopped being a forwarder and belongs on one side of the split or the
// other.

// Renderer turns an error into a response. See [porthttp.Renderer].
type Renderer = porthttp.Renderer

// EnvelopeRenderer renders [Envelope]. See [porthttp.EnvelopeRenderer].
type EnvelopeRenderer = porthttp.EnvelopeRenderer

// RenderOption wires one part of an [EnvelopeRenderer].
type RenderOption = porthttp.RenderOption

// Envelope is the only body this library puts on the wire for a failed request.
type Envelope = porthttp.Envelope

// Groups are the envelope's two buckets.
type Groups = porthttp.Groups

const (
	// MaxViolations is how many violations one body carries.
	MaxViolations = porthttp.MaxViolations
	// DefaultRetryAfter is the Retry-After a 503 carries, in seconds.
	DefaultRetryAfter = porthttp.DefaultRetryAfter
	// MaxKeptBody caps the copy [DecodeJSONKeep] retains.
	MaxKeptBody = porthttp.MaxKeptBody
	// MaxBody is how many bytes of request body a binding reads before refusing.
	MaxBody = porthttp.MaxBody
)

// ErrBadRequest marks a failure the binding itself produced. It is the same
// variable as port.ErrBadRequest and not a copy of it.
var ErrBadRequest = porthttp.ErrBadRequest

// NewRenderer builds the default renderer.
func NewRenderer(opts ...RenderOption) *EnvelopeRenderer { return porthttp.NewRenderer(opts...) }

// WithCodes replaces the vocabulary the kind and the default messages are
// resolved through.
func WithCodes(c *errs.Codes) RenderOption { return porthttp.WithCodes(c) }

// WithMessages wires the message catalogue.
func WithMessages(m errs.MessageSource) RenderOption { return porthttp.WithMessages(m) }

// WithResolvers wires the path-translation hops this layer applies, in order.
func WithResolvers(rs ...errs.Resolver) RenderOption { return porthttp.WithResolvers(rs...) }

// WithMaxViolations caps how many violations one body carries.
func WithMaxViolations(n int) RenderOption { return porthttp.WithMaxViolations(n) }

// WithRetryAfter sets the seconds a 503 advertises.
func WithRetryAfter(seconds int) RenderOption { return porthttp.WithRetryAfter(seconds) }

// Internal is the one body a 500 ever has.
func Internal() Envelope { return porthttp.Internal() }

// Status maps a repository or query error to an HTTP status code.
func Status(err error) int { return porthttp.Status(err) }

// StatusFor is the kind-to-status table, arm by arm.
func StatusFor(k errs.Kind) int { return porthttp.StatusFor(k) }

// KindForStatus is [StatusFor] read backwards.
func KindForStatus(code int) errs.Kind { return porthttp.KindForStatus(code) }

// KindOf resolves the one kind that decides the status.
func KindOf(err error) errs.Kind { return porthttp.KindOf(err) }

// ParseEnvelope reads a failure body back into the [Envelope] a renderer wrote.
func ParseEnvelope(body []byte) (Envelope, bool) { return porthttp.ParseEnvelope(body) }

// BadRequest wraps an error as a client mistake.
func BadRequest(err error) error { return porthttp.BadRequest(err) }

// BadRequestf builds a client mistake from a message.
func BadRequestf(format string, args ...any) error { return porthttp.BadRequestf(format, args...) }

// BadRequestAs builds a client mistake that already knows its code and the part
// of the request it is about.
func BadRequestAs(code errs.Code, path errs.Path, format string, args ...any) error {
	return porthttp.BadRequestAs(code, path, format, args...)
}

// MalformedBody marks a body that would not decode.
func MalformedBody(err error) error { return porthttp.MalformedBody(err) }

// TooLarge marks a body past the cap the binding reads to.
func TooLarge(limit int) error { return porthttp.TooLarge(limit) }

// BodyResolver is the path hop for handlers nobody generated a mapper for.
func BodyResolver(raw []byte) errs.Resolver { return porthttp.BodyResolver(raw) }

// DecodeJSON reads a JSON body onto v.
func DecodeJSON(r io.Reader, v any) error { return porthttp.DecodeJSON(r, v) }

// DecodeJSONKeep decodes like [DecodeJSON] and hands back the bytes it decoded.
func DecodeJSONKeep(r io.Reader, v any) ([]byte, error) { return porthttp.DecodeJSONKeep(r, v) }

// DecodeJSONKeepLimit is [DecodeJSONKeep] with the cap named.
func DecodeJSONKeepLimit(r io.Reader, v any, limit int) ([]byte, error) {
	return porthttp.DecodeJSONKeepLimit(r, v, limit)
}

// KeepBody returns a copy of b that outlives the handler.
func KeepBody(b []byte) []byte { return porthttp.KeepBody(b) }

// WithBody carries the retained request body to the renderer.
func WithBody(ctx context.Context, body []byte) context.Context {
	return porthttp.WithBody(ctx, body)
}

// BodyFrom answers the retained body, or nil.
func BodyFrom(ctx context.Context) []byte { return porthttp.BodyFrom(ctx) }

// WithLocale carries the request's language to the renderer.
func WithLocale(ctx context.Context, locale string) context.Context {
	return porthttp.WithLocale(ctx, locale)
}

// LocaleFrom answers the request's language, or "".
func LocaleFrom(ctx context.Context) string { return porthttp.LocaleFrom(ctx) }

// AcceptLanguage reads the language a request asked for out of an
// Accept-Language header.
func AcceptLanguage(header string) string { return porthttp.AcceptLanguage(header) }
