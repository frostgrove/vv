package i18nflow

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/http/authnet"
	"github.com/frostgrove/vv/crud/http/crudhttp"
	"github.com/frostgrove/vv/crud/http/crudnet"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/i18n"
	"github.com/frostgrove/vv/port"
	"github.com/frostgrove/vv/port/porthttp"
)

type httpOperation struct {
	name      string
	operation string
	method    string
	target    string
	body      string
}

var fullHTTPOperations = []httpOperation{
	{name: "list", operation: "list", method: http.MethodGet, target: "/products"},
	{name: "query", operation: "list", method: http.MethodPost, target: "/products/query", body: `{}`},
	{name: "count", operation: "count", method: http.MethodGet, target: "/products/count"},
	{name: "count document", operation: "count", method: http.MethodPost, target: "/products/count", body: `{}`},
	{name: "get", operation: "get", method: http.MethodGet, target: "/products/42"},
	{name: "create", operation: "create", method: http.MethodPost, target: "/products", body: `{"name":"bolt"}`},
	{name: "update", operation: "update", method: http.MethodPatch, target: "/products/42", body: `{"name":"nut"}`},
	{name: "replace", operation: "replace", method: http.MethodPut, target: "/products/42", body: `{"name":"nut"}`},
	{name: "delete", operation: "delete", method: http.MethodDelete, target: "/products/42"},
	{name: "bulk delete", operation: "bulk_delete", method: http.MethodPost, target: "/products/bulk-delete", body: `{"ids":[1,2]}`},
}

type grpcOperation struct {
	method    string
	operation string
	body      string
}

var fullGRPCOperations = []grpcOperation{
	{method: "List", operation: "list", body: `{}`},
	{method: "Count", operation: "count", body: `{}`},
	{method: "Get", operation: "get", body: `{"id":"42"}`},
	{method: "Create", operation: "create", body: `{"name":"bolt"}`},
	{method: "Update", operation: "update", body: `{"id":"42","patch":{"name":"nut"}}`},
	{method: "Replace", operation: "replace", body: `{"id":"42","entity":{"name":"nut"}}`},
	{method: "Delete", operation: "delete", body: `{"id":"42"}`},
	{method: "BulkDelete", operation: "bulk_delete", body: `{"ids":["1","2"]}`},
}

func nestedViewMessageSource(t testing.TB, snapshot *i18n.Snapshot, localeName string) errs.MessageSource {
	t.Helper()
	plan, err := snapshot.ErrorPlan(i18n.ErrorPlanSpec{
		Mappings: []i18n.ErrorMapping{{
			Ladder:        "payload.label." + string(capacityCode),
			Key:           i18n.Qualify("errors", "capacity"),
			Params:        []i18n.ErrorParam{{Param: "count", Argument: "count"}},
			FieldArgument: "field",
		}},
		FieldLabels: []i18n.FieldLabel{{Field: "label", Key: i18n.Qualify("errors", "name")}},
	})
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

func privateCapacityFault(operation string, count int64, secret string) error {
	return errs.Validation().Op(operation).Entity("Product").Code(capacityCode).
		Field("Name").Code(capacityCode).Message(secret).
		Params(errs.P{"count": count, "diagnostic": secret}).
		Origin(errs.OriginState).
		Source(errs.Source{Schema: "private", Table: "products", Constraint: secret, Columns: []string{"secret_column"}}).
		Detail(errs.Detail{Dialect: "postgres", SQLState: "23505", Constraint: secret, Table: "products", Driver: errors.New(secret)}).
		Wrapping(errors.New(secret)).Fault()
}

func assertNestedHTTPFailure(t testing.TB, result httpResult, wantMessage string, secrets ...string) {
	t.Helper()
	if result.status != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", result.status, result.body)
	}
	if localeName := result.header.Get("Content-Language"); localeName != "fr" {
		t.Fatalf("Content-Language = %q, want fr", localeName)
	}
	partial, violations := strictHTTPValidation(t, result.body)
	if partial || len(violations) != 1 {
		t.Fatalf("nested response = partial %v, violations %+v", partial, violations)
	}
	violation := violations[0]
	wantPath := []any{"payload", "product", "label"}
	if !slicesEqualAny(violation.Field, wantPath) || violation.Code != string(capacityCode) || violation.Message != wantMessage || violation.MessageLocale != "fr" {
		t.Fatalf("nested violation = %+v, want path %v, code %q, message %q", violation, wantPath, capacityCode, wantMessage)
	}
	assertNoFragments(t, string(result.body), append(secrets, "secret_column", "23505"))
}

