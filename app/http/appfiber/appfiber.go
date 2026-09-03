package appfiber

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
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

// Contributions is every route this graph mounts, in one list: the ones
// contributed as a Route and the ones a RouteSet projected into one. Mount reads
// exactly this, and it is exported because a consumer's own test that asks what
// its application declares must read the same list — one that reads `Routes`
// alone sees nothing a registrar contributed and reports a surface with no
// declarations that boot accepted a moment earlier.
func (this Mounted) Contributions() ([]Route, error) { return this.contributions() }

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
	if spec.Listen.EnablePrefork {
		return fx.Error(errors.New(
			"appfiber: prefork re-executes the process, so each child rebuilds the graph this one already built and the parent binds nothing; run replicas instead"))
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
		OnStart: func(ctx context.Context) error {
			return bind(ctx, shutdowner, fiberApp, spec, log)
		},
		OnStop: func(ctx context.Context) error {
			return fiberApp.ShutdownWithContext(ctx)
		},
	})
}

// bind returns once the port is held and Fiber has stopped writing to the
// application, or once the attempt to hold the port failed.
//
// A listener opened in the background makes "the application started" mean "a
// goroutine was scheduled": the address a second replica already holds, the
// privileged port, the typo in the configuration all arrive after fx has told
// every other hook that start-up succeeded, and the process then answers its
// readiness probe while nothing is listening. So the start waits.
//
// What it waits for is the OnListen hook and not ListenerAddrFunc, because
// Fiber fires the latter from createListener and only afterwards runs
// startupProcess, which appends the automatic HEAD route of every GET —
// a write to the route table. Returning on the address would hand the start
// back while that write is still in flight, and every reader of the mounted
// surface after it, authfiber.Verify included, would race the framework's own
// goroutine. The OnListen hook runs after startupProcess and before Serve,
// which is the first moment both halves are true. It must not return an error:
// Fiber panics on one. Whatever the caller put in ListenerAddrFunc still runs,
// and the address it carries is what gets logged, because a listener asked for
// port 0 knows its port and the configuration does not.
func bind(ctx context.Context, shutdowner fx.Shutdowner, fiberApp *fiber.App, spec Spec, log *slog.Logger) error {
	var held net.Addr
	bound := make(chan struct{}, 1)
	stopped := make(chan error, 1)

	config := spec.Listen
	announced := config.ListenerAddrFunc
	config.ListenerAddrFunc = func(addr net.Addr) {
		held = addr
		if announced != nil {
			announced(addr)
		}
	}
	fiberApp.Hooks().OnListen(func(fiber.ListenData) error {
		select {
		case bound <- struct{}{}:
		default:
		}
		return nil
	})

	go func() { stopped <- fiberApp.Listen(spec.Addr, config) }()

	select {
	case <-bound:
		go watchServer(stopped, shutdowner, spec, log)
		log.Info("the api is listening", slog.String("addr", listeningOn(held, spec.Addr)))
		return nil
	case err := <-stopped:
		if err == nil {
			err = errors.New("the listener returned before it served anything")
		}
		return fmt.Errorf("appfiber: listening on %s: %w", spec.Addr, err)
	case <-ctx.Done():
		_ = fiberApp.Shutdown()
		return fmt.Errorf("appfiber: listening on %s: %w", spec.Addr, ctx.Err())
	}
}

func listeningOn(held net.Addr, configured string) string {
	if held != nil {
		return held.String()
	}
	return configured
}

func watchServer(stopped <-chan error, shutdowner fx.Shutdowner, spec Spec, log *slog.Logger) {
	err := <-stopped
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return
	}
	log.Error("the http server stopped",
		slog.String("addr", spec.Addr), slog.String("err", err.Error()))
	_ = shutdowner.Shutdown(fx.ExitCode(1))
}

func logger(l *slog.Logger) *slog.Logger {
	if l != nil {
		return l
	}
	return slog.Default()
}
