package i18nflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/frostgrove/vv/app/http/appfiber"
	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authfiber"
	"github.com/frostgrove/vv/auth/http/authgin"
	"github.com/frostgrove/vv/auth/http/authnet"
	"github.com/frostgrove/vv/auth/rpc/authgrpc"
	"github.com/frostgrove/vv/crud/http/crudfiber"
	"github.com/frostgrove/vv/crud/http/crudgin"
	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/crud/http/crudnet"
	"github.com/frostgrove/vv/crud/rpc/crudgrpc"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/i18n"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/port/porthttp"
)

type manualHTTPBinding struct {
	name  string
	serve func(*testing.T, *fixture, error) httpResult
}

var manualHTTPBindings = []manualHTTPBinding{
	{
		name: "net/http",
		serve: func(t *testing.T, f *fixture, failure error) httpResult {
			t.Helper()
			inner := crudnet.WithErrors(func(http.ResponseWriter, *http.Request) error {
				return failure
			}, crudhttp.WithCodes(f.codes), crudhttp.WithMessages(f.messages), crudhttp.WithResolvers(f.service.Paths()))
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := resolvedContext(r.Context(), f.snapshot, r.Header.Values("Accept-Language")...)
				inner.ServeHTTP(w, r.WithContext(ctx))
			})
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, request(http.MethodGet, "/manual", ""))
			return httpResult{status: w.Code, header: w.Header().Clone(), body: bytes.Clone(w.Body.Bytes())}
		},
	},
	{
		name: "gin",
		serve: func(t *testing.T, f *fixture, failure error) httpResult {
			t.Helper()
			gin.SetMode(gin.TestMode)
			engine := gin.New()
			engine.Use(crudgin.Errors(crudhttp.WithCodes(f.codes), crudhttp.WithMessages(f.messages), crudhttp.WithResolvers(f.service.Paths())))
			engine.Use(func(c *gin.Context) {
				ctx := resolvedContext(c.Request.Context(), f.snapshot, c.Request.Header.Values("Accept-Language")...)
				c.Request = c.Request.WithContext(ctx)
				c.Next()
			})
			engine.GET("/manual", func(c *gin.Context) { _ = c.Error(failure) })
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, request(http.MethodGet, "/manual", ""))
			return httpResult{status: w.Code, header: w.Header().Clone(), body: bytes.Clone(w.Body.Bytes())}
		},
	},
	{
		name: "fiber",
		serve: func(t *testing.T, f *fixture, failure error) httpResult {
			t.Helper()
			app := fiber.New()
			app.Use(crudfiber.Errors(crudhttp.WithCodes(f.codes), crudhttp.WithMessages(f.messages), crudhttp.WithResolvers(f.service.Paths())))
			app.Use(func(c fiber.Ctx) error {
				ctx := resolvedContext(c.Context(), f.snapshot, c.GetReqHeaders()[fiber.HeaderAcceptLanguage]...)
				c.SetContext(ctx)
				return c.Next()
			})
			app.Get("/manual", func(fiber.Ctx) error { return failure })
			response, err := app.Test(request(http.MethodGet, "/manual", ""), fiber.TestConfig{Timeout: 0})
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			raw, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			return httpResult{status: response.StatusCode, header: response.Header.Clone(), body: raw}
		},
	},
}

func TestHandwrittenHTTPEndpointsUseTheSameLocalizedErrorBoundary(t *testing.T) {
	for _, binding := range manualHTTPBindings {
		t.Run(binding.name, func(t *testing.T) {
			result := binding.serve(t, newFixture(t), capacityFault("manual", 2))
			assertHTTPFailure(t, result, "nom a 2 conflits")
		})
	}
}

