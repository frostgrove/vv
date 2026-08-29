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

// An Option configures the handlers this package builds.
type Option func(*settings)

type settings struct {
	cookies    accesshttp.Cookies
	delivering bool
	render     []porthttp.RenderOption
}

// Rendering chooses what a refusal is written through. This binding writes its
// own — Fiber and Gin hand the error to the application's middleware — so it is
// the one of the three with a renderer to configure.
func Rendering(options ...porthttp.RenderOption) Option {
	return func(s *settings) { s.render = append(s.render, options...) }
}

// Delivering lets a caller ask for its credentials in HttpOnly cookies.
//
// Without it every credential travels in the body and a request that asks for a
// cookie is refused, which is the honest answer from a deployment that has not
// decided what Secure and SameSite should be. With it the three deliveries in
// [accesshttp.Delivery] are all available, and a request that names none gets
// the most closed one.
func Delivering(cookies accesshttp.Cookies) Option {
	return func(s *settings) { s.cookies, s.delivering = cookies, true }
}

func optionsOf(options []Option) settings {
	var out settings
	for _, option := range options {
		if option != nil {
			option(&out)
		}
	}
	return out
}

// jar is the cookie half of these handlers, shared by all three of them so that
// the sign-in, the sign-up and the rotation cannot drift on where a credential
// goes. It is a field rather than an embedded type: promoting
// [accesshttp.Credentials]'s methods would put Answer and Clear on the public
// surface of every handler here.
type jar struct{ credentials accesshttp.Credentials }

func newJar(table accesshttp.Table, options settings) jar {
	if !options.delivering {
		return jar{}
	}
	return jar{credentials: accesshttp.NewCredentials(table, options.cookies)}
}

// requested answers the delivery this request asked for, or refuses it.
func (this jar) requested(r *http.Request) (accesshttp.Delivery, error) {
	return this.credentials.Requested(r.Header.Get)
}

// write sets the cookies. Before the status line, which is where the ordering
// matters: an http.ResponseWriter sends the headers with WriteHeader, and a
// Set-Cookie added afterwards is dropped without a word.
func (this jar) write(w http.ResponseWriter, cookies []accesshttp.Cookie) {
	for _, cookie := range cookies {
		http.SetCookie(w, cookie.HTTP())
	}
}

// answer writes the response with each credential in exactly one place.
func (this jar) answer(
	w http.ResponseWriter,
	status int,
	response access.AuthResponse,
	delivery accesshttp.Delivery,
) {
	body, cookies := this.credentials.Answer(response, delivery)
	this.write(w, cookies)
	write(w, status, body)
}

// Handler is one subject's endpoints as an http.Handler tree.
type Handler struct {
	endpoints access.Endpoints
	table     accesshttp.Table
	renderer  porthttp.Renderer
	jar       jar
}

