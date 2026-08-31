package crudfiber

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestAPathNobodyServesIsRenderedInTheSameEnvelope(t *testing.T) {
	app := fiber.New()
	app.Use(Errors())
	app.Get("/widgets", func(c fiber.Ctx) error { return c.SendString("ok") })

	r := do(t, app, http.MethodGet, "/nowhere", "")

	if r.status != http.StatusNotFound {
		t.Fatalf("a path nobody serves answered %d, want 404: %s", r.status, r.body)
	}
	if !strings.Contains(string(r.body), `"not_found"`) {
		t.Fatalf("the refusal does not carry the code a client branches on: %s", r.body)
	}
	if got := r.header.Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("the refusal is %q, so a client parsing one shape for every failure has nothing to parse", got)
	}

	if r := do(t, app, http.MethodGet, "/widgets", ""); r.status != http.StatusOK {
		t.Fatalf("a mounted route answered %d, so the refusal above proves nothing", r.status)
	}
}
