// Package accessfiber mounts an access sign-in surface on Fiber v3.
package accessfiber

import (
	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/auth/access"
	"github.com/frostgrove/vv/auth/access/http/accesshttp"
	"github.com/frostgrove/vv/port/porthttp"
)

// An Option configures the handlers this package builds.
type Option func(*settings)

type settings struct {
	cookies    accesshttp.Cookies
	delivering bool
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

// jar is the cookie half of these handlers, shared by all three of them so that
// the sign-in, the sign-up and the rotation cannot drift on where a credential
// goes. It is a field rather than an embedded type: promoting
// [accesshttp.Credentials]'s methods would put Answer and Clear on the public
// surface of every handler here.
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

// requested answers the delivery this request asked for, or refuses it.
func (this jar) requested(c fiber.Ctx) (accesshttp.Delivery, error) {
	return this.credentials.Requested(func(name string) string { return c.Get(name) })
}

// answer writes the response with each credential in exactly one place.
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
			// Unreadable from the page, and off plain HTTP wherever the
			// deployment is not somebody's workstation.
			HTTPOnly: true,
			Secure:   cookie.Secure,
			SameSite: sameSite(cookie.SameSite),
		})
	}
}

// sameSite translates rather than passes the string through. The two
// vocabularies happen to spell the three values alike today, and a translation
// that relies on that breaks silently the first time either side renames one.
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

// Handler is one subject's endpoints, ready to register on a router.
type Handler struct {
	endpoints access.Endpoints
	table     accesshttp.Table
	jar       jar
}

// New builds the handler for a mounted subject.
//
// No error and no type parameter: everything it needs is on the mounted
// subject already. The sign-up route is NewRegister, separately.
func New(mounted *access.MountedSubject, options ...Option) *Handler {
	table := accesshttp.For(mounted)
	return &Handler{
		endpoints: mounted.Endpoints(),
		table:     table,
		jar:       newJar(table, options),
	}
}

// Mount registers every route on a router.
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
	var body access.SignInRequest
	if err := c.Bind().Body(&body); err != nil {
		return porthttp.BadRequest(err)
	}
	// Before the password is checked rather than after: a request nobody can
	// answer is refused without spending a hash, and without minting a session
	// whose credentials have nowhere to go.
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

// SignOut closes the session and takes the browser's copy of the credentials
// with it.
func (this *Handler) SignOut(c fiber.Ctx) error {
	response, err := this.endpoints.SignOut(c.Context())
	if err != nil {
		return err
	}
	this.jar.write(c, this.jar.credentials.Clear())
	return c.JSON(response)
}

func (this *Handler) SignOutAll(c fiber.Ctx) error {
	everywhere := c.Query("all") == "true"
	response, err := this.endpoints.SignOutAll(c.Context(), everywhere)
	if err != nil {
		return err
	}
	// Only when this session went with them. Closing the others leaves the
	// caller signed in, and clearing the cookies would sign them out of the
	// browser while the server still holds their session.
	if everywhere {
		this.jar.write(c, this.jar.credentials.Clear())
	}
	return c.JSON(response)
}

func (this *Handler) ChangeSecret(c fiber.Ctx) error {
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
	if err := this.endpoints.KillSession(c.Context(), c.Params("id")); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// agentOf reads what the transport knows about the caller's device.
//
// c.IP() is the peer address unless the server is configured to trust a proxy
// header, which is the right default: an X-Forwarded-For nobody validated is a
// string the caller chose.
func agentOf(c fiber.Ctx) access.Agent {
	return access.Agent{UserAgent: c.Get(fiber.HeaderUserAgent), IP: c.IP()}
}

// RegisterHandler is the sign-up route, on its own because it is the one
// endpoint whose body is the application's.
//
// P is that body. It lives here and on nothing else, so the seven endpoints a
// deployment always has stay free of a type parameter they never read.
type RegisterHandler[P any] struct {
	signUp *access.SignUpUseCase[P]
	route  accesshttp.Route
	jar    jar
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
	return &RegisterHandler[P]{
		signUp: signUp,
		route:  table.RegisterRoute(),
		jar:    newJar(table, options),
	}
}

// Mount registers the sign-up route.
func (this *RegisterHandler[P]) Mount(r fiber.Router) {
	r.Add([]string{this.route.Method}, this.route.Path, this.Register)
}

// Route answers where it was mounted.
func (this *RegisterHandler[P]) Route() accesshttp.Route { return this.route }

func (this *RegisterHandler[P]) Register(c fiber.Ctx) error {
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

// RefreshHandler is the rotation route, mounted only for a strategy that
// rotates.
//
// Separate from Handler for the same reason RegisterHandler is: a deployment
// may not have it, and a route that exists and always refuses tells a stranger
// this deployment rotates and that they may not.
type RefreshHandler struct {
	endpoints access.Endpoints
	route     accesshttp.Route
	jar       jar
}

// NewRefresh builds the rotation route for a mounted subject.
func NewRefresh(mounted *access.MountedSubject, options ...Option) *RefreshHandler {
	table := accesshttp.For(mounted)
	return &RefreshHandler{
		endpoints: mounted.Endpoints(),
		route:     table.RefreshRoute(),
		jar:       newJar(table, options),
	}
}

// Mount registers the rotation route.
func (this *RefreshHandler) Mount(r fiber.Router) {
	r.Add([]string{this.route.Method}, this.route.Path, this.Refresh)
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
func (this *RefreshHandler) Refresh(c fiber.Ctx) error {
	var body access.RefreshRequest
	// An absent body is the ordinary case and not a malformed request: a browser
	// rotating by cookie has nothing to send, and demanding `{}` of it would
	// make the common path the one that needs explaining.
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
		// A cookie that no longer rotates anything is worth taking away, so the
		// browser stops presenting it and the next sign-in starts clean.
		if byCookie {
			this.jar.write(c, this.jar.credentials.ClearRefresh())
		}
		return err
	}
	return this.jar.answer(c, fiber.StatusOK, response, accesshttp.Rotating(requested, byCookie))
}
