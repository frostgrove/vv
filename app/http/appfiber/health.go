package appfiber

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/health"
	"github.com/frostgrove/vv/port/porthttp"
)

const DefaultHealthPath = "/health"

type HealthSpec struct {
	Registry *health.Registry

	Path string

	Operator []auth.Permission

	Render []porthttp.RenderOption
}

func Health(spec HealthSpec) (Route, error) {
	if spec.Registry == nil {
		return nil, errors.New("appfiber: Health needs a health registry to answer from")
	}
	path := spec.Path
	if path == "" {
		path = DefaultHealthPath
	}
	set, err := NewRouteSet(RouteSetSpec{Prefix: path, Render: spec.Render})
	if err != nil {
		return nil, fmt.Errorf("appfiber: the health path %q must start with / and not end with one: %w", path, err)
	}

	pages := &healthPages{registry: spec.Registry}
	set.GET("/live", Public(
		"a liveness probe is the orchestrator asking whether to restart this process, and it has no account"),
		pages.live)
	set.GET("/ready", Public(
		"a readiness probe is the load balancer asking whether to send traffic here, and it has no account"),
		pages.ready)
	if len(spec.Operator) > 0 {
		set.GET("/detail", Requires(spec.Operator...), pages.detail)
	}
	return set.Route()
}

type healthPages struct {
	registry *health.Registry
}

func (this *healthPages) live(fiberContext fiber.Ctx) error {
	return fiberContext.Status(http.StatusOK).JSON(this.registry.Live())
}

func (this *healthPages) ready(fiberContext fiber.Ctx) error {
	report := this.registry.Ready(fiberContext.Context())
	return fiberContext.Status(statusFor(report.Status)).JSON(report)
}

func (this *healthPages) detail(fiberContext fiber.Ctx) error {
	detail := this.registry.Inspect(fiberContext.Context())
	return fiberContext.Status(statusFor(detail.Status)).JSON(detail)
}

// statusFor keeps a degraded replica in rotation. Taking it out is the move
// that ends the incident: every replica shares the dependency that degraded,
// so the answer that looks careful removes the last replica still serving the
// half of the API that works. Only a required dependency closes the door.
func statusFor(status health.Status) int {
	if status == health.StatusDown {
		return http.StatusServiceUnavailable
	}
	return http.StatusOK
}
