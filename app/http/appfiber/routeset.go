package appfiber

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port/porthttp"
)

var ErrRouteSet = errors.New("a route set was given an operation it cannot mount")

type policyKind uint8

const (
	policyUnstated policyKind = iota
	policyRequires
	policyAuthenticated
	policyPublic
)

type Policy struct {
	kind  policyKind
	needs []auth.Permission
	why   string
}

func Requires(permissions ...auth.Permission) Policy {
	return Policy{kind: policyRequires, needs: slices.Clone(permissions)}
}

func Authenticated(why string) Policy {
	return Policy{kind: policyAuthenticated, why: why}
}

func Public(why string) Policy {
	return Policy{kind: policyPublic, why: why}
}

func (this Policy) problem() string {
	switch this.kind {
	case policyUnstated:
		return "was registered without an access policy"
	case policyRequires:
		if len(this.needs) == 0 {
			return "requires nothing; an operation that needs no permission is Public and says why"
		}
		for _, permission := range this.needs {
			if strings.TrimSpace(string(permission)) == "" {
				return "names an empty permission"
			}
		}
		return ""
	case policyAuthenticated:
		if strings.TrimSpace(this.why) == "" {
			return "asks only that the caller be signed in and says nothing about why that is enough"
		}
		return ""
	default:
		if strings.TrimSpace(this.why) == "" {
			return "is open to callers nothing checks and says nothing about why"
		}
		return ""
	}
}

func (this Policy) endpoint(method, path string) authhttp.Endpoint {
	switch this.kind {
	case policyRequires:
		return authhttp.Requires(method, path, this.needs...)
	case policyAuthenticated:
		return authhttp.Authenticated(method, path, this.why)
	default:
		return authhttp.Public(method, path, this.why)
	}
}

func (this Policy) enforcement(renderer porthttp.Renderer) fiber.Handler {
	if this.kind != policyRequires && this.kind != policyAuthenticated {
		return nil
	}
	needs := slices.Clone(this.needs)
	return func(fiberContext fiber.Ctx) error {
		principal, err := auth.Require(fiberContext.Context())
		if err != nil {
			return refuse(fiberContext, renderer, err)
		}
		if !auth.HasAll(principal, needs...) {
			return refuse(fiberContext, renderer, errs.Forbidden().
				Code(errs.CodeForbidden).
				Message("this account does not hold the permission this operation needs").
				Fault())
		}
		return fiberContext.Next()
	}
}

type RouteSetSpec struct {
	Prefix string

	Render []porthttp.RenderOption
}

type RootRouteSetSpec struct {
	App *fiber.App

	Render []porthttp.RenderOption
}

type RouteSet struct {
	prefix   string
	root     *fiber.App
	renderer porthttp.Renderer
	mounted  map[string]struct{}
	entries  []operation
	problems []string
}

type operation struct {
	method  string
	path    string
	policy  Policy
	handler fiber.Handler
}

func NewRouteSet(spec RouteSetSpec) (*RouteSet, error) {
	set := newRouteSet(spec)
	if len(set.problems) > 0 {
		return nil, set.refused()
	}
	return set, nil
}

func MustRouteSet(spec RouteSetSpec) *RouteSet {
	set, err := NewRouteSet(spec)
	if err != nil {
		panic(err)
	}
	return set
}

func Routes(prefix string, options ...porthttp.RenderOption) *RouteSet {
	return newRouteSet(RouteSetSpec{Prefix: prefix, Render: options})
}

func NewRootRouteSet(spec RootRouteSetSpec) (*RouteSet, error) {
	set := newRootRouteSet(spec)
	if len(set.problems) > 0 {
		return nil, set.refused()
	}
	return set, nil
}

func MustRootRouteSet(spec RootRouteSetSpec) *RouteSet {
	set, err := NewRootRouteSet(spec)
	if err != nil {
		panic(err)
	}
	return set
}

func RootRoutes(fiberApp *fiber.App, options ...porthttp.RenderOption) *RouteSet {
	return newRootRouteSet(RootRouteSetSpec{App: fiberApp, Render: options})
}

func newRootRouteSet(spec RootRouteSetSpec) *RouteSet {
	set := &RouteSet{
		root:     spec.App,
		renderer: authhttp.RendererFor(spec.Render),
		mounted:  map[string]struct{}{},
	}
	if spec.App == nil {
		set.problems = append(set.problems,
			"a root route set answers where nothing carries the prefix, so it needs the *fiber.App the process serves with")
	}
	return set
}

func newRouteSet(spec RouteSetSpec) *RouteSet {
	set := &RouteSet{
		prefix:   spec.Prefix,
		renderer: authhttp.RendererFor(spec.Render),
		mounted:  map[string]struct{}{},
	}
	if problem := segmentProblem(spec.Prefix); problem != "" {
		set.problems = append(set.problems, fmt.Sprintf("the prefix %q %s", spec.Prefix, problem))
	}
	return set
}

