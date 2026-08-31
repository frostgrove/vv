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

type Option func(*settings)

type settings struct {
	cookies    accesshttp.Cookies
	delivering bool
	render     []porthttp.RenderOption
}

func Rendering(options ...porthttp.RenderOption) Option {
	return func(s *settings) { s.render = append(s.render, options...) }
}

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

type jar struct{ credentials accesshttp.Credentials }

func newJar(table accesshttp.Table, options settings) jar {
	if !options.delivering {
		return jar{}
	}
	return jar{credentials: accesshttp.NewCredentials(table, options.cookies)}
}

func (this jar) requested(r *http.Request) (accesshttp.Delivery, error) {
	return this.credentials.Requested(r.Header.Get)
}

func (this jar) write(w http.ResponseWriter, cookies []accesshttp.Cookie) {
	for _, cookie := range cookies {
		http.SetCookie(w, cookie.HTTP())
	}
}

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

type Handler struct {
	endpoints access.Endpoints
	table     accesshttp.Table
	renderer  porthttp.Renderer
	jar       jar
}

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

func agentOf(r *http.Request) access.Agent {
	return access.Agent{UserAgent: r.Header.Get("User-Agent"), IP: r.RemoteAddr}
}

type RegisterHandler[P any] struct {
	signUp   *access.SignUpUseCase[P]
	route    accesshttp.Route
	renderer porthttp.Renderer
	jar      jar
}

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

func (this *RegisterHandler[P]) Mount(mux *http.ServeMux) {
	mux.HandleFunc(this.route.Method+" "+pattern(this.route.Path), this.Register)
}

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

type RefreshHandler struct {
	endpoints access.Endpoints
	route     accesshttp.Route
	renderer  porthttp.Renderer
	jar       jar
}

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

func (this *RefreshHandler) Mount(mux *http.ServeMux) {
	mux.HandleFunc(this.route.Method+" "+pattern(this.route.Path), this.Refresh)
}

func (this *RefreshHandler) Route() accesshttp.Route { return this.route }

func (this *RefreshHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var body access.RefreshRequest

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
		if byCookie {
			this.jar.write(w, this.jar.credentials.ClearRefresh())
		}
		authhttp.Refuse(w, r, this.renderer, err)
		return
	}
	this.jar.answer(w, http.StatusOK, response, accesshttp.Rotating(requested, byCookie))
}
