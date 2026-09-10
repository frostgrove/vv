package i18nflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v3"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authhttp"
	"github.com/frostgrove/vv/crud"
	"github.com/frostgrove/vv/crud/http/crudfiber"
	"github.com/frostgrove/vv/crud/http/crudgin"
	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/crud/http/crudnet"
	"github.com/frostgrove/vv/crud/rpc/crudgrpc"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/i18n"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/port/porthttp"
	"github.com/frostgrove/vv/tenancy"
)

const capacityCode errs.Code = "capacity"

type integrationContextKey struct{}

const integrationContextMarker = "i18nflow-request"

type Product struct {
	ID   int64  `db:"id,pk" json:"id"`
	Name string `db:"name" json:"name"`
}

type ProductUpdate struct {
	Name *string `json:"name"`
}

var productMeta = func() *crud.Meta {
	meta, err := crud.NewMeta[Product]("products")
	if err != nil {
		panic(err)
	}
	return meta
}()

type failingService struct {
	fault    error
	faultFor func(context.Context, string) error
	paths    errs.Resolver
}

func (s *failingService) Meta() *crud.Meta { return productMeta }

func (s *failingService) Paths() errs.Resolver {
	if s.paths != nil {
		return s.paths
	}
	return port.Fields{"Name": port.At("name")}
}

func (s *failingService) failure(ctx context.Context, operation string) error {
	if s.faultFor != nil {
		return s.faultFor(ctx, operation)
	}
	return s.fault
}

func (s *failingService) List(ctx context.Context, _ port.ListCommand) (crud.PaginatedResponse[Product], error) {
	return crud.PaginatedResponse[Product]{}, s.failure(ctx, "list")
}

func (s *failingService) Count(ctx context.Context, _ port.CountCommand) (int64, error) {
	return 0, s.failure(ctx, "count")
}

func (s *failingService) Get(ctx context.Context, _ port.GetCommand[int64]) (Product, error) {
	return Product{}, s.failure(ctx, "get")
}

func (s *failingService) Create(ctx context.Context, _ port.CreateCommand[Product]) (Product, error) {
	return Product{}, s.failure(ctx, "create")
}

func (s *failingService) Update(ctx context.Context, _ port.UpdateCommand[int64, ProductUpdate]) (Product, error) {
	return Product{}, s.failure(ctx, "update")
}

func (s *failingService) Replace(ctx context.Context, _ port.ReplaceCommand[int64, Product]) (Product, error) {
	return Product{}, s.failure(ctx, "replace")
}

func (s *failingService) Delete(ctx context.Context, _ port.DeleteCommand[int64]) (int64, error) {
	return 0, s.failure(ctx, "delete")
}

func (s *failingService) DeleteMany(ctx context.Context, _ port.BulkDeleteCommand[int64]) (int64, error) {
	return 0, s.failure(ctx, "bulk_delete")
}

type fixture struct {
	snapshot *i18n.Snapshot
	messages errs.MessageSource
	codes    *errs.Codes
	service  *failingService
}

func approved(t testing.TB, module string, message i18n.MessageSpec) i18n.MessageSpec {
	t.Helper()
	digest, err := i18n.ExpectedSourceDigestForLocale(i18n.GrammarProfile, "en", module, message)
	if err != nil {
		t.Fatal(err)
	}
	for index := range message.Translations {
		message.Translations[index].SourceDigest = digest
		message.Translations[index].ReviewDigest, err = i18n.ExpectedReviewDigest(digest, message.Translations[index].Locale, message.Translations[index].Text)
		if err != nil {
			t.Fatal(err)
		}
	}
	return message
}