func (this *RouteSet) GET(path string, policy Policy, handler fiber.Handler) *RouteSet {
	return this.Handle(http.MethodGet, path, policy, handler)
}

func (this *RouteSet) POST(path string, policy Policy, handler fiber.Handler) *RouteSet {
	return this.Handle(http.MethodPost, path, policy, handler)
}

func (this *RouteSet) PUT(path string, policy Policy, handler fiber.Handler) *RouteSet {
	return this.Handle(http.MethodPut, path, policy, handler)
}

func (this *RouteSet) PATCH(path string, policy Policy, handler fiber.Handler) *RouteSet {
	return this.Handle(http.MethodPatch, path, policy, handler)
}

func (this *RouteSet) DELETE(path string, policy Policy, handler fiber.Handler) *RouteSet {
	return this.Handle(http.MethodDelete, path, policy, handler)
}

func (this *RouteSet) Handle(method, path string, policy Policy, handler fiber.Handler) *RouteSet {
	full := this.prefix + path
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		this.problems = append(this.problems, fmt.Sprintf("%q was registered without a method", full))
		return this
	}
	if problem := this.pathProblem(path); problem != "" {
		this.problems = append(this.problems, fmt.Sprintf("%s %q %s", method, path, problem))
		return this
	}
	if handler == nil {
		this.problems = append(this.problems, fmt.Sprintf("%s %s has no handler", method, full))
		return this
	}
	if problem := policy.problem(); problem != "" {
		this.problems = append(this.problems, fmt.Sprintf("%s %s %s", method, full, problem))
		return this
	}

	key := method + " " + full
	if _, duplicate := this.mounted[key]; duplicate {
		this.problems = append(this.problems, fmt.Sprintf("%s %s is registered twice", method, full))
		return this
	}
	this.mounted[key] = struct{}{}

	this.entries = append(this.entries, operation{
		method:  method,
		path:    full,
		policy:  policy,
		handler: handler,
	})
	return this
}

func (this *RouteSet) Route() (Route, error) {
	if len(this.problems) > 0 {
		return nil, this.refused()
	}
	if len(this.entries) == 0 {
		return nil, fmt.Errorf("%w: the set under %q mounts nothing", ErrRouteSet, this.prefix)
	}
	return &registeredRoutes{entries: slices.Clone(this.entries), renderer: this.renderer, root: this.root}, nil
}

func (this *RouteSet) pathProblem(path string) string {
	if this.root == nil {
		return segmentProblem(path)
	}
	switch path {
	case "":
		return "is empty, and a path answering outside every prefix is the whole address"
	case "/":
		return ""
	}
	return segmentProblem(path)
}

func (this *RouteSet) MustRoute() Route {
	route, err := this.Route()
	if err != nil {
		panic(err)
	}
	return route
}

func (this *RouteSet) refused() error {
	problems := slices.Clone(this.problems)
	sort.Strings(problems)
	return fmt.Errorf("%w:\n  - %s", ErrRouteSet, strings.Join(problems, "\n  - "))
}

type registeredRoutes struct {
	entries  []operation
	renderer porthttp.Renderer
	root     *fiber.App
}

func (this *registeredRoutes) Mount(router fiber.Router) {
	if this.root != nil {
		router = this.root
	}
	for _, entry := range this.entries {
		enforcement := entry.policy.enforcement(this.renderer)
		if enforcement == nil {
			router.Add([]string{entry.method}, entry.path, entry.handler)
			continue
		}
		router.Add([]string{entry.method}, entry.path, enforcement, entry.handler)
	}
}

func (this *registeredRoutes) checks() map[string]struct{} {
	checked := make(map[string]struct{}, len(this.entries))
	for _, entry := range this.entries {
		if entry.policy.enforcement(this.renderer) == nil {
			continue
		}
		checked[endpointKey(entry.method, entry.path)] = struct{}{}
	}
	return checked
}

func (this *registeredRoutes) Access() []authhttp.Endpoint {
	endpoints := make([]authhttp.Endpoint, 0, len(this.entries))
	for _, entry := range this.entries {
		endpoint := entry.policy.endpoint(entry.method, entry.path)
		if this.root != nil {
			endpoint = authhttp.AtRoot(endpoint)
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints
}

func segmentProblem(segment string) string {
	switch {
	case segment == "":
		return ""
	case !strings.HasPrefix(segment, "/"):
		return "does not start with /"
	case strings.HasSuffix(segment, "/"):
		return "ends with /"
	}
	return ""
}

func refuse(fiberContext fiber.Ctx, renderer porthttp.Renderer, err error) error {
	status, header, body := renderer.Render(fiberContext.Context(), err)
	for name, values := range header {
		for _, value := range values {
			fiberContext.Response().Header.Add(name, value)
		}
	}
	if body == nil {
		return fiberContext.SendStatus(status)
	}
	return fiberContext.Status(status).JSON(body)
}
