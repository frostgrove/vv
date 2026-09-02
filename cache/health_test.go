package cache

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type unhealthyBackend struct {
	*seamBackend
	failure error
	calls   int
}

func (this *unhealthyBackend) CheckBackend(context.Context) error {
	this.calls++
	return this.failure
}

func TestACacheThatWasNeverActivatedRefusesToReportItselfUsable(t *testing.T) {
	target := Auto[string, string](Hot)

	if err := target.Check(context.Background()); !errors.Is(err, ErrNotActivated) {
		t.Fatalf("Check() error = %v, want a not-activated refusal", err)
	}
}

func TestACacheReportsWhatItsBackendSaysAboutItself(t *testing.T) {
	policy := newCacheTestPolicy(64)
	outage := errors.New("dial tcp: connection refused")
	backend := &unhealthyBackend{seamBackend: newSeamBackend(policy), failure: outage}
	cache, _ := newBatchTestCache(t, backend, Bytes(1), policy, &batchRecordingObserver{})

	err := cache.Check(context.Background())

	if !errors.Is(err, ErrBackend) {
		t.Fatalf("Check() error = %v, want a backend failure", err)
	}
	if backend.calls != 1 {
		t.Fatalf("CheckBackend() calls = %d, want 1", backend.calls)
	}
	if strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("the probe rendered the driver's own message: %v", err)
	}

	backend.failure = nil
	if err := cache.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v, want a passing probe", err)
	}
}

func TestABackendWithNothingToProbeStillAnswersTheProbe(t *testing.T) {
	policy := newCacheTestPolicy(64)
	cache, _ := newBatchTestCache(t, newSeamBackend(policy), Bytes(1), policy, &batchRecordingObserver{})

	if err := cache.Check(context.Background()); err != nil {
		t.Fatalf("Check() error = %v, want a passing probe", err)
	}
}
