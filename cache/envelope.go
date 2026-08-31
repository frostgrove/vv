package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"
)

const (
	envelopeVersion   = uint16(1)
	envelopeFixedSize = 58
	envelopeHashSize  = sha256.Size
)

var envelopeMagic = [8]byte{'F', 'V', 'C', 'A', 'C', 'H', 'E', 0}

type envelope struct {
	presence   Presence
	writtenAt  time.Time
	freshTill  time.Time
	staleTill  time.Time
	retainTill time.Time
	payload    []byte
}

func encodeEnvelope[V any](runtime Runtime, codec Codec[V], descriptor codecDescriptor, policy Policy, result LoadResult[V]) ([]byte, []byte, Expiry, error) {
	if result.Presence != Found && result.Presence != CleanAbsent {
		return nil, nil, Expiry{}, fmt.Errorf("%w: loader presence is invalid", ErrInvalid)
	}
	var payload []byte
	var err error
	if result.Presence == Found {
		payload, err = invokeEncode(codec, result.Value, ValueLimit{
			MaxBytes:        policy.MaxValueBytes,
			MaxDecodedBytes: policy.MaxValueBytes,
			MaxDepth:        policy.MaxValueDepth,
		})
		if err != nil {
			return nil, nil, Expiry{}, failure("encode value", err)
		}
		if len(payload) > policy.MaxValueBytes {
			return nil, nil, Expiry{}, failure("encode value", ErrTooLarge)
		}
		payload = bytes.Clone(payload)
	}
	now, err := runtimeNow(runtime.Clock)
	if err != nil {
		return nil, nil, Expiry{}, err
	}
	now = normalizedTime(now)
	freshTill, staleTill, retainTill, err := deadlines(runtime, policy, result.Presence, now)
	if err != nil {
		return nil, nil, Expiry{}, err
	}
	total, ok := envelopeSize(len(descriptor.id), len(payload))
	if !ok {
		return nil, nil, Expiry{}, failure("encode value", ErrTooLarge)
	}
	encoded := make([]byte, total)
	copy(encoded[:8], envelopeMagic[:])
	binary.BigEndian.PutUint16(encoded[8:10], envelopeVersion)
	encoded[10] = byte(result.Presence)
	binary.BigEndian.PutUint16(encoded[12:14], uint16(len(descriptor.id)))
	binary.BigEndian.PutUint32(encoded[14:18], uint32(descriptor.schema))
	binary.BigEndian.PutUint64(encoded[18:26], uint64(now.UnixNano()))
	binary.BigEndian.PutUint64(encoded[26:34], timeBits(freshTill))
	binary.BigEndian.PutUint64(encoded[34:42], timeBits(staleTill))
	binary.BigEndian.PutUint64(encoded[42:50], timeBits(retainTill))
	binary.BigEndian.PutUint64(encoded[50:58], uint64(len(payload)))
	copy(encoded[envelopeFixedSize:], descriptor.id)
	payloadStart := envelopeFixedSize + len(descriptor.id)
	copy(encoded[payloadStart:], payload)
	checksum := sha256.Sum256(encoded[:total-envelopeHashSize])
	copy(encoded[total-envelopeHashSize:], checksum[:])
	expiry, err := physicalExpiry(policy, result.Presence, now, retainTill)
	if err != nil {
		return nil, nil, Expiry{}, err
	}
	return encoded, payload, expiry, nil
}

