package cachetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/frostgrove/vv/cache"
	"github.com/frostgrove/vv/cache/cachememory"
)

func TestControllerFailurePauseRecordAndCapability(t *testing.T) {
	clock := NewClock()
	backend, err := cachememory.New(
		cachememory.Limits{MaxEntries: 16, MaxBytes: 1 << 20, MaxItemBytes: 1 << 10},
		cachememory.WithClock(clock),
	)
	if err != nil {
		t.Fatal(err)
	}
	controller := MustController(backend)
	wrapped := controller.Backend()
	if !cache.Supports(wrapped, cache.BatchReadCapability) {
		t.Fatal("batch capability was lost")
	}
	address := testAddress(1)
	expiry := cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Hour}

	pause := controller.MustPauseNext(PutOperation)
	done := make(chan error, 1)
	go func() {
		done <- wrapped.Put(context.Background(), address, []byte("secret-value"), expiry)
	}()
	waitForPause(t, pause, "paused put")
	select {
	case err := <-done:
		t.Fatalf("put completed before release: %v", err)
	default:
	}
	pause.Release()
	if err := receive(t, done, "released put"); err != nil {
		t.Fatal(err)
	}

	injected := errors.New("private backend failure")
	controller.MustFailNext(GetOperation, injected)
	if _, _, err := wrapped.Get(context.Background(), address, cache.ReadLimit{MaxBytes: 64}); !errors.Is(err, injected) {
		t.Fatalf("get error = %v", err)
	}
	value, found, err := wrapped.Get(context.Background(), address, cache.ReadLimit{MaxBytes: 64})
	if err != nil || !found || string(value) != "secret-value" {
		t.Fatalf("get = %q, %v, %v", value, found, err)
	}
	records := controller.Records()
	if len(records) != 3 {
		t.Fatalf("records = %+v", records)
	}
	if records[0].Operation != PutOperation || records[1].Operation != GetOperation || !records[1].Failed || records[2].ValueBytes != int64(len(value)) {
		t.Fatalf("records = %+v", records)
	}
}

func TestControllerPauseCancellationAndReset(t *testing.T) {
	backend, err := cachememory.New(cachememory.Limits{MaxEntries: 4, MaxBytes: 4096, MaxItemBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	controller := MustController(backend)
	pause := controller.MustPauseNext(DeleteOperation)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.Backend().Delete(ctx, testAddress(2)) }()
	waitForPause(t, pause, "paused delete")
	cancel()
	if err := receive(t, done, "cancelled delete"); !errors.Is(err, context.Canceled) {
		t.Fatalf("delete error = %v", err)
	}

	pending := controller.MustPauseNext(GetOperation)
	active := controller.MustPauseNext(PutOperation)
	activeDone := make(chan error, 1)
	go func() {
		activeDone <- controller.Backend().Put(context.Background(), testAddress(3), []byte("value"), cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Minute})
	}()
	waitForPause(t, active, "active put")
	controller.Reset()
	waitCtx, stopWait := context.WithTimeout(context.Background(), testTimeout)
	defer stopWait()
	if err := pending.Wait(waitCtx); !errors.Is(err, ErrPauseCanceled) {
		t.Fatalf("pending pause after reset = %v", err)
	}
	if err := receive(t, activeDone, "reset put"); err != nil {
		t.Fatalf("active operation after reset = %v", err)
	}
	if records := controller.Records(); len(records) != 1 || records[0].Operation != PutOperation {
		t.Fatalf("records after reset = %+v", records)
	}
}

func TestControllerBatchRecordsAggregateOnly(t *testing.T) {
	backend, err := cachememory.New(cachememory.Limits{MaxEntries: 4, MaxBytes: 4096, MaxItemBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	controller := MustController(backend)
	wrapped := controller.Backend()
	expiry := cache.Expiry{Mode: cache.RelativeExpiry, RetainFor: time.Hour}
	for index, value := range [][]byte{[]byte("a"), []byte("bb")} {
		if err := wrapped.Put(context.Background(), testAddress(byte(index+1)), value, expiry); err != nil {
			t.Fatal(err)
		}
	}
	controller.Reset()
	reader, ok := cache.BatchReaderOf(wrapped)
	if !ok {
		t.Fatal("batch reader is absent")
	}
	values, err := reader.GetMany(context.Background(), []cache.Address{testAddress(1), testAddress(2)}, cache.BatchReadLimit{MaxItems: 2, MaxItemBytes: 16, MaxTotalBytes: 32})
	if err != nil || len(values) != 2 {
		t.Fatalf("values = %v, %v", values, err)
	}
	records := controller.Records()
	if len(records) != 1 || records[0].Items != 2 || records[0].ValueBytes != 3 || !records[0].Found {
		t.Fatalf("records = %+v", records)
	}
}

func testAddress(value byte) cache.Address {
	var address cache.Address
	address.KeyDigest[0] = value
	return address
}
