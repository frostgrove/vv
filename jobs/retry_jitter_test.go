package jobs

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestSampleRetryDelayHonorsPolicyAndExplicitMinimum(t *testing.T) {
	tests := []struct {
		name    string
		policy  BackoffPolicy
		spent   uint16
		minimum time.Duration
		entropy []byte
		want    time.Duration
	}{
		{name: "no jitter cap", policy: Exponential(time.Second, 4*time.Second, NoJitter), spent: 2, want: 4 * time.Second},
		{name: "full jitter lower", policy: Exponential(time.Second, 4*time.Second, FullJitter), spent: 2, entropy: make([]byte, 8), want: MinRetryDelay},
		{name: "full jitter upper", policy: Exponential(time.Second, 4*time.Second, FullJitter), spent: 2, entropy: bytes.Repeat([]byte{0xff}, 8), want: 4 * time.Second},
		{name: "retry after lower", policy: Exponential(time.Second, 4*time.Second, FullJitter), spent: 2, minimum: 3 * time.Second, entropy: make([]byte, 8), want: 3 * time.Second},
		{name: "retry after above cap", policy: Exponential(time.Second, 4*time.Second, FullJitter), spent: 1, minimum: 3 * time.Second, want: 3 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := bytes.NewReader(test.entropy)
			delay, err := sampleRetryDelay(&entropySource{reader: reader}, test.policy, test.spent, test.minimum)
			if err != nil || delay != test.want {
				t.Fatalf("sampleRetryDelay() = (%s, %v), want %s", delay, err, test.want)
			}
			if reader.Len() != 0 {
				t.Fatalf("unused entropy bytes = %d", reader.Len())
			}
		})
	}
}

func TestSampleRetryDelayFailsClosed(t *testing.T) {
	if delay, err := sampleRetryDelay(&entropySource{reader: errReader{err: errors.New("private")}}, Exponential(time.Second, 4*time.Second, FullJitter), 1, 0); delay != 0 || !errors.Is(err, ErrEntropy) {
		t.Fatalf("entropy failure = (%s, %v)", delay, err)
	}
	if delay, err := sampleRetryDelay(&entropySource{reader: bytes.NewReader(make([]byte, 8))}, Exponential(time.Second, 4*time.Second, JitterMode(255)), 1, 0); delay != 0 || !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid jitter = (%s, %v)", delay, err)
	}
	if delay, err := sampleRetryDelay(nil, Exponential(time.Second, 4*time.Second, FullJitter), 1, 0); delay != 0 || !errors.Is(err, ErrEntropy) {
		t.Fatalf("nil entropy = (%s, %v)", delay, err)
	}
	for _, test := range []struct {
		policy  BackoffPolicy
		minimum time.Duration
	}{
		{policy: Exponential(MinRetryDelay-1, time.Second, FullJitter)},
		{policy: Exponential(time.Second, MaxRetryDelay+1, FullJitter)},
		{policy: Exponential(time.Second, 4*time.Second, FullJitter), minimum: MinRetryDelay - 1},
		{policy: Exponential(time.Second, 4*time.Second, FullJitter), minimum: MaxRetryDelay + 1},
	} {
		if delay, err := sampleRetryDelay(&entropySource{reader: bytes.NewReader(make([]byte, 8))}, test.policy, 1, test.minimum); delay != 0 || !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid bounds = (%s, %v)", delay, err)
		}
	}
}
