// Package authhttp is the half of an authentication middleware that is HTTP but
// not framework.
//
// It stands to authnet, authgin and authfiber exactly as crudhttp stands to
// crudnet, crudgin and crudfiber: the status table and the envelope come from
// porthttp, which belongs to neither subsystem ([[D-059]]), and this is the
// small amount left over — which renderer a middleware refuses through, and how
// a refusal reaches an http.ResponseWriter.
//
// A refusal is written here rather than handed to an outer error middleware.
// Deferring would be tidier and is wrong: a consumer who mounts an auth
// middleware without also mounting crudnet.Errors would get an empty 200 for
// every unauthenticated request, and the failure mode of "the door was open"
// must not depend on a second thing being installed. Both crudgin.Errors and
// crudfiber.Errors leave an already-written response alone, so writing here
// composes with them rather than fighting them.
//
// Nothing in this package knows what a credential is. That is auth's, and the
// transport-neutral decisions — optional or not, which header, whether a second
// guard re-authenticates — are auth.Guard's, so the gRPC interceptor gets them
// without an HTTP package in its build ([[D-045]]).
package authhttp

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/port/porthttp"
)

// defaultRenderer is what a middleware with no options refuses through. One
// value, built once: a renderer holds a vocabulary and a catalogue and nothing
// per-request, so sharing it is what makes the zero-config case free.
var defaultRenderer = porthttp.NewRenderer()

// RendererFor answers the renderer these options describe, keeping the shared
// value for the ordinary case of no options at all.
func RendererFor(opts []porthttp.RenderOption) porthttp.Renderer {
	if len(opts) == 0 {
		return defaultRenderer
	}
	return porthttp.NewRenderer(opts...)
}

// Locale is the rendering context a refusal is written in — the request's
// language, read from the header rather than carried on the fault.
func Locale(r *http.Request) context.Context {
	if r == nil {
		return context.Background()
	}
	return porthttp.WithLocale(r.Context(), porthttp.AcceptLanguage(r.Header.Get("Accept-Language")))
}

// Refuse writes the refusal. It is the one place a 401 leaves the net/http
// shaped bindings, which is both of the three that have an
// http.ResponseWriter — Fiber has its own writer and calls [RendererFor] and
// then its own four lines.
//
// The WWW-Authenticate header is deliberately not set. RFC 7235 says a 401
// carries one, and a browser answers a Basic challenge with a modal login box
// that no API wants; a bearer challenge additionally has an error= parameter
// whose whole purpose is to say which part of the token was wrong, which is the
// disclosure [[D-056]] exists to prevent. A consumer who needs the header for a
// standards-checking client sets it in a wrapper.
func Refuse(w http.ResponseWriter, r *http.Request, rd porthttp.Renderer, err error) {
	ctx := Locale(r)
	status, header, body := rd.Render(ctx, err)
	for k, vs := range header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if body == nil {
		w.WriteHeader(status)
		return
	}
	b, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		// The envelope failed to marshal, which is this library's bug and not
		// the caller's. Say nothing, and do not leak the marshal error.
		port.Logger(ctx).Error("authhttp: encoding the refusal", "err", marshalErr)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, writeErr := w.Write(b); writeErr != nil {
		port.Logger(ctx).Error("authhttp: writing the refusal", "err", writeErr)
	}
}
