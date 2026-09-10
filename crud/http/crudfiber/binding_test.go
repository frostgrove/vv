package crudfiber

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/http/crudhttp"
)

func TestTheFiberErrorHandlerCoversAGeneratedRoute(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mount func(*fiber.App, *fakeRepo)
	}{
		{"registered routes", func(app *fiber.App, fake *fakeRepo) {
			New[Widget, int64, WidgetUpdate](fake).Register(app.Group("/widgets"))
		}},
		{"mounted standalone app", func(app *fiber.App, fake *fakeRepo) {
			app.Use("/widgets", New[Widget, int64, WidgetUpdate](fake).Routes())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFake()
			fake.err = crud.ErrUnavailable
			app := fiber.New(fiber.Config{
				ErrorHandler: ErrorHandler(crudhttp.WithRetryAfter(29)),
			})
			tc.mount(app, fake)

			r := do(t, app, http.MethodGet, "/widgets/42", "")

			if r.status != http.StatusServiceUnavailable {
				t.Fatalf("the process error handler answered %d, want 503: %s", r.status, r.body)
			}
			if got := r.header.Get("Retry-After"); got != "29" {
				t.Fatalf("the generated route bypassed the process error handler: Retry-After = %q, want 29", got)
			}
		})
	}
}

func TestTheFiberErrorHandlerComposesGeneratedResourceRendering(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mount func(*fiber.App, *fakeRepo)
	}{
		{"registered routes", func(app *fiber.App, fake *fakeRepo) {
			New[Widget, int64, WidgetUpdate](fake).Rendering(crudhttp.WithRetryAfter(47)).Register(app.Group("/widgets"))
		}},
		{"mounted standalone app", func(app *fiber.App, fake *fakeRepo) {
			app.Use("/widgets", New[Widget, int64, WidgetUpdate](fake).Rendering(crudhttp.WithRetryAfter(47)).Routes())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFake()
			fake.err = crud.ErrUnavailable
			app := fiber.New(fiber.Config{
				ErrorHandler: ErrorHandler(crudhttp.WithRetryAfter(29)),
			})
			tc.mount(app, fake)

			r := do(t, app, http.MethodGet, "/widgets/42", "")

			if got := r.header.Get("Retry-After"); got != "47" {
				t.Fatalf("resource rendering did not extend the process handler: Retry-After = %q, want 47", got)
			}
		})
	}
}
