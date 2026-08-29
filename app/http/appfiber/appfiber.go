// Package appfiber assembles a Fiber API out of an fx graph.
//
// It is the seam between a bounded context and the HTTP transport, and it exists
// so that neither side has to import the other: a module contributes its routes
// and its middleware to a value group typed here, and the transport walks the
// group. Without it the transport would name every module — and could not be
// reused — or every module would import the transport, and a context would
// depend on how it happens to be exposed today.
//
// The whole of a server's wiring is then two lines:
//
//	fx.Provide(appfiber.AsRoute(users.NewHandler)),
//	appfiber.Serving(appfiber.Spec{Prefix: "/api/v1", Addr: ":8080"}),
//
// # What Mount refuses to start without
//
// Every contributed route declares what reaching it requires, and [Mount]
// compares that declaration against Fiber's own routing table before the server
// can accept anything. A route nobody declared is a start-up failure, and so is
// a declaration whose route no longer exists. See [authhttp.Verify] for why both
// halves matter.
//
// # Why this is a module and not a package of the library
//
// The framework holds no container ([[D-037]]). What it holds here is an adapter
// to one the consumer chose: fx keeps the graph and resolves by type, and
// nothing in `github.com/frostgrove/vv` learns how to find a component. A
// consumer who wires by hand never imports this ([[D-074]]).
package appfiber

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"

	"github.com/frostgrove/vv/app"
	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authfiber"
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port/porthttp"
)

// A Route is one bounded context's endpoints.
type Route interface {
	// Mount registers on the router it is handed. That router already carries
	// the API prefix and every middleware, so an implementation writes relative
	// paths and nothing else.
	Mount(r fiber.Router)

	// Access declares what each of those endpoints requires, relative to the API
	// prefix. [Mount] compares it against what Mount actually registered and
	// fails start-up when the two disagree in either direction.
	//
	// It is on the interface rather than in a group of its own so that a new
	// module cannot be written without confronting it: adding a Mount and
	// forgetting an Access is not something the compiler would otherwise catch.
	Access() []authhttp.Endpoint
}

// A Middleware runs before every route under the API prefix, in the order it
// declared. See [app.Ordered] for why the order is a number and why it is not
// left to registration order.
type Middleware = app.Ordered[fiber.Handler]

// OrderAuth is where a guard goes: before everything that assumes a caller.
//
// It is a number rather than a position in a list so that a contributor can slot
// in between two others without editing either — rate limiting before the guard,
// tenancy after it.
const OrderAuth = 100

const (
	routeGroup      = `group:"vv.appfiber.routes"`
	middlewareGroup = `group:"vv.appfiber.middleware"`
	resolverGroup   = `group:"vv.appfiber.resolvers"`
)

// AsRoute annotates a constructor so its result joins the route group.
//
//	fx.Provide(appfiber.AsRoute(NewHandler))
func AsRoute(constructor any) any {
	return fx.Annotate(constructor, fx.As(new(Route)), fx.ResultTags(routeGroup))
}

// AsMiddleware annotates a constructor returning a [Middleware].
func AsMiddleware(constructor any) any {
	return fx.Annotate(constructor, fx.ResultTags(middlewareGroup))
}

// AsResolver annotates a constructor returning the last path hop for a module's
// hand-written endpoints.
//
// A CRUD resource declares its hop on its own service, with port.WithPaths, and
// needs nothing here. This is for a handler that decodes a body of its own:
// nothing between it and the renderer knows what key the client sent, so the
// module that wrote the struct has to say ([[D-043]]).
func AsResolver(constructor any) any {
	return fx.Annotate(constructor, fx.As(new(errs.Resolver)), fx.ResultTags(resolverGroup))
}

// Mounted is what the transport asks fx for.
type Mounted struct {
	fx.In

	Routes      []Route      `group:"vv.appfiber.routes"`
	Middlewares []Middleware `group:"vv.appfiber.middleware"`
	// Logger is optional: an application that has one in its graph gets the
	// mount log through it, and one that does not still starts.
	Logger *slog.Logger `optional:"true"`
}

// Resolvers is the set of last-hop path translations the renderer chains.
//
// It is a group of its own, and not a field on [Mounted], because it is needed
// earlier: the renderer is built when the *fiber.App is constructed, and the
// routes are mounted afterwards.
//
//	func newApp(resolvers appfiber.Resolvers) *fiber.App {
//		return fiber.New(fiber.Config{
//			ErrorHandler: crudfiber.ErrorHandler(crudhttp.WithResolvers(resolvers.All...)),
//		})
//	}
type Resolvers struct {
	fx.In

	All []errs.Resolver `group:"vv.appfiber.resolvers"`
}

