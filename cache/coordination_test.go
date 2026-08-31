package cache

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

func TestResolveObsoleteMissRechecksGeneration(t *testing.T) {
	backend := newCoordinationBackend()
	firstMiss := make(chan struct{})
	releaseFirstMiss := make(chan struct{})
	backend.getHook = func(ctx context.Context, address Address, _ ReadLimit, call int) ([]byte, bool, error) {
		if call == 1 {
			close(firstMiss)
			<-releaseFirstMiss
			return nil, false, nil
		}
		return backend.load(ctx, address)
	}
	instance := newCoordinationCache(t, backend, String(ValueSchema(1)))
	var loads atomic.Int32
	loader := func(context.Context, string) (LoadResult[string], error) {
		loads.Add(1)
		return Present("fresh"), nil
	}

	first := resolveAsync(context.Background(), instance, "key", loader)
	waitSignal(t, firstMiss, "first miss")
	second, err := instance.Resolve(context.Background(), "key", loader)
	if err != nil || second.Value != "fresh" || second.State != Loaded {
		t.Fatalf("second result = %+v, err = %v", second, err)
	}
	close(releaseFirstMiss)
	assertStringResult(t, receiveResolve(t, first), "fresh", Hit)
	if loads.Load() != 1 {
		t.Fatalf("loader calls = %d", loads.Load())
	}
	gets, puts, deletes := backend.calls()
	if gets != 3 || puts != 1 || deletes != 0 {
		t.Fatalf("backend calls = get:%d put:%d delete:%d", gets, puts, deletes)
	}
	assertQuiescent(t, instance)
}

func TestPutFencesLoaderBeforeCommit(t *testing.T) {
	backend := newCoordinationBackend()
	instance := newCoordinationCache(t, backend, String(ValueSchema(1)))
	loaderEntered := make(chan struct{})
	releaseLoader := make(chan struct{})
	var loads atomic.Int32
	loader := func(context.Context, string) (LoadResult[string], error) {
		loads.Add(1)
		close(loaderEntered)
		<-releaseLoader
		return Present("loader"), nil
	}

	resolved := resolveAsync(context.Background(), instance, "key", loader)
	waitSignal(t, loaderEntered, "loader entry")
	if err := instance.Put(context.Background(), "key", "explicit"); err != nil {
		t.Fatal(err)
	}
	close(releaseLoader)
	assertStringResult(t, receiveResolve(t, resolved), "explicit", Hit)
	if loads.Load() != 1 {
		t.Fatalf("loader calls = %d", loads.Load())
	}
	_, puts, _ := backend.calls()
	if puts != 1 {
		t.Fatalf("backend put calls = %d", puts)
	}
	assertQuiescent(t, instance)
}

func TestPutWinsAfterClaimedLoaderWrite(t *testing.T) {
	backend := newCoordinationBackend()
	loaderWriteClaimed := make(chan struct{})
	releaseLoaderWrite := make(chan struct{})
	explicitWriteStarted := make(chan struct{})
	backend.setPutHook(func(ctx context.Context, address Address, value []byte, _ Expiry, call int) error {
		switch call {
		case 1:
			close(loaderWriteClaimed)
			<-releaseLoaderWrite
			return backend.store(ctx, address, value)
		case 2:
			close(explicitWriteStarted)
			return backend.store(ctx, address, value)
		default:
			return unexpectedCall("put", call)
		}
	})
	instance := newCoordinationCache(t, backend, String(ValueSchema(1)))
	resolved := resolveAsync(context.Background(), instance, "key", func(context.Context, string) (LoadResult[string], error) {
		return Present("loader"), nil
	})
	waitSignal(t, loaderWriteClaimed, "loader backend write")

	putDone := make(chan error, 1)
	go func() { putDone <- instance.Put(context.Background(), "key", "explicit") }()
	waitAddressState(t, instance, "key", func(state *addressState) bool {
		return state.writeActive && state.refs >= 3
	})
	assertNotSignaled(t, explicitWriteStarted, "explicit backend write")
	close(releaseLoaderWrite)
	waitSignal(t, explicitWriteStarted, "explicit backend write")
	if err := receiveError(t, putDone); err != nil {
		t.Fatal(err)
	}
	if outcome := receiveResolve(t, resolved); outcome.err != nil {
		t.Fatalf("resolve error = %v", outcome.err)
	}
	result, err := instance.Lookup(context.Background(), "key")
	if err != nil || result.Value != "explicit" || result.State != Hit {
		t.Fatalf("final result = %+v, err = %v", result, err)
	}
	_, puts, _ := backend.calls()
	if puts != 2 {
		t.Fatalf("backend put calls = %d", puts)
	}
	assertQuiescent(t, instance)
}

