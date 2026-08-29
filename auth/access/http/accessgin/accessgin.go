// Package accessgin mounts an access sign-in surface on Gin.
package accessgin

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/frostgrove/vv/auth/access"
	"github.com/frostgrove/vv/auth/access/http/accesshttp"
)

// Handler is one subject's endpoints, ready to register on a router.
type Handler struct {
	endpoints access.Endpoints
	table     accesshttp.Table
}

// New builds the handler for a mounted subject.
//
// No error and no type parameter: everything it needs is on the mounted
// subject already. The sign-up route is NewRegister, separately.
func New(mounted *access.MountedSubject) *Handler {
	return &Handler{
		endpoints: mounted.Endpoints(),
		table:     accesshttp.For(mounted),
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
		this.refuse(c, err)
		return
	}
	response, err := this.endpoints.SignIn(c.Request.Context(), body, agentOf(c))
	if err != nil {
		this.refuse(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (this *Handler) SignOut(c *gin.Context) {
	response, err := this.endpoints.SignOut(c.Request.Context())
	if err != nil {
		this.refuse(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (this *Handler) SignOutAll(c *gin.Context) {
	response, err := this.endpoints.SignOutAll(c.Request.Context(), c.Query("all") == "true")
	if err != nil {
		this.refuse(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (this *Handler) ChangeSecret(c *gin.Context) {
	var body access.ChangeSecretRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		this.refuse(c, err)
		return
	}
	response, err := this.endpoints.ChangeSecret(c.Request.Context(), body)
	if err != nil {
		this.refuse(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (this *Handler) WhoAmI(c *gin.Context) {
	response, err := this.endpoints.WhoAmI(c.Request.Context())
	if err != nil {
		this.refuse(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (this *Handler) ListSessions(c *gin.Context) {
	response, err := this.endpoints.ListSessions(c.Request.Context())
	if err != nil {
		this.refuse(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (this *Handler) KillSession(c *gin.Context) {
	if err := this.endpoints.KillSession(c.Request.Context(), c.Param("id")); err != nil {
		this.refuse(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// refuse hands the error to the application's error middleware.
func (this *Handler) refuse(c *gin.Context, err error) {
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
}

// NewRegister builds the sign-up route for a mounted subject.
//
// The use case is the one access.Mount answered with, so the payload type is
// carried rather than erased and asserted back.
func NewRegister[P any](mounted *access.MountedSubject, signUp *access.SignUpUseCase[P]) *RegisterHandler[P] {
	return &RegisterHandler[P]{signUp: signUp, route: accesshttp.For(mounted).RegisterRoute()}
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
		_ = c.Error(err)
		c.Abort()
		return
	}
	response, err := this.signUp.Execute(c.Request.Context(), payload, agentOf(c))
	if err != nil {
		_ = c.Error(err)
		c.Abort()
		return
	}
	c.JSON(http.StatusCreated, response)
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
}

// NewRefresh builds the rotation route for a mounted subject.
func NewRefresh(mounted *access.MountedSubject) *RefreshHandler {
	return &RefreshHandler{
		endpoints: mounted.Endpoints(),
		route:     accesshttp.For(mounted).RefreshRoute(),
	}
}

// Mount registers the rotation route.
func (this *RefreshHandler) Mount(r gin.IRouter) {
	r.Handle(this.route.Method, this.route.Path, this.Refresh)
}

// Route answers where it was mounted.
func (this *RefreshHandler) Route() accesshttp.Route { return this.route }

func (this *RefreshHandler) Refresh(c *gin.Context) {
	var body access.RefreshRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		_ = c.Error(err)
		c.Abort()
		return
	}
	response, err := this.endpoints.Refresh(c.Request.Context(), body, agentOf(c))
	if err != nil {
		_ = c.Error(err)
		c.Abort()
		return
	}
	c.JSON(http.StatusOK, response)
}
