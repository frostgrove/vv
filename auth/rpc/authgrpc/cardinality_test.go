package authgrpc_test

import (
	"errors"
	"testing"

	"google.golang.org/grpc"

	"github.com/frostgrove/vv/auth"
	"github.com/frostgrove/vv/auth/rpc/authgrpc"
	"github.com/frostgrove/vv/errs"
	"github.com/frostgrove/vv/port"
)

func TestCredentialCardinalityIsSingularForEveryTransportSource(t *testing.T) {
	for _, source := range []struct {
		name    string
		key     string
		options []auth.Option
	}{
		{name: "Authorization metadata", key: "authorization"},
		{name: "configured metadata", key: "x-credential", options: []auth.Option{auth.Header("X-Credential")}},
	} {
		t.Run(source.name, func(t *testing.T) {
			t.Run("one value is authenticated", func(t *testing.T) {
				h, err, calls := callGRPCHeaderValues(t, source.key, []string{"Bearer one"}, source.options...)
				if err != nil || !h.ran || calls != 1 {
					t.Fatalf("a singular credential answered %v, ran=%v, authentications=%d", err, h.ran, calls)
				}
			})
			t.Run("optional absence is anonymous", func(t *testing.T) {
				options := append(append([]auth.Option(nil), source.options...), auth.Optional())
				h, err, calls := callGRPCHeaderValues(t, source.key, nil, options...)
				if err != nil || !h.ran || h.found || calls != 0 {
					t.Fatalf("optional absence answered %v, ran=%v, principal=%v, authentications=%d", err, h.ran, h.found, calls)
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
					h, err, calls := callGRPCHeaderValues(t, source.key, duplicate.values, options...)
					if !errors.Is(err, auth.ErrCredentialCardinality) || !errors.Is(err, auth.ErrUnauthenticated) {
						t.Fatalf("duplicate metadata answered %v, want a typed authentication refusal", err)
					}
					if port.KindOf(err) != errs.KindUnauthorized || h.ran || calls != 0 {
						t.Fatalf("duplicate metadata classified as %v, ran=%v, authentications=%d", port.KindOf(err), h.ran, calls)
					}
				})
			}
		})
	}
}

func TestDuplicateMetadataNeverOpensAStream(t *testing.T) {
	for _, source := range []struct {
		name    string
		key     string
		options []auth.Option
	}{
		{name: "Authorization metadata", key: "authorization"},
		{name: "configured metadata", key: "x-credential", options: []auth.Option{auth.Header("X-Credential")}},
	} {
		t.Run(source.name, func(t *testing.T) {
			calls := 0
			runs := 0
			guard := auth.NewGuard(counting(&calls), source.options...)
			ctx := incoming(source.key, "Bearer same", source.key, "Bearer same")
			err := authgrpc.Stream(guard)(
				nil,
				&fakeStream{ctx: ctx},
				&grpc.StreamServerInfo{FullMethod: articleCreate},
				func(any, grpc.ServerStream) error { runs++; return nil },
			)
			if !errors.Is(err, auth.ErrCredentialCardinality) || port.KindOf(err) != errs.KindUnauthorized {
				t.Fatalf("duplicate stream metadata answered %v", err)
			}
			if runs != 0 || calls != 0 {
				t.Fatalf("duplicate stream metadata opened %d handlers and ran %d authentications", runs, calls)
			}
		})
	}
}

func callGRPCHeaderValues(
	t *testing.T,
	key string,
	values []string,
	options ...auth.Option,
) (*seen, error, int) {
	t.Helper()
	calls := 0
	guard := auth.NewGuard(counting(&calls), options...)
	pairs := make([]string, 0, len(values)*2)
	for _, value := range values {
		pairs = append(pairs, key, value)
	}
	handler, err := call(t, guard, incoming(pairs...))
	return handler, err, calls
}