func TestFormattingFailureFallsBackToSafeHTTPWording(t *testing.T) {
	failure := errs.Validation().Op("manual").Code(capacityCode).
		Field("Name").Code(capacityCode).
		Params(errs.P{"private": "do-not-leak"}).
		Origin(errs.OriginState).Fault()
	for _, binding := range manualHTTPBindings {
		t.Run(binding.name, func(t *testing.T) {
			result := binding.serve(t, newFixture(t), failure)
			if result.status != http.StatusConflict {
				t.Fatalf("status = %d, want 409: %s", result.status, result.body)
			}
			if language := result.header.Get("Content-Language"); language != "" {
				t.Fatalf("fallback claimed template locale %q", language)
			}
			violation := validationViolation(t, result.body)
			if violation.Field != "name" || violation.Code != string(capacityCode) || violation.Message != "capacity conflict" || violation.Locale != "" {
				t.Fatalf("fallback violation = %+v", violation)
			}
			if bytes.Contains(result.body, []byte("do-not-leak")) {
				t.Fatalf("formatting failure leaked private params: %s", result.body)
			}
		})
	}
}

func TestPartialFaultIsExplicitAcrossHTTPAndGRPCBoundaries(t *testing.T) {
	partial := errs.Validation().Op("partial").Code(capacityCode).Partial(true).
		Field("Name").Code(capacityCode).Params(errs.P{"count": int64(2)}).
		Origin(errs.OriginState).Fault()
	for _, binding := range manualHTTPBindings {
		t.Run("http/"+binding.name, func(t *testing.T) {
			result := binding.serve(t, newFixture(t), partial)
			if result.status != http.StatusConflict || result.header.Get("Content-Language") != "fr" {
				t.Fatalf("partial response = %d in %q: %s", result.status, result.header.Get("Content-Language"), result.body)
			}
			isPartial, violations := strictHTTPValidation(t, result.body)
			if !isPartial || len(violations) != 1 || violations[0].Message != "nom a 2 conflits" || violations[0].MessageLocale != "fr" {
				t.Fatalf("partial envelope = %v %+v", isPartial, violations)
			}
		})
	}

	t.Run("grpc", func(t *testing.T) {
		f := newFixture(t)
		f.service.fault = partial
		result := serveGRPC(t, f).failure(t, "List", `{}`)
		if result.Code() != codes.AlreadyExists || result.Message() != "nom a 2 conflits" {
			t.Fatalf("partial status = %s %q", result.Code(), result.Message())
		}
		violation := strictGRPCDetails(t, result, string(capacityCode), true)
		if violation.GetField() != "name" || violation.GetReason() != string(capacityCode) ||
			violation.GetDescription() != "nom a 2 conflits" || violation.GetLocalizedMessage().GetLocale() != "fr" {
			t.Fatalf("partial violation = %+v", violation)
		}
	})
}

type wireViolation struct {
	Field   string
	Code    string
	Message string
	Locale  string
}