func slicesEqualAny(left, right []any) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func assertNestedGRPCFailure(t testing.TB, result *status.Status, wantMessage string, secrets ...string) {
	t.Helper()
	if result.Code() != codes.AlreadyExists || result.Message() != wantMessage {
		t.Fatalf("status = %s %q, want AlreadyExists %q", result.Code(), result.Message(), wantMessage)
	}
	violation := strictGRPCDetails(t, result, string(capacityCode), false)
	localized := violation.GetLocalizedMessage()
	if violation.GetField() != "payload.product.label" || violation.GetReason() != string(capacityCode) ||
		violation.GetDescription() != wantMessage || localized.GetLocale() != "fr" || localized.GetMessage() != wantMessage {
		t.Fatalf("nested field violation = %+v", violation)
	}
	assertGRPCNoFragments(t, result, append(secrets, "secret_column", "23505")...)
}

func TestViewErrorPlanPreservesNestedRenamesAcrossEveryCRUDOperation(t *testing.T) {
	const secret = "private constraint product_label_slot_73"
	for operationIndex, endpoint := range fullHTTPOperations {
		t.Run("http/"+endpoint.name, func(t *testing.T) {
			for _, binding := range httpBindings {
				t.Run(binding.name, func(t *testing.T) {
					f := newFixture(t)
					f.messages = nestedViewMessageSource(t, f.snapshot, "fr")
					f.service.paths = port.Fields{"Name": port.At("payload", "product", "label")}
					called := ""
					count := int64(101 + operationIndex)
					f.service.faultFor = func(_ context.Context, operation string) error {
						called = operation
						return privateCapacityFault(operation, count, secret)
					}
					result := binding.serve(t, f, endpoint.method, endpoint.target, endpoint.body)
					if called != endpoint.operation {
						t.Fatalf("service operation = %q, want %q", called, endpoint.operation)
					}
					assertNestedHTTPFailure(t, result, "nom a "+strconv.FormatInt(count, 10)+" conflits", secret)
				})
			}
		})
	}

	for operationIndex, call := range fullGRPCOperations {
		t.Run("grpc/"+call.method, func(t *testing.T) {
			f := newFixture(t)
			f.messages = nestedViewMessageSource(t, f.snapshot, "fr")
			f.service.paths = port.Fields{"Name": port.At("payload", "product", "label")}
			called := ""
			count := int64(201 + operationIndex)
			f.service.faultFor = func(_ context.Context, operation string) error {
				called = operation
				return privateCapacityFault(operation, count, secret)
			}
			result := serveGRPC(t, f).failure(t, call.method, call.body)
			if called != call.operation {
				t.Fatalf("service operation = %q, want %q", called, call.operation)
			}
			assertNestedGRPCFailure(t, result, "nom a "+strconv.FormatInt(count, 10)+" conflits", secret)
		})
	}
}

