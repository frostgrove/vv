package crudgrpc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

type operationContextKey struct{}

type contextMessages struct {
	mu     sync.Mutex
	prefix string
	calls  int
}

func (this *contextMessages) Message(ctx context.Context, _ errs.Violation, locale string) (string, bool) {
	this.mu.Lock()
	this.calls++
	this.mu.Unlock()
	return fmt.Sprintf("%s:%s:%v", this.prefix, locale, ctx.Value(operationContextKey{})), true
}

func (this *contextMessages) count() int {
	this.mu.Lock()
	defer this.mu.Unlock()
	return this.calls
}

func contextFault() error {
	return errs.Validation().Code(errs.CodeCheck).
		Field("name").Code("chosen_context").Fault()
}

func renderedDescription(t *testing.T, err error) string {
	t.Helper()
	violations := fieldViolations(t, statusOf(t, err))
	if len(violations) != 1 {
		t.Fatalf("rendered %d violations, want one", len(violations))
	}
	return violations[0].GetDescription()
}

func TestUnaryErrorsUsesTheContextChosenInsideItsBoundary(t *testing.T) {
	base := port.WithLocale(metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("grpc-accept-language", "header")), "public")

	t.Run("an early refusal keeps the public context", func(t *testing.T) {
		messages := &contextMessages{prefix: "early"}
		_, err := Errors(WithMessages(messages))(base, nil, &grpc.UnaryServerInfo{},
			func(context.Context, any) (any, error) { return nil, contextFault() })
		if got := renderedDescription(t, err); got != "early:public:<nil>" {
			t.Fatalf("the early refusal rendered with %q", got)
		}
	})

	t.Run("an explicit post-auth handoff reaches the outer renderer", func(t *testing.T) {
		messages := &contextMessages{prefix: "late"}
		_, err := Errors(WithMessages(messages))(base, nil, &grpc.UnaryServerInfo{},
			func(ctx context.Context, _ any) (any, error) {
				ctx = context.WithValue(ctx, operationContextKey{}, "post-auth")
				ctx = port.WithLocale(ctx, "fr")
				return nil, ContextError(ctx, contextFault())
			})
		if got := renderedDescription(t, err); got != "late:fr:post-auth" {
			t.Fatalf("the post-auth failure rendered with %q", got)
		}
	})
}

func TestNestedUnaryErrorsRendersOnceWithTheInnermostPolicy(t *testing.T) {
	outerMessages := &contextMessages{prefix: "outer"}
	innerMessages := &contextMessages{prefix: "inner"}
	outer := Errors(WithMessages(outerMessages))
	inner := Errors(WithMessages(innerMessages))
	info := &grpc.UnaryServerInfo{}

	_, err := outer(context.Background(), nil, info, func(ctx context.Context, request any) (any, error) {
		return inner(ctx, request, info, func(ctx context.Context, _ any) (any, error) {
			ctx = port.WithLocale(context.WithValue(ctx, operationContextKey{}, "inside"), "fr")
			return nil, ContextError(ctx, contextFault())
		})
	})

	if got := renderedDescription(t, err); got != "inner:fr:inside" {
		t.Fatalf("the nested interceptors rendered with %q", got)
	}
	if got := innerMessages.count(); got != 1 {
		t.Fatalf("the inner policy rendered %d times, want once", got)
	}
	if got := outerMessages.count(); got != 0 {
		t.Fatalf("the outer policy rendered an already-rendered status %d times", got)
	}
}

type testServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (this *testServerStream) Context() context.Context { return this.ctx }

func TestStreamErrorsUsesTheContextChosenInsideItsBoundary(t *testing.T) {
	base := port.WithLocale(metadata.NewIncomingContext(context.Background(),
		metadata.Pairs("grpc-accept-language", "header")), "public")
	stream := &testServerStream{ctx: base}
	info := &grpc.StreamServerInfo{}

	t.Run("an early refusal keeps the public context", func(t *testing.T) {
		messages := &contextMessages{prefix: "early"}
		err := StreamErrors(WithMessages(messages))(nil, stream, info,
			func(any, grpc.ServerStream) error { return contextFault() })
		if got := renderedDescription(t, err); got != "early:public:<nil>" {
			t.Fatalf("the early refusal rendered with %q", got)
		}
	})

	t.Run("an explicit post-auth stream handoff reaches the outer renderer", func(t *testing.T) {
		messages := &contextMessages{prefix: "late"}
		err := StreamErrors(WithMessages(messages))(nil, stream, info,
			func(_ any, ss grpc.ServerStream) error {
				ctx := context.WithValue(ss.Context(), operationContextKey{}, "post-auth")
				ctx = port.WithLocale(ctx, "fr")
				return ContextError(ctx, contextFault())
			})
		if got := renderedDescription(t, err); got != "late:fr:post-auth" {
			t.Fatalf("the post-auth failure rendered with %q", got)
		}
	})
}