func validationViolation(t testing.TB, body []byte) wireViolation {
	t.Helper()
	var envelope struct {
		Errors struct {
			Validation []struct {
				Field   []any  `json:"field"`
				Code    string `json:"error_code"`
				Message string `json:"message"`
				Locale  string `json:"message_locale"`
			} `json:"validation"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("invalid envelope: %v: %s", err, body)
	}
	if len(envelope.Errors.Validation) != 1 {
		t.Fatalf("validation violations = %d, want one: %s", len(envelope.Errors.Validation), body)
	}
	got := envelope.Errors.Validation[0]
	field := ""
	if len(got.Field) == 1 {
		field, _ = got.Field[0].(string)
	}
	return wireViolation{Field: field, Code: got.Code, Message: got.Message, Locale: got.Locale}
}

type routingHTTPBinding struct {
	name                     string
	supportsMethodNotAllowed bool
	serve                    func(*testing.T, *fixture, string, string) httpResult
}

var routingHTTPBindings = []routingHTTPBinding{
	{
		name: "net/http", supportsMethodNotAllowed: false,
		serve: func(t *testing.T, f *fixture, method, target string) httpResult {
			t.Helper()
			mux := http.NewServeMux()
			mux.HandleFunc("GET /manual", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			crudnet.Routing(mux, crudhttp.WithMessages(f.messages))
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := resolvedContext(r.Context(), f.snapshot, r.Header.Values("Accept-Language")...)
				mux.ServeHTTP(w, r.WithContext(ctx))
			})
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, request(method, target, ""))
			return httpResult{status: w.Code, header: w.Header().Clone(), body: bytes.Clone(w.Body.Bytes())}
		},
	},
	{
		name: "gin", supportsMethodNotAllowed: true,
		serve: func(t *testing.T, f *fixture, method, target string) httpResult {
			t.Helper()
			gin.SetMode(gin.TestMode)
			engine := gin.New()
			engine.Use(func(c *gin.Context) {
				ctx := resolvedContext(c.Request.Context(), f.snapshot, c.Request.Header.Values("Accept-Language")...)
				c.Request = c.Request.WithContext(ctx)
				c.Next()
			})
			engine.GET("/manual", func(c *gin.Context) { c.Status(http.StatusNoContent) })
			crudgin.Routing(engine, crudhttp.WithMessages(f.messages))
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, request(method, target, ""))
			return httpResult{status: w.Code, header: w.Header().Clone(), body: bytes.Clone(w.Body.Bytes())}
		},
	},
	{
		name: "fiber", supportsMethodNotAllowed: true,
		serve: func(t *testing.T, f *fixture, method, target string) httpResult {
			t.Helper()
			app := fiber.New(fiber.Config{ErrorHandler: crudfiber.ErrorHandler(crudhttp.WithMessages(f.messages))})
			app.Use(func(c fiber.Ctx) error {
				ctx := resolvedContext(c.Context(), f.snapshot, c.GetReqHeaders()[fiber.HeaderAcceptLanguage]...)
				c.SetContext(ctx)
				return c.Next()
			})
			app.Get("/manual", func(c fiber.Ctx) error { return c.SendStatus(http.StatusNoContent) })
			response, err := app.Test(request(method, target, ""), fiber.TestConfig{Timeout: 0})
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			raw, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			return httpResult{status: response.StatusCode, header: response.Header.Clone(), body: raw}
		},
	},
}

func TestRouterNativeRefusalsUseNegotiatedMessagesWhereTheAdapterSupportsThem(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		target  string
		status  int
		code    errs.Code
		message string
	}{
		{name: "not found", method: http.MethodGet, target: "/missing", status: http.StatusNotFound, code: errs.CodeNotFound, message: "introuvable"},
		{name: "method not allowed", method: http.MethodPost, target: "/manual", status: http.StatusMethodNotAllowed, code: errs.CodeMethodNotAllowed, message: "méthode non autorisée"},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			for _, binding := range routingHTTPBindings {
				if scenario.status == http.StatusMethodNotAllowed && !binding.supportsMethodNotAllowed {
					continue
				}
				t.Run(binding.name, func(t *testing.T) {
					result := binding.serve(t, newFixture(t), scenario.method, scenario.target)
					if result.status != scenario.status || result.header.Get("Content-Language") != "fr" {
						t.Fatalf("refusal = %d in %q, want %d in fr: %s", result.status, result.header.Get("Content-Language"), scenario.status, result.body)
					}
					violation := generalViolation(t, result.body)
					if violation.Code != string(scenario.code) || violation.Message != scenario.message || violation.Locale != "fr" {
						t.Fatalf("refusal violation = %+v", violation)
					}
				})
			}
		})
	}
}

func TestAppFiberRouteSetAccessRefusalUsesTheI18nSnapshot(t *testing.T) {
	f := newFixture(t)
	handlerCalls := 0
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		ctx := resolvedContext(c.Context(), f.snapshot, c.GetReqHeaders()[fiber.HeaderAcceptLanguage]...)
		c.SetContext(ctx)
		return c.Next()
	})
	set := appfiber.Routes("", porthttp.WithMessages(f.messages)).
		GET("/private", appfiber.Authenticated("the operation reads account-owned data"), func(c fiber.Ctx) error {
			handlerCalls++
			return c.SendStatus(http.StatusNoContent)
		})
	route, err := set.Route()
	if err != nil {
		t.Fatal(err)
	}
	route.Mount(app)
	response, err := app.Test(request(http.MethodGet, "/private", ""), fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if handlerCalls != 0 || response.StatusCode != http.StatusUnauthorized || response.Header.Get("Content-Language") != "fr" {
		t.Fatalf("RouteSet refusal = calls %d, status %d, locale %q: %s", handlerCalls, response.StatusCode, response.Header.Get("Content-Language"), body)
	}
	violation := generalViolation(t, body)
	if violation.Code != string(errs.CodeUnauthenticated) || violation.Message != "authentification requise" || violation.Locale != "fr" {
		t.Fatalf("RouteSet violation = %+v", violation)
	}
}

func generalViolation(t testing.TB, body []byte) wireViolation {
	t.Helper()
	var envelope struct {
		Errors struct {
			General []struct {
				Code    string `json:"error_code"`
				Message string `json:"message"`
				Locale  string `json:"message_locale"`
			} `json:"general"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("invalid envelope: %v: %s", err, body)
	}
	if len(envelope.Errors.General) != 1 {
		t.Fatalf("general violations = %d, want one: %s", len(envelope.Errors.General), body)
	}
	got := envelope.Errors.General[0]
	return wireViolation{Code: got.Code, Message: got.Message, Locale: got.Locale}
}

type authHTTPResult struct {
	httpResult
	authenticatorCalls int
	handlerCalls       int
}

type authHTTPBinding struct {
	name  string
	serve func(*testing.T, *fixture, string) authHTTPResult
}

func rejectingGuard(secret string, calls *int) *auth.Guard {
	return auth.NewGuard(auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		(*calls)++
		return nil, auth.Unauthenticated(secret)
	}))
}