func TestNetHTTPPostAuthLocaleSplitUsesTheFailureStageContext(t *testing.T) {
	t.Run("early authentication refusal keeps request locale", func(t *testing.T) {
		const secret = "forged signature key slot 91"
		f := newFixture(t)
		authenticatorCalls, handlerCalls := 0, 0
		guarded := authnet.Middleware(rejectingGuard(secret, &authenticatorCalls), porthttp.WithMessages(f.messages))(
			http.HandlerFunc(func(http.ResponseWriter, *http.Request) { handlerCalls++ }),
		)
		boundary := crudnet.Errors(crudhttp.WithCodes(f.codes), crudhttp.WithMessages(f.messages))(guarded)
		r := request(http.MethodGet, "/private", "")
		r.Header.Set("Accept-Language", "en")
		r.Header.Set("Authorization", "Bearer forged")
		r = r.WithContext(resolvedContext(r.Context(), f.snapshot, "en"))
		w := httptest.NewRecorder()
		boundary.ServeHTTP(w, r)
		if authenticatorCalls != 1 || handlerCalls != 0 || w.Code != http.StatusUnauthorized || w.Header().Get("Content-Language") != "en" {
			t.Fatalf("early refusal = auth/handler %d/%d, status %d, locale %q: %s", authenticatorCalls, handlerCalls, w.Code, w.Header().Get("Content-Language"), w.Body.Bytes())
		}
		violation := generalViolation(t, w.Body.Bytes())
		if violation.Code != string(errs.CodeUnauthenticated) || violation.Message != "authentication is required" || violation.Locale != "en" {
			t.Fatalf("early refusal violation = %+v", violation)
		}
		assertNoFragments(t, w.Body.String(), []string{secret, "signature", "slot 91"})
	})

	t.Run("accepted request uses downstream operation locale", func(t *testing.T) {
		f := newFixture(t)
		authenticatorCalls, postAuthCalls, serviceCalls := 0, 0, 0
		authSawOriginal, postAuthSawOriginal, principalObserved, serviceSawOperationLocale := false, false, false, false
		guard := auth.NewGuard(auth.AuthenticatorFunc(func(ctx context.Context, credential auth.Credential) (auth.Principal, error) {
			authenticatorCalls++
			authSawOriginal = credential.Token == "valid" && port.LocaleFrom(ctx) == "en"
			return auth.Claims{Sub: "user-42"}, nil
		}))
		f.service.faultFor = func(ctx context.Context, operation string) error {
			serviceCalls++
			principal, found := auth.PrincipalFrom(ctx)
			serviceSawOperationLocale = operation == "list" && port.LocaleFrom(ctx) == "fr"
			principalObserved = found && principal.Subject() == "user-42"
			return capacityFault(operation, 2)
		}
		mux := http.NewServeMux()
		crudnet.Serving[Product, int64, ProductUpdate](f.service).Mount(mux, "/products")
		postAuth := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			postAuthCalls++
			principal, found := auth.PrincipalFrom(r.Context())
			principalObserved = found && principal.Subject() == "user-42"
			postAuthSawOriginal = port.LocaleFrom(r.Context()) == "en"
			ctx := port.WithLocale(r.Context(), "fr")
			mux.ServeHTTP(w, r.WithContext(ctx))
		})
		guarded := authnet.Middleware(guard, porthttp.WithMessages(f.messages))(postAuth)
		boundary := crudnet.Errors(
			crudhttp.WithCodes(f.codes),
			crudhttp.WithMessages(f.messages),
		)(guarded)
		r := request(http.MethodGet, "/products", "")
		r.Header.Set("Accept-Language", "en")
		r.Header.Set("Authorization", "Bearer valid")
		r = r.WithContext(resolvedContext(r.Context(), f.snapshot, "en"))
		w := httptest.NewRecorder()
		boundary.ServeHTTP(w, r)
		if authenticatorCalls != 1 || postAuthCalls != 1 || serviceCalls != 1 || !authSawOriginal ||
			!postAuthSawOriginal || !serviceSawOperationLocale || !principalObserved {
			t.Fatalf("accepted evidence = calls %d/%d/%d, locale %v/%v/%v, principal %v",
				authenticatorCalls, postAuthCalls, serviceCalls, authSawOriginal, postAuthSawOriginal, serviceSawOperationLocale, principalObserved)
		}
		assertHTTPFailure(t, httpResult{status: w.Code, header: w.Header().Clone(), body: bytes.Clone(w.Body.Bytes())}, "nom a 2 conflits")
	})
}

type capturedSourceContextKey struct{}

type capturedMessageSource struct{}

func (capturedMessageSource) Message(ctx context.Context, violation errs.Violation, localeName string) (string, bool) {
	message, _, ok := capturedMessageSource{}.MessageWithLocale(ctx, violation, localeName)
	return message, ok
}

func (capturedMessageSource) MessageWithLocale(ctx context.Context, violation errs.Violation, localeName string) (string, string, bool) {
	source, ok := ctx.Value(capturedSourceContextKey{}).(errs.LocalizedMessageSource)
	if !ok || source == nil {
		return "", "", false
	}
	return source.MessageWithLocale(ctx, violation, localeName)
}

func twoCapacityFaults(operation string) error {
	return errs.Validation().Op(operation).Code(capacityCode).
		Field("Name").Code(capacityCode).Message("private-old-a").Params(errs.P{"count": int64(1)}).
		Field("Name").Code(capacityCode).Message("private-old-b").Params(errs.P{"count": int64(2)}).
		Origin(errs.OriginState).Fault()
}

func capacityMessages(t testing.TB, snapshot *i18n.Snapshot, localeName string) errs.LocalizedMessageSource {
	t.Helper()
	source := viewMessageSource(t, snapshot, localeName)
	localized, ok := source.(errs.LocalizedMessageSource)
	if !ok {
		t.Fatal("view-bound i18n source lost locale provenance")
	}
	return localized
}

