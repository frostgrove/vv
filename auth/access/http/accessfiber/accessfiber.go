// Package accessfiber mounts an access sign-in surface on Fiber v3.
package accessfiber

import (
	"github.com/gofiber/fiber/v3"

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
		return err
	}
	response, err := this.endpoints.SignIn(c.Context(), body, agentOf(c))
	if err != nil {
		return err
	}
	return c.JSON(response)
}

func (this *Handler) SignOut(c fiber.Ctx) error {
	response, err := this.endpoints.SignOut(c.Context())
	if err != nil {
		return err
	}
	return c.JSON(response)
}

func (this *Handler) SignOutAll(c fiber.Ctx) error {
	response, err := this.endpoints.SignOutAll(c.Context(), c.Query("all") == "true")
	if err != nil {
		return err
	}
	return c.JSON(response)
}

func (this *Handler) ChangeSecret(c fiber.Ctx) error {
	var body access.ChangeSecretRequest
	if err := c.Bind().Body(&body); err != nil {
		return err
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
}

// NewRegister builds the sign-up route for a mounted subject.
//
// The use case is the one access.Mount answered with, so the payload type is
// carried rather than erased and asserted back.
func NewRegister[P any](mounted *access.MountedSubject, signUp *access.SignUpUseCase[P]) *RegisterHandler[P] {
	return &RegisterHandler[P]{signUp: signUp, route: accesshttp.For(mounted).RegisterRoute()}
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
		return err
	}
	response, err := this.signUp.Execute(c.Context(), payload, agentOf(c))
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(response)
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
func (this *RefreshHandler) Mount(r fiber.Router) {
	r.Add([]string{this.route.Method}, this.route.Path, this.Refresh)
}

// Route answers where it was mounted.
func (this *RefreshHandler) Route() accesshttp.Route { return this.route }

func (this *RefreshHandler) Refresh(c fiber.Ctx) error {
	var body access.RefreshRequest
	if err := c.Bind().Body(&body); err != nil {
		return err
	}
	response, err := this.endpoints.Refresh(c.Context(), body, agentOf(c))
	if err != nil {
		return err
	}
	return c.JSON(response)
}
