// Package accessnet mounts an access sign-in surface on net/http.
//
// Stdlib, so it ships inside the access module rather than as one of its own —
// the same reason `crud/http/crudnet` and `auth/http/authnet` do.
package accessnet

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/frostgrove/vv/auth/access"
	"github.com/frostgrove/vv/auth/access/http/accesshttp"
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/frostgrove/vv/port/porthttp"
)

// Handler is one subject's endpoints as an http.Handler tree.
type Handler struct {
	endpoints access.Endpoints
	table     accesshttp.Table
	renderer  porthttp.Renderer
}

// New builds the handler for a mounted subject.
//
// No error and no type parameter: everything it needs is on the mounted
// subject already. The sign-up route is NewRegister, separately.
func New(mounted *access.MountedSubject, options ...porthttp.RenderOption) *Handler {
	return &Handler{
		endpoints: mounted.Endpoints(),
		table:     accesshttp.For(mounted),
		renderer:  authhttp.RendererFor(options),
	}
}

// Mount registers every route on a ServeMux.
//
// The pattern carries the method, so a path this surface owns and a verb it
// does not answers 405 rather than falling through to whatever else the mux
// has. Path parameters are rewritten from the canonical `:id` to net/http's
// `{id}` here, which is the one thing that differs between the three bindings'
// route tables and the reason [accesshttp.Route.Path] is not used verbatim.
func (this *Handler) Mount(mux *http.ServeMux) {
	for _, route := range this.table.Routes() {
		mux.HandleFunc(route.Method+" "+pattern(route.Path), this.dispatch(route.Name))
	}
}

func pattern(path string) string {
	return strings.ReplaceAll(path, "/:id", "/{id}")
}

func (this *Handler) dispatch(name string) http.HandlerFunc {
	switch name {
	case accesshttp.SignIn:
		return this.SignIn
	case accesshttp.SignOut:
		return this.SignOut
	case accesshttp.SignOutAll:
		return this.SignOutAll
	case accesshttp.ChangeSecret:
		return this.ChangeSecret
	case accesshttp.WhoAmI:
		return this.WhoAmI
	case accesshttp.ListSessions:
		return this.ListSessions
	default:
		return this.KillSession
	}
}

func (this *Handler) SignIn(w http.ResponseWriter, r *http.Request) {
	var body access.SignInRequest
	if err := porthttp.DecodeJSON(r.Body, &body); err != nil {
		this.refuse(w, r, err)
		return
	}
	response, err := this.endpoints.SignIn(r.Context(), body, agentOf(r))
	this.write(w, r, http.StatusOK, response, err)
}

func (this *Handler) SignOut(w http.ResponseWriter, r *http.Request) {
	response, err := this.endpoints.SignOut(r.Context())
	this.write(w, r, http.StatusOK, response, err)
}

func (this *Handler) SignOutAll(w http.ResponseWriter, r *http.Request) {
	response, err := this.endpoints.SignOutAll(r.Context(), r.URL.Query().Get("all") == "true")
	this.write(w, r, http.StatusOK, response, err)
}

func (this *Handler) ChangeSecret(w http.ResponseWriter, r *http.Request) {
	var body access.ChangeSecretRequest
	if err := porthttp.DecodeJSON(r.Body, &body); err != nil {
		this.refuse(w, r, err)
		return
	}
	response, err := this.endpoints.ChangeSecret(r.Context(), body)
	this.write(w, r, http.StatusOK, response, err)
}

func (this *Handler) WhoAmI(w http.ResponseWriter, r *http.Request) {
	response, err := this.endpoints.WhoAmI(r.Context())
	this.write(w, r, http.StatusOK, response, err)
}

func (this *Handler) ListSessions(w http.ResponseWriter, r *http.Request) {
	response, err := this.endpoints.ListSessions(r.Context())
	this.write(w, r, http.StatusOK, response, err)
}