func baseSnapshot(t testing.TB) *i18n.Snapshot {
	t.Helper()
	capacity := approved(t, "errors", i18n.MessageSpec{
		ID:          "capacity",
		Revision:    "capacity/v1",
		Description: "A public field conflicts with the current capacity.",
		Source:      ".input {$field :string}\n.input {$count :number select=plural}\n.match $count\none {{{$field} has {$count} conflict}}\n* {{{$field} has {$count} conflicts}}",
		Arguments: []i18n.ArgumentSpec{
			{Name: "field", Type: i18n.TypeText, Required: true},
			{Name: "count", Type: i18n.TypeInteger, Required: true},
		},
		Override: i18n.OverrideAny,
		Public:   true,
		Translations: []i18n.Translation{{
			Locale:           "fr",
			Text:             ".input {$field :string}\n.input {$count :number select=plural}\n.match $count\none {{{$field} a {$count} conflit}}\n* {{{$field} a {$count} conflits}}",
			Review:           i18n.ReviewApproved,
			ContractRevision: "capacity/v1",
		}},
	})
	name := approved(t, "errors", i18n.MessageSpec{
		ID:          "name",
		Revision:    "name/v1",
		Description: "The public name field label.",
		Source:      "name",
		Public:      true,
		Translations: []i18n.Translation{{
			Locale:           "fr",
			Text:             "nom",
			Review:           i18n.ReviewApproved,
			ContractRevision: "name/v1",
		}},
	})
	unauthenticated := approved(t, "errors", i18n.MessageSpec{
		ID:          "unauthenticated",
		Revision:    "unauthenticated/v1",
		Description: "A public authentication refusal.",
		Source:      "authentication is required",
		Public:      true,
		Translations: []i18n.Translation{{
			Locale:           "fr",
			Text:             "authentification requise",
			Review:           i18n.ReviewApproved,
			ContractRevision: "unauthenticated/v1",
		}},
	})
	notFound := approved(t, "errors", i18n.MessageSpec{
		ID:          "not_found",
		Revision:    "not_found/v1",
		Description: "A public route or resource was not found.",
		Source:      "not found",
		Public:      true,
		Translations: []i18n.Translation{{
			Locale:           "fr",
			Text:             "introuvable",
			Review:           i18n.ReviewApproved,
			ContractRevision: "not_found/v1",
		}},
	})
	methodNotAllowed := approved(t, "errors", i18n.MessageSpec{
		ID:          "method_not_allowed",
		Revision:    "method_not_allowed/v1",
		Description: "A public route does not support the requested method.",
		Source:      "this path does not answer that method",
		Public:      true,
		Translations: []i18n.Translation{{
			Locale:           "fr",
			Text:             "méthode non autorisée",
			Review:           i18n.ReviewApproved,
			ContractRevision: "method_not_allowed/v1",
		}},
	})
	snapshot, err := i18n.New(i18n.CatalogSpec{
		Revision:        "integration/v1",
		SourceLocale:    "en",
		DefaultLocale:   "en",
		Supported:       []string{"en", "fr", "fr-CA"},
		Required:        []string{"en", "fr"},
		Parents:         []i18n.LocaleEdge{{Locale: "fr-CA", Parent: "fr"}},
		DefaultTimeZone: "UTC",
		Modules:         []i18n.Module{{Name: "errors", Messages: []i18n.MessageSpec{capacity, name, unauthenticated, notFound, methodNotAllowed}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func messageSource(t testing.TB, snapshot *i18n.Snapshot) errs.MessageSource {
	t.Helper()
	mappings, labels := errorMappings()
	source, err := snapshot.ErrorMessages(i18n.ErrorSpec{
		Mappings:     mappings,
		FieldLabels:  labels,
		Presentation: i18n.PresentationNoIsolation,
	})
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func errorMappings() ([]i18n.ErrorMapping, []i18n.FieldLabel) {
	return []i18n.ErrorMapping{
		{
			Ladder:        "name." + string(capacityCode),
			Key:           i18n.Qualify("errors", "capacity"),
			Params:        []i18n.ErrorParam{{Param: "count", Argument: "count"}},
			FieldArgument: "field",
		},
		{Ladder: string(errs.CodeUnauthenticated), Key: i18n.Qualify("errors", "unauthenticated")},
		{Ladder: string(errs.CodeNotFound), Key: i18n.Qualify("errors", "not_found")},
		{Ladder: string(errs.CodeMethodNotAllowed), Key: i18n.Qualify("errors", "method_not_allowed")},
	}, []i18n.FieldLabel{{Field: "name", Key: i18n.Qualify("errors", "name")}}
}

func viewMessageSource(t testing.TB, snapshot *i18n.Snapshot, localeName string) errs.MessageSource {
	t.Helper()
	mappings, labels := errorMappings()
	plan, err := snapshot.ErrorPlan(i18n.ErrorPlanSpec{Mappings: mappings, FieldLabels: labels})
	if err != nil {
		t.Fatal(err)
	}
	view, err := snapshot.View(i18n.ViewSpec{
		Resolution:   snapshot.Resolve(i18n.Exact(i18n.SourceExplicit, localeName)),
		Presentation: i18n.PresentationNoIsolation,
	})
	if err != nil {
		t.Fatal(err)
	}
	source, err := view.ErrorMessages(plan)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func newFixture(t testing.TB) *fixture {
	t.Helper()
	return fixtureForSnapshot(t, baseSnapshot(t))
}

func fixtureForSnapshot(t testing.TB, snapshot *i18n.Snapshot) *fixture {
	t.Helper()
	catalogue := errs.NewCodes()
	if err := catalogue.Add(capacityCode, errs.KindConflict, "capacity conflict"); err != nil {
		t.Fatal(err)
	}
	return &fixture{
		snapshot: snapshot,
		messages: messageSource(t, snapshot),
		codes:    catalogue,
		service:  &failingService{fault: capacityFault("default", 2)},
	}
}

func capacityFault(operation string, count int64) error {
	return errs.Validation().Op(operation).Code(capacityCode).
		Field("Name").Code(capacityCode).Params(errs.P{"count": count}).
		Origin(errs.OriginState).Fault()
}

func resolvedContext(ctx context.Context, snapshot *i18n.Snapshot, values ...string) context.Context {
	ctx = context.WithValue(ctx, integrationContextKey{}, integrationContextMarker)
	resolution := snapshot.ResolveContext(ctx, i18n.AcceptLanguage(i18n.SourceProtocol, values...))
	if !resolution.Matched() {
		return ctx
	}
	return port.WithLocale(ctx, resolution.Locale)
}

func serviceContextMatches(ctx context.Context) bool {
	return ctx.Value(integrationContextKey{}) == integrationContextMarker &&
		port.LocaleFrom(ctx) == "fr-CA" && len(port.HopsFrom(ctx)) > 0
}

func request(method, target, body string) *http.Request {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, target, reader)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Accept-Language", "en;q=0.1, fr-CA;q=0.9")
	return r
}

type httpResult struct {
	status int
	header http.Header
	body   []byte
}

type httpBinding struct {
	name  string
	serve func(*testing.T, *fixture, string, string, string) httpResult
}

var httpBindings = []httpBinding{
	{
		name: "crudnet",
		serve: func(t *testing.T, f *fixture, method, target, body string) httpResult {
			t.Helper()
			mux := http.NewServeMux()
			crudnet.Serving[Product, int64, ProductUpdate](f.service).
				Rendering(crudhttp.WithMessages(f.messages)).Mount(mux, "/products")
			inner := crudnet.Errors(crudhttp.WithCodes(f.codes))(mux)
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := resolvedContext(r.Context(), f.snapshot, r.Header.Values("Accept-Language")...)
				inner.ServeHTTP(w, r.WithContext(ctx))
			})
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, request(method, target, body))
			return httpResult{status: w.Code, header: w.Header().Clone(), body: bytes.Clone(w.Body.Bytes())}
		},
	},
	{
		name: "crudgin",
		serve: func(t *testing.T, f *fixture, method, target, body string) httpResult {
			t.Helper()
			gin.SetMode(gin.TestMode)
			engine := gin.New()
			engine.Use(crudgin.Errors(crudhttp.WithCodes(f.codes)))
			engine.Use(func(c *gin.Context) {
				ctx := resolvedContext(c.Request.Context(), f.snapshot, c.Request.Header.Values("Accept-Language")...)
				c.Request = c.Request.WithContext(ctx)
				c.Next()
			})
			crudgin.Serving[Product, int64, ProductUpdate](f.service).
				Rendering(crudhttp.WithMessages(f.messages)).Mount(engine, "/products")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, request(method, target, body))
			return httpResult{status: w.Code, header: w.Header().Clone(), body: bytes.Clone(w.Body.Bytes())}
		},
	},
	{
		name: "crudfiber",
		serve: func(t *testing.T, f *fixture, method, target, body string) httpResult {
			t.Helper()
			app := fiber.New(fiber.Config{ErrorHandler: crudfiber.ErrorHandler(crudhttp.WithCodes(f.codes))})
			app.Use(func(c fiber.Ctx) error {
				ctx := resolvedContext(c.Context(), f.snapshot, c.GetReqHeaders()[fiber.HeaderAcceptLanguage]...)
				c.SetContext(ctx)
				return c.Next()
			})
			resource := crudfiber.Serving[Product, int64, ProductUpdate](f.service).
				Rendering(crudhttp.WithMessages(f.messages))
			app.Use("/products", resource.Routes())
			response, err := app.Test(request(method, target, body), fiber.TestConfig{Timeout: 0})
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

func assertHTTPFailure(t testing.TB, result httpResult, wantMessage string) {
	assertHTTPFailureIn(t, result, wantMessage, "fr")
}

func assertHTTPFailureIn(t testing.TB, result httpResult, wantMessage, wantLocale string) {
	t.Helper()
	if result.status != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", result.status, result.body)
	}
	if got := result.header.Get("Content-Language"); got != wantLocale {
		t.Fatalf("Content-Language = %q, want actual template locale %s", got, wantLocale)
	}
	partial, violations := strictHTTPValidation(t, result.body)
	if partial {
		t.Fatalf("ordinary failure unexpectedly claimed a partial result: %s", result.body)
	}
	if len(violations) != 1 || len(violations[0].Field) != 1 || violations[0].Field[0] != "name" ||
		violations[0].Code != string(capacityCode) || violations[0].Message != wantMessage || violations[0].MessageLocale != wantLocale {
		t.Fatalf("violation = %+v, want mapped field, stable code, %q, and locale %q", violations, wantMessage, wantLocale)
	}
}

type strictHTTPViolation struct {
	Field         []any  `json:"field"`
	Code          string `json:"error_code"`
	Message       string `json:"message"`
	MessageLocale string `json:"message_locale"`
}

func strictHTTPValidation(t testing.TB, body []byte) (bool, []strictHTTPViolation) {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("invalid envelope: %v: %s", err, body)
	}
	for key := range top {
		if key != "type" && key != "partial" && key != "errors" {
			t.Fatalf("unknown top-level envelope member %q: %s", key, body)
		}
	}
	var envelopeType string
	if err := json.Unmarshal(top["type"], &envelopeType); err != nil || envelopeType != "error" {
		t.Fatalf("envelope type = %q (%v), want error: %s", envelopeType, err, body)
	}
	partial := false
	if raw, present := top["partial"]; present {
		if err := json.Unmarshal(raw, &partial); err != nil {
			t.Fatalf("invalid partial flag: %v: %s", err, body)
		}
	}
	var groups map[string]json.RawMessage
	if err := json.Unmarshal(top["errors"], &groups); err != nil {
		t.Fatalf("invalid error groups: %v: %s", err, body)
	}
	if len(groups) != 1 || groups["validation"] == nil {
		t.Fatalf("error groups = %v, want exactly validation: %s", mapsKeys(groups), body)
	}
	var rawViolations []json.RawMessage
	if err := json.Unmarshal(groups["validation"], &rawViolations); err != nil {
		t.Fatalf("invalid validation group: %v: %s", err, body)
	}
	violations := make([]strictHTTPViolation, 0, len(rawViolations))
	for index, raw := range rawViolations {
		var members map[string]json.RawMessage
		if err := json.Unmarshal(raw, &members); err != nil {
			t.Fatalf("invalid validation violation %d: %v: %s", index, err, body)
		}
		if len(members) != 4 || members["field"] == nil || members["error_code"] == nil || members["message"] == nil || members["message_locale"] == nil {
			t.Fatalf("validation violation %d has members %v, want exactly field/error_code/message/message_locale: %s", index, mapsKeys(members), body)
		}
		var violation strictHTTPViolation
		if err := json.Unmarshal(raw, &violation); err != nil {
			t.Fatalf("invalid validation violation %d: %v: %s", index, err, body)
		}
		violations = append(violations, violation)
	}
	return partial, violations
}

func mapsKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func TestWeightedNegotiationAndTemplateFallbackKeepSeparateProvenance(t *testing.T) {
	snapshot := baseSnapshot(t)
	resolution := snapshot.Resolve(i18n.AcceptLanguage(i18n.SourceProtocol, "en;q=0.1, fr-CA;q=0.9"))
	if resolution.Locale != "fr-CA" || resolution.Source != i18n.SourceProtocol || resolution.Outcome != i18n.OutcomeSuccess || resolution.Reason != i18n.ReasonExact {
		t.Fatalf("resolution = %+v, want an exact protocol choice of fr-CA", resolution)
	}
	source := messageSource(t, snapshot).(errs.LocalizedMessageSource)
	message, locale, ok := source.MessageWithLocale(context.Background(), errs.Violation{
		Path: errs.Path{errs.Named("name")}, Code: capacityCode, Params: errs.P{"count": int64(2)},
	}, resolution.Locale)
	if !ok || message != "nom a 2 conflits" || locale != "fr" {
		t.Fatalf("render = %q in %q/%v, want fr-CA to use the fr template", message, locale, ok)
	}
}

func TestViewBoundErrorPlanRemainsAuthoritativeAcrossCRUDTransports(t *testing.T) {
	for _, binding := range httpBindings {
		t.Run(binding.name, func(t *testing.T) {
			f := newFixture(t)
			f.messages = viewMessageSource(t, f.snapshot, "en")
			result := binding.serve(t, f, http.MethodGet, "/products", "")
			assertHTTPFailureIn(t, result, "name has 2 conflicts", "en")
		})
	}

	f := newFixture(t)
	f.messages = viewMessageSource(t, f.snapshot, "en")
	result := serveGRPC(t, f).failure(t, "List", `{}`)
	assertGRPCFailureIn(t, result, "name has 2 conflicts", "en")
}

func TestAllHTTPRoutesComposeProcessCodesResourceMessagesAndResolvedLocales(t *testing.T) {
	for _, endpoint := range []struct {
		name      string
		operation string
		count     int64
		method    string
		target    string
		body      string
	}{
		{name: "list", operation: "list", count: 11, method: http.MethodGet, target: "/products"},
		{name: "query", operation: "list", count: 12, method: http.MethodPost, target: "/products/query", body: `{}`},
		{name: "count", operation: "count", count: 13, method: http.MethodGet, target: "/products/count"},
		{name: "count document", operation: "count", count: 14, method: http.MethodPost, target: "/products/count", body: `{}`},
		{name: "get", operation: "get", count: 15, method: http.MethodGet, target: "/products/42"},
		{name: "create", operation: "create", count: 16, method: http.MethodPost, target: "/products", body: `{"name":"bolt"}`},
		{name: "update", operation: "update", count: 17, method: http.MethodPatch, target: "/products/42", body: `{"name":"nut"}`},
		{name: "replace", operation: "replace", count: 18, method: http.MethodPut, target: "/products/42", body: `{"name":"nut"}`},
		{name: "delete", operation: "delete", count: 19, method: http.MethodDelete, target: "/products/42"},
		{name: "bulk delete", operation: "bulk_delete", count: 20, method: http.MethodPost, target: "/products/bulk-delete", body: `{"ids":[1,2]}`},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			var first []byte
			for index, binding := range httpBindings {
				t.Run(binding.name, func(t *testing.T) {
					f := newFixture(t)
					called := ""
					contextObserved := false
					f.service.faultFor = func(ctx context.Context, operation string) error {
						called = operation
						contextObserved = serviceContextMatches(ctx)
						if !contextObserved {
							return errs.Internal().Fault()
						}
						return capacityFault(operation, endpoint.count)
					}
					result := binding.serve(t, f, endpoint.method, endpoint.target, endpoint.body)
					if called != endpoint.operation {
						t.Fatalf("service operation = %q, want %q", called, endpoint.operation)
					}
					if !contextObserved {
						t.Fatal("service did not observe the negotiated locale, request marker, and installed path hops")
					}
					assertHTTPFailure(t, result, "nom a "+strconv.FormatInt(endpoint.count, 10)+" conflits")
					if index == 0 {
						first = bytes.Clone(result.body)
					} else if !bytes.Equal(result.body, first) {
						t.Fatalf("body differs from crudnet: %s != %s", result.body, first)
					}
				})
			}
		})
	}
}

type grpcClient struct {
	connection *grpc.ClientConn
	service    string
}

func localeInterceptor(snapshot *i18n.Snapshot) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx = context.WithValue(ctx, integrationContextKey{}, integrationContextMarker)
		if incoming, ok := metadata.FromIncomingContext(ctx); ok {
			resolution := snapshot.ResolveContext(ctx, i18n.AcceptLanguage(i18n.SourceProtocol, incoming.Get("grpc-accept-language")...))
			if resolution.Matched() {
				ctx = port.WithLocale(ctx, resolution.Locale)
			}
		}
		response, err := handler(ctx, request)
		return response, crudgrpc.ContextError(ctx, err)
	}
}