var authHTTPBindings = []authHTTPBinding{
	{
		name: "authnet",
		serve: func(t *testing.T, f *fixture, secret string) authHTTPResult {
			t.Helper()
			authenticatorCalls, handlerCalls := 0, 0
			guarded := authnet.Middleware(rejectingGuard(secret, &authenticatorCalls), porthttp.WithMessages(f.messages))(
				http.HandlerFunc(func(http.ResponseWriter, *http.Request) { handlerCalls++ }),
			)
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := resolvedContext(r.Context(), f.snapshot, r.Header.Values("Accept-Language")...)
				guarded.ServeHTTP(w, r.WithContext(ctx))
			})
			r := request(http.MethodGet, "/private", "")
			r.Header.Set("Authorization", "Bearer forged")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			return authHTTPResult{httpResult: httpResult{status: w.Code, header: w.Header().Clone(), body: bytes.Clone(w.Body.Bytes())}, authenticatorCalls: authenticatorCalls, handlerCalls: handlerCalls}
		},
	},
	{
		name: "authgin",
		serve: func(t *testing.T, f *fixture, secret string) authHTTPResult {
			t.Helper()
			authenticatorCalls, handlerCalls := 0, 0
			gin.SetMode(gin.TestMode)
			engine := gin.New()
			engine.Use(func(c *gin.Context) {
				ctx := resolvedContext(c.Request.Context(), f.snapshot, c.Request.Header.Values("Accept-Language")...)
				c.Request = c.Request.WithContext(ctx)
				c.Next()
			})
			engine.Use(authgin.Middleware(rejectingGuard(secret, &authenticatorCalls), porthttp.WithMessages(f.messages)))
			engine.GET("/private", func(c *gin.Context) { handlerCalls++; c.Status(http.StatusNoContent) })
			r := request(http.MethodGet, "/private", "")
			r.Header.Set("Authorization", "Bearer forged")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, r)
			return authHTTPResult{httpResult: httpResult{status: w.Code, header: w.Header().Clone(), body: bytes.Clone(w.Body.Bytes())}, authenticatorCalls: authenticatorCalls, handlerCalls: handlerCalls}
		},
	},
	{
		name: "authfiber",
		serve: func(t *testing.T, f *fixture, secret string) authHTTPResult {
			t.Helper()
			authenticatorCalls, handlerCalls := 0, 0
			app := fiber.New()
			app.Use(func(c fiber.Ctx) error {
				ctx := resolvedContext(c.Context(), f.snapshot, c.GetReqHeaders()[fiber.HeaderAcceptLanguage]...)
				c.SetContext(ctx)
				return c.Next()
			})
			app.Use(authfiber.Middleware(rejectingGuard(secret, &authenticatorCalls), porthttp.WithMessages(f.messages)))
			app.Get("/private", func(c fiber.Ctx) error { handlerCalls++; return c.SendStatus(http.StatusNoContent) })
			r := request(http.MethodGet, "/private", "")
			r.Header.Set("Authorization", "Bearer forged")
			response, err := app.Test(r, fiber.TestConfig{Timeout: 0})
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			raw, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			return authHTTPResult{httpResult: httpResult{status: response.StatusCode, header: response.Header.Clone(), body: raw}, authenticatorCalls: authenticatorCalls, handlerCalls: handlerCalls}
		},
	},
}

