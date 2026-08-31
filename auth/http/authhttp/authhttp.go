package authhttp

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/port/porthttp"
)

var defaultRenderer = porthttp.NewRenderer()

func RendererFor(options []porthttp.RenderOption) porthttp.Renderer {
	if len(options) == 0 {
		return defaultRenderer
	}
	return porthttp.NewRenderer(options...)
}

func Locale(r *http.Request) context.Context {
	if r == nil {
		return context.Background()
	}
	return porthttp.WithLocale(r.Context(), porthttp.AcceptLanguage(r.Header.Get("Accept-Language")))
}

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
