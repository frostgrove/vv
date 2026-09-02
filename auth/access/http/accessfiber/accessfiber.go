package accessfiber

import (
	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/auth/access"
	"github.com/frostgrove/vv/auth/access/http/accesshttp"
	"github.com/frostgrove/vv/port/porthttp"
)

type Option func(*settings)

type settings struct {
	cookies    accesshttp.Cookies
	delivering bool
}

func Delivering(cookies accesshttp.Cookies) Option {
	return func(s *settings) { s.cookies, s.delivering = cookies, true }
}

type jar struct{ credentials accesshttp.Credentials }

func newJar(table accesshttp.Table, options []Option) jar {
	var chosen settings
	for _, option := range options {
		if option != nil {
			option(&chosen)
		}
	}
	if !chosen.delivering {
		return jar{}
	}
	return jar{credentials: accesshttp.NewCredentials(table, chosen.cookies)}
}

func (this jar) protect(c fiber.Ctx) error {
	return this.credentials.Protect(c.Method(),
		func(name string) string { return c.Get(name) },
		func(name string) string { return c.Cookies(name) })
}

func (this jar) requested(c fiber.Ctx) (accesshttp.Delivery, error) {
	return this.credentials.Requested(func(name string) string { return c.Get(name) })
}

func (this jar) answer(
	c fiber.Ctx,
	status int,
	response access.AuthResponse,
	delivery accesshttp.Delivery,
) error {
	body, cookies := this.credentials.Answer(response, delivery)
	this.write(c, cookies)
	return c.Status(status).JSON(body)
}

func (this jar) write(c fiber.Ctx, cookies []accesshttp.Cookie) {
	for _, cookie := range cookies {
		c.Cookie(&fiber.Cookie{
			Name:    cookie.Name,
			Value:   cookie.Value,
			Path:    cookie.Path,
			Domain:  cookie.Domain,
			Expires: cookie.Expires,

			HTTPOnly: true,
			Secure:   cookie.Secure,
			SameSite: sameSite(cookie.SameSite),
		})
	}
}

func sameSite(value accesshttp.SameSite) string {
	switch value {
	case accesshttp.SameSiteLax:
		return fiber.CookieSameSiteLaxMode
	case accesshttp.SameSiteNone:
		return fiber.CookieSameSiteNoneMode
	default:
		return fiber.CookieSameSiteStrictMode
	}
}

type Handler struct {
	endpoints access.Endpoints
	table     accesshttp.Table
	jar       jar
}

func New(mounted *access.MountedSubject, options ...Option) *Handler {
	table := accesshttp.For(mounted)
	return &Handler{
		endpoints: mounted.Endpoints(),
		table:     table,
		jar:       newJar(table, options),
	}
}

func (this *Handler) Mount(r fiber.Router) {
	for _, route := range this.table.Routes() {
		r.Add([]string{route.Method}, route.Path, this.dispatch(route.Name))
	}
}