func TestForgetWaitsForWriteAndFencesNewPut(t *testing.T) {
	backend := newCoordinationBackend()
	oldWriteStarted := make(chan struct{})
	releaseOldWrite := make(chan struct{})
	newWriteStarted := make(chan struct{})
	deleteStarted := make(chan struct{})
	releaseDelete := make(chan struct{})
	backend.setPutHook(func(ctx context.Context, address Address, value []byte, _ Expiry, call int) error {
		switch call {
		case 1:
			close(oldWriteStarted)
			<-releaseOldWrite
			return backend.store(ctx, address, value)
		case 2:
			close(newWriteStarted)
			return backend.store(ctx, address, value)
		default:
			return unexpectedCall("put", call)
		}
	})
	backend.setDeleteHook(func(ctx context.Context, address Address, call int) error {
		if call != 1 {
			return unexpectedCall("delete", call)
		}
		close(deleteStarted)
		<-releaseDelete
		return backend.remove(ctx, address)
	})
	instance := newCoordinationCache(t, backend, String(ValueSchema(1)))

	oldPut := make(chan error, 1)
	go func() { oldPut <- instance.Put(context.Background(), "key", "old") }()
	waitSignal(t, oldWriteStarted, "old backend write")
	forgetDone := make(chan error, 1)
	go func() { forgetDone <- instance.Forget(context.Background(), "key") }()
	waitAddressState(t, instance, "key", func(state *addressState) bool { return state.invalidating })
	newPut := make(chan error, 1)
	go func() { newPut <- instance.Put(context.Background(), "key", "new") }()
	waitAddressState(t, instance, "key", func(state *addressState) bool { return state.refs >= 3 })
	assertNotSignaled(t, deleteStarted, "delete")
	assertNotSignaled(t, newWriteStarted, "new backend write")

	close(releaseOldWrite)
	if err := receiveError(t, oldPut); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, deleteStarted, "delete")
	assertNotSignaled(t, newWriteStarted, "new backend write")
	close(releaseDelete)
	if err := receiveError(t, forgetDone); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, newWriteStarted, "new backend write")
	if err := receiveError(t, newPut); err != nil {
		t.Fatal(err)
	}
	result, err := instance.Lookup(context.Background(), "key")
	if err != nil || result.Value != "new" || result.State != Hit {
		t.Fatalf("final result = %+v, err = %v", result, err)
	}
	_, puts, deletes := backend.calls()
	if puts != 2 || deletes != 1 {
		t.Fatalf("backend calls = put:%d delete:%d", puts, deletes)
	}
	assertQuiescent(t, instance)
}

