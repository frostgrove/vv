package appfiber

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/fx"

	"github.com/frostgrove/vv/app"
	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authfiber"
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port/porthttp"
)

type Route interface {
	Mount(r fiber.Router)

	Access() []authhttp.Endpoint
}

type Operations interface {
	Operations() *RouteSet
}

type Middleware = app.Ordered[fiber.Handler]

const OrderAuth = 100

const (
	routeGroup      = `group:"vv.appfiber.routes"`
	operationGroup  = `group:"vv.appfiber.operations"`
	middlewareGroup = `group:"vv.appfiber.middleware"`
	resolverGroup   = `group:"vv.appfiber.resolvers"`
)

func AsRoute(constructor any) any {
	return fx.Annotate(constructor, fx.As(new(Route)), fx.ResultTags(routeGroup))
}

func AsOperations(constructor any) any {
	return fx.Annotate(constructor, fx.As(new(Operations)), fx.ResultTags(operationGroup))
}

func AsMiddleware(constructor any) any {
	return fx.Annotate(constructor, fx.ResultTags(middlewareGroup))
}

func AsResolver(constructor any) any {
	return fx.Annotate(constructor, fx.As(new(errs.Resolver)), fx.ResultTags(resolverGroup))
}

type Mounted struct {
	fx.In

	Routes      []Route      `group:"vv.appfiber.routes"`
	Operations  []Operations `group:"vv.appfiber.operations"`
	Middlewares []Middleware `group:"vv.appfiber.middleware"`

	Unchecked UncheckedRule `optional:"true"`

	Logger *slog.Logger `optional:"true"`
}

func (this Mounted) contributions() ([]Route, error) {
	routes := slices.Clone(this.Routes)
	for _, contributor := range this.Operations {
		set := contributor.Operations()
		if set == nil {
			return nil, fmt.Errorf("appfiber: the operations of %T came out nil", contributor)
		}
		route, err := set.Route()
		if err != nil {
			return nil, fmt.Errorf("appfiber: the operations of %T were refused: %w", contributor, err)
		}
		routes = append(routes, route)
	}
	return routes, nil
}

func (this Mounted) unchecked() UncheckedRule {
	if this.Unchecked != nil {
		return this.Unchecked
	}
	return NamingUnchecked
}

type Resolvers struct {
	fx.In

	All []errs.Resolver `group:"vv.appfiber.resolvers"`
}

func Guarding(name string, guard *auth.Guard, options ...porthttp.RenderOption) Middleware {
	return Middleware{Name: name, Order: OrderAuth, Handler: authfiber.Middleware(guard, options...)}
}

type Spec struct {
	Prefix string

	Addr string

	Listen fiber.ListenConfig
}

func Serving(spec Spec) fx.Option {
	mounting := spec
	mounting.Addr = ""
	return fx.Options(Mounting(mounting), Listening(spec))
}

func Mounting(spec Spec) fx.Option {
	if spec.Addr != "" {
		return fx.Error(fmt.Errorf(
			"appfiber: Mounting was given the address %q and never listens; Serving is the option that does both",
			spec.Addr))
	}
	return fx.Module("vv.appfiber.mounting",
		fx.Invoke(func(fiberApp *fiber.App, mounted Mounted) error {
			return Mount(fiberApp, mounted, spec.Prefix)
		}),
	)
}

func Listening(spec Spec) fx.Option {
	if spec.Addr == "" {
		return fx.Error(errors.New(
			"appfiber: no address was given to listen on; Mounting is the option that mounts the API without listening"))
	}
	return fx.Module("vv.appfiber.listening",
		fx.Invoke(func(in serving) {
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

func Mount(fiberApp *fiber.App, mounted Mounted, prefix string) error {
	log := logger(mounted.Logger)

	api := fiberApp.Group(prefix)
	for _, middleware := range app.Sorted(mounted.Middlewares) {
		// A contribution that arrived without a handler is a wiring branch that fell through, and
		// skipping it silently is how a named guard stops running while the surface it protected
		// keeps answering. Verify cannot see it: `Use` registrations are not in the route table.
		if middleware.Handler == nil {
			return fmt.Errorf("appfiber: the middleware %q was contributed without a handler",
				middleware.Name)
		}
		api.Use(middleware.Handler)
		log.Info("api middleware mounted",
			slog.String("name", middleware.Name), slog.Int("order", middleware.Order))
	}

	routes, err := mounted.contributions()
	if err != nil {
		return err
	}

	var declared []authhttp.Endpoint
	var unchecked []Unchecked
	for _, route := range routes {
		route.Mount(api)
		declared = append(declared, route.Access()...)
		unchecked = append(unchecked, uncheckedIn(route)...)
	}

	if err := authfiber.Verify(fiberApp, declared, authhttp.UnderPrefix(prefix)); err != nil {
		return err
	}
	if err := mounted.unchecked()(log, unchecked); err != nil {
		return err
	}

	log.Info("api mounted",
		slog.String("prefix", prefix),
		slog.Int("routes", len(routes)),
		slog.Int("declared_endpoints", len(declared)))
	return nil
}

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
