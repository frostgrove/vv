package jobs

import (
	"encoding/binary"
	"math/bits"
	"time"
)

func sampleRetryDelay(entropy *entropySource, backoff BackoffPolicy, spent uint16, minimum time.Duration) (time.Duration, error) {
	if backoff.Initial < MinRetryDelay || backoff.Maximum < backoff.Initial || backoff.Maximum > MaxRetryDelay || backoff.Jitter < NoJitter || backoff.Jitter > FullJitter || minimum != 0 && (minimum < MinRetryDelay || minimum > MaxRetryDelay) {
		return 0, ErrInvalid
	}
	maximum := retryBackoffCap(backoff, spent)
	if minimum < MinRetryDelay {
		minimum = MinRetryDelay
	}
	if minimum > maximum {
		maximum = minimum
	}
	if minimum < MinRetryDelay || maximum < minimum || maximum > MaxRetryDelay {
		return 0, ErrInvalid
	}
	switch backoff.Jitter {
	case NoJitter:
		return maximum, nil
	case FullJitter:
		if minimum == maximum {
			return minimum, nil
		}
	default:
		return 0, ErrInvalid
	}
	var value [8]byte
	if err := entropy.read(value[:]); err != nil {
		return 0, err
	}
	width := uint64(maximum-minimum) + 1
	upper, _ := bits.Mul64(binary.BigEndian.Uint64(value[:]), width)
	return minimum + time.Duration(upper), nil
}
