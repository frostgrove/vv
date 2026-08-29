package crudgin

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// A router's own refusal is the one failure that never reaches the error
// contract by itself, and the one an application is most likely to render as a
// 500 by accident. A 404 rendered as a 500 reads as an outage — the difference
// between "you asked for something that is not there" and "this service is
// broken" — and a client retries the second one.

func TestAPathNobodyServesIsRenderedInTheSameEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/widgets", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	Routing(engine)

	r := do(t, engine, http.MethodGet, "/nowhere", "")

	if r.status != http.StatusNotFound {
		t.Fatalf("a path nobody serves answered %d, want 404: %s", r.status, r.body)
	}
	if !strings.Contains(string(r.body), `"not_found"`) {
		t.Fatalf("the refusal does not carry the code a client branches on: %s", r.body)
	}
	if got := r.header.Get("Content-Type"); !strings.Contains(got, "application/json") {
		t.Fatalf("the refusal is %q, so a client parsing one shape for every failure has nothing to parse", got)
	}

	// The control. A route that is served still answers its own body, so the
	// case above is the router being rendered rather than everything being
	// turned into a 404.
	if r := do(t, engine, http.MethodGet, "/widgets", ""); r.status != http.StatusOK {
		t.Fatalf("a mounted route answered %d, so the refusal above proves nothing", r.status)
	}
}