func (this *Handler) dispatch(name string) fiber.Handler {
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

func (this *Handler) SignIn(c fiber.Ctx) error {
	if err := this.jar.protect(c); err != nil {
		return err
	}
	var body access.SignInRequest
	if err := c.Bind().Body(&body); err != nil {
		return porthttp.BadRequest(err)
	}

	delivery, err := this.jar.requested(c)
	if err != nil {
		return err
	}
	response, err := this.endpoints.SignIn(c.Context(), body, agentOf(c))
	if err != nil {
		return err
	}
	return this.jar.answer(c, fiber.StatusOK, response, delivery)
}

func (this *Handler) SignOut(c fiber.Ctx) error {
	if err := this.jar.protect(c); err != nil {
		return err
	}
	response, err := this.endpoints.SignOut(c.Context())
	if err != nil {
		return err
	}
	this.jar.write(c, this.jar.credentials.Clear())
	return c.JSON(response)
}

func (this *Handler) SignOutAll(c fiber.Ctx) error {
	if err := this.jar.protect(c); err != nil {
		return err
	}
	everywhere := c.Query("all") == "true"
	response, err := this.endpoints.SignOutAll(c.Context(), everywhere)
	if err != nil {
		return err
	}

	if everywhere {
		this.jar.write(c, this.jar.credentials.Clear())
	}
	return c.JSON(response)
}

func (this *Handler) ChangeSecret(c fiber.Ctx) error {
	if err := this.jar.protect(c); err != nil {
		return err
	}
	var body access.ChangeSecretRequest
	if err := c.Bind().Body(&body); err != nil {
		return porthttp.BadRequest(err)
	}
	response, err := this.endpoints.ChangeSecret(c.Context(), body)
	if err != nil {
		return err
	}
	return c.JSON(response)
}

func (this *Handler) WhoAmI(c fiber.Ctx) error {
	response, err := this.endpoints.WhoAmI(c.Context())
	if err != nil {
		return err
	}
	return c.JSON(response)
}

func (this *Handler) ListSessions(c fiber.Ctx) error {
	response, err := this.endpoints.ListSessions(c.Context())
	if err != nil {
		return err
	}
	return c.JSON(response)
}

func (this *Handler) KillSession(c fiber.Ctx) error {
	if err := this.jar.protect(c); err != nil {
		return err
	}
	if err := this.endpoints.KillSession(c.Context(), c.Params("id")); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func agentOf(c fiber.Ctx) access.Agent {
	return access.Agent{UserAgent: c.Get(fiber.HeaderUserAgent), IP: c.IP()}
}

type RegisterHandler[P any] struct {
	signUp *access.SignUpUseCase[P]
	route  accesshttp.Route
	jar    jar
}

func NewRegister[P any](
	mounted *access.MountedSubject,
	signUp *access.SignUpUseCase[P],
	options ...Option,
) *RegisterHandler[P] {
	table := accesshttp.For(mounted)
	return &RegisterHandler[P]{
		signUp: signUp,
		route:  table.RegisterRoute(),
		jar:    newJar(table, options),
	}
}

func (this *RegisterHandler[P]) Mount(r fiber.Router) {
	r.Add([]string{this.route.Method}, this.route.Path, this.Register)
}

func (this *RegisterHandler[P]) Route() accesshttp.Route { return this.route }

func (this *RegisterHandler[P]) Register(c fiber.Ctx) error {
	if err := this.jar.protect(c); err != nil {
		return err
	}
	var payload P
	if err := c.Bind().Body(&payload); err != nil {
		return porthttp.BadRequest(err)
	}
	delivery, err := this.jar.requested(c)
	if err != nil {
		return err
	}
	response, err := this.signUp.Execute(c.Context(), payload, agentOf(c))
	if err != nil {
		return err
	}
	return this.jar.answer(c, fiber.StatusCreated, response, delivery)
}

type RefreshHandler struct {
	endpoints access.Endpoints
	route     accesshttp.Route
	jar       jar
}

func NewRefresh(mounted *access.MountedSubject, options ...Option) *RefreshHandler {
	table := accesshttp.For(mounted)
	return &RefreshHandler{
		endpoints: mounted.Endpoints(),
		route:     table.RefreshRoute(),
		jar:       newJar(table, options),
	}
}

func (this *RefreshHandler) Mount(r fiber.Router) {
	r.Add([]string{this.route.Method}, this.route.Path, this.Refresh)
}

func (this *RefreshHandler) Route() accesshttp.Route { return this.route }

func (this *RefreshHandler) Refresh(c fiber.Ctx) error {
	if err := this.jar.protect(c); err != nil {
		return err
	}
	var body access.RefreshRequest

	if len(c.Body()) > 0 {
		if err := c.Bind().Body(&body); err != nil {
			return porthttp.BadRequest(err)
		}
	}
	requested, err := this.jar.requested(c)
	if err != nil {
		return err
	}

	credential, byCookie := body.Refresh, false
	if credential == "" && this.jar.credentials.InCookies() {
		if presented := c.Cookies(this.jar.credentials.RefreshCookie()); presented != "" {
			credential, byCookie = presented, true
		}
	}

	response, err := this.endpoints.Refresh(c.Context(),
		access.RefreshRequest{Refresh: credential}, agentOf(c))
	if err != nil {
		if byCookie {
			this.jar.write(c, this.jar.credentials.ClearRefresh())
		}
		return err
	}
	return this.jar.answer(c, fiber.StatusOK, response, accesshttp.Rotating(requested, byCookie))
}