func TestAuthGuardMiddlewareLocalizesRefusalsAcrossHTTPAdapters(t *testing.T) {
	const secret = "signature segment and key id must stay private"
	for _, binding := range authHTTPBindings {
		t.Run(binding.name, func(t *testing.T) {
			result := binding.serve(t, newFixture(t), secret)
			if result.authenticatorCalls != 1 || result.handlerCalls != 0 {
				t.Fatalf("authenticator/handler calls = %d/%d, want 1/0", result.authenticatorCalls, result.handlerCalls)
			}
			if result.status != http.StatusUnauthorized || result.header.Get("Content-Language") != "fr" {
				t.Fatalf("refusal = %d in %q: %s", result.status, result.header.Get("Content-Language"), result.body)
			}
			violation := generalViolation(t, result.body)
			if violation.Code != string(errs.CodeUnauthenticated) || violation.Message != "authentification requise" || violation.Locale != "fr" {
				t.Fatalf("refusal violation = %+v", violation)
			}
			if bytes.Contains(result.body, []byte("signature")) || bytes.Contains(result.body, []byte("key id")) {
				t.Fatalf("authentication details leaked: %s", result.body)
			}
		})
	}
}

func serveAuthenticatedGRPC(t *testing.T, f *fixture, guard *auth.Guard) *grpcClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer(grpc.ChainUnaryInterceptor(
		crudgrpc.Errors(crudgrpc.WithCodes(f.codes), crudgrpc.WithMessages(f.messages)),
		localeInterceptor(f.snapshot),
		authgrpc.Unary(guard),
	))
	resource := crudgrpc.Serving[Product, int64, ProductUpdate](f.service).
		Rendering(crudgrpc.WithMessages(f.messages))
	resource.Register(server, "AuthenticatedProduct")
	go func() { _ = server.Serve(listener) }()
	connection, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = connection.Close()
		server.Stop()
		_ = listener.Close()
	})
	return &grpcClient{connection: connection, service: crudgrpc.ServiceName("AuthenticatedProduct")}
}

func (c *grpcClient) failureWithMetadata(t testing.TB, method, raw string, pairs ...string) *status.Status {
	t.Helper()
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(pairs...))
	err := c.connection.Invoke(ctx, "/"+c.service+"/"+method, grpcDocument(t, raw), &structpb.Struct{})
	if err == nil {
		t.Fatalf("%s unexpectedly succeeded", method)
	}
	result, ok := status.FromError(err)
	if !ok {
		t.Fatalf("%s returned a non-status error: %v", method, err)
	}
	return result
}

func TestAuthGRPCUnaryRefusalIsLocalizedAndDoesNotReachTheService(t *testing.T) {
	const secret = "forged signature from private key slot 19"
	f := newFixture(t)
	authenticatorCalls, serviceCalls := 0, 0
	f.service.faultFor = func(context.Context, string) error {
		serviceCalls++
		return nil
	}
	client := serveAuthenticatedGRPC(t, f, rejectingGuard(secret, &authenticatorCalls))
	result := client.failureWithMetadata(t, "List", `{}`,
		"grpc-accept-language", "en;q=0.1, fr-CA;q=0.9",
		"authorization", "Bearer forged",
	)
	if authenticatorCalls != 1 || serviceCalls != 0 {
		t.Fatalf("authenticator/service calls = %d/%d, want 1/0", authenticatorCalls, serviceCalls)
	}
	assertGRPCAuthRefusal(t, result, secret)
}

