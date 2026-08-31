package cachetest

import (
	"context"
	"runtime"
	"testing"
	"time"
)

const testTimeout = 10 * time.Second

func receive[T any](t *testing.T, values <-chan T, name string) T {
	t.Helper()
	timer := time.NewTimer(testTimeout)
	defer timer.Stop()
	select {
	case value := <-values:
		return value
	case <-timer.C:
		t.Fatalf("cachetest: timed out waiting for %s", name)
		var zero T
		return zero
	}
}

func waitUntil(t *testing.T, name string, ready func() bool) {
	t.Helper()
	timer := time.NewTimer(testTimeout)
	defer timer.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-timer.C:
			t.Fatalf("cachetest: timed out waiting for %s", name)
		default:
			runtime.Gosched()
		}
	}
}

func waitForPause(t *testing.T, pause *Pause, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	if err := pause.Wait(ctx); err != nil {
		t.Fatalf("cachetest: waiting for %s: %v", name, err)
	}
}