func decodeEnvelope[V any](encoded []byte, runtime Runtime, codec Codec[V], descriptor codecDescriptor, policy Policy) (Result[V], int, error) {
	maximum, err := maxEnvelopeBytes(policy)
	if err != nil || len(encoded) > maximum || len(encoded) < envelopeFixedSize+envelopeHashSize || !bytes.Equal(encoded[:8], envelopeMagic[:]) {
		return Result[V]{}, 0, ErrCorrupt
	}
	if binary.BigEndian.Uint16(encoded[8:10]) != envelopeVersion || encoded[11] != 0 {
		return Result[V]{}, 0, ErrCorrupt
	}
	presence := Presence(encoded[10])
	if presence != Found && presence != CleanAbsent {
		return Result[V]{}, 0, ErrCorrupt
	}
	codecLength := int(binary.BigEndian.Uint16(encoded[12:14]))
	payloadLength64 := binary.BigEndian.Uint64(encoded[50:58])
	if payloadLength64 > uint64(policy.MaxValueBytes) || payloadLength64 > uint64(math.MaxInt) {
		return Result[V]{}, 0, ErrCorrupt
	}
	payloadLength := int(payloadLength64)
	total, ok := envelopeSize(codecLength, payloadLength)
	if !ok || total != len(encoded) || codecLength == 0 || codecLength > MaxCodecIDBytes {
		return Result[V]{}, 0, ErrCorrupt
	}
	wantHash := sha256.Sum256(encoded[:total-envelopeHashSize])
	if !bytes.Equal(wantHash[:], encoded[total-envelopeHashSize:]) {
		return Result[V]{}, 0, ErrCorrupt
	}
	codecID := string(encoded[envelopeFixedSize : envelopeFixedSize+codecLength])
	if codecID != descriptor.id || ValueSchema(binary.BigEndian.Uint32(encoded[14:18])) != descriptor.schema {
		return Result[V]{}, 0, ErrCorrupt
	}
	entry := envelope{
		presence:   presence,
		writtenAt:  timeFromBits(binary.BigEndian.Uint64(encoded[18:26])),
		freshTill:  timeFromBits(binary.BigEndian.Uint64(encoded[26:34])),
		staleTill:  timeFromBits(binary.BigEndian.Uint64(encoded[34:42])),
		retainTill: timeFromBits(binary.BigEndian.Uint64(encoded[42:50])),
		payload:    encoded[envelopeFixedSize+codecLength : total-envelopeHashSize],
	}
	now, err := conservativeNow(runtime)
	if err != nil {
		return Result[V]{}, 0, err
	}
	if validateStoredEnvelope(entry, now) != nil {
		return Result[V]{}, 0, ErrCorrupt
	}
	state, validUntil, err := effectiveState(entry, policy, now)
	if err != nil {
		return Result[V]{}, 0, ErrCorrupt
	}
	if state == Miss {
		return Result[V]{State: Miss}, len(entry.payload), nil
	}
	if state == Negative {
		return Result[V]{State: Negative, validUntil: validUntil}, 0, nil
	}
	value, err := invokeDecode(codec, bytes.Clone(entry.payload), ValueLimit{
		MaxBytes:        policy.MaxValueBytes,
		MaxDecodedBytes: policy.MaxValueBytes,
		MaxDepth:        policy.MaxValueDepth,
	})
	if err != nil {
		if errors.Is(err, ErrTooLarge) {
			return Result[V]{}, 0, ErrCorrupt
		}
		return Result[V]{}, 0, fmt.Errorf("%w: value decode failed", ErrCorrupt)
	}
	return Result[V]{Value: value, State: state, validUntil: validUntil}, len(entry.payload), nil
}

func deadlines(runtime Runtime, policy Policy, presence Presence, now time.Time) (time.Time, time.Time, time.Time, error) {
	if presence == CleanAbsent {
		if policy.Negative.duration <= 0 {
			return time.Time{}, time.Time{}, time.Time{}, fmt.Errorf("%w: negative caching is disabled", ErrInvalid)
		}
		until, ok := addTime(now, policy.Negative.duration)
		if !ok {
			return time.Time{}, time.Time{}, time.Time{}, ErrInvalid
		}
		return until, until, until, nil
	}
	retainTill := time.Time{}
	if !policy.Retention.capacityOnly {
		var ok bool
		retainTill, ok = addTime(now, policy.Retention.expiresAfter)
		if !ok {
			return time.Time{}, time.Time{}, time.Time{}, ErrInvalid
		}
	}
	if policy.Freshness.always {
		return retainTill, retainTill, retainTill, nil
	}
	freshDuration := policy.Freshness.freshFor
	if maximum := policy.Jitter.subtractUpTo; policy.Jitter.enabled {
		random, err := runtimeRandom(runtime.Random)
		if err != nil {
			return time.Time{}, time.Time{}, time.Time{}, err
		}
		freshDuration -= time.Duration(random % uint64(maximum))
	}
	window, ok := addDuration(policy.Freshness.freshFor, policy.Freshness.staleFor)
	if !ok {
		return time.Time{}, time.Time{}, time.Time{}, ErrInvalid
	}
	freshTill, freshOK := addTime(now, freshDuration)
	staleTill, staleOK := addTime(now, window)
	if !freshOK || !staleOK {
		return time.Time{}, time.Time{}, time.Time{}, ErrInvalid
	}
	return freshTill, staleTill, retainTill, nil
}

func physicalExpiry(policy Policy, presence Presence, now, retainTill time.Time) (Expiry, error) {
	if presence == CleanAbsent {
		return Expiry{Mode: RelativeExpiry, RetainFor: retainTill.Sub(now), deadline: retainTill}, nil
	}
	if policy.Retention.capacityOnly {
		return Expiry{Mode: CapacityOnlyExpiry}, nil
	}
	return Expiry{Mode: RelativeExpiry, RetainFor: retainTill.Sub(now), deadline: retainTill}, nil
}