func TestHandwrittenPostAuthUnaryFailurePreservesPrincipalAndRequestContext(t *testing.T) {
	f := newFixture(t)
	authenticatorCalls, handlerCalls := 0, 0
	authContextObserved, handlerContextObserved := false, false
	guard := auth.NewGuard(auth.AuthenticatorFunc(func(ctx context.Context, credential auth.Credential) (auth.Principal, error) {
		authenticatorCalls++
		authContextObserved = credential.Token == "valid" &&
			ctx.Value(integrationContextKey{}) == integrationContextMarker && port.LocaleFrom(ctx) == "en"
		return auth.Claims{Sub: "user-42"}, nil
	}))
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer(grpc.ChainUnaryInterceptor(
		crudgrpc.Errors(crudgrpc.WithCodes(f.codes), crudgrpc.WithMessages(f.messages), crudgrpc.WithResolvers(f.service.Paths())),
		localeInterceptor(f.snapshot),
		authgrpc.Unary(guard),
	))
	const serviceName = "vv.test.I18nUnaryBoundary"
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: serviceName,
		HandlerType: (*any)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "Execute",
			Handler: func(_ any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
				request := &structpb.Struct{}
				if err := decode(request); err != nil {
					return nil, err
				}
				handler := func(ctx context.Context, _ any) (any, error) {
					handlerCalls++
					principal, found := auth.PrincipalFrom(ctx)
					handlerContextObserved = found && principal.Subject() == "user-42" &&
						ctx.Value(integrationContextKey{}) == integrationContextMarker && port.LocaleFrom(ctx) == "en"
					ctx = port.WithLocale(ctx, "fr")
					return nil, crudgrpc.ContextError(ctx, capacityFault("execute", 2))
				}
				if interceptor == nil {
					response, err := handler(ctx, request)
					return response, crudgrpc.ContextError(ctx, err)
				}
				return interceptor(ctx, request, &grpc.UnaryServerInfo{FullMethod: "/" + serviceName + "/Execute"}, handler)
			},
		}},
		Metadata: serviceName,
	}, nil)
	go func() { _ = server.Serve(listener) }()
	connection, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = connection.Close()
		server.Stop()
		_ = listener.Close()
	})
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		"grpc-accept-language", "en",
		"authorization", "Bearer valid",
	))
	err = connection.Invoke(ctx, "/"+serviceName+"/Execute", &structpb.Struct{}, &structpb.Struct{})
	result := status.Convert(err)
	if err == nil {
		t.Fatal("handwritten unary method unexpectedly succeeded")
	}
	if authenticatorCalls != 1 || handlerCalls != 1 || !authContextObserved || !handlerContextObserved {
		t.Fatalf("auth/handler evidence = calls %d/%d context %v/%v", authenticatorCalls, handlerCalls, authContextObserved, handlerContextObserved)
	}
	assertGRPCFailure(t, result, "nom a 2 conflits")
}

func assertGRPCAuthRefusal(t testing.TB, result *status.Status, secret string) {
	t.Helper()
	if result.Code() != codes.Unauthenticated || result.Message() != "authentification requise" {
		t.Fatalf("refusal = %s %q", result.Code(), result.Message())
	}
	fragments := []string{secret}
	for _, fragment := range strings.Fields(secret) {
		if len(fragment) >= 6 {
			fragments = append(fragments, fragment)
		}
	}
	assertGRPCNoFragments(t, result, fragments...)
	violation := strictGRPCDetails(t, result, string(errs.CodeUnauthenticated), false)
	localized := violation.GetLocalizedMessage()
	if violation.GetField() != "" || violation.GetReason() != string(errs.CodeUnauthenticated) ||
		violation.GetDescription() != "authentification requise" || localized.GetLocale() != "fr" ||
		localized.GetMessage() != "authentification requise" {
		t.Fatalf("refusal violation = %+v", violation)
	}
}

type integrationServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *integrationServerStream) Context() context.Context { return s.ctx }

func localeStreamInterceptor(snapshot *i18n.Snapshot) grpc.StreamServerInterceptor {
	return func(server any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := context.WithValue(stream.Context(), integrationContextKey{}, integrationContextMarker)
		if incoming, ok := metadata.FromIncomingContext(ctx); ok {
			resolution := snapshot.ResolveContext(ctx, i18n.AcceptLanguage(i18n.SourceProtocol, incoming.Get("grpc-accept-language")...))
			if resolution.Matched() {
				ctx = port.WithLocale(ctx, resolution.Locale)
			}
		}
		err := handler(server, &integrationServerStream{ServerStream: stream, ctx: ctx})
		return crudgrpc.ContextError(ctx, err)
	}
}

