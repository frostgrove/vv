package i18nflow

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/cache/cachememory"
	"github.com/frostgrove/vv/i18n"
	"github.com/frostgrove/vv/tenancy"
	"github.com/frostgrove/vv/tenancy/tenancycache"
)

type renderCacheRequest struct {
	identity string
	view     *i18n.View
	message  i18n.Message
	payload  string
}

type loaderContextKey struct{}

func globalRenderCache(t testing.TB, profile cache.Profile) *cache.Cache[renderCacheRequest, string] {
	t.Helper()
	backend, err := cachememory.New(cachememory.Limits{MaxEntries: 128, MaxBytes: 64 << 20, MaxItemBytes: 32 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	policy, err := profile.Build()
	if err != nil {
		t.Fatal(err)
	}
	keys := cache.MustKeyFunc(cache.KeyVersion(1), func(key renderCacheRequest, limit cache.KeyLimit) ([]byte, error) {
		encoded := []byte(key.identity)
		if len(encoded) == 0 || len(encoded) > limit.MaxBytes {
			return nil, cache.ErrTooLarge
		}
		return encoded, nil
	})
	instance, err := cache.New(
		cache.Runtime{ClockSkew: cache.SingleProcessClock()},
		backend,
		cache.Global[renderCacheRequest](cache.MustNamespace("i18nflow", "test", "rendered-messages", 1)),
		keys,
		cache.String(cache.ValueSchema(1)),
		policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func renderedRequest(t testing.TB, snapshot *i18n.Snapshot, localeName, formattingLocale string, count int64) renderCacheRequest {
	t.Helper()
	resolution := snapshot.Resolve(i18n.Exact(i18n.SourceExplicit, localeName))
	view, err := snapshot.View(i18n.ViewSpec{
		Resolution:       resolution,
		FormattingLocale: formattingLocale,
		Presentation:     i18n.PresentationNoIsolation,
	})
	if err != nil {
		t.Fatal(err)
	}
	message, err := snapshot.Bind(i18n.Qualify("errors", "capacity"), i18n.Text("field", "name"), i18n.Integer("count", count))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := view.RenderKey(message)
	if err != nil {
		t.Fatal(err)
	}
	return renderCacheRequest{identity: identity, view: view, message: message}
}

func loadRendered(ctx context.Context, request renderCacheRequest) (cache.LoadResult[string], error) {
	rendered, err := request.view.Render(ctx, request.message)
	if err != nil {
		return cache.LoadResult[string]{}, err
	}
	return cache.Present(rendered.Text), nil
}

func TestRenderKeySeparatesEveryOutputAffectingCacheInput(t *testing.T) {
	snapshot := baseSnapshot(t)
	base := renderedRequest(t, snapshot, "en", "en", 2)
	inputs := []renderCacheRequest{
		base,
		renderedRequest(t, snapshot, "fr", "fr", 2),
		renderedRequest(t, snapshot, "en", "de", 2),
		renderedRequest(t, snapshot, "en", "en", 3),
	}
	released := overlaySnapshot(t, snapshot, i18n.ApplicationOverlay("application-release/v2"))
	inputs = append(inputs, renderedRequest(t, released, "en", "en", 2))

	seen := make(map[string]bool, len(inputs))
	for index, input := range inputs {
		if seen[input.identity] {
			t.Fatalf("cache identity %d collided: %q", index, input.identity)
		}
		seen[input.identity] = true
	}
	repeated := renderedRequest(t, snapshot, "en", "en", 2)
	if repeated.identity != base.identity {
		t.Fatalf("same render identity drifted: %q != %q", repeated.identity, base.identity)
	}
}

func TestRenderedMessageCacheExercisesMissLoadAndHit(t *testing.T) {
	instance := globalRenderCache(t, cache.Hot)
	request := renderedRequest(t, baseSnapshot(t), "fr", "fr", 2)

	miss, err := instance.Lookup(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if miss.State != cache.Miss {
		t.Fatalf("initial state = %v, want miss", miss.State)
	}
	var loads atomic.Int64
	loader := func(ctx context.Context, key renderCacheRequest) (cache.LoadResult[string], error) {
		loads.Add(1)
		return loadRendered(ctx, key)
	}
	loaded, err := instance.Resolve(context.Background(), request, loader)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != cache.Loaded || loaded.Value != "name a 2 conflits" {
		t.Fatalf("loaded = %+v", loaded)
	}
	hit, err := instance.Resolve(context.Background(), request, loader)
	if err != nil {
		t.Fatal(err)
	}
	if hit.State != cache.Hit || hit.Value != loaded.Value || loads.Load() != 1 {
		t.Fatalf("hit = %+v, loads = %d", hit, loads.Load())
	}
}

func TestDisabledRenderedMessageCachePreservesLoaderSemantics(t *testing.T) {
	instance := globalRenderCache(t, cache.Disabled)
	request := renderedRequest(t, baseSnapshot(t), "en", "en", 2)
	var loads atomic.Int64
	loader := func(ctx context.Context, key renderCacheRequest) (cache.LoadResult[string], error) {
		loads.Add(1)
		return loadRendered(ctx, key)
	}
	for range 2 {
		result, err := instance.Resolve(context.Background(), request, loader)
		if err != nil {
			t.Fatal(err)
		}
		if result.State != cache.Loaded || result.Value != "name has 2 conflicts" {
			t.Fatalf("disabled result = %+v", result)
		}
	}
	if loads.Load() != 2 {
		t.Fatalf("disabled loads = %d, want 2", loads.Load())
	}
}

func TestSharedRenderFlightDoesNotInheritAWaitersContext(t *testing.T) {
	instance := globalRenderCache(t, cache.Hot)
	request := renderedRequest(t, baseSnapshot(t), "en", "en", 2)
	started := make(chan struct{})
	release := make(chan struct{})
	var loads atomic.Int64
	loader := func(ctx context.Context, key renderCacheRequest) (cache.LoadResult[string], error) {
		loads.Add(1)
		if ctx.Value(loaderContextKey{}) != nil {
			return cache.LoadResult[string]{}, errors.New("request context value reached shared loader")
		}
		close(started)
		select {
		case <-release:
			return loadRendered(ctx, key)
		case <-ctx.Done():
			return cache.LoadResult[string]{}, ctx.Err()
		}
	}
	type outcome struct {
		result cache.Result[string]
		err    error
	}
	leaderResult := make(chan outcome, 1)
	go func() {
		ctx := context.WithValue(context.Background(), loaderContextKey{}, "leader-private-value")
		result, err := instance.Resolve(ctx, request, loader)
		leaderResult <- outcome{result: result, err: err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("shared loader did not start")
	}

	waiterCtx, cancelWaiter := context.WithCancel(context.WithValue(context.Background(), loaderContextKey{}, "waiter-private-value"))
	waiterResult := make(chan error, 1)
	go func() {
		_, err := instance.Resolve(waiterCtx, request, loader)
		waiterResult <- err
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for instance.Stats().FlightWaiters == 0 {
		select {
		case <-deadline.C:
			t.Fatal("second caller did not join the shared flight")
		default:
			runtime.Gosched()
		}
	}
	cancelWaiter()
	if err := <-waiterResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter error = %v", err)
	}
	close(release)
	leader := <-leaderResult
	if leader.err != nil || leader.result.State != cache.Loaded || leader.result.Value != "name has 2 conflicts" {
		t.Fatalf("leader = %+v, %v", leader.result, leader.err)
	}
	if loads.Load() != 1 {
		t.Fatalf("shared loader calls = %d", loads.Load())
	}
}

func tenantRenderCache(t testing.TB) *cache.Cache[tenancycache.Key[renderCacheRequest], string] {
	t.Helper()
	backend, err := cachememory.New(cachememory.Limits{MaxEntries: 128, MaxBytes: 64 << 20, MaxItemBytes: 32 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	keys := cache.MustKeyFunc(cache.KeyVersion(1), func(key tenancycache.Key[renderCacheRequest], limit cache.KeyLimit) ([]byte, error) {
		encoded := []byte(key.Unwrap().identity)
		if len(encoded) == 0 || len(encoded) > limit.MaxBytes {
			return nil, cache.ErrTooLarge
		}
		return encoded, nil
	})
	policy, err := cache.Hot.Build()
	if err != nil {
		t.Fatal(err)
	}
	instance, err := cache.New(
		cache.Runtime{ClockSkew: cache.SingleProcessClock()},
		backend,
		tenancycache.Partitioned[renderCacheRequest](cache.MustNamespace("i18nflow", "test", "tenant-rendered-messages", 1)),
		keys,
		cache.String(cache.ValueSchema(1)),
		policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func TestTenantRenderCacheIsPartitionedOnlyAfterAdmission(t *testing.T) {
	resolutions := map[string]tenancy.Resolution{
		"tenant-a":        tenantResolution(t, "tenant-a", tenancy.Active),
		"tenant-b":        tenantResolution(t, "tenant-b", tenancy.Active),
		"tenant-inactive": tenantResolution(t, "tenant-inactive", tenancy.Suspended),
	}
	authority, err := tenancy.New(tenancy.Spec{
		Resolver: tenantDirectory{resolutions: resolutions}, Admission: tenancy.AdmitAll(tenancy.Active), Origin: "i18n-cache-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	instance := tenantRenderCache(t)
	var loads atomic.Int64
	loader := func(_ context.Context, key tenancycache.Key[renderCacheRequest]) (cache.LoadResult[string], error) {
		loads.Add(1)
		return cache.Present(key.Unwrap().payload), nil
	}
	resolve := func(reference, payload string) cache.Result[string] {
		t.Helper()
		ctx, err := tenantScope(context.Background(), authority, reference)
		if err != nil {
			t.Fatal(err)
		}
		key, err := tenancycache.Keyed(ctx, authority, tenancy.ClassRead, renderCacheRequest{identity: "same-render-identity", payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		result, err := instance.Resolve(ctx, key, loader)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	a := resolve("tenant-a", "tenant A text")
	b := resolve("tenant-b", "tenant B text")
	if a.State != cache.Loaded || a.Value != "tenant A text" || b.State != cache.Loaded || b.Value != "tenant B text" {
		t.Fatalf("partitioned values = %+v / %+v", a, b)
	}
	aHit := resolve("tenant-a", "must not replace tenant A text")
	if aHit.State != cache.Hit || aHit.Value != "tenant A text" || loads.Load() != 2 {
		t.Fatalf("tenant hit = %+v, loads = %d", aHit, loads.Load())
	}
	if _, err := tenantScope(context.Background(), authority, "tenant-inactive"); !errors.Is(err, tenancy.ErrInactive) {
		t.Fatalf("inactive admission error = %v", err)
	}
	if _, err := tenantScope(context.Background(), authority, "unknown"); !errors.Is(err, tenancy.ErrUnmapped) {
		t.Fatalf("unknown admission error = %v", err)
	}
	if loads.Load() != 2 {
		t.Fatalf("refused tenants reached cache loader: %d", loads.Load())
	}
}
