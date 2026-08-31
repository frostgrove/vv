package crudfiber

import (
	"context"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/errs"
)

func serve(t *testing.T, h fiber.Handler, mw ...fiber.Handler) response {
	t.Helper()
	app := fiber.New()
	for _, m := range mw {
		app.Use(m)
	}
	app.Get("/anything", h)
	return do(t, app, http.MethodGet, "/anything", "")
}

func TestTheMiddlewareRendersAnErrorTheHandlerReturned(t *testing.T) {
	r := serve(t, func(fiber.Ctx) error { return crud.ErrNotFound }, Errors())

	if r.status != http.StatusNotFound {
		t.Fatalf("a returned ErrNotFound answered %d, want 404: %s", r.status, r.body)
	}
	if got := failed(t, r).Code; got != "not_found" {
		t.Fatalf("the envelope names the error %q, want not_found", got)
	}
}

func TestAHandlerThatAlreadyWroteIsLeftAlone(t *testing.T) {
	r := serve(t, func(c fiber.Ctx) error {
		if err := c.Status(fiber.StatusTeapot).JSON(fiber.Map{"mine": true}); err != nil {
			return err
		}
		return crud.ErrNotFound
	}, Errors())

	if r.status != http.StatusTeapot {
		t.Fatalf("a handler that had already answered was overwritten with %d: %s", r.status, r.body)
	}
	if got := string(r.body); got != `{"mine":true}` {
		t.Fatalf("the handler's own body became %s", got)
	}

	silent := serve(t, func(fiber.Ctx) error { return crud.ErrNotFound }, Errors())
	if silent.status != http.StatusNotFound {
		t.Fatalf("a handler that wrote nothing answered %d, want the middleware's 404: %s", silent.status, silent.body)
	}
}

func TestInstallingTheMiddlewareTwiceRendersOnce(t *testing.T) {
	h := func(fiber.Ctx) error { return crud.ErrConflict }

	once := serve(t, h, Errors())
	twice := serve(t, h, Errors(), Errors())

	if twice.status != once.status {
		t.Fatalf("two installs answered %d where one answered %d", twice.status, once.status)
	}

	if len(twice.body) != len(once.body) {
		t.Fatalf("two installs wrote %d bytes where one wrote %d: %s", len(twice.body), len(once.body), twice.body)
	}
	if got := failed(t, twice).Code; got != "conflict" {
		t.Fatalf("the envelope names the error %q, want conflict", got)
	}
}

func TestTheMiddlewareCoversAHandRolledRoute(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler()})
	app.Get("/reports", func(fiber.Ctx) error { return crud.ErrForbidden })
	app.Get("/healthy", func(c fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) })

	r := do(t, app, http.MethodGet, "/reports", "")
	if r.status != http.StatusForbidden {
		t.Fatalf("a hand-rolled route answered %d, want 403: %s", r.status, r.body)
	}
	if got := failed(t, r).Code; got != "forbidden" {
		t.Fatalf("the envelope names the error %q, want forbidden", got)
	}

	if r := do(t, app, http.MethodGet, "/healthy", ""); r.status != http.StatusOK || string(r.body) != `{"ok":true}` {
		t.Fatalf("a successful route answered %d %s", r.status, r.body)
	}
}

type panicky struct{}

func (panicky) Message(context.Context, errs.Violation, string) (string, bool) {
	panic("the catalogue is not loaded")
}

func TestAPanicInTheRendererBecomesASilent500(t *testing.T) {
	h := func(fiber.Ctx) error { return crud.ErrNotFound }

	r := serve(t, h, Errors(crudhttp.WithMessages(panicky{})))

	if r.status != http.StatusInternalServerError {
		t.Fatalf("a panicking renderer answered %d, want 500: %s", r.status, r.body)
	}
	if got := string(r.body); got != `{"type":"error","errors":{"general":[{"error_code":"internal"}]}}` {
		t.Fatalf("the recovered 500 answered %s, want nothing but the status", got)
	}

	if fine := serve(t, h, Errors()); fine.status != http.StatusNotFound {
		t.Fatalf("a working renderer answered %d, want 404: %s", fine.status, fine.body)
	}
}
