package jobspg

import (
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/frostgrove/vv/jobs"
)

func TestIntentKeysEncodingRoundTripIsBoundedAndOrdered(t *testing.T) {
	keys := testIntentKeyPair(t)
	encoded, err := encodeIntentKeys(keys)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) != maxEncodedIntentKeysBytes || encoded[0] != intentKeysEncodingVersion || encoded[1] != byte(len(keys)) {
		t.Fatalf("encoded intent keys = (%d, %d, %d)", len(encoded), encoded[0], encoded[1])
	}
	decoded, err := decodeIntentKeys(encoded)
	if err != nil || !slices.Equal(decoded, keys) {
		t.Fatalf("decoded intent keys = (%v, %v), want %v", decoded, err, keys)
	}
	single, err := encodeIntentKeys(keys[:1])
	if err != nil || len(single) != minEncodedIntentKeysBytes {
		t.Fatalf("single encoded intent key = (%d, %v)", len(single), err)
	}
	if decoded, err := decodeIntentKeys(nil); err != nil || decoded != nil {
		t.Fatalf("NULL intent keys = (%v, %v)", decoded, err)
	}
	if _, err := encodeIntentKeys(nil); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("empty intent keys encoded: %v", err)
	}
}

func TestIntentKeysEncodingRejectsMalformedOrIncompatibleMetadata(t *testing.T) {
	keys := testIntentKeyPair(t)
	valid, err := encodeIntentKeys(keys)
	if err != nil {
		t.Fatal(err)
	}
	malformed := [][]byte{
		{},
		{intentKeysEncodingVersion},
		append([]byte{intentKeysEncodingVersion + 1}, valid[1:]...),
		append([]byte(nil), valid[:len(valid)-1]...),
		append([]byte{intentKeysEncodingVersion, 0}, valid[2:]...),
		append([]byte{intentKeysEncodingVersion, jobs.MaxIntentDigestKeys + 1}, valid[2:]...),
	}
	zeroScope := append([]byte(nil), valid...)
	clear(zeroScope[intentKeysHeaderBytes : intentKeysHeaderBytes+jobs.IntentScopeBytes])
	malformed = append(malformed, zeroScope)
	duplicate := append([]byte(nil), valid...)
	copy(duplicate[intentKeysHeaderBytes+intentKeyEncodingBytes:], duplicate[intentKeysHeaderBytes:intentKeysHeaderBytes+intentKeyEncodingBytes])
	malformed = append(malformed, duplicate)
	wrongScope := append([]byte(nil), valid...)
	wrongScope[intentKeysHeaderBytes+intentKeyEncodingBytes] ^= 1
	malformed = append(malformed, wrongScope)
	wrongPurpose := append([]byte(nil), valid...)
	secondPurpose := intentKeysHeaderBytes + intentKeyEncodingBytes + jobs.IntentScopeBytes + 2
	wrongPurpose[secondPurpose] = byte(jobs.IntentOnce)
	malformed = append(malformed, wrongPurpose)
	for index, encoded := range malformed {
		if _, err := decodeIntentKeys(encoded); !errors.Is(err, jobs.ErrCorrupt) {
			t.Fatalf("malformed case %d = %v", index, err)
		}
	}
	if _, err := encodeIntentKeys(append(append([]jobs.IntentKey(nil), keys...), keys[0])); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatalf("too many intent keys encoded: %v", err)
	}

	namespace, catalog, placement := testPlacementWith(t, jobs.Unique("reservation-key"))
	driver, err := New(Spec{DB: &sql.DB{}, Namespace: namespace, Catalog: catalog})
	if err != nil {
		t.Fatal(err)
	}
	insert, err := driver.newPlacement(placement, placement.Candidate(), time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	record, err := decodeRecord(insert.record)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := jobs.RestoreDeliveryRecord(catalog, record)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validateInvocationIntentKeys(restored.Invocation(), []jobs.IntentKey{keys[1], keys[0]}); !errors.Is(err, jobs.ErrCorrupt) {
		t.Fatalf("non-current first key = %v", err)
	}
	if got, err := validateInvocationIntentKeys(restored.Invocation(), nil); err != nil || !slices.Equal(got, []jobs.IntentKey{restored.Invocation().Intent()}) {
		t.Fatalf("legacy current-only fallback = (%v, %v)", got, err)
	}
}

func TestIntentKeyOrderingKeepsInvocationCurrentFirst(t *testing.T) {
	keys := testIntentKeyPair(t)
	ordered, err := orderIntentKeys(keys[0], []jobs.IntentKey{keys[1], keys[0]})
	if err != nil || !slices.Equal(ordered, keys) {
		t.Fatalf("ordered intent keys = (%v, %v), want %v", ordered, err, keys)
	}
	if _, err := orderIntentKeys(keys[0], keys[1:]); !errors.Is(err, jobs.ErrCorrupt) {
		t.Fatalf("missing current key = %v", err)
	}
}

func testIntentKeyPair(t *testing.T) []jobs.IntentKey {
	t.Helper()
	_, _, placement := testPlacementWith(t, jobs.Unique("reservation-key"))
	current := placement.IntentDigests().Current()
	digestBytes := current.Digest().Bytes()
	digestBytes[0] ^= 1
	digest, err := jobs.IntentDigestFromBytes(digestBytes)
	if err != nil {
		t.Fatal(err)
	}
	revision := jobs.DigestRevision2
	if current.Revision() == jobs.DigestRevision2 {
		revision = jobs.DigestRevision1
	}
	compatibility, err := jobs.NewIntentKey(current.Scope(), revision, current.Purpose(), digest)
	if err != nil {
		t.Fatal(err)
	}
	return []jobs.IntentKey{current, compatibility}
}
