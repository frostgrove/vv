package jobs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type codecPayload struct {
	Name   string         `json:"name"`
	Values []int          `json:"values"`
	Labels map[string]int `json:"labels"`
}

var codecHookCalls atomic.Int64

type codecHook struct{ Value string }

func (value codecHook) MarshalJSON() ([]byte, error) {
	codecHookCalls.Add(1)
	return json.Marshal(value.Value)
}

func (value *codecHook) UnmarshalJSON(encoded []byte) error {
	codecHookCalls.Add(1)
	return json.Unmarshal(encoded, &value.Value)
}

type hiddenJSONPayload struct{ Value string }
type embeddedHiddenJSONPayload struct{ *hiddenJSONPayload }
type ignoredHiddenJSONPayload struct {
	*hiddenJSONPayload `json:"-"`
}

func TestPrimitiveCodecsCloneAndEnforceSeparateLimits(t *testing.T) {
	limit := PayloadLimit{MaxBytes: 8, MaxDecodedBytes: 8, MaxDepth: 1}
	encoded, err := Bytes(1).Encode([]byte("value"), limit)
	if err != nil {
		t.Fatal(err)
	}
	encoded[0] = 'X'
	decoded, err := Bytes(1).Decode([]byte("value"), limit)
	if err != nil {
		t.Fatal(err)
	}
	decoded[0] = 'X'
	if string(encoded) != "Xalue" || string(decoded) != "Xalue" {
		t.Fatal("test mutation did not occur")
	}
	source := []byte("value")
	payload, err := NewEncodedPayload(builtinCodecID("bytes"), 1, source)
	if err != nil {
		t.Fatal(err)
	}
	source[0] = 'X'
	copyOne := payload.Bytes()
	copyOne[0] = 'Y'
	if string(payload.Bytes()) != "value" {
		t.Fatal("encoded payload aliases caller memory")
	}
	if formatted := fmt.Sprintf("%#v", payload); strings.Contains(formatted, "value") || formatted != "[job payload codec=bytes version=1 bytes=5]" {
		t.Fatalf("payload formatting exposed bytes: %q", formatted)
	}
	if _, err := String(1).Encode("123456789", limit); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
	if _, err := Bytes(1).Decode(make([]byte, 9), limit); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
}

func TestTakenEncodedPayloadOwnsCallerBuffer(t *testing.T) {
	data := []byte("owned")
	payload, err := takeEncodedPayload(builtinCodecID("bytes"), 1, data)
	if err != nil {
		t.Fatal(err)
	}
	if &payload.encodedBytes()[0] != &data[0] {
		t.Fatal("taken payload copied caller buffer")
	}
	data[0] = 'O'
	if string(payload.encodedBytes()) != "Owned" {
		t.Fatal("taken payload did not retain ownership")
	}
}

func TestRFC3339UTCIsCanonicalAndRejectsOffsets(t *testing.T) {
	codec := RFC3339UTC(3)
	limit := PayloadLimit{MaxBytes: 30, MaxDecodedBytes: 64, MaxDepth: 1}
	value := time.Date(2026, 9, 1, 4, 5, 6, 123456000, time.FixedZone("odd", 17*60))
	encoded, err := codec.Encode(value, limit)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "2026-09-01T03:48:06.123456Z" {
		t.Fatalf("unexpected canonical value %q", encoded)
	}
	decoded, err := codec.Decode(encoded, limit)
	if err != nil || decoded.Location() != time.UTC || !decoded.Equal(value) {
		t.Fatalf("roundtrip failed: %v, %v", decoded, err)
	}
	for _, invalid := range []string{"2026-09-01T03:48:06+00:00", `"2026-09-01T03:48:06Z"`, "2026-09-01T03:48:06.0Z", "10000-01-01T00:00:00Z"} {
		if _, err := codec.Decode([]byte(invalid), limit); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("%q: expected ErrCorrupt, got %v", invalid, err)
		}
	}
	if _, err := codec.Encode(value, PayloadLimit{MaxBytes: 30, MaxDecodedBytes: 1, MaxDepth: 1}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected decoded bound failure, got %v", err)
	}
}