func serveGRPC(t *testing.T, f *fixture) *grpcClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer(grpc.ChainUnaryInterceptor(
		crudgrpc.Errors(crudgrpc.WithCodes(f.codes)),
		localeInterceptor(f.snapshot),
	))
	resource := crudgrpc.Serving[Product, int64, ProductUpdate](f.service).
		Rendering(crudgrpc.WithMessages(f.messages))
	resource.Register(server, "Product")
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
	return &grpcClient{connection: connection, service: crudgrpc.ServiceName("Product")}
}

func grpcDocument(t testing.TB, raw string) *structpb.Struct {
	t.Helper()
	document := &structpb.Struct{}
	if raw != "" {
		if err := document.UnmarshalJSON([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	return document
}

func (c *grpcClient) failure(t testing.TB, method, raw string) *status.Status {
	t.Helper()
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		"grpc-accept-language", "en;q=0.1, fr-CA;q=0.9",
	))
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

func assertGRPCFailure(t testing.TB, result *status.Status, wantMessage string) {
	assertGRPCFailureIn(t, result, wantMessage, "fr")
}

func assertGRPCFailureIn(t testing.TB, result *status.Status, wantMessage, wantLocale string) {
	t.Helper()
	if result.Code() != codes.AlreadyExists || result.Message() != wantMessage {
		t.Fatalf("status = %s %q, want AlreadyExists %q", result.Code(), result.Message(), wantMessage)
	}
	violation := strictGRPCDetails(t, result, string(capacityCode), false)
	localized := violation.GetLocalizedMessage()
	if violation.GetField() != "name" || violation.GetReason() != string(capacityCode) ||
		violation.GetDescription() != wantMessage || localized.GetLocale() != wantLocale ||
		localized.GetMessage() != wantMessage {
		t.Fatalf("field violation = %+v", violation)
	}
}

func strictGRPCDetails(
	t testing.TB,
	result *status.Status,
	wantReason string,
	wantPartial bool,
) *errdetails.BadRequest_FieldViolation {
	t.Helper()
	details := result.Details()
	if len(details) != 2 {
		t.Fatalf("detail count = %d, want exactly BadRequest and ErrorInfo: %v", len(details), details)
	}
	var badRequest *errdetails.BadRequest
	var info *errdetails.ErrorInfo
	for _, detail := range details {
		switch typed := detail.(type) {
		case *errdetails.BadRequest:
			if badRequest != nil {
				t.Fatal("status has duplicate BadRequest details")
			}
			badRequest = typed
		case *errdetails.ErrorInfo:
			if info != nil {
				t.Fatal("status has duplicate ErrorInfo details")
			}
			info = typed
		default:
			t.Fatalf("unexpected status detail %T", detail)
		}
	}
	if badRequest == nil || info == nil {
		t.Fatalf("details = %T/%T, want BadRequest and ErrorInfo", badRequest, info)
	}
	violations := badRequest.GetFieldViolations()
	if len(violations) != 1 {
		t.Fatalf("field violations = %d, want one", len(violations))
	}
	if info.GetReason() != wantReason || info.GetDomain() != crudgrpc.ErrorDomain {
		t.Fatalf("ErrorInfo = reason %q domain %q, want %q/%q", info.GetReason(), info.GetDomain(), wantReason, crudgrpc.ErrorDomain)
	}
	wantMetadata := map[string]string{}
	if wantPartial {
		wantMetadata[crudgrpc.PartialKey] = "true"
	}
	if !maps.Equal(info.GetMetadata(), wantMetadata) {
		t.Fatalf("ErrorInfo metadata = %v, want %v", info.GetMetadata(), wantMetadata)
	}
	return violations[0]
}

func assertGRPCNoFragments(t testing.TB, result *status.Status, fragments ...string) {
	t.Helper()
	jsonBytes, err := protojson.Marshal(result.Proto())
	if err != nil {
		t.Fatalf("marshalling status as JSON: %v", err)
	}
	binaryBytes, err := proto.Marshal(result.Proto())
	if err != nil {
		t.Fatalf("marshalling status as protobuf: %v", err)
	}
	haystack := bytes.ToLower(append(jsonBytes, binaryBytes...))
	for _, fragment := range fragments {
		if bytes.Contains(haystack, bytes.ToLower([]byte(fragment))) {
			t.Fatalf("serialized status leaks %q: %s", fragment, jsonBytes)
		}
	}
}

func TestAllGRPCMethodsComposeProcessCodesResourceMessagesAndResolvedLocales(t *testing.T) {
	f := newFixture(t)
	counts := map[string]int64{
		"list": 21, "count": 22, "get": 23, "create": 24,
		"update": 25, "replace": 26, "delete": 27, "bulk_delete": 28,
	}
	f.service.faultFor = func(ctx context.Context, operation string) error {
		if !serviceContextMatches(ctx) {
			return errs.Internal().Fault()
		}
		return capacityFault(operation, counts[operation])
	}
	client := serveGRPC(t, f)
	for _, call := range []struct {
		method    string
		operation string
		body      string
	}{
		{method: "List", operation: "list", body: `{}`},
		{method: "Count", operation: "count", body: `{}`},
		{method: "Get", operation: "get", body: `{"id":"42"}`},
		{method: "Create", operation: "create", body: `{"name":"bolt"}`},
		{method: "Update", operation: "update", body: `{"id":"42","patch":{"name":"nut"}}`},
		{method: "Replace", operation: "replace", body: `{"id":"42","entity":{"name":"nut"}}`},
		{method: "Delete", operation: "delete", body: `{"id":"42"}`},
		{method: "BulkDelete", operation: "bulk_delete", body: `{"ids":["1","2"]}`},
	} {
		t.Run(call.method, func(t *testing.T) {
			want := "nom a " + strconv.FormatInt(counts[call.operation], 10) + " conflits"
			assertGRPCFailure(t, client.failure(t, call.method, call.body), want)
		})
	}
}

func TestAuthRefusalUsesTheSameResolvedMessageSourceWithoutLeakingItsReason(t *testing.T) {
	f := newFixture(t)
	r := request(http.MethodGet, "/private", "")
	r = r.WithContext(resolvedContext(r.Context(), f.snapshot, r.Header.Values("Accept-Language")...))
	w := httptest.NewRecorder()
	renderer := authhttp.RendererFor([]porthttp.RenderOption{porthttp.WithMessages(f.messages)})
	authhttp.Refuse(w, r, renderer, auth.Unauthenticated("signature and key details stay private"))
	if w.Code != http.StatusUnauthorized || w.Header().Get("Content-Language") != "fr" {
		t.Fatalf("refusal = %d in %q: %s", w.Code, w.Header().Get("Content-Language"), w.Body.Bytes())
	}
	if strings.Contains(w.Body.String(), "signature") {
		t.Fatalf("private refusal detail leaked: %s", w.Body.Bytes())
	}
	var envelope struct {
		Errors struct {
			General []struct {
				Code          string `json:"error_code"`
				Message       string `json:"message"`
				MessageLocale string `json:"message_locale"`
			} `json:"general"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	violations := envelope.Errors.General
	if len(violations) != 1 || violations[0].Code != string(errs.CodeUnauthenticated) || violations[0].Message != "authentification requise" || violations[0].MessageLocale != "fr" {
		t.Fatalf("refusal violations = %+v", violations)
	}
}

func capacityOverride(t testing.TB, base *i18n.Snapshot, replacement string) i18n.Override {
	t.Helper()
	digest, ok := base.SourceDigest(i18n.Qualify("errors", "capacity"))
	if !ok {
		t.Fatal("capacity source digest is missing")
	}
	reviewDigest, err := i18n.ExpectedReviewDigest(digest, "fr", replacement)
	if err != nil {
		t.Fatal(err)
	}
	return i18n.Override{
		Key:              i18n.Qualify("errors", "capacity"),
		Locale:           "fr",
		Text:             replacement,
		ContractRevision: "capacity/v1",
		SourceDigest:     digest,
		ReviewDigest:     reviewDigest,
		Review:           i18n.ReviewApproved,
	}
}

func overlaySnapshot(t testing.TB, base *i18n.Snapshot, spec i18n.OverlaySpec) *i18n.Snapshot {
	t.Helper()
	snapshot, err := base.Overlay(spec)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestApplicationAndTenantLayersRenderThroughRealCRUDTransportBoundaries(t *testing.T) {
	base := baseSnapshot(t)
	baseDigest := base.Digest()
	applicationText := ".input {$field :string}\n.input {$count :number select=plural}\n.match $count\none {{{$field} application {$count}}}\n* {{{$field} application {$count}}}"
	application := overlaySnapshot(t, base, i18n.ApplicationOverlay(
		"application/v1", capacityOverride(t, base, applicationText),
	))
	applicationDigest := application.Digest()
	tenantAText := ".input {$field :string}\n.input {$count :number select=plural}\n.match $count\none {{{$field} tenant A {$count}}}\n* {{{$field} tenant A {$count}}}"
	tenantBText := ".input {$field :string}\n.input {$count :number select=plural}\n.match $count\none {{{$field} tenant B {$count}}}\n* {{{$field} tenant B {$count}}}"
	tenantA := overlaySnapshot(t, application, i18n.TenantOverlay(
		"tenant-a/v1", capacityOverride(t, application, tenantAText),
	))
	tenantB := overlaySnapshot(t, application, i18n.TenantOverlay(
		"tenant-b/v1", capacityOverride(t, application, tenantBText),
	))

	assertHTTPFailure(t, httpBindings[0].serve(t, fixtureForSnapshot(t, application), http.MethodGet, "/products", ""), "nom application 2")
	assertHTTPFailure(t, httpBindings[1].serve(t, fixtureForSnapshot(t, tenantA), http.MethodGet, "/products", ""), "nom tenant A 2")
	assertGRPCFailure(t, serveGRPC(t, fixtureForSnapshot(t, tenantB)).failure(t, "List", `{}`), "nom tenant B 2")

	assertHTTPFailure(t, httpBindings[2].serve(t, fixtureForSnapshot(t, base), http.MethodGet, "/products", ""), "nom a 2 conflits")
	assertHTTPFailure(t, httpBindings[0].serve(t, fixtureForSnapshot(t, application), http.MethodGet, "/products", ""), "nom application 2")
	assertHTTPFailure(t, httpBindings[1].serve(t, fixtureForSnapshot(t, tenantA), http.MethodGet, "/products", ""), "nom tenant A 2")
	if base.Digest() != baseDigest || application.Digest() != applicationDigest || tenantA.Digest() == tenantB.Digest() {
		t.Fatalf("overlay identity changed unexpectedly: base=%q application=%q tenants=%q/%q", base.Digest(), application.Digest(), tenantA.Digest(), tenantB.Digest())
	}
}

type tenantDirectory struct {
	resolutions map[string]tenancy.Resolution
}

func (d tenantDirectory) Resolve(context.Context) (tenancy.Resolution, error) {
	return tenancy.Resolution{}, tenancy.ErrNoScope
}

func (d tenantDirectory) Lookup(_ context.Context, reference tenancy.Reference) (tenancy.Resolution, error) {
	resolution, ok := d.resolutions[reference.Value()]
	if !ok {
		return tenancy.Resolution{}, tenancy.ErrUnmapped
	}
	return resolution, nil
}

type tenantMessageSource struct {
	application errs.LocalizedMessageSource
	private     map[string]errs.LocalizedMessageSource
	mu          sync.Mutex
	calls       map[string]int
}

func (s *tenantMessageSource) source(ctx context.Context) errs.LocalizedMessageSource {
	scope, ok := tenancy.From(ctx)
	if !ok {
		return s.application
	}
	reference := scope.Reference().Value()
	source, ok := s.private[reference]
	if !ok {
		return s.application
	}
	s.mu.Lock()
	s.calls[reference]++
	s.mu.Unlock()
	return source
}

func (s *tenantMessageSource) Message(ctx context.Context, violation errs.Violation, locale string) (string, bool) {
	return s.source(ctx).Message(ctx, violation, locale)
}

func (s *tenantMessageSource) MessageWithLocale(ctx context.Context, violation errs.Violation, locale string) (string, string, bool) {
	return s.source(ctx).MessageWithLocale(ctx, violation, locale)
}

func (s *tenantMessageSource) count(reference string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[reference]
}

func tenantResolution(t testing.TB, raw string, lifecycle tenancy.Lifecycle) tenancy.Resolution {
	t.Helper()
	reference, err := tenancy.ParseReference(raw)
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := tenancy.NewEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	return tenancy.Resolution{Reference: reference, Lifecycle: lifecycle, Epoch: epoch}
}

func tenantScope(ctx context.Context, authority *tenancy.Authority, raw string) (context.Context, error) {
	reference, err := tenancy.ParseReference(raw)
	if err != nil {
		return nil, err
	}
	scope, err := authority.Lookup(ctx, reference, tenancy.ClassRead)
	if err != nil {
		return nil, err
	}
	return authority.With(ctx, scope)
}

func tenantHTTPBoundary(f *fixture, authority *tenancy.Authority) http.Handler {
	mux := http.NewServeMux()
	crudnet.Serving[Product, int64, ProductUpdate](f.service).
		Rendering(crudhttp.WithMessages(f.messages)).Mount(mux, "/products")
	return crudnet.WithErrors(func(w http.ResponseWriter, r *http.Request) error {
		ctx, err := tenantScope(r.Context(), authority, r.Header.Get("X-Tenant"))
		if err != nil {
			return err
		}
		resolution := f.snapshot.ResolveContext(ctx,
			i18n.AcceptLanguage(i18n.SourceProtocol, r.Header.Values("Accept-Language")...))
		if resolution.Matched() {
			ctx = port.WithLocale(ctx, resolution.Locale)
		}
		mux.ServeHTTP(w, r.WithContext(ctx))
		return nil
	}, crudhttp.WithCodes(f.codes), crudhttp.WithMessages(f.messages))
}

func tenantGRPCBoundary(t testing.TB, f *fixture, authority *tenancy.Authority) *grpcClient {
	t.Helper()
	admit := func(ctx context.Context, request any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		incoming, _ := metadata.FromIncomingContext(ctx)
		ctx, err := tenantScope(ctx, authority, firstMetadata(incoming.Get("x-tenant")))
		if err != nil {
			return nil, err
		}
		response, err := handler(ctx, request)
		return response, crudgrpc.ContextError(ctx, err)
	}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer(grpc.ChainUnaryInterceptor(
		crudgrpc.Errors(crudgrpc.WithCodes(f.codes), crudgrpc.WithMessages(f.messages)),
		localeInterceptor(f.snapshot),
		admit,
	))
	resource := crudgrpc.Serving[Product, int64, ProductUpdate](f.service).
		Rendering(crudgrpc.WithMessages(f.messages))
	resource.Register(server, "TenantProduct")
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
	return &grpcClient{connection: connection, service: crudgrpc.ServiceName("TenantProduct")}
}

func firstMetadata(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func TestOneApplicationBoundaryKeepsTenantCatalogsIsolated(t *testing.T) {
	base := baseSnapshot(t)
	application := overlaySnapshot(t, base, i18n.ApplicationOverlay("application/v1", capacityOverride(t, base,
		".input {$field :string}\n.input {$count :number select=plural}\n.match $count\none {{{$field} réservé application {$count} fois}}\n* {{{$field} réservé application {$count} fois}}")))
	overlay := func(revision, text string) *i18n.Snapshot {
		return overlaySnapshot(t, application, i18n.TenantOverlay(revision, capacityOverride(t, application, text)))
	}
	a := overlay("tenant-a/v1", ".input {$field :string}\n.input {$count :number select=plural}\n.match $count\none {{{$field} privé A {$count} fois}}\n* {{{$field} privé A {$count} fois}}")
	b := overlay("tenant-b/v1", ".input {$field :string}\n.input {$count :number select=plural}\n.match $count\none {{{$field} privé B {$count} fois}}\n* {{{$field} privé B {$count} fois}}")
	inactive := overlay("tenant-inactive/v1", ".input {$field :string}\n.input {$count :number select=plural}\n.match $count\none {{{$field} SECRET INACTIVE {$count}}}\n* {{{$field} SECRET INACTIVE {$count}}}")

	resolutions := map[string]tenancy.Resolution{
		"tenant-a":        tenantResolution(t, "tenant-a", tenancy.Active),
		"tenant-b":        tenantResolution(t, "tenant-b", tenancy.Active),
		"tenant-inactive": tenantResolution(t, "tenant-inactive", tenancy.Suspended),
	}
	authority, err := tenancy.New(tenancy.Spec{
		Resolver:  tenantDirectory{resolutions: resolutions},
		Admission: tenancy.AdmitAll(tenancy.Active),
		Origin:    "i18nflow",
	})
	if err != nil {
		t.Fatal(err)
	}
	localized := func(snapshot *i18n.Snapshot) errs.LocalizedMessageSource {
		return messageSource(t, snapshot).(errs.LocalizedMessageSource)
	}
	messages := &tenantMessageSource{
		application: localized(application),
		private: map[string]errs.LocalizedMessageSource{
			"tenant-a": localized(a), "tenant-b": localized(b), "tenant-inactive": localized(inactive),
		},
		calls: map[string]int{},
	}
	var serviceCalls atomic.Int64
	f := fixtureForSnapshot(t, application)
	f.messages = messages
	f.service.faultFor = func(_ context.Context, operation string) error {
		serviceCalls.Add(1)
		return capacityFault(operation, 2)
	}
	httpServer := tenantHTTPBoundary(f, authority)
	grpcServer := tenantGRPCBoundary(t, f, authority)

	httpFailure := func(reference string) httpResult {
		r := request(http.MethodGet, "/products", "")
		r.Header.Set("X-Tenant", reference)
		w := httptest.NewRecorder()
		httpServer.ServeHTTP(w, r)
		return httpResult{status: w.Code, header: w.Header().Clone(), body: bytes.Clone(w.Body.Bytes())}
	}
	grpcCall := func(reference string) (*status.Status, error) {
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
			"grpc-accept-language", "en;q=0.1, fr-CA;q=0.9", "x-tenant", reference,
		))
		err := grpcServer.connection.Invoke(ctx, "/"+grpcServer.service+"/List", &structpb.Struct{}, &structpb.Struct{})
		if err == nil {
			return nil, errors.New("gRPC call unexpectedly succeeded")
		}
		return status.Convert(err), nil
	}
	grpcFailure := func(reference string) *status.Status {
		result, err := grpcCall(reference)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	assertHTTPFailure(t, httpFailure("tenant-a"), "nom privé A 2 fois")
	assertGRPCFailure(t, grpcFailure("tenant-b"), "nom privé B 2 fois")
	acceptedCalls := serviceCalls.Load()
	privateA, privateB := messages.count("tenant-a"), messages.count("tenant-b")
	for _, refusal := range []struct {
		name      string
		reference string
		call      func(string) string
	}{
		{"unknown over HTTP", "unknown-private-reference", func(reference string) string {
			result := httpFailure(reference)
			if result.status != http.StatusForbidden {
				t.Fatalf("unknown tenant answered HTTP %d: %s", result.status, result.body)
			}
			return string(result.body)
		}},
		{"inactive over gRPC", "tenant-inactive", func(reference string) string {
			result := grpcFailure(reference)
			if result.Code() != codes.PermissionDenied {
				t.Fatalf("inactive tenant answered gRPC %s: %s", result.Code(), result.Message())
			}
			raw, marshalErr := protojson.Marshal(result.Proto())
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			return string(raw)
		}},
	} {
		t.Run(refusal.name, func(t *testing.T) {
			body := refusal.call(refusal.reference)
			if strings.Contains(body, refusal.reference) || strings.Contains(body, "SECRET INACTIVE") {
				t.Fatalf("refusal disclosed private tenant data: %s", body)
			}
		})
	}
	if serviceCalls.Load() != acceptedCalls || messages.count("tenant-a") != privateA ||
		messages.count("tenant-b") != privateB || messages.count("tenant-inactive") != 0 {
		t.Fatalf("refused tenants reached service/private sources: service %d->%d, sources A=%d B=%d inactive=%d",
			acceptedCalls, serviceCalls.Load(), messages.count("tenant-a"), messages.count("tenant-b"), messages.count("tenant-inactive"))
	}

	type concurrentResult struct {
		index int
		http  *httpResult
		grpc  *status.Status
		err   error
	}
	results := make(chan concurrentResult, 100)
	var wait sync.WaitGroup
	for index := range 100 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if index%2 == 0 {
				result := httpFailure("tenant-a")
				results <- concurrentResult{index: index, http: &result}
				return
			}
			result, err := grpcCall("tenant-b")
			results <- concurrentResult{index: index, grpc: result, err: err}
		}()
	}
	wait.Wait()
	close(results)
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent call %d: %v", result.index, result.err)
		}
		if result.http != nil {
			assertHTTPFailure(t, *result.http, "nom privé A 2 fois")
			continue
		}
		assertGRPCFailure(t, result.grpc, "nom privé B 2 fois")
	}
}
