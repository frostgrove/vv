package authgrpc_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/rpc/authgrpc"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

// call runs one unary call through the interceptor.
func call(t *testing.T, guard *auth.Guard, ctx context.Context, options ...authgrpc.Option) (*seen, error) {
	t.Helper()
	h := &seen{}
	_, err := authgrpc.Unary(guard, options...)(ctx, nil, info(articleCreate), h.handle)
	return h, err
}

func TestAnAuthenticatedCallReachesTheMethodWithItsPrincipal(t *testing.T) {
	h, err := call(t, auth.NewGuard(accepts()), incoming("authorization", "Bearer t"))

	if err != nil {
		t.Fatalf("an authenticated call was refused: %v", err)
	}
	if !h.ran {
		t.Fatal("the method behind the interceptor never ran")
	}
	if !h.found {
		t.Fatal("the method saw no principal, so no policy downstream would see one either")
	}
	if h.principal.Subject() != "u-1" {
		t.Fatalf("the method saw subject %q, want u-1", h.principal.Subject())
	}
}

// The metadata key is lowercased by the transport, and md.Get lowercases what
// it is asked for — so the Guard's default "Authorization" finds it. If it did
// not, every call would be a 401 and the default would be useless.
func TestTheMetadataKeyIsFoundWhateverItsCase(t *testing.T) {
	for _, key := range []string{"authorization", "Authorization"} {
		t.Run(key, func(t *testing.T) {
			if _, err := call(t, auth.NewGuard(accepts()), incoming(key, "Bearer t")); err != nil {
				t.Fatalf("a credential under %q was not found: %v", key, err)
			}
		})
	}
}

func TestAnUnauthenticatedCallIsRefusedAndTheMethodNeverRuns(t *testing.T) {
	t.Run("no metadata at all", func(t *testing.T) {
		h, err := call(t, auth.NewGuard(accepts()), incoming())
		if !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("a call with no metadata answered %v, want a refusal", err)
		}
		if h.ran {
			t.Fatal("the method ran for a call nobody authenticated")
		}
	})

	t.Run("a credential that does not verify", func(t *testing.T) {
		h, err := call(t, auth.NewGuard(refuses()), incoming("authorization", "Bearer forged"))
		if !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("a forged credential answered %v, want a refusal", err)
		}
		if h.ran {
			t.Fatal("the method ran for a call that failed to authenticate")
		}
	})
}

func TestAKeyProviderOutageRemainsTypedAndTheMethodNeverRuns(t *testing.T) {
	h, err := call(t, auth.NewGuard(unavailable()), incoming("authorization", "Bearer valid-looking"))
	if !errors.Is(err, errKeyProviderUnavailable) {
		t.Fatalf("a key-provider outage became %v", err)
	}
	if errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("a key-provider outage became a credential refusal: %v", err)
	}
	if h.ran {
		t.Fatal("the method ran when verification trust was unavailable")
	}
}

// There is no status table in this package: the refusal is an error, and the
// kind it already carries is what crudgrpc.Errors turns into a status. This is
// the gRPC spelling of the HTTP bindings' "the refusal body is the shared
// envelope".
func TestTheRefusalCarriesUnauthenticatedAndNamesNoReason(t *testing.T) {
	_, err := call(t, auth.NewGuard(refuses()), incoming("authorization", "Bearer forged"))

	if k := port.KindOf(err); k != errs.KindUnauthorized {
		t.Fatalf("the refusal classified as %v, want unauthorized", k)
	}
	if c := status.Code(err); c != codes.Unknown && c != codes.Unauthenticated {
		t.Fatalf("the refusal already carried status code %v; this package writes no status", c)
	}
	if strings.Contains(err.Error(), badToken) {
		t.Fatalf("the refusal says which half of the token was wrong: %s", err.Error())
	}
}

func TestAnOptionalGuardLetsAnAnonymousCallThrough(t *testing.T) {
	t.Run("no credential reaches the method unauthenticated", func(t *testing.T) {
		h, err := call(t, auth.NewGuard(accepts(), auth.Optional()), incoming())
		if err != nil || !h.ran {
			t.Fatalf("an optional guard refused an anonymous call: %v", err)
		}
		if h.found {
			t.Fatal("an optional guard invented a principal for a call that presented none")
		}
	})

	t.Run("a bad credential is still refused", func(t *testing.T) {
		h, err := call(t, auth.NewGuard(refuses(), auth.Optional()), incoming("authorization", "Bearer forged"))
		if err == nil {
			t.Fatal("an optional guard accepted a forged token as anonymous")
		}
		if h.ran {
			t.Fatal("the method ran with a forged token downgraded to anonymous")
		}
	})
}

