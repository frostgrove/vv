package authfiber_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authfiber"
)

func TestCredentialCardinalityIsSingularForEveryTransportSource(t *testing.T) {
	for _, source := range []struct {
		name    string
		header  string
		options []auth.Option
	}{
		{name: "Authorization", header: auth.HeaderAuthorization},
		{name: "configured header", header: "X-Credential", options: []auth.Option{auth.Header("X-Credential")}},
	} {
		t.Run(source.name, func(t *testing.T) {
			t.Run("one value is authenticated", func(t *testing.T) {
				h, status, calls, rawCount := serveFiberHeaderValues(t, source.header, []string{"Bearer one"}, source.options...)
				if status != http.StatusOK || !h.ran || calls != 1 || rawCount != 1 {
					t.Fatalf("a singular credential answered %d, ran=%v, authentications=%d, raw values=%d", status, h.ran, calls, rawCount)
				}
			})
			t.Run("optional absence is anonymous", func(t *testing.T) {
				options := append(append([]auth.Option(nil), source.options...), auth.Optional())
				h, status, calls, rawCount := serveFiberHeaderValues(t, source.header, nil, options...)
				if status != http.StatusOK || !h.ran || h.found || calls != 0 || rawCount != 0 {
					t.Fatalf("optional absence answered %d, ran=%v, principal=%v, authentications=%d, raw values=%d", status, h.ran, h.found, calls, rawCount)
				}
			})

			for _, duplicate := range []struct {
				name     string
				values   []string
				optional bool
			}{
				{name: "different duplicates", values: []string{"Bearer first", "Bearer second"}},
				{name: "identical duplicates", values: []string{"Bearer same", "Bearer same"}},
				{name: "optional still refuses duplicates", values: []string{"Bearer same", "Bearer same"}, optional: true},
			} {
				t.Run(duplicate.name, func(t *testing.T) {
					options := append([]auth.Option(nil), source.options...)
					if duplicate.optional {
						options = append(options, auth.Optional())
					}
					h, status, calls, rawCount := serveFiberHeaderValues(t, source.header, duplicate.values, options...)
					if status != http.StatusUnauthorized || h.ran || calls != 0 || rawCount != len(duplicate.values) {
						t.Fatalf("duplicate credentials answered %d, ran=%v, authentications=%d, raw values=%d", status, h.ran, calls, rawCount)
					}
				})
			}
		})
	}

	t.Run("disabled header normalization", func(t *testing.T) {
		for _, source := range []struct {
			name      string
			canonical string
			lowercase string
			options   []auth.Option
		}{
			{
				name:      "Authorization",
				canonical: auth.HeaderAuthorization,
				lowercase: "authorization",
			},
			{
				name:      "configured header",
				canonical: "X-Credential",
				lowercase: "x-credential",
				options:   []auth.Option{auth.Header("X-Credential")},
			},
		} {
			t.Run(source.name, func(t *testing.T) {
				t.Run("a lowercase singular value is authenticated", func(t *testing.T) {
					h, status, calls, spellings := serveFiberRawHeaders(
						t,
						[]rawFiberHeader{{name: source.lowercase, value: "Bearer one"}},
						source.options...,
					)
					if status != http.StatusOK || !h.ran || calls != 1 {
						t.Fatalf("a lowercase credential answered %d, ran=%v, authentications=%d", status, h.ran, calls)
					}
					if spellings[source.lowercase] != 1 || spellings[source.canonical] != 0 {
						t.Fatalf("the fixture did not preserve lowercase spelling: %#v", spellings)
					}
				})

				t.Run("mixed-case duplicates are refused", func(t *testing.T) {
					h, status, calls, spellings := serveFiberRawHeaders(
						t,
						[]rawFiberHeader{
							{name: source.canonical, value: "Bearer same"},
							{name: source.lowercase, value: "Bearer same"},
						},
						source.options...,
					)
					if status != http.StatusUnauthorized || h.ran || calls != 0 {
						t.Fatalf("mixed-case duplicates answered %d, ran=%v, authentications=%d", status, h.ran, calls)
					}
					if spellings[source.lowercase] != 1 || spellings[source.canonical] != 1 {
						t.Fatalf("the fixture did not preserve two distinct spellings: %#v", spellings)
					}
				})
			})
		}
	})
}

func serveFiberHeaderValues(
	t *testing.T,
	name string,
	values []string,
	options ...auth.Option,
) (*seen, int, int, int) {
	t.Helper()
	calls := 0
	rawCount := 0
	handler := &seen{}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		for _, value := range values {
			c.Request().Header.Add(name, value)
		}
		rawCount = len(c.Request().Header.PeekAll(name))
		return c.Next()
	})
	app.Use(authfiber.Middleware(auth.NewGuard(counting(&calls), options...)))
	app.Get("/articles", handler.handle)

	request := httptest.NewRequest(http.MethodGet, "/articles", nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("serving the request: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return handler, response.StatusCode, calls, rawCount
}

type rawFiberHeader struct {
	name  string
	value string
}

func serveFiberRawHeaders(
	t *testing.T,
	headers []rawFiberHeader,
	options ...auth.Option,
) (*seen, int, int, map[string]int) {
	t.Helper()
	calls := 0
	spellings := map[string]int{}
	handler := &seen{}
	app := fiber.New(fiber.Config{DisableHeaderNormalizing: true})
	app.Use(func(c fiber.Ctx) error {
		for _, header := range headers {
			c.Request().Header.Add(header.name, header.value)
		}
		for key := range c.Request().Header.All() {
			for _, header := range headers {
				if bytes.Equal(key, []byte(header.name)) {
					spellings[header.name]++
				}
			}
		}
		return c.Next()
	})
	app.Use(authfiber.Middleware(auth.NewGuard(counting(&calls), options...)))
	app.Get("/articles", handler.handle)

	request := httptest.NewRequest(http.MethodGet, "/articles", nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("serving the request: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return handler, response.StatusCode, calls, spellings
}
