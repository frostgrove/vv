package jobspg

import (
	"encoding/binary"
	"fmt"

	"github.com/frostgrove/vv/jobs"
)

const (
	intentKeysEncodingVersion = 1
	intentKeysHeaderBytes     = 2
	intentKeyEncodingBytes    = jobs.IntentScopeBytes + 2 + 1 + jobs.IntentDigestBytes
	minEncodedIntentKeysBytes = intentKeysHeaderBytes + intentKeyEncodingBytes
	maxEncodedIntentKeysBytes = intentKeysHeaderBytes + jobs.MaxIntentDigestKeys*intentKeyEncodingBytes
)

func encodeIntentKeys(keys []jobs.IntentKey) ([]byte, error) {
	if _, err := intentDigestsFromKeys(keys); err != nil {
		return nil, err
	}
	encoded := make([]byte, intentKeysHeaderBytes+len(keys)*intentKeyEncodingBytes)
	encoded[0] = intentKeysEncodingVersion
	encoded[1] = byte(len(keys))
	offset := intentKeysHeaderBytes
	for _, key := range keys {
		scope := key.Scope().Bytes()
		copy(encoded[offset:offset+jobs.IntentScopeBytes], scope[:])
		offset += jobs.IntentScopeBytes
		binary.BigEndian.PutUint16(encoded[offset:offset+2], uint16(key.Revision()))
		offset += 2
		encoded[offset] = byte(key.Purpose())
		offset++
		digest := key.Digest().Bytes()
		copy(encoded[offset:offset+jobs.IntentDigestBytes], digest[:])
		offset += jobs.IntentDigestBytes
	}
	return encoded, nil
}

func decodeIntentKeys(encoded []byte) ([]jobs.IntentKey, error) {
	if encoded == nil {
		return nil, nil
	}
	if len(encoded) < intentKeysHeaderBytes || encoded[0] != intentKeysEncodingVersion {
		return nil, corruptIntentKeys()
	}
	count := int(encoded[1])
	if count == 0 || count > jobs.MaxIntentDigestKeys || len(encoded) != intentKeysHeaderBytes+count*intentKeyEncodingBytes {
		return nil, corruptIntentKeys()
	}
	keys := make([]jobs.IntentKey, count)
	offset := intentKeysHeaderBytes
	for index := range keys {
		var scopeBytes [jobs.IntentScopeBytes]byte
		copy(scopeBytes[:], encoded[offset:offset+jobs.IntentScopeBytes])
		offset += jobs.IntentScopeBytes
		revision := jobs.DigestRevision(binary.BigEndian.Uint16(encoded[offset : offset+2]))
		offset += 2
		purpose := jobs.IntentPurpose(encoded[offset])
		offset++
		var digestBytes [jobs.IntentDigestBytes]byte
		copy(digestBytes[:], encoded[offset:offset+jobs.IntentDigestBytes])
		offset += jobs.IntentDigestBytes
		scope, err := jobs.IntentScopeBindingFromBytes(scopeBytes)
		if err != nil {
			return nil, corruptIntentKeys()
		}
		digest, err := jobs.IntentDigestFromBytes(digestBytes)
		if err != nil {
			return nil, corruptIntentKeys()
		}
		keys[index], err = jobs.NewIntentKey(scope, revision, purpose, digest)
		if err != nil {
			return nil, corruptIntentKeys()
		}
	}
	if _, err := intentDigestsFromKeys(keys); err != nil {
		return nil, corruptIntentKeys()
	}
	return keys, nil
}

func intentDigestsFromKeys(keys []jobs.IntentKey) (jobs.IntentDigests, error) {
	if len(keys) == 0 || len(keys) > jobs.MaxIntentDigestKeys {
		return jobs.IntentDigests{}, fmt.Errorf("jobspg: %w: intent reservation keys", jobs.ErrInvalid)
	}
	digests, err := jobs.NewIntentDigests(keys[0], keys[1:]...)
	if err != nil {
		return jobs.IntentDigests{}, fmt.Errorf("jobspg: %w: intent reservation keys", jobs.ErrInvalid)
	}
	return digests, nil
}

func orderIntentKeys(current jobs.IntentKey, keys []jobs.IntentKey) ([]jobs.IntentKey, error) {
	if current.IsZero() || len(keys) == 0 || len(keys) > jobs.MaxIntentDigestKeys {
		return nil, corruptIntentKeys()
	}
	ordered := make([]jobs.IntentKey, 0, len(keys))
	ordered = append(ordered, current)
	foundCurrent := false
	for _, key := range keys {
		if key == current {
			if foundCurrent {
				return nil, corruptIntentKeys()
			}
			foundCurrent = true
			continue
		}
		ordered = append(ordered, key)
	}
	if !foundCurrent || len(ordered) != len(keys) {
		return nil, corruptIntentKeys()
	}
	if _, err := intentDigestsFromKeys(ordered); err != nil {
		return nil, corruptIntentKeys()
	}
	return ordered, nil
}

func validateInvocationIntentKeys(invocation jobs.Invocation, keys []jobs.IntentKey) ([]jobs.IntentKey, error) {
	if len(keys) == 0 {
		digests, err := jobs.RestoreInvocationIntentDigests(invocation)
		if err != nil {
			return nil, err
		}
		return digests.ReservationKeys(), nil
	}
	if keys[0] != invocation.Intent() {
		return nil, corruptIntentKeys()
	}
	if _, err := intentDigestsFromKeys(keys); err != nil {
		return nil, corruptIntentKeys()
	}
	return append([]jobs.IntentKey(nil), keys...), nil
}

func corruptIntentKeys() error {
	return fmt.Errorf("jobspg: %w: intent reservation keys", jobs.ErrCorrupt)
}