// Guarding is a guard, as an ordered middleware contribution.
//
// It is here rather than written out in each application because the order is
// the part that is easy to get wrong and impossible to see: a guard behind the
// handler it protects authenticates nobody, and every test that mounts one
// module still passes.
func Guarding(name string, guard *auth.Guard, options ...porthttp.RenderOption) Middleware {
	return Middleware{Name: name, Order: OrderAuth, Handler: authfiber.Middleware(guard, options...)}
}

// A Spec is what a server is assembled from.
type Spec struct {
	// Prefix is where every contributed route is mounted, and the surface the
	// access gate covers. Empty mounts at the root and checks everything.
	Prefix string
	// Addr is what the server listens on. Empty does not listen, which is what a
	// test that only wants the routes mounted asks for.
	Addr string
	// Listen is handed to Fiber untouched.
	Listen fiber.ListenConfig
}

// Serving is the whole of a server's wiring: mount, verify, listen.
func Serving(spec Spec) fx.Option {
	return fx.Module("vv.appfiber",
		fx.Invoke(func(fiberApp *fiber.App, mounted Mounted) error {
			return Mount(fiberApp, mounted, spec.Prefix)
		}),
		fx.Invoke(func(in serving) {
			if spec.Addr == "" {
				return
			}
			Listen(in.Lifecycle, in.Shutdowner, in.App, spec, logger(in.Logger))
		}),
	)
}

type serving struct {
	fx.In

	Lifecycle  fx.Lifecycle
	Shutdowner fx.Shutdowner
	App        *fiber.App
	Logger     *slog.Logger `optional:"true"`
}

// Mount installs the API: the middleware every module contributed, in the order
// they declared, and then the routes.
//
// Sorting rather than trusting registration order is the point. An fx value
// group is unordered, and "the guard runs before the handler" decided by which
// provider fx happened to visit first is a security property decided by luck.
//
// The access gate runs last, before the server can accept anything, and the
// error is returned rather than logged: that is the difference between a
// deployment that refuses to start and one that serves an undeclared endpoint
// with a warning nobody reads.
func Mount(fiberApp *fiber.App, mounted Mounted, prefix string) error {
	log := logger(mounted.Logger)

	api := fiberApp.Group(prefix)
	for _, middleware := range app.Sorted(mounted.Middlewares) {
		if middleware.Handler == nil {
			continue
		}
		api.Use(middleware.Handler)
		log.Info("api middleware mounted",
			slog.String("name", middleware.Name), slog.Int("order", middleware.Order))
	}

	var declared []authhttp.Endpoint
	for _, route := range mounted.Routes {
		route.Mount(api)
		declared = append(declared, route.Access()...)
	}

	if err := authfiber.Verify(fiberApp, declared, authhttp.UnderPrefix(prefix)); err != nil {
		return err
	}

	log.Info("api mounted",
		slog.String("prefix", prefix),
		slog.Int("routes", len(mounted.Routes)),
		slog.Int("declared_endpoints", len(declared)))
	return nil
}

// Listen starts the server on the lifecycle and brings the process down if it
// cannot.
//
// The listen runs in a goroutine because Listen blocks, so a failure to bind
// arrives after OnStart has already returned nil. Logging it and carrying on
// leaves a process that is up, answers a health check from whatever supervises
// it, and serves nothing on the port it was asked for. A port already in use is
// the ordinary way that happens.
func Listen(lifecycle fx.Lifecycle, shutdowner fx.Shutdowner, fiberApp *fiber.App, spec Spec, log *slog.Logger) {
	lifecycle.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				err := fiberApp.Listen(spec.Addr, spec.Listen)
				if err == nil || errors.Is(err, http.ErrServerClosed) {
					return
				}
				log.Error("the http server stopped",
					slog.String("addr", spec.Addr), slog.String("err", err.Error()))
				_ = shutdowner.Shutdown(fx.ExitCode(1))
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return fiberApp.ShutdownWithContext(ctx)
		},
	})
}

func logger(l *slog.Logger) *slog.Logger {
	if l != nil {
		return l
	}
	return slog.Default()
}