func TestCancelledForgetCompletesSynchronousCleanup(t *testing.T) {
	backend := newCoordinationBackend()
	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	deleteStarted := make(chan struct{})
	releaseDelete := make(chan struct{})
	deleteContext := make(chan error, 1)
	backend.setPutHook(func(ctx context.Context, address Address, value []byte, _ Expiry, call int) error {
		if call != 1 {
			return unexpectedCall("put", call)
		}
		close(writeStarted)
		<-releaseWrite
		return backend.store(ctx, address, value)
	})
	backend.setDeleteHook(func(ctx context.Context, address Address, call int) error {
		if call != 1 {
			return unexpectedCall("delete", call)
		}
		deleteContext <- ctx.Err()
		close(deleteStarted)
		<-releaseDelete
		return backend.remove(ctx, address)
	})
	instance := newCoordinationCache(t, backend, String(ValueSchema(1)))

	putDone := make(chan error, 1)
	go func() { putDone <- instance.Put(context.Background(), "key", "value") }()
	waitSignal(t, writeStarted, "backend write")
	forgetCtx, cancelForget := context.WithCancel(context.Background())
	forgetDone := make(chan error, 1)
	forgetReturned := make(chan struct{})
	go func() {
		forgetDone <- instance.Forget(forgetCtx, "key")
		close(forgetReturned)
	}()
	waitAddressState(t, instance, "key", func(state *addressState) bool { return state.invalidating })
	cancelForget()
	assertNotSignaled(t, forgetReturned, "Forget return")
	close(releaseWrite)
	if err := receiveError(t, putDone); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, deleteStarted, "delete")
	if err := <-deleteContext; err != nil {
		t.Fatalf("cleanup context error = %v", err)
	}
	assertNotSignaled(t, forgetReturned, "Forget return")
	close(releaseDelete)
	if err := receiveError(t, forgetDone); err != nil {
		t.Fatal(err)
	}
	result, err := instance.Lookup(context.Background(), "key")
	if err != nil || result.State != Miss {
		t.Fatalf("final result = %+v, err = %v", result, err)
	}
	_, _, deletes := backend.calls()
	if deletes != 1 {
		t.Fatalf("backend delete calls = %d", deletes)
	}
	assertQuiescent(t, instance)
}

type coordinationOwnedChild struct {
	Name string
}

type coordinationOwnedValue struct {
	Bytes  []byte
	Labels map[string]string
	Child  *coordinationOwnedChild
}

func TestJoinedWaitersReceiveIndependentResults(t *testing.T) {
	requireSafeJSONRuntime(t)
	backend := newCoordinationBackend()
	instance := newCoordinationCache(t, backend, JSON[*coordinationOwnedValue](ValueSchema(1)))
	loaderEntered := make(chan struct{})
	releaseLoader := make(chan struct{})
	var loads atomic.Int32
	original := &coordinationOwnedValue{
		Bytes:  []byte("bytes"),
		Labels: map[string]string{"key": "value"},
		Child:  &coordinationOwnedChild{Name: "child"},
	}
	loader := func(context.Context, string) (LoadResult[*coordinationOwnedValue], error) {
		loads.Add(1)
		close(loaderEntered)
		<-releaseLoader
		return Present(original), nil
	}

	first := resolveAsync(context.Background(), instance, "key", loader)
	waitSignal(t, loaderEntered, "loader entry")
	second := resolveAsync(context.Background(), instance, "key", loader)
	waitAddressState(t, instance, "key", func(state *addressState) bool {
		return state.member != nil && state.member.waiters == 2
	})
	close(releaseLoader)
	firstResult := receiveResolve(t, first)
	secondResult := receiveResolve(t, second)
	if firstResult.err != nil || secondResult.err != nil || firstResult.result.State != Loaded || secondResult.result.State != Loaded {
		t.Fatalf("first = %+v/%v, second = %+v/%v", firstResult.result, firstResult.err, secondResult.result, secondResult.err)
	}
	if firstResult.result.Value == secondResult.result.Value || firstResult.result.Value == original || secondResult.result.Value == original {
		t.Fatal("waiters or loader share the top-level result")
	}
	firstResult.result.Value.Bytes[0] = 'X'
	firstResult.result.Value.Labels["key"] = "changed"
	firstResult.result.Value.Child.Name = "changed"
	if string(secondResult.result.Value.Bytes) != "bytes" || secondResult.result.Value.Labels["key"] != "value" || secondResult.result.Value.Child.Name != "child" {
		t.Fatalf("second result was mutated: %+v", secondResult.result.Value)
	}
	if string(original.Bytes) != "bytes" || original.Labels["key"] != "value" || original.Child.Name != "child" {
		t.Fatalf("loader result was mutated: %+v", original)
	}
	if loads.Load() != 1 {
		t.Fatalf("loader calls = %d", loads.Load())
	}
	_, puts, _ := backend.calls()
	if puts != 1 {
		t.Fatalf("backend put calls = %d", puts)
	}
	assertQuiescent(t, instance)
}