// New builds the handler for a mounted subject.
//
// No error and no type parameter: everything it needs is on the mounted
// subject already. The sign-up route is NewRegister, separately.
func New(mounted *access.MountedSubject, options ...Option) *Handler {
	table := accesshttp.For(mounted)
	chosen := optionsOf(options)
	return &Handler{
		endpoints: mounted.Endpoints(),
		table:     table,
		renderer:  authhttp.RendererFor(chosen.render),
		jar:       newJar(table, chosen),
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
	// Before the password is checked rather than after: a request nobody can
	// answer is refused without spending a hash, and without minting a session
	// whose credentials have nowhere to go.
	delivery, err := this.jar.requested(r)
	if err != nil {
		this.refuse(w, r, err)
		return
	}
	response, err := this.endpoints.SignIn(r.Context(), body, agentOf(r))
	if err != nil {
		this.refuse(w, r, err)
		return
	}
	this.jar.answer(w, http.StatusOK, response, delivery)
}

// SignOut closes the session and takes the browser's copy of the credentials
// with it.
func (this *Handler) SignOut(w http.ResponseWriter, r *http.Request) {
	response, err := this.endpoints.SignOut(r.Context())
	if err != nil {
		this.refuse(w, r, err)
		return
	}
	this.jar.write(w, this.jar.credentials.Clear())
	write(w, http.StatusOK, response)
}

func (this *Handler) SignOutAll(w http.ResponseWriter, r *http.Request) {
	everywhere := r.URL.Query().Get("all") == "true"
	response, err := this.endpoints.SignOutAll(r.Context(), everywhere)
	if err != nil {
		this.refuse(w, r, err)
		return
	}
	// Only when this session went with them. Closing the others leaves the
	// caller signed in, and clearing the cookies would sign them out of the
	// browser while the server still holds their session.
	if everywhere {
		this.jar.write(w, this.jar.credentials.Clear())
	}
	write(w, http.StatusOK, response)
}

func (this *Handler) ChangeSecret(w http.ResponseWriter, r *http.Request) {
	var body access.ChangeSecretRequest
	if err := porthttp.DecodeJSON(r.Body, &body); err != nil {
		this.refuse(w, r, err)
		return
	}
	response, err := this.endpoints.ChangeSecret(r.Context(), body)
	if err != nil {
		this.refuse(w, r, err)
		return
	}
	write(w, http.StatusOK, response)
}

func (this *Handler) WhoAmI(w http.ResponseWriter, r *http.Request) {
	response, err := this.endpoints.WhoAmI(r.Context())
	if err != nil {
		this.refuse(w, r, err)
		return
	}
	write(w, http.StatusOK, response)
}

func (this *Handler) ListSessions(w http.ResponseWriter, r *http.Request) {
	response, err := this.endpoints.ListSessions(r.Context())
	if err != nil {
		this.refuse(w, r, err)
		return
	}
	write(w, http.StatusOK, response)
}

func (this *Handler) KillSession(w http.ResponseWriter, r *http.Request) {
	if err := this.endpoints.KillSession(r.Context(), r.PathValue("id")); err != nil {
		this.refuse(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func write(w http.ResponseWriter, status int, body any) {
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
	jar      jar
}

// NewRegister builds the sign-up route for a mounted subject.
//
// The use case is the one access.Mount answered with, so the payload type is
// carried rather than erased and asserted back.
func NewRegister[P any](
	mounted *access.MountedSubject,
	signUp *access.SignUpUseCase[P],
	options ...Option,
) *RegisterHandler[P] {
	table := accesshttp.For(mounted)
	chosen := optionsOf(options)
	return &RegisterHandler[P]{
		signUp:   signUp,
		route:    table.RegisterRoute(),
		renderer: authhttp.RendererFor(chosen.render),
		jar:      newJar(table, chosen),
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
	delivery, err := this.jar.requested(r)
	if err != nil {
		authhttp.Refuse(w, r, this.renderer, err)
		return
	}
	response, err := this.signUp.Execute(r.Context(), payload, agentOf(r))
	if err != nil {
		authhttp.Refuse(w, r, this.renderer, err)
		return
	}
	this.jar.answer(w, http.StatusCreated, response, delivery)
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
	jar       jar
}

// NewRefresh builds the rotation route for a mounted subject.
func NewRefresh(mounted *access.MountedSubject, options ...Option) *RefreshHandler {
	table := accesshttp.For(mounted)
	chosen := optionsOf(options)
	return &RefreshHandler{
		endpoints: mounted.Endpoints(),
		route:     table.RefreshRoute(),
		renderer:  authhttp.RendererFor(chosen.render),
		jar:       newJar(table, chosen),
	}
}

// Mount registers the rotation route on a ServeMux.
func (this *RefreshHandler) Mount(mux *http.ServeMux) {
	mux.HandleFunc(this.route.Method+" "+pattern(this.route.Path), this.Refresh)
}

// Route answers where it was mounted.
func (this *RefreshHandler) Route() accesshttp.Route { return this.route }

// Refresh rotates, and answers the rotating credential through the channel it
// arrived on — see [accesshttp.Rotating] for why the caller has no say in that.
//
// The body is read first and the cookie is the fallback, not the other way
// round: a caller that sent a credential meant that one, and a cookie left over
// from a browser session must not quietly rotate a native client's lineage
// instead.
func (this *RefreshHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var body access.RefreshRequest
	// An absent body is the ordinary case and not a malformed request: a browser
	// rotating by cookie has nothing to send, and demanding `{}` of it would
	// make the common path the one that needs explaining.
	if r.Body != nil && r.ContentLength != 0 {
		if err := porthttp.DecodeJSON(r.Body, &body); err != nil {
			authhttp.Refuse(w, r, this.renderer, err)
			return
		}
	}
	requested, err := this.jar.requested(r)
	if err != nil {
		authhttp.Refuse(w, r, this.renderer, err)
		return
	}

	credential, byCookie := body.Refresh, false
	if credential == "" && this.jar.credentials.InCookies() {
		if presented, err := r.Cookie(this.jar.credentials.RefreshCookie()); err == nil && presented.Value != "" {
			credential, byCookie = presented.Value, true
		}
	}

	response, err := this.endpoints.Refresh(r.Context(),
		access.RefreshRequest{Refresh: credential}, agentOf(r))
	if err != nil {
		// A cookie that no longer rotates anything is worth taking away, so the
		// browser stops presenting it and the next sign-in starts clean.
		if byCookie {
			this.jar.write(w, this.jar.credentials.ClearRefresh())
		}
		authhttp.Refuse(w, r, this.renderer, err)
		return
	}
	this.jar.answer(w, http.StatusOK, response, accesshttp.Rotating(requested, byCookie))
}
