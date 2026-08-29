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

type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (this *fakeStream) Context() context.Context { return this.ctx }

func TestANilGuardRefusesToStart(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func()
	}{
		{"Unary", func() { authgrpc.Unary(nil) }},
		{"Stream", func() { authgrpc.Stream(nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("the interceptor was built with no guard, so nothing is authenticated and every call looks fine")
				}
			}()
			tc.make()
		})
	}
}
