// Package accessgin mounts an access sign-in surface on Gin.
package accessgin

import (
	"net/http"

	"github.com/gin-gonic/gin"

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
func (this jar) requested(c *gin.Context) (accesshttp.Delivery, error) {
	return this.credentials.Requested(c.GetHeader)
}

// answer writes the response with each credential in exactly one place.
func (this jar) answer(
	c *gin.Context,
	status int,
	response access.AuthResponse,
	delivery accesshttp.Delivery,
) {
	body, cookies := this.credentials.Answer(response, delivery)
	this.write(c, cookies)
	c.JSON(status, body)
}

// write sets the cookies through net/http rather than through c.SetCookie,
// which takes a Max-Age and would make every call compute one from an expiry
// the library already knows.
func (this jar) write(c *gin.Context, cookies []accesshttp.Cookie) {
	for _, cookie := range cookies {
		http.SetCookie(c.Writer, cookie.HTTP())
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
func (this *Handler) Mount(r gin.IRouter) {
	for _, route := range this.table.Routes() {
		r.Handle(route.Method, route.Path, this.dispatch(route.Name))
	}
}

func (this *Handler) dispatch(name string) gin.HandlerFunc {
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

func (this *Handler) SignIn(c *gin.Context) {
	var body access.SignInRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		refuse(c, porthttp.BadRequest(err))
		return
	}
	// Before the password is checked rather than after: a request nobody can
	// answer is refused without spending a hash, and without minting a session
	// whose credentials have nowhere to go.
	delivery, err := this.jar.requested(c)
	if err != nil {
		refuse(c, err)
		return
	}
	response, err := this.endpoints.SignIn(c.Request.Context(), body, agentOf(c))
	if err != nil {
		refuse(c, err)
		return
	}
	this.jar.answer(c, http.StatusOK, response, delivery)
}

// SignOut closes the session and takes the browser's copy of the credentials
// with it.
func (this *Handler) SignOut(c *gin.Context) {
	response, err := this.endpoints.SignOut(c.Request.Context())
	if err != nil {
		refuse(c, err)
		return
	}
	this.jar.write(c, this.jar.credentials.Clear())
	c.JSON(http.StatusOK, response)
}

func (this *Handler) SignOutAll(c *gin.Context) {
	everywhere := c.Query("all") == "true"
	response, err := this.endpoints.SignOutAll(c.Request.Context(), everywhere)
	if err != nil {
		refuse(c, err)
		return
	}
	// Only when this session went with them. Closing the others leaves the
	// caller signed in, and clearing the cookies would sign them out of the
	// browser while the server still holds their session.
	if everywhere {
		this.jar.write(c, this.jar.credentials.Clear())
	}
	c.JSON(http.StatusOK, response)
}

func (this *Handler) ChangeSecret(c *gin.Context) {
	var body access.ChangeSecretRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		refuse(c, porthttp.BadRequest(err))
		return
	}
	response, err := this.endpoints.ChangeSecret(c.Request.Context(), body)
	if err != nil {
		refuse(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (this *Handler) WhoAmI(c *gin.Context) {
	response, err := this.endpoints.WhoAmI(c.Request.Context())
	if err != nil {
		refuse(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (this *Handler) ListSessions(c *gin.Context) {
	response, err := this.endpoints.ListSessions(c.Request.Context())
	if err != nil {
		refuse(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (this *Handler) KillSession(c *gin.Context) {
	if err := this.endpoints.KillSession(c.Request.Context(), c.Param("id")); err != nil {
		refuse(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// refuse hands the error to the application's error middleware.
func refuse(c *gin.Context, err error) {
	_ = c.Error(err)
	c.Abort()
}

// agentOf reads what the transport knows about the caller's device.
//
// ClientIP() honours Gin's own trusted-proxy configuration, so what is trusted
// is a decision the application already made rather than one taken here.
func agentOf(c *gin.Context) access.Agent {
	return access.Agent{UserAgent: c.GetHeader("User-Agent"), IP: c.ClientIP()}
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
func (this *RegisterHandler[P]) Mount(r gin.IRouter) {
	r.Handle(this.route.Method, this.route.Path, this.Register)
}

// Route answers where it was mounted.
func (this *RegisterHandler[P]) Route() accesshttp.Route { return this.route }

func (this *RegisterHandler[P]) Register(c *gin.Context) {
	var payload P
	if err := c.ShouldBindJSON(&payload); err != nil {
		refuse(c, porthttp.BadRequest(err))
		return
	}
	delivery, err := this.jar.requested(c)
	if err != nil {
		refuse(c, err)
		return
	}
	response, err := this.signUp.Execute(c.Request.Context(), payload, agentOf(c))
	if err != nil {
		refuse(c, err)
		return
	}
	this.jar.answer(c, http.StatusCreated, response, delivery)
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
func (this *RefreshHandler) Mount(r gin.IRouter) {
	r.Handle(this.route.Method, this.route.Path, this.Refresh)
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
func (this *RefreshHandler) Refresh(c *gin.Context) {
	var body access.RefreshRequest
	// An absent body is the ordinary case and not a malformed request: a browser
	// rotating by cookie has nothing to send, and demanding `{}` of it would
	// make the common path the one that needs explaining.
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&body); err != nil {
			refuse(c, porthttp.BadRequest(err))
			return
		}
	}
	requested, err := this.jar.requested(c)
	if err != nil {
		refuse(c, err)
		return
	}

	credential, byCookie := body.Refresh, false
	if credential == "" && this.jar.credentials.InCookies() {
		if presented, err := c.Cookie(this.jar.credentials.RefreshCookie()); err == nil && presented != "" {
			credential, byCookie = presented, true
		}
	}

	response, err := this.endpoints.Refresh(c.Request.Context(),
		access.RefreshRequest{Refresh: credential}, agentOf(c))
	if err != nil {
		// A cookie that no longer rotates anything is worth taking away, so the
		// browser stops presenting it and the next sign-in starts clean.
		if byCookie {
			this.jar.write(c, this.jar.credentials.ClearRefresh())
		}
		refuse(c, err)
		return
	}
	this.jar.answer(c, http.StatusOK, response, accesshttp.Rotating(requested, byCookie))
}