func assertTwoRevisionMessages(t testing.TB, result httpResult, want map[string]bool) {
	t.Helper()
	if result.status != http.StatusConflict || result.header.Get("Content-Language") != "fr" {
		t.Fatalf("revision response = status %d, locale %q: %s", result.status, result.header.Get("Content-Language"), result.body)
	}
	partial, violations := strictHTTPValidation(t, result.body)
	if partial || len(violations) != 2 {
		t.Fatalf("revision response = partial %v, violations %+v", partial, violations)
	}
	got := make(map[string]bool, len(violations))
	for _, violation := range violations {
		if !slicesEqualAny(violation.Field, []any{"name"}) || violation.Code != string(capacityCode) || violation.MessageLocale != "fr" {
			t.Fatalf("revision violation = %+v", violation)
		}
		got[violation.Message] = true
	}
	if !maps.Equal(got, want) {
		t.Fatalf("revision messages = %v, want %v", got, want)
	}
	assertNoFragments(t, string(result.body), []string{"private-old-a", "private-old-b"})
}

func TestRequestKeepsOneViewWhenControllerActivatesMidResponse(t *testing.T) {
	oldSnapshot := baseSnapshot(t)
	newText := ".input {$field :string}\n.input {$count :number select=plural}\n.match $count\none {{NEW {$field} {$count} conflict}}\n* {{NEW {$field} {$count} conflicts}}"
	newSnapshot := overlaySnapshot(t, oldSnapshot, i18n.ApplicationOverlay(
		"integration/v2", capacityOverride(t, oldSnapshot, newText),
	))
	controller, err := i18n.NewController(i18n.ControllerSpec{Initial: oldSnapshot, MaxRetained: 2})
	if err != nil {
		t.Fatal(err)
	}
	f := fixtureForSnapshot(t, oldSnapshot)
	f.messages = capturedMessageSource{}
	sources := map[i18n.SnapshotRef]errs.LocalizedMessageSource{
		oldSnapshot.Reference(): capacityMessages(t, oldSnapshot, "fr"),
		newSnapshot.Reference(): capacityMessages(t, newSnapshot, "fr"),
	}
	activated := make(chan struct{})
	releaseFirst := make(chan struct{})
	var activationErr error
	serviceCalls := 0
	var serviceMu sync.Mutex
	f.service.faultFor = func(_ context.Context, operation string) error {
		serviceMu.Lock()
		serviceCalls++
		call := serviceCalls
		serviceMu.Unlock()
		if call == 1 {
			_, activationErr = controller.Activate(controller.Current(), newSnapshot)
			close(activated)
			<-releaseFirst
		}
		return twoCapacityFaults(operation)
	}
	mux := http.NewServeMux()
	crudnet.Serving[Product, int64, ProductUpdate](f.service).
		Rendering(crudhttp.WithMessages(f.messages)).Mount(mux, "/products")
	inner := crudnet.Errors(crudhttp.WithCodes(f.codes))(mux)
	var capturedMu sync.Mutex
	captured := make([]i18n.SnapshotRef, 0, 2)
	boundary := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		head := controller.Current()
		source := sources[head.Reference()]
		capturedMu.Lock()
		captured = append(captured, head.Reference())
		capturedMu.Unlock()
		ctx := context.WithValue(port.WithLocale(r.Context(), "en"), capturedSourceContextKey{}, source)
		inner.ServeHTTP(w, r.WithContext(ctx))
	})
	serve := func() httpResult {
		w := httptest.NewRecorder()
		boundary.ServeHTTP(w, request(http.MethodGet, "/products", ""))
		return httpResult{status: w.Code, header: w.Header().Clone(), body: bytes.Clone(w.Body.Bytes())}
	}

	firstResult := make(chan httpResult, 1)
	go func() { firstResult <- serve() }()
	<-activated
	if activationErr != nil {
		close(releaseFirst)
		<-firstResult
		t.Fatalf("activation during first request: %v", activationErr)
	}
	if controller.Current().Reference() != newSnapshot.Reference() {
		close(releaseFirst)
		<-firstResult
		t.Fatalf("controller head = %+v, want new snapshot %+v", controller.Current().Reference(), newSnapshot.Reference())
	}
	second := serve()
	close(releaseFirst)
	first := <-firstResult
	assertTwoRevisionMessages(t, first, map[string]bool{
		"nom a 1 conflit":  true,
		"nom a 2 conflits": true,
	})
	assertTwoRevisionMessages(t, second, map[string]bool{
		"NEW nom 1 conflict":  true,
		"NEW nom 2 conflicts": true,
	})
	capturedMu.Lock()
	defer capturedMu.Unlock()
	if len(captured) != 2 || captured[0] != oldSnapshot.Reference() || captured[1] != newSnapshot.Reference() {
		t.Fatalf("request view captures = %+v, want old then new", captured)
	}
}
