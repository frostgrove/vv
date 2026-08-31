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

type Route interface {
	Mount(r fiber.Router)

	Access() []authhttp.Endpoint
}

type Middleware = app.Ordered[fiber.Handler]

const OrderAuth = 100

const (
	routeGroup      = `group:"vv.appfiber.routes"`
	middlewareGroup = `group:"vv.appfiber.middleware"`
	resolverGroup   = `group:"vv.appfiber.resolvers"`
)

func AsRoute(constructor any) any {
	return fx.Annotate(constructor, fx.As(new(Route)), fx.ResultTags(routeGroup))
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
	Middlewares []Middleware `group:"vv.appfiber.middleware"`

	Logger *slog.Logger `optional:"true"`
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