func streamFailure(t *testing.T, f *fixture, guard *auth.Guard, handler grpc.StreamHandler, pairs ...string) *status.Status {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer(grpc.ChainStreamInterceptor(
		crudgrpc.StreamErrors(crudgrpc.WithCodes(f.codes), crudgrpc.WithMessages(f.messages), crudgrpc.WithResolvers(f.service.Paths())),
		localeStreamInterceptor(f.snapshot),
		authgrpc.Stream(guard),
	))
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: "vv.test.I18nBoundary",
		HandlerType: (*any)(nil),
		Streams: []grpc.StreamDesc{{
			StreamName:    "Watch",
			Handler:       handler,
			ServerStreams: true,
		}},
		Metadata: "vv.test.i18n",
	}, nil)
	go func() { _ = server.Serve(listener) }()
	connection, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = connection.Close()
		server.Stop()
		_ = listener.Close()
	})
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(pairs...))
	stream, err := connection.NewStream(ctx, &grpc.StreamDesc{StreamName: "Watch", ServerStreams: true}, "/vv.test.I18nBoundary/Watch")
	if err != nil {
		return status.Convert(err)
	}
	if err := stream.SendMsg(&structpb.Struct{}); err != nil && !errors.Is(err, io.EOF) {
		return status.Convert(err)
	}
	err = stream.RecvMsg(&structpb.Struct{})
	if err == nil {
		t.Fatal("stream unexpectedly succeeded")
	}
	return status.Convert(err)
}

func TestGRPCStreamBoundaryPreservesAuthenticationLocaleAndSafeRendering(t *testing.T) {
	t.Run("authenticated application failure", func(t *testing.T) {
		f := newFixture(t)
		authenticatorCalls, handlerCalls := 0, 0
		authSawPublicLocale, handlerSawPublicLocale := false, false
		guard := auth.NewGuard(auth.AuthenticatorFunc(func(ctx context.Context, _ auth.Credential) (auth.Principal, error) {
			authenticatorCalls++
			authSawPublicLocale = port.LocaleFrom(ctx) == "en"
			return auth.Claims{Sub: "user-1"}, nil
		}))
		result := streamFailure(t, f, guard, func(_ any, stream grpc.ServerStream) error {
			handlerCalls++
			principal, ok := auth.PrincipalFrom(stream.Context())
			if !ok || principal.Subject() != "user-1" {
				return errs.Internal().Fault()
			}
			handlerSawPublicLocale = port.LocaleFrom(stream.Context()) == "en"
			ctx := port.WithLocale(stream.Context(), "fr")
			return crudgrpc.ContextError(ctx, capacityFault("watch", 2))
		}, "grpc-accept-language", "en", "authorization", "Bearer token")
		if authenticatorCalls != 1 || handlerCalls != 1 || !authSawPublicLocale || !handlerSawPublicLocale {
			t.Fatalf("authenticator/handler evidence = calls %d/%d locale %v/%v", authenticatorCalls, handlerCalls, authSawPublicLocale, handlerSawPublicLocale)
		}
		assertGRPCFailure(t, result, "nom a 2 conflits")
	})

	t.Run("authentication refusal", func(t *testing.T) {
		const secret = "stream token signature is private"
		f := newFixture(t)
		authenticatorCalls, handlerCalls := 0, 0
		result := streamFailure(t, f, rejectingGuard(secret, &authenticatorCalls),
			func(any, grpc.ServerStream) error { handlerCalls++; return nil },
			"grpc-accept-language", "en;q=0.1, fr-CA;q=0.9", "authorization", "Bearer token")
		if authenticatorCalls != 1 || handlerCalls != 0 {
			t.Fatalf("authenticator/handler calls = %d/%d, want 1/0", authenticatorCalls, handlerCalls)
		}
		assertGRPCAuthRefusal(t, result, secret)
	})
}