func TestSafeJSONRoundTripAndBounds(t *testing.T) {
	codec := JSON[codecPayload](1)
	limit := DefaultPayloadLimit()
	if !safeJSONRuntimeSupported {
		if _, err := describeCodec(codec); !errors.Is(err, ErrInvalid) {
			t.Fatalf("jsonv2 safe JSON must refuse activation, got %v", err)
		}
		if _, err := Define(DefinitionSpec[codecPayload]{Name: testJobName(t, "codec.safe-json-v2"), Codec: codec, Policy: testPolicy(t)}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("jsonv2 safe definition activated: %v", err)
		}
		if _, err := Define(DefinitionSpec[codecPayload]{Name: testJobName(t, "codec.trusted-json-v2"), Codec: TrustedJSON[codecPayload](1), Policy: testPolicy(t)}); err != nil {
			t.Fatalf("jsonv2 trusted definition failed: %v", err)
		}
		return
	}
	value := codecPayload{Name: "<&>", Values: []int{1, 2, 3}, Labels: map[string]int{"a": 1, "b": 2}}
	encoded, err := codec.Encode(value, limit)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`\u003c\u0026\u003e`)) {
		t.Fatalf("wire compatibility changed: %s", encoded)
	}
	decoded, err := codec.Decode(encoded, limit)
	if err != nil || decoded.Name != value.Name || len(decoded.Values) != 3 || decoded.Labels["b"] != 2 {
		t.Fatalf("roundtrip failed: %#v, %v", decoded, err)
	}
	if _, err := codec.Encode(codecPayload{Name: strings.Repeat("x", 1<<20)}, PayloadLimit{MaxBytes: 16, MaxDecodedBytes: MaxDecodedBytes, MaxDepth: MaxPayloadDepth}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected bounded preflight rejection, got %v", err)
	}
	if _, err := codec.Decode([]byte(`{"name":"x"`), limit); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("expected corrupt JSON, got %v", err)
	}
	invalidUTF8 := []byte{'{', '"', 'n', 'a', 'm', 'e', '"', ':', '"', 0xff, '"', '}'}
	if _, err := codec.Decode(invalidUTF8, limit); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("expected raw invalid UTF-8 refusal, got %v", err)
	}
}

func TestSafeJSONRejectsHooksInterfacesTimeAndUnexportedAnonymousPointers(t *testing.T) {
	codecHookCalls.Store(0)
	tests := []any{
		JSON[codecHook](1),
		JSON[[]codecHook](1),
		JSON[any](1),
		JSON[time.Time](1),
		JSON[embeddedHiddenJSONPayload](1),
	}
	for index, codec := range tests {
		validator, ok := codec.(interface{ validateCodec() error })
		if !ok || !errors.Is(validator.validateCodec(), ErrInvalid) {
			t.Fatalf("case %d did not fail closed", index)
		}
	}
	if codecHookCalls.Load() != 0 {
		t.Fatalf("safe validation invoked a hook %d times", codecHookCalls.Load())
	}
	ignored := JSON[ignoredHiddenJSONPayload](1)
	if safeJSONRuntimeSupported {
		if validator := ignored.(interface{ validateCodec() error }); validator.validateCodec() != nil {
			t.Fatalf("explicitly ignored embedding was rejected: %v", validator.validateCodec())
		}
	}
}

func TestTrustedJSONIsExplicitAndStillBoundsWire(t *testing.T) {
	codecHookCalls.Store(0)
	codec := TrustedJSON[codecHook](2)
	descriptor, err := describeCodec(codec)
	if err != nil || descriptor.id.String() != "trusted-json" || descriptor.version != 2 {
		t.Fatalf("unexpected descriptor: %#v, %v", descriptor, err)
	}
	limit := PayloadLimit{MaxBytes: 32, MaxDecodedBytes: 128, MaxDepth: 8}
	encoded, err := codec.Encode(codecHook{Value: "ok"}, limit)
	if err != nil || string(encoded) != `"ok"` || codecHookCalls.Load() == 0 {
		t.Fatalf("trusted encode failed: %q, %v, calls=%d", encoded, err, codecHookCalls.Load())
	}
	decoded, err := codec.Decode(encoded, limit)
	if err != nil || decoded.Value != "ok" {
		t.Fatalf("trusted decode failed: %#v, %v", decoded, err)
	}
	if _, err := codec.Encode(codecHook{Value: strings.Repeat("x", 64)}, limit); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("trusted codec did not enforce wire limit: %v", err)
	}
}

func TestJSONDecodedAllocationPreflightRejectsContainerBombs(t *testing.T) {
	if !safeJSONRuntimeSupported {
		t.Skip("safe JSON intentionally refuses jsonv2")
	}
	codec := JSON[map[string][128]byte](1)
	encoded := []byte(`{"x":null}`)
	if _, err := codec.Decode(encoded, PayloadLimit{MaxBytes: len(encoded), MaxDecodedBytes: 256, MaxDepth: 4}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected first map group to be charged, got %v", err)
	}
	if _, err := codec.Decode(encoded, PayloadLimit{MaxBytes: len(encoded), MaxDecodedBytes: 4096, MaxDepth: 4}); err != nil {
		t.Fatalf("conservative sufficient limit failed: %v", err)
	}
}

func TestPayloadLimitsAndCodecVersionFailFast(t *testing.T) {
	invalidLimits := []PayloadLimit{
		{},
		{MaxBytes: MaxPayloadBytes + 1, MaxDecodedBytes: 1, MaxDepth: 1},
		{MaxBytes: 1, MaxDecodedBytes: MaxDecodedBytes + 1, MaxDepth: 1},
		{MaxBytes: 1, MaxDecodedBytes: 1, MaxDepth: MaxPayloadDepth + 1},
	}
	for index, limit := range invalidLimits {
		if _, err := String(1).Encode("", limit); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case %d: expected ErrInvalid, got %v", index, err)
		}
	}
	if _, err := describeCodec(String(0)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero schema version accepted: %v", err)
	}
}