func TestTerminalPathsReleaseCoordinationState(t *testing.T) {
	terminalErr := errors.New("terminal failure")
	loaderCases := []struct {
		name    string
		loader  Loader[string, string]
		wantErr bool
	}{
		{name: "success", loader: func(context.Context, string) (LoadResult[string], error) { return Present("value"), nil }},
		{name: "error", loader: func(context.Context, string) (LoadResult[string], error) { return LoadResult[string]{}, terminalErr }, wantErr: true},
		{name: "panic", loader: func(context.Context, string) (LoadResult[string], error) { panic("loader") }, wantErr: true},
		{name: "invalid presence", loader: func(context.Context, string) (LoadResult[string], error) { return LoadResult[string]{}, nil }, wantErr: true},
		{name: "encode failure", loader: func(context.Context, string) (LoadResult[string], error) {
			return Present(strings.Repeat("x", 5<<10)), nil
		}, wantErr: true},
	}
	for _, test := range loaderCases {
		t.Run(test.name, func(t *testing.T) {
			backend := newCoordinationBackend()
			instance := newCoordinationCache(t, backend, String(ValueSchema(1)))
			_, err := instance.Resolve(context.Background(), "key", test.loader)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, want error = %t", err, test.wantErr)
			}
			assertQuiescent(t, instance)
		})
	}
	t.Run("backend write failure", func(t *testing.T) {
		backend := newCoordinationBackend()
		backend.setPutHook(func(context.Context, Address, []byte, Expiry, int) error { return terminalErr })
		instance := newCoordinationCache(t, backend, String(ValueSchema(1)))
		_, err := instance.Resolve(context.Background(), "key", func(context.Context, string) (LoadResult[string], error) {
			return Present("value"), nil
		})
		if !errors.Is(err, terminalErr) {
			t.Fatalf("error = %v", err)
		}
		assertQuiescent(t, instance)
	})
	t.Run("caller cancellation", func(t *testing.T) {
		backend := newCoordinationBackend()
		instance := newCoordinationCache(t, backend, String(ValueSchema(1)))
		entered := make(chan struct{})
		exited := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		done := resolveAsync(ctx, instance, "key", func(ctx context.Context, _ string) (LoadResult[string], error) {
			close(entered)
			<-ctx.Done()
			close(exited)
			return LoadResult[string]{}, ctx.Err()
		})
		waitSignal(t, entered, "loader entry")
		cancel()
		outcome := receiveResolve(t, done)
		if !errors.Is(outcome.err, context.Canceled) {
			t.Fatalf("error = %v", outcome.err)
		}
		waitSignal(t, exited, "loader exit")
		assertQuiescent(t, instance)
	})
	t.Run("direct put failure", func(t *testing.T) {
		backend := newCoordinationBackend()
		backend.setPutHook(func(context.Context, Address, []byte, Expiry, int) error { return terminalErr })
		instance := newCoordinationCache(t, backend, String(ValueSchema(1)))
		if err := instance.Put(context.Background(), "key", "value"); !errors.Is(err, terminalErr) {
			t.Fatalf("error = %v", err)
		}
		assertQuiescent(t, instance)
	})
	t.Run("forget failure", func(t *testing.T) {
		backend := newCoordinationBackend()
		instance := newCoordinationCache(t, backend, String(ValueSchema(1)))
		if err := instance.Put(context.Background(), "key", "value"); err != nil {
			t.Fatal(err)
		}
		backend.setDeleteHook(func(context.Context, Address, int) error { return terminalErr })
		if err := instance.Forget(context.Background(), "key"); !errors.Is(err, terminalErr) {
			t.Fatalf("error = %v", err)
		}
		assertQuiescent(t, instance)
	})
}