func TestNestedStreamErrorsRendersOnceWithTheInnermostPolicy(t *testing.T) {
	outerMessages := &contextMessages{prefix: "outer"}
	innerMessages := &contextMessages{prefix: "inner"}
	outer := StreamErrors(WithMessages(outerMessages))
	inner := StreamErrors(WithMessages(innerMessages))
	info := &grpc.StreamServerInfo{}
	stream := &testServerStream{ctx: context.Background()}

	err := outer(nil, stream, info, func(srv any, ss grpc.ServerStream) error {
		return inner(srv, ss, info, func(_ any, ss grpc.ServerStream) error {
			ctx := port.WithLocale(context.WithValue(ss.Context(), operationContextKey{}, "inside"), "fr")
			return ContextError(ctx, contextFault())
		})
	})

	if got := renderedDescription(t, err); got != "inner:fr:inside" {
		t.Fatalf("the nested interceptors rendered with %q", got)
	}
	if got := innerMessages.count(); got != 1 {
		t.Fatalf("the inner policy rendered %d times, want once", got)
	}
	if got := outerMessages.count(); got != 0 {
		t.Fatalf("the outer policy rendered an already-rendered status %d times", got)
	}
}

func TestContextErrorIsNilSafeAndDoesNothingOutsideAnErrorsBoundary(t *testing.T) {
	if ContextError(context.Background(), nil) != nil {
		t.Fatal("nil became an error")
	}
	err := contextFault()
	if got := ContextError(port.WithLocale(context.Background(), "fr"), err); !errors.Is(got, err) {
		t.Fatalf("the uninstalled handoff changed %v into %v", err, got)
	}
}

func TestAContextErrorCannotCarryOneRequestsContextIntoAnother(t *testing.T) {
	var carried error
	_, err := Errors()(context.Background(), nil, &grpc.UnaryServerInfo{},
		func(ctx context.Context, _ any) (any, error) {
			ctx = port.WithLocale(context.WithValue(ctx, operationContextKey{}, "first"), "fr")
			carried = ContextError(ctx, contextFault())
			return struct{}{}, nil
		})
	if err != nil || carried == nil {
		t.Fatalf("capturing a control error = %v / %v", err, carried)
	}

	base := port.WithLocale(context.WithValue(context.Background(), operationContextKey{}, "second"), "de")
	_, err = Errors(WithMessages(&contextMessages{prefix: "current"}))(base, nil, &grpc.UnaryServerInfo{},
		func(context.Context, any) (any, error) { return nil, carried })
	if got := renderedDescription(t, err); got != "current:de:second" {
		t.Fatalf("a prior request supplied the rendering context: %q", got)
	}
}

func TestStreamErrorsHonorsADeferredGeneratedContextAndPolicy(t *testing.T) {
	fake := newFake()
	fake.err = installedFault()
	handler := NewFor(Repository[Widget, int64, WidgetUpdate](fake), WidgetMapper{}).
		Rendering(WithMessages(installedMessages(t)))
	stream := &testServerStream{ctx: context.Background()}

	err := StreamErrors(WithCodes(installedCodes(t)))(nil, stream, &grpc.StreamServerInfo{},
		func(_ any, ss grpc.ServerStream) error {
			_, err := handler.Create(ss.Context(), doc(t, `{"label":"bolt","price":250}`))
			return err
		})

	st := statusOf(t, err)
	violations := fieldViolations(t, st)
	if st.Code() != codes.AlreadyExists || len(violations) != 1 || violations[0].GetField() != "label" || violations[0].GetDescription() != "the resource catalogue" {
		t.Fatalf("the deferred generated failure answered %s with %+v", st.Code(), violations)
	}
}
