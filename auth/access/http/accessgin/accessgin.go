package accessgin

import (
	"net/http"

	"github.com/gin-gonic/gin"

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

func (this jar) protect(c *gin.Context) error {
	return this.credentials.Protect(c.Request.Method, c.GetHeader,
		func(name string) string {
			value, err := c.Cookie(name)
			if err != nil {
				return ""
			}
			return value
		})
}

func (this jar) requested(c *gin.Context) (accesshttp.Delivery, error) {
	return this.credentials.Requested(c.GetHeader)
}

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

func (this jar) write(c *gin.Context, cookies []accesshttp.Cookie) {
	for _, cookie := range cookies {
		http.SetCookie(c.Writer, cookie.HTTP())
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
	if err := this.jar.protect(c); err != nil {
		refuse(c, err)
		return
	}
	var body access.SignInRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		refuse(c, porthttp.BadRequest(err))
		return
	}

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

func (this *Handler) SignOut(c *gin.Context) {
	if err := this.jar.protect(c); err != nil {
		refuse(c, err)
		return
	}
	response, err := this.endpoints.SignOut(c.Request.Context())
	if err != nil {
		refuse(c, err)
		return
	}
	this.jar.write(c, this.jar.credentials.Clear())
	c.JSON(http.StatusOK, response)
}

func (this *Handler) SignOutAll(c *gin.Context) {
	if err := this.jar.protect(c); err != nil {
		refuse(c, err)
		return
	}
	everywhere := c.Query("all") == "true"
	response, err := this.endpoints.SignOutAll(c.Request.Context(), everywhere)
	if err != nil {
		refuse(c, err)
		return
	}

	if everywhere {
		this.jar.write(c, this.jar.credentials.Clear())
	}
	c.JSON(http.StatusOK, response)
}

func (this *Handler) ChangeSecret(c *gin.Context) {
	if err := this.jar.protect(c); err != nil {
		refuse(c, err)
		return
	}
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
	if err := this.jar.protect(c); err != nil {
		refuse(c, err)
		return
	}
	if err := this.endpoints.KillSession(c.Request.Context(), c.Param("id")); err != nil {
		refuse(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func refuse(c *gin.Context, err error) {
	_ = c.Error(err)
	c.Abort()
}

func agentOf(c *gin.Context) access.Agent {
	return access.Agent{UserAgent: c.GetHeader("User-Agent"), IP: c.ClientIP()}
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

func (this *RegisterHandler[P]) Mount(r gin.IRouter) {
	r.Handle(this.route.Method, this.route.Path, this.Register)
}

func (this *RegisterHandler[P]) Route() accesshttp.Route { return this.route }

func (this *RegisterHandler[P]) Register(c *gin.Context) {
	if err := this.jar.protect(c); err != nil {
		refuse(c, err)
		return
	}
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

func (this *RefreshHandler) Mount(r gin.IRouter) {
	r.Handle(this.route.Method, this.route.Path, this.Refresh)
}

func (this *RefreshHandler) Route() accesshttp.Route { return this.route }

func (this *RefreshHandler) Refresh(c *gin.Context) {
	if err := this.jar.protect(c); err != nil {
		refuse(c, err)
		return
	}
	var body access.RefreshRequest

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
		if byCookie {
			this.jar.write(c, this.jar.credentials.ClearRefresh())
		}
		refuse(c, err)
		return
	}
	this.jar.answer(c, http.StatusOK, response, accesshttp.Rotating(requested, byCookie))
}