func expiryForWrite(runtime Runtime, expiry Expiry) (Expiry, bool, error) {
	if expiry.Mode == CapacityOnlyExpiry {
		return Expiry{Mode: CapacityOnlyExpiry}, true, nil
	}
	if expiry.Mode != RelativeExpiry || expiry.deadline.IsZero() {
		return Expiry{}, false, ErrInvalid
	}
	now, err := runtimeNow(runtime.Clock)
	if err != nil {
		return Expiry{}, false, err
	}
	now = normalizedTime(now)
	if !now.Before(expiry.deadline) {
		return Expiry{}, false, nil
	}
	remaining := expiry.deadline.Sub(now)
	if remaining <= 0 {
		return Expiry{}, false, nil
	}
	if remaining > expiry.RetainFor {
		remaining = expiry.RetainFor
	}
	return Expiry{Mode: RelativeExpiry, RetainFor: remaining}, true, nil
}

func validateStoredEnvelope(entry envelope, conservative time.Time) error {
	if entry.writtenAt.IsZero() || entry.freshTill.IsZero() || entry.staleTill.IsZero() ||
		entry.freshTill.Before(entry.writtenAt) || entry.staleTill.Before(entry.freshTill) ||
		entry.writtenAt.After(conservative) {
		return ErrCorrupt
	}
	if entry.presence == CleanAbsent {
		if entry.freshTill != entry.staleTill || entry.retainTill != entry.staleTill || len(entry.payload) != 0 {
			return ErrCorrupt
		}
		return nil
	}
	if !entry.retainTill.IsZero() && entry.retainTill.Before(entry.staleTill) {
		return ErrCorrupt
	}
	return nil
}

func effectiveState(entry envelope, policy Policy, now time.Time) (State, time.Time, error) {
	if entry.presence == CleanAbsent {
		if policy.Negative.duration <= 0 {
			return Miss, time.Time{}, nil
		}
		maximum, ok := addTime(entry.writtenAt, policy.Negative.duration)
		if !ok {
			return 0, time.Time{}, ErrCorrupt
		}
		validUntil := earlier(entry.freshTill, maximum)
		if now.Before(validUntil) {
			return Negative, validUntil, nil
		}
		return Miss, time.Time{}, nil
	}
	if policy.Retention.capacityOnly != entry.retainTill.IsZero() {
		return 0, time.Time{}, ErrCorrupt
	}
	var maximumFresh, maximumStale time.Time
	if policy.Freshness.always {
		maximum, ok := addTime(entry.writtenAt, policy.Retention.expiresAfter)
		if !ok {
			return 0, time.Time{}, ErrCorrupt
		}
		maximumFresh, maximumStale = maximum, maximum
	} else {
		window, ok := addDuration(policy.Freshness.freshFor, policy.Freshness.staleFor)
		if !ok {
			return 0, time.Time{}, ErrCorrupt
		}
		maximumFresh, ok = addTime(entry.writtenAt, policy.Freshness.freshFor)
		if !ok {
			return 0, time.Time{}, ErrCorrupt
		}
		maximumStale, ok = addTime(entry.writtenAt, window)
		if !ok {
			return 0, time.Time{}, ErrCorrupt
		}
	}
	effectiveRetain := entry.retainTill
	if !policy.Retention.capacityOnly {
		maximumRetain, ok := addTime(entry.writtenAt, policy.Retention.expiresAfter)
		if !ok {
			return 0, time.Time{}, ErrCorrupt
		}
		if effectiveRetain.IsZero() {
			effectiveRetain = maximumRetain
		} else {
			effectiveRetain = earlier(effectiveRetain, maximumRetain)
		}
	}
	if !effectiveRetain.IsZero() && !now.Before(effectiveRetain) {
		return Miss, time.Time{}, nil
	}
	freshUntil := earlier(entry.freshTill, maximumFresh)
	staleUntil := earlier(entry.staleTill, maximumStale)
	if !effectiveRetain.IsZero() {
		freshUntil = earlier(freshUntil, effectiveRetain)
		staleUntil = earlier(staleUntil, effectiveRetain)
	}
	if now.Before(freshUntil) {
		return Hit, freshUntil, nil
	}
	if now.Before(staleUntil) {
		return Stale, staleUntil, nil
	}
	return Miss, time.Time{}, nil
}