func TestInternalFailuresAreRedactedAcrossHTTPAndGRPCBoundaries(t *testing.T) {
	secret := errors.New(`postgres password for reporting at 10.0.0.5 in prod`)
	rich := errs.Internal().Op("Save").Entity("Product").Code(errs.CodeInternal).
		Message(secret.Error()).
		Field("Name").Code(errs.CodeInternal).Message(secret.Error()).
		Params(errs.P{"host": "10.0.0.5", "user": "reporting"}).
		Source(errs.Source{Table: "products", Schema: "prod", Constraint: "products_name_key", Columns: []string{"name"}}).
		Detail(errs.Detail{Dialect: "postgres", SQLState: "28P01", Constraint: "products_name_key", Table: "products", Driver: secret}).
		Wrapping(secret).Fault()
	leaks := []string{"postgres", "password", "reporting", "10.0.0.5", "prod", "products_name_key"}
	for _, binding := range manualHTTPBindings {
		t.Run(binding.name, func(t *testing.T) {
			result := binding.serve(t, newFixture(t), rich)
			if result.status != http.StatusInternalServerError || result.header.Get("Content-Language") != "" {
				t.Fatalf("internal response = %d in %q: %s", result.status, result.header.Get("Content-Language"), result.body)
			}
			if string(result.body) != `{"type":"error","errors":{"general":[{"error_code":"internal"}]}}` {
				t.Fatalf("internal body = %s", result.body)
			}
			assertNoFragments(t, string(result.body), leaks)
		})
	}

	t.Run("grpc", func(t *testing.T) {
		f := newFixture(t)
		f.service.fault = rich
		result := serveGRPC(t, f).failure(t, "List", `{}`)
		if result.Code() != codes.Internal || result.Message() != string(errs.CodeInternal) || len(result.Details()) != 0 {
			t.Fatalf("internal status = %s %q with %d details", result.Code(), result.Message(), len(result.Details()))
		}
		assertGRPCNoFragments(t, result, leaks...)
	})
}

func assertNoFragments(t testing.TB, text string, fragments []string) {
	t.Helper()
	for _, fragment := range fragments {
		if strings.Contains(text, fragment) {
			t.Fatalf("response leaks %q: %s", fragment, text)
		}
	}
}

func TestFormattingFailureFallsBackToSafeGRPCWording(t *testing.T) {
	f := newFixture(t)
	f.service.fault = errs.Validation().Op("list").Code(capacityCode).
		Field("Name").Code(capacityCode).
		Params(errs.P{"private": "do-not-leak"}).
		Origin(errs.OriginState).Fault()
	result := serveGRPC(t, f).failure(t, "List", `{}`)
	if result.Code() != codes.AlreadyExists || result.Message() != "capacity conflict" {
		t.Fatalf("fallback = %s %q", result.Code(), result.Message())
	}
	violation := strictGRPCDetails(t, result, string(capacityCode), false)
	if violation.GetField() != "name" || violation.GetReason() != string(capacityCode) ||
		violation.GetDescription() != "capacity conflict" || violation.GetLocalizedMessage() != nil {
		t.Fatalf("fallback violation = %+v", violation)
	}
	assertGRPCNoFragments(t, result, "do-not-leak")
}

func TestTenantOverlayAdmissionRejectsMessagesOutsideTheDeclaredPolicy(t *testing.T) {
	base := baseSnapshot(t)
	key := i18n.Qualify("errors", "unauthenticated")
	digest, ok := base.SourceDigest(key)
	if !ok {
		t.Fatal("unauthenticated source digest is missing")
	}
	_, err := base.Overlay(i18n.TenantOverlay("tenant-forbidden/v1", i18n.Override{
		Key:              key,
		Locale:           "fr",
		Text:             "tenant-controlled authentication text",
		ContractRevision: "unauthenticated/v1",
		SourceDigest:     digest,
		Review:           i18n.ReviewApproved,
	}))
	if err == nil {
		t.Fatal("tenant overlay replaced a message whose policy does not admit tenant overrides")
	}
	if !strings.Contains(err.Error(), "does not allow this layer") {
		t.Fatalf("tenant admission failed for the wrong reason: %v", err)
	}
	source := messageSource(t, base).(errs.LocalizedMessageSource)
	message, locale, found := source.MessageWithLocale(context.Background(), errs.Violation{Code: errs.CodeUnauthenticated}, "fr")
	if !found || message != "authentification requise" || locale != "fr" {
		t.Fatalf("rejected overlay changed the base snapshot: %q in %q/%v", message, locale, found)
	}
}