func TestADoubleInstallAuthenticatesOnce(t *testing.T) {
	n := 0
	guard := auth.NewGuard(counting(&n))
	h := &seen{}
	ctx := incoming("authorization", "Bearer t")

	inner := authgrpc.Unary(guard)
	outer := authgrpc.Unary(guard)
	_, err := outer(ctx, nil, info(articleCreate), func(ctx context.Context, request any) (any, error) {
		return inner(ctx, request, info(articleCreate), h.handle)
	})
	if err != nil {
		t.Fatal(err)
	}

	if !h.found {
		t.Fatal("a chained install lost the principal")
	}
	if n != 1 {
		t.Fatalf("the credential was verified %d times; chaining the interceptor pays twice", n)
	}
}

func TestDifferentGuardsAuthenticateIndependently(t *testing.T) {
	firstCalls, secondCalls := 0, 0
	first := auth.NewGuard(counting(&firstCalls))
	second := auth.NewGuard(counting(&secondCalls))
	h := &seen{}
	ctx := incoming("authorization", "Bearer t")

	inner := authgrpc.Unary(second)
	outer := authgrpc.Unary(first)
	_, err := outer(ctx, nil, info(articleCreate), func(ctx context.Context, request any) (any, error) {
		return inner(ctx, request, info(articleCreate), h.handle)
	})
	if err != nil {
		t.Fatal(err)
	}

	if !h.found {
		t.Fatal("composed guards lost the principal")
	}
	if firstCalls != 1 || secondCalls != 1 {
		t.Fatalf("composed guards authenticated %d and %d times, want once each", firstCalls, secondCalls)
	}
}

// Skip keys on the full method name, which is why crudgrpc gives each resource
// its own service rather than sharing one.
func TestSkipLeavesTheNamedMethodAlone(t *testing.T) {
	const health = "/grpc.health.v1.Health/Check"

	t.Run("the named method runs unauthenticated", func(t *testing.T) {
		h := &seen{}
		_, err := authgrpc.Unary(auth.NewGuard(accepts()), authgrpc.Skip(health))(
			incoming(), nil, info(health), h.handle)
		if err != nil {
			t.Fatalf("a skipped method was refused: %v", err)
		}
		if !h.ran {
			t.Fatal("a skipped method did not run")
		}
	})

	// The control. Without it the test above passes for a Skip that skips
	// everything, which would leave the whole server unauthenticated.
	t.Run("control: every other method is still authenticated", func(t *testing.T) {
		h, err := call(t, auth.NewGuard(accepts()), incoming(), authgrpc.Skip(health))
		if err == nil {
			t.Fatal("Skip left a method it does not name unauthenticated")
		}
		if h.ran {
			t.Fatal("a method Skip does not name ran anyway")
		}
	})

	t.Run("a prefix is not a skip", func(t *testing.T) {
		if _, err := call(t, auth.NewGuard(accepts()), incoming(), authgrpc.Skip("/vv.crud.v1.Article/")); err == nil {
			t.Fatal("a prefix was accepted as a method name, so a method added later would be skipped silently")
		}
	})
}

func TestAStreamIsAuthenticatedWhenItOpens(t *testing.T) {
	guard := auth.NewGuard(accepts())

	t.Run("the handler's stream carries the principal", func(t *testing.T) {
		var found bool
		err := authgrpc.Stream(guard)(nil, &fakeStream{ctx: incoming("authorization", "Bearer t")},
			&grpc.StreamServerInfo{FullMethod: articleCreate},
			func(_ any, ss grpc.ServerStream) error {
				_, found = auth.PrincipalFrom(ss.Context())
				return nil
			})
		if err != nil {
			t.Fatalf("an authenticated stream was refused: %v", err)
		}
		if !found {
			t.Fatal("the stream handed to the handler does not carry the principal")
		}
	})

	t.Run("an unauthenticated stream never opens", func(t *testing.T) {
		ran := false
		err := authgrpc.Stream(guard)(nil, &fakeStream{ctx: incoming()},
			&grpc.StreamServerInfo{FullMethod: articleCreate},
			func(any, grpc.ServerStream) error { ran = true; return nil })
		if !errors.Is(err, auth.ErrUnauthenticated) {
			t.Fatalf("an unauthenticated stream answered %v", err)
		}
		if ran {
			t.Fatal("the stream handler ran for a call nobody authenticated")
		}
	})
}