func invokeEncode[V any](codec Codec[V], value V, limit ValueLimit) (encoded []byte, err error) {
	defer func() {
		if recover() != nil {
			encoded = nil
			err = fmt.Errorf("codec panicked")
		}
	}()
	encoded, err = codec.Encode(value, limit)
	if err != nil {
		encoded = nil
		err = sanitizedError(err, ErrInvalid)
	}
	return encoded, err
}

func invokeDecode[V any](codec Codec[V], encoded []byte, limit ValueLimit) (value V, err error) {
	defer func() {
		if recover() != nil {
			var zero V
			value = zero
			err = ErrCorrupt
		}
	}()
	value, err = codec.Decode(encoded, limit)
	if err != nil {
		var zero V
		value = zero
		err = sanitizedError(err, ErrCorrupt)
	}
	return value, err
}

func envelopeSize(codecLength, payloadLength int) (int, bool) {
	if codecLength < 0 || payloadLength < 0 || codecLength > math.MaxInt-envelopeFixedSize-envelopeHashSize ||
		payloadLength > math.MaxInt-envelopeFixedSize-envelopeHashSize-codecLength {
		return 0, false
	}
	return envelopeFixedSize + codecLength + payloadLength + envelopeHashSize, true
}

func maxEnvelopeBytes(policy Policy) (int, error) {
	size, ok := envelopeSize(MaxCodecIDBytes, policy.MaxValueBytes)
	if !ok {
		return 0, fmt.Errorf("%w: maximum envelope size overflows", ErrTooLarge)
	}
	return size, nil
}

func normalizedTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	nanoseconds := value.UnixNano()
	normalized := time.Unix(0, nanoseconds).UTC()
	if nanoseconds <= 0 || !normalized.Equal(value) {
		return time.Time{}
	}
	return normalized
}

func timeBits(value time.Time) uint64 {
	if value.IsZero() {
		return 0
	}
	return uint64(value.UnixNano())
}

func timeFromBits(value uint64) time.Time {
	if value == 0 || value > math.MaxInt64 {
		return time.Time{}
	}
	return time.Unix(0, int64(value)).UTC()
}

func addTime(value time.Time, duration time.Duration) (time.Time, bool) {
	value = normalizedTime(value)
	if value.IsZero() || duration <= 0 || value.UnixNano() > math.MaxInt64-int64(duration) {
		return time.Time{}, false
	}
	return time.Unix(0, value.UnixNano()+int64(duration)).UTC(), true
}

func conservativeNow(runtime Runtime) (time.Time, error) {
	now, err := runtimeNow(runtime.Clock)
	if err != nil {
		return time.Time{}, err
	}
	now = normalizedTime(now)
	if runtime.ClockSkew.bound == 0 {
		return now, nil
	}
	result, ok := addTime(now, runtime.ClockSkew.bound)
	if !ok {
		return time.Time{}, ErrInvalid
	}
	return result, nil
}

func validateRuntimePolicy(runtime Runtime, policy Policy) error {
	now, err := runtimeNow(runtime.Clock)
	if err != nil {
		return err
	}
	now = normalizedTime(now)
	if runtime.ClockSkew.bound > 0 {
		if _, ok := addTime(now, runtime.ClockSkew.bound); !ok {
			return fmt.Errorf("%w: clock skew overflows timestamp", ErrInvalid)
		}
	}
	if policy.Freshness.always {
		if _, ok := addTime(now, policy.Retention.expiresAfter); !ok {
			return fmt.Errorf("%w: retention overflows timestamp", ErrInvalid)
		}
	} else {
		window, ok := addDuration(policy.Freshness.freshFor, policy.Freshness.staleFor)
		if !ok {
			return fmt.Errorf("%w: freshness window overflows", ErrInvalid)
		}
		if _, ok := addTime(now, policy.Freshness.freshFor); !ok {
			return fmt.Errorf("%w: freshness overflows timestamp", ErrInvalid)
		}
		if _, ok := addTime(now, window); !ok {
			return fmt.Errorf("%w: stale window overflows timestamp", ErrInvalid)
		}
		if !policy.Retention.capacityOnly {
			if _, ok := addTime(now, policy.Retention.expiresAfter); !ok {
				return fmt.Errorf("%w: retention overflows timestamp", ErrInvalid)
			}
		}
	}
	if policy.Negative.duration > 0 {
		if _, ok := addTime(now, policy.Negative.duration); !ok {
			return fmt.Errorf("%w: negative duration overflows timestamp", ErrInvalid)
		}
	}
	return nil
}

func earlier(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}