func (this *Handler) KillSession(w http.ResponseWriter, r *http.Request) {
	if err := this.endpoints.KillSession(r.Context(), r.PathValue("id")); err != nil {
		this.refuse(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (this *Handler) write(w http.ResponseWriter, r *http.Request, status int, body any, err error) {
	if err != nil {
		this.refuse(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (this *Handler) refuse(w http.ResponseWriter, r *http.Request, err error) {
	authhttp.Refuse(w, r, this.renderer, err)
}

// agentOf reads what the transport knows about the caller's device.
//
// RemoteAddr and not X-Forwarded-For: a proxy header nobody validated is a
// string the caller chose, and this value is shown back to them in a session
// list as "where you signed in from".
func agentOf(r *http.Request) access.Agent {
	return access.Agent{UserAgent: r.Header.Get("User-Agent"), IP: r.RemoteAddr}
}

// RegisterHandler is the sign-up route, on its own because it is the one
// endpoint whose body is the application's.
//
// P is that body. It lives here and on nothing else, so the seven endpoints a
// deployment always has stay free of a type parameter they never read.
type RegisterHandler[P any] struct {
	signUp   *access.SignUpUseCase[P]
	route    accesshttp.Route
	renderer porthttp.Renderer
}

// NewRegister builds the sign-up route for a mounted subject.
//
// The use case is the one access.Mount answered with, so the payload type is
// carried rather than erased and asserted back.
func NewRegister[P any](
	mounted *access.MountedSubject,
	signUp *access.SignUpUseCase[P],
	options ...porthttp.RenderOption,
) *RegisterHandler[P] {
	return &RegisterHandler[P]{
		signUp:   signUp,
		route:    accesshttp.For(mounted).RegisterRoute(),
		renderer: authhttp.RendererFor(options),
	}
}

// Mount registers the sign-up route on a ServeMux.
func (this *RegisterHandler[P]) Mount(mux *http.ServeMux) {
	mux.HandleFunc(this.route.Method+" "+pattern(this.route.Path), this.Register)
}

// Route answers where it was mounted.
func (this *RegisterHandler[P]) Route() accesshttp.Route { return this.route }

func (this *RegisterHandler[P]) Register(w http.ResponseWriter, r *http.Request) {
	var payload P
	if err := porthttp.DecodeJSON(r.Body, &payload); err != nil {
		authhttp.Refuse(w, r, this.renderer, err)
		return
	}
	response, err := this.signUp.Execute(r.Context(), payload, agentOf(r))
	if err != nil {
		authhttp.Refuse(w, r, this.renderer, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(response)
}

// RefreshHandler is the rotation route, mounted only for a strategy that
// rotates.
//
// Separate from Handler for the same reason RegisterHandler is: a deployment
// may not have it, and a route that exists and always refuses tells a stranger
// this deployment rotates and that they may not.
type RefreshHandler struct {
	endpoints access.Endpoints
	route     accesshttp.Route
	renderer  porthttp.Renderer
}

// NewRefresh builds the rotation route for a mounted subject.
func NewRefresh(mounted *access.MountedSubject, options ...porthttp.RenderOption) *RefreshHandler {
	return &RefreshHandler{
		endpoints: mounted.Endpoints(),
		route:     accesshttp.For(mounted).RefreshRoute(),
		renderer:  authhttp.RendererFor(options),
	}
}

// Mount registers the rotation route on a ServeMux.
func (this *RefreshHandler) Mount(mux *http.ServeMux) {
	mux.HandleFunc(this.route.Method+" "+pattern(this.route.Path), this.Refresh)
}

// Route answers where it was mounted.
func (this *RefreshHandler) Route() accesshttp.Route { return this.route }

func (this *RefreshHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var body access.RefreshRequest
	if err := porthttp.DecodeJSON(r.Body, &body); err != nil {
		authhttp.Refuse(w, r, this.renderer, err)
		return
	}
	response, err := this.endpoints.Refresh(r.Context(), body, agentOf(r))
	if err != nil {
		authhttp.Refuse(w, r, this.renderer, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}