func TestADoubleStreamInstallAuthenticatesOnce(t *testing.T) {
	calls := 0
	guard := auth.NewGuard(counting(&calls))
	found := false
	err := streamChain(authgrpc.Stream(guard), authgrpc.Stream(guard))(
		nil,
		&fakeStream{ctx: incoming("authorization", "Bearer t")},
		&grpc.StreamServerInfo{FullMethod: articleCreate},
		func(_ any, stream grpc.ServerStream) error {
			_, found = auth.PrincipalFrom(stream.Context())
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("a double stream install lost the principal")
	}
	if calls != 1 {
		t.Fatalf("the stream credential was verified %d times, want once", calls)
	}
}

func TestDifferentStreamGuardsAuthenticateIndependently(t *testing.T) {
	firstCalls, secondCalls := 0, 0
	first := auth.NewGuard(auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		firstCalls++
		return auth.Claims{Sub: "ordinary"}, nil
	}))
	second := auth.NewGuard(auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
		secondCalls++
		return auth.Claims{Sub: "step-up"}, nil
	}))
	var subject string
	err := streamChain(authgrpc.Stream(first), authgrpc.Stream(second))(
		nil,
		&fakeStream{ctx: incoming("authorization", "Bearer t")},
		&grpc.StreamServerInfo{FullMethod: articleCreate},
		func(_ any, stream grpc.ServerStream) error {
			principal, ok := auth.PrincipalFrom(stream.Context())
			if !ok {
				return errors.New("stream handler saw no principal")
			}
			subject = principal.Subject()
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if firstCalls != 1 || secondCalls != 1 {
		t.Fatalf("stream guards authenticated %d and %d times, want once each", firstCalls, secondCalls)
	}
	if subject != "step-up" {
		t.Fatalf("the stream handler saw %q, want the last verified principal", subject)
	}
}

func TestAReenteredStreamGuardFailsClosedWithoutGuessingAssurance(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		firstSubject, secondSubject string
	}{
		{"ordinary -> step-up -> ordinary", "ordinary", "step-up"},
		{"strict -> weak -> strict", "strict", "weak"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			firstCalls, middleCalls := 0, 0
			first := auth.NewGuard(auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
				firstCalls++
				return auth.Claims{Sub: tc.firstSubject}, nil
			}))
			middle := auth.NewGuard(auth.AuthenticatorFunc(func(context.Context, auth.Credential) (auth.Principal, error) {
				middleCalls++
				return auth.Claims{Sub: tc.secondSubject}, nil
			}))
			ran := false
			err := streamChain(authgrpc.Stream(first), authgrpc.Stream(middle), authgrpc.Stream(first))(
				nil,
				&fakeStream{ctx: incoming("authorization", "Bearer t")},
				&grpc.StreamServerInfo{FullMethod: articleCreate},
				func(any, grpc.ServerStream) error { ran = true; return nil },
			)
			if !errors.Is(err, auth.ErrAmbiguousGuardOrder) {
				t.Fatalf("ambiguous stream guard order answered %v", err)
			}
			if ran {
				t.Fatal("the stream handler ran after ambiguous identity ordering")
			}
			if firstCalls != 1 || middleCalls != 1 {
				t.Fatalf("ambiguous stream re-entry called authenticators %d and %d times", firstCalls, middleCalls)
			}
		})
	}
}

func streamChain(interceptors ...grpc.StreamServerInterceptor) grpc.StreamServerInterceptor {
	return func(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		chained := handler
		for i := len(interceptors) - 1; i >= 0; i-- {
			interceptor := interceptors[i]
			next := chained
			chained = func(server any, stream grpc.ServerStream) error {
				return interceptor(server, stream, info, next)
			}
		}
		return chained(server, stream)
	}
}

type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (this *fakeStream) Context() context.Context { return this.ctx }

func TestANilGuardRefusesToStart(t *testing.T) {
	for _, tc := range []struct {
		name  string
		guard *auth.Guard
	}{
		{"nil", nil},
		{"zero", new(auth.Guard)},
	} {
		for _, constructor := range []struct {
			name string
			make func(*auth.Guard)
		}{
			{"Unary", func(guard *auth.Guard) { authgrpc.Unary(guard) }},
			{"Stream", func(guard *auth.Guard) { authgrpc.Stream(guard) }},
		} {
			t.Run(constructor.name+"/"+tc.name, func(t *testing.T) {
				defer func() {
					if recover() == nil {
						t.Fatal("the interceptor was built with a Guard that has no authenticator")
					}
				}()
				constructor.make(tc.guard)
			})
		}
	}
}
